package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

type measurement struct {
	duration time.Duration
	status   int
	failed   bool
}
type report struct {
	Target            string      `json:"target"`
	Requests          int         `json:"requests"`
	Concurrency       int         `json:"concurrency"`
	Success           int         `json:"success"`
	Errors            int         `json:"errors"`
	StatusCodes       map[int]int `json:"status_codes"`
	ElapsedSeconds    float64     `json:"elapsed_seconds"`
	RequestsPerSecond float64     `json:"requests_per_second"`
	MedianMS          float64     `json:"median_ms"`
	P95MS             float64     `json:"p95_ms"`
	P99MS             float64     `json:"p99_ms"`
}

// measure выполняет только GET-запросы; клиенту нужен доступ к выбранному маршруту.
func measure(ctx context.Context, client *http.Client, target, token string, count, concurrency int) (report, error) {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return report{}, errors.New("нужен корректный HTTP(S)-адрес без учётных данных")
	}
	if count < 1 || count > 100000 || concurrency < 1 || concurrency > 100 || concurrency > count {
		return report{}, errors.New("число запросов: 1–100000, параллельность: 1–100 и не больше числа запросов")
	}
	display := *u
	display.RawQuery = ""
	display.Fragment = ""
	out := report{Target: display.String(), Requests: count, Concurrency: concurrency, StatusCodes: map[int]int{}}
	jobs := make(chan struct{})
	results := make(chan measurement, count)
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				start := time.Now()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
				if err == nil && token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				if err != nil {
					results <- measurement{duration: time.Since(start), failed: true}
					continue
				}
				res, err := client.Do(req)
				m := measurement{duration: time.Since(start), failed: err != nil}
				if err == nil {
					_, readErr := io.Copy(io.Discard, res.Body)
					res.Body.Close()
					m.duration = time.Since(start)
					m.status = res.StatusCode
					m.failed = readErr != nil || res.StatusCode < 200 || res.StatusCode >= 300
				}
				results <- m
			}
		}()
	}
	go func() {
		for i := 0; i < count; i++ {
			jobs <- struct{}{}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var times []float64
	for m := range results {
		times = append(times, float64(m.duration)/float64(time.Millisecond))
		if m.status != 0 {
			out.StatusCodes[m.status]++
		}
		if m.failed {
			out.Errors++
		} else {
			out.Success++
		}
	}
	out.ElapsedSeconds = time.Since(started).Seconds()
	if out.ElapsedSeconds > 0 {
		out.RequestsPerSecond = float64(count) / out.ElapsedSeconds
	}
	sort.Float64s(times)
	percentile := func(p int) float64 {
		index := (len(times)*p+99)/100 - 1
		if index < 0 {
			index = 0
		}
		return times[index]
	}
	out.MedianMS = percentile(50)
	out.P95MS = percentile(95)
	out.P99MS = percentile(99)
	return out, nil
}

func main() {
	target := flag.String("url", "http://127.0.0.1:8080/api/v1/health", "адрес GET-маршрута")
	count := flag.Int("requests", 100, "количество запросов")
	concurrency := flag.Int("concurrency", 5, "количество параллельных запросов")
	timeout := flag.Duration("timeout", 5*time.Second, "тайм-аут одного запроса")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "тайм-аут должен быть положительным")
		os.Exit(2)
	}
	result, err := measure(context.Background(), &http.Client{Timeout: *timeout}, *target, os.Getenv("MODEUS_LOAD_TOKEN"), *count, *concurrency)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if result.Errors != 0 {
		os.Exit(1)
	}
}
