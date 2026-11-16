// Package metrics keeps in-process counters for LLM agent calls, tokens,
// latency and reasoning levels. It renders Prometheus text exposition without
// external dependencies.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"activity-descriptor/internal/llm"
)

var latencyBuckets = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 240, 480}

type callKey struct {
	agent  string
	model  string
	status string
	code   string
}

type errorKey struct {
	agent string
	model string
	code  string
}

type tokenKey struct {
	agent     string
	model     string
	direction string
}

type reasoningKey struct {
	agent     string
	reasoning string
}

type latencyKey struct {
	agent string
	model string
}

type histogram struct {
	buckets []int64
	sum     float64
	count   int64
}

// Registry is a concurrency-safe collection of agent metrics.
type Registry struct {
	mu        sync.Mutex
	calls     map[callKey]int64
	errors    map[errorKey]int64
	tokens    map[tokenKey]int64
	reasoning map[reasoningKey]int64
	latency   map[latencyKey]*histogram
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{
		calls:     make(map[callKey]int64),
		errors:    make(map[errorKey]int64),
		tokens:    make(map[tokenKey]int64),
		reasoning: make(map[reasoningKey]int64),
		latency:   make(map[latencyKey]*histogram),
	}
}

// ObserveCall records one model attempt, its latency, outcome and reasoning
// level. err may be nil.
func (r *Registry) ObserveCall(agent, model, reasoning string, latency time.Duration, err error) {
	if r == nil {
		return
	}
	agent = label(agent)
	model = label(model)
	reasoning = label(reasoning)
	status, code := "ok", "ok"
	if err != nil {
		status = "error"
		code = llm.Code(err)
	}
	key := callKey{agent: agent, model: model, status: status, code: code}
	latKey := latencyKey{agent: agent, model: model}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[key]++
	if err != nil {
		r.errors[errorKey{agent: agent, model: model, code: code}]++
	}
	r.reasoning[reasoningKey{agent: agent, reasoning: reasoning}]++
	hist := r.latency[latKey]
	if hist == nil {
		hist = &histogram{buckets: make([]int64, len(latencyBuckets))}
		r.latency[latKey] = hist
	}
	seconds := latency.Seconds()
	hist.sum += seconds
	hist.count++
	for i, bucket := range latencyBuckets {
		if seconds <= bucket {
			hist.buckets[i]++
		}
	}
}

// ObserveUsage records token usage for one successful provider call.
func (r *Registry) ObserveUsage(agent string, usage llm.Usage) {
	if r == nil {
		return
	}
	agent = label(agent)
	model := label(usage.Model)
	r.mu.Lock()
	defer r.mu.Unlock()
	if usage.PromptTokens > 0 {
		r.tokens[tokenKey{agent: agent, model: model, direction: "input"}] += int64(usage.PromptTokens)
	}
	if usage.CompletionTokens > 0 {
		r.tokens[tokenKey{agent: agent, model: model, direction: "output"}] += int64(usage.CompletionTokens)
	}
}

// WritePrometheus writes the registry in Prometheus text exposition format.
func (r *Registry) WritePrometheus(w io.Writer) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	calls := cloneMap(r.calls)
	errs := cloneMap(r.errors)
	tokens := cloneMap(r.tokens)
	reasoning := cloneMap(r.reasoning)
	latency := make(map[latencyKey]histogram, len(r.latency))
	for key, value := range r.latency {
		copied := *value
		copied.buckets = append([]int64(nil), value.buckets...)
		latency[key] = copied
	}
	r.mu.Unlock()

	var b strings.Builder
	b.WriteString("# HELP activity_descriptor_agent_calls_total Model calls per agent.\n")
	b.WriteString("# TYPE activity_descriptor_agent_calls_total counter\n")
	for _, key := range sortedCalls(calls) {
		fmt.Fprintf(&b, "activity_descriptor_agent_calls_total{agent=%q,model=%q,status=%q,code=%q} %d\n",
			key.agent, key.model, key.status, key.code, calls[key])
	}

	b.WriteString("# HELP activity_descriptor_agent_errors_total Failed model calls per agent.\n")
	b.WriteString("# TYPE activity_descriptor_agent_errors_total counter\n")
	for _, key := range sortedErrors(errs) {
		fmt.Fprintf(&b, "activity_descriptor_agent_errors_total{agent=%q,model=%q,code=%q} %d\n",
			key.agent, key.model, key.code, errs[key])
	}

	b.WriteString("# HELP activity_descriptor_agent_tokens_total Tokens consumed by agent calls.\n")
	b.WriteString("# TYPE activity_descriptor_agent_tokens_total counter\n")
	for _, key := range sortedTokens(tokens) {
		fmt.Fprintf(&b, "activity_descriptor_agent_tokens_total{agent=%q,model=%q,direction=%q} %d\n",
			key.agent, key.model, key.direction, tokens[key])
	}

	b.WriteString("# HELP activity_descriptor_agent_reasoning_total Calls by reasoning level.\n")
	b.WriteString("# TYPE activity_descriptor_agent_reasoning_total counter\n")
	for _, key := range sortedReasoning(reasoning) {
		fmt.Fprintf(&b, "activity_descriptor_agent_reasoning_total{agent=%q,reasoning=%q} %d\n",
			key.agent, key.reasoning, reasoning[key])
	}

	b.WriteString("# HELP activity_descriptor_agent_latency_seconds Model call latency.\n")
	b.WriteString("# TYPE activity_descriptor_agent_latency_seconds histogram\n")
	for _, key := range sortedLatency(latency) {
		hist := latency[key]
		for i, bucket := range latencyBuckets {
			fmt.Fprintf(&b, "activity_descriptor_agent_latency_seconds_bucket{agent=%q,model=%q,le=%q} %d\n",
				key.agent, key.model, strconv.FormatFloat(bucket, 'f', -1, 64), hist.buckets[i])
		}
		fmt.Fprintf(&b, "activity_descriptor_agent_latency_seconds_bucket{agent=%q,model=%q,le=\"+Inf\"} %d\n",
			key.agent, key.model, hist.count)
		fmt.Fprintf(&b, "activity_descriptor_agent_latency_seconds_sum{agent=%q,model=%q} %s\n",
			key.agent, key.model, strconv.FormatFloat(hist.sum, 'f', -1, 64))
		fmt.Fprintf(&b, "activity_descriptor_agent_latency_seconds_count{agent=%q,model=%q} %d\n",
			key.agent, key.model, hist.count)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func label(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func cloneMap[K comparable](input map[K]int64) map[K]int64 {
	out := make(map[K]int64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func sortedCalls(input map[callKey]int64) []callKey {
	keys := make([]callKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		if keys[i].status != keys[j].status {
			return keys[i].status < keys[j].status
		}
		return keys[i].code < keys[j].code
	})
	return keys
}

func sortedErrors(input map[errorKey]int64) []errorKey {
	keys := make([]errorKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].code < keys[j].code
	})
	return keys
}

func sortedTokens(input map[tokenKey]int64) []tokenKey {
	keys := make([]tokenKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].direction < keys[j].direction
	})
	return keys
}

func sortedReasoning(input map[reasoningKey]int64) []reasoningKey {
	keys := make([]reasoningKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		return keys[i].reasoning < keys[j].reasoning
	})
	return keys
}

func sortedLatency(input map[latencyKey]histogram) []latencyKey {
	keys := make([]latencyKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		return keys[i].model < keys[j].model
	})
	return keys
}
