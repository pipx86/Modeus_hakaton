package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/auth"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestСерверСохраняетИсследованиеПослеПерезапуска(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL")
	}
	auth.SetSecretForTests("server-restart-test-secret")
	token, err := auth.Issue("local-server-test", auth.RoleTeacher, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	client := &http.Client{Timeout: time.Second}
	base := "http://" + address
	start := func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, config{address: address, databaseURL: dsn, jwtSecret: "server-restart-test-secret"})
		}()
		stop := sync.OnceFunc(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("остановка сервера: %v", err)
				}
			case <-time.After(6 * time.Second):
				t.Error("сервер не остановился")
			}
		})
		t.Cleanup(stop)
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			res, e := client.Get(base + "/api/v1/health")
			if e == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					return stop
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("сервер не запустился")
		return stop
	}
	stop := start()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/runs", strings.NewReader(`{"profession":"проверка перезапуска"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID         string `json:"id"`
		Profession string `json:"profession"`
	}
	err = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	if err != nil || res.StatusCode != 201 || created.ID == "" {
		t.Fatalf("создание: HTTP %d %v", res.StatusCode, err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM runs WHERE id=$1", created.ID); pool.Close() })
	stop()
	start()
	req, err = http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/runs/%s", base, created.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		ID         string `json:"id"`
		Profession string `json:"profession"`
	}
	err = json.NewDecoder(res.Body).Decode(&restored)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || restored != created {
		t.Fatalf("восстановление: HTTP %d %+v %v", res.StatusCode, restored, err)
	}
}
