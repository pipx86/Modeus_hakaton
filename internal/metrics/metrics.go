// Package metrics implements a dependency-free subset of the Prometheus text
// exposition format (version 0.0.4) for the activity-descriptor backend.
//
// The demo API serves the exposition on GET /metrics. Labels use a small,
// fixed set so dashboards stay cheap; run-level identifiers are deliberately
// never used as labels, and prompts or credentials are never recorded.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Namespace is the prefix of every exposed metric name.
const Namespace = "activity_descriptor"

// defaultBuckets covers HTTP request latencies in seconds.
var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// llmBuckets covers LLM call and run latencies in seconds.
var llmBuckets = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120}

// runIDPattern matches the identifiers used by the in-memory store and by
// UUID-based runs so they never leak into a metric label.
var runIDPattern = regexp.MustCompile(`^(?:run-\d+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|\d+)$`)

// Metrics is the process-wide metrics registry. It is safe for concurrent use.
type Metrics struct {
	mu sync.Mutex

	httpRequests *vec
	httpDuration *vec
	httpInFlight *vec
	llmRequests  *vec
	llmDuration  *vec
	llmTokens    *vec
	agentSteps   *vec
	runs         *vec
	runDuration  *vec
	vacancies    *vec
}

// New returns an empty registry with the backend metric families registered.
func New() *Metrics {
	return &Metrics{
		httpRequests: newVec("http_requests_total", "HTTP requests processed, by route and status.", kindCounter,
			[]string{"method", "route", "status"}, nil),
		httpDuration: newVec("http_request_duration_seconds", "HTTP request latency in seconds.", kindHistogram,
			[]string{"method", "route"}, defaultBuckets),
		httpInFlight: newVec("http_in_flight_requests", "HTTP requests currently being served.", kindGauge,
			nil, nil),
		llmRequests: newVec("llm_requests_total", "LLM requests, by agent, model and status.", kindCounter,
			[]string{"agent", "model", "status"}, nil),
		llmDuration: newVec("llm_request_duration_seconds", "LLM request latency in seconds.", kindHistogram,
			[]string{"agent", "model"}, llmBuckets),
		llmTokens: newVec("llm_tokens_total", "LLM tokens, by agent, model and direction.", kindCounter,
			[]string{"agent", "model", "direction"}, nil),
		agentSteps: newVec("agent_steps_total", "Agent steps, by agent, step and status.", kindCounter,
			[]string{"agent", "step", "status"}, nil),
		runs: newVec("runs_total", "Activity-descriptor runs, by terminal status.", kindCounter,
			[]string{"status"}, nil),
		runDuration: newVec("run_duration_seconds", "Activity-descriptor run latency in seconds.", kindHistogram,
			[]string{"status"}, llmBuckets),
		vacancies: newVec("vacancies_loaded", "Vacancy sources currently available.", kindGauge, nil, nil),
	}
}

// ObserveHTTPRequest records one HTTP request. route must be the normalized
// route pattern (for example "/health"), never a raw path with IDs, so label
// cardinality stays bounded.
func (m *Metrics) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	statusLabel := strconv.Itoa(status)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpRequests.inc([]string{method, route, statusLabel})
	m.httpDuration.observe(duration.Seconds(), []string{method, route})
}

// ObserveLLMRequest records one LLM call. No prompt, response or credential
// data may be passed here.
func (m *Metrics) ObserveLLMRequest(agent, model, status string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.llmRequests.inc([]string{agent, model, status})
	m.llmDuration.observe(duration.Seconds(), []string{agent, model})
}

// AddTokens records token usage for one LLM call.
func (m *Metrics) AddTokens(agent, model string, input, output int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if input > 0 {
		m.llmTokens.add(uint64(input), []string{agent, model, "input"})
	}
	if output > 0 {
		m.llmTokens.add(uint64(output), []string{agent, model, "output"})
	}
}

// SetVacancies updates the vacancy gauge.
func (m *Metrics) SetVacancies(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vacancies.set(float64(count), nil)
}

// ObserveAgentStep records one agent step outcome.
func (m *Metrics) ObserveAgentStep(agent, step, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentSteps.inc([]string{agent, step, status})
}

// ObserveRun records one finished run and its wall-clock duration.
func (m *Metrics) ObserveRun(status string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs.inc([]string{status})
	m.runDuration.observe(duration.Seconds(), []string{status})
}

// Middleware records HTTP request counts, latencies and the in-flight gauge.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.addInFlight(1)
		rec := &responseRecorder{ResponseWriter: w}
		defer func() {
			m.addInFlight(-1)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			m.ObserveHTTPRequest(r.Method, routeLabel(r), status, time.Since(start))
		}()
		next.ServeHTTP(rec, r)
	})
}

func (m *Metrics) addInFlight(delta float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpInFlight.addFloat(delta, nil)
}

// knownRouteSegments bounds path-derived route labels. Only literals used by
// the backend API and the demo surfaces are allowed; anything else collapses
// to "unmatched" so arbitrary 404 paths cannot explode cardinality.
var knownRouteSegments = map[string]struct{}{
	"api": {}, "v1": {}, "health": {}, "metrics": {},
	"runs": {}, "tree": {}, "answers": {}, "export": {}, "events": {},
	"messages": {}, "search": {}, "sources": {}, "index": {},
	"evidences": {}, "approve": {},
}

// routeLabel returns the normalized route for r. When the router matched a
// non-root pattern (Go net/http sets Request.Pattern) that pattern is used;
// otherwise the path is normalized (run identifiers become "{id}", unknown
// segments become "unmatched"). Nested ServeMux routers lose the inner pattern
// at the outer middleware, which is why the path fallback exists.
func routeLabel(r *http.Request) string {
	if pattern := r.Pattern; pattern != "" {
		if _, route, ok := strings.Cut(pattern, " "); ok && route != "" {
			return route
		}
		if pattern != "/" {
			return pattern
		}
	}
	if r.URL == nil || r.URL.Path == "" || r.URL.Path == "/" {
		return "unmatched"
	}
	return normalizePath(r.URL.Path)
}

// normalizePath replaces IDs with "{id}" and rejects unknown segments so labels
// stay bounded.
func normalizePath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "unmatched"
	}
	segments := strings.Split(trimmed, "/")
	for i, segment := range segments {
		if segment == "" {
			continue
		}
		if runIDPattern.MatchString(segment) {
			segments[i] = "{id}"
			continue
		}
		if _, known := knownRouteSegments[segment]; !known {
			return "unmatched"
		}
	}
	return "/" + strings.Join(segments, "/")
}

// Handler returns the HTTP handler serving the Prometheus text exposition.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := m.Text()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	})
}

// Text renders the exposition in the Prometheus text format. Output is sorted
// by metric family and label set so scrapes are diff-friendly.
func (m *Metrics) Text() string {
	var buf strings.Builder
	_, _ = m.WriteTo(&buf)
	return buf.String()
}

// WriteTo renders the exposition to w and satisfies io.WriterTo.
func (m *Metrics) WriteTo(w io.Writer) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sink := &countingWriter{w: w}
	families := []*vec{
		m.httpRequests, m.httpDuration, m.httpInFlight,
		m.llmRequests, m.llmDuration, m.llmTokens,
		m.agentSteps, m.runs, m.runDuration,
	}
	sort.Slice(families, func(i, j int) bool { return families[i].name < families[j].name })

	for _, family := range families {
		if len(family.series) == 0 {
			continue // do not expose empty families; keeps scrapes small
		}
		fullName := Namespace + "_" + family.name
		if _, err := fmt.Fprintf(sink, "# HELP %s %s\n# TYPE %s %s\n", fullName, family.help, fullName, family.kind); err != nil {
			return sink.n, err
		}
		keys := make([]string, 0, len(family.series))
		for key := range family.series {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := family.writeSeries(sink, fullName, family.series[key]); err != nil {
				return sink.n, err
			}
		}
	}
	return sink.n, nil
}

// countingWriter tracks how many bytes were written.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

type metricKind string

const (
	kindCounter   metricKind = "counter"
	kindHistogram metricKind = "histogram"
	kindGauge     metricKind = "gauge"
)

// vec is a labelled metric family.
type vec struct {
	name       string
	help       string
	kind       metricKind
	labelNames []string
	buckets    []float64 // histograms only
	series     map[string]*series
}

func newVec(name, help string, kind metricKind, labelNames []string, buckets []float64) *vec {
	return &vec{
		name:       name,
		help:       help,
		kind:       kind,
		labelNames: labelNames,
		buckets:    buckets,
		series:     make(map[string]*series),
	}
}

type series struct {
	labels []string // aligned with vec.labelNames
	value  uint64   // counters only
	gauge  float64  // gauges only
	hist   *histogram
}

// key builds a canonical, collision-free map key for a label value tuple.
func (v *vec) key(values []string) string {
	var b strings.Builder
	for i, name := range v.labelNames {
		if i > 0 {
			b.WriteByte('\xff')
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(values[i])
	}
	return b.String()
}

func (v *vec) getOrCreate(values []string) *series {
	key := v.key(values)
	if existing, ok := v.series[key]; ok {
		return existing
	}
	s := &series{labels: append([]string(nil), values...)}
	if v.kind == kindHistogram {
		s.hist = newHistogram(v.buckets)
	}
	v.series[key] = s
	return s
}

func (v *vec) inc(values []string) {
	v.getOrCreate(values).value++
}

func (v *vec) add(delta uint64, values []string) {
	v.getOrCreate(values).value += delta
}

func (v *vec) addFloat(delta float64, values []string) {
	v.getOrCreate(values).gauge += delta
}

func (v *vec) set(value float64, values []string) {
	v.getOrCreate(values).gauge = value
}

func (v *vec) observe(value float64, values []string) {
	v.getOrCreate(values).hist.observe(value, v.buckets)
}

func (v *vec) writeSeries(w io.Writer, fullName string, s *series) error {
	labels := renderLabels(v.labelNames, s.labels)
	switch v.kind {
	case kindCounter:
		_, err := fmt.Fprintf(w, "%s%s %d\n", fullName, labels, s.value)
		return err
	case kindGauge:
		_, err := fmt.Fprintf(w, "%s%s %s\n", fullName, labels, formatFloat(s.gauge))
		return err
	case kindHistogram:
		cumulative := uint64(0)
		for i, bound := range v.buckets {
			cumulative += s.hist.counts[i]
			le := strconv.FormatFloat(bound, 'g', -1, 64)
			if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", fullName, appendLabel(labels, "le", le), cumulative); err != nil {
				return err
			}
		}
		cumulative += s.hist.counts[len(v.buckets)] // +Inf overflow bucket
		if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", fullName, appendLabel(labels, "le", "+Inf"), cumulative); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s_sum%s %s\n", fullName, labels, formatFloat(s.hist.sum)); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "%s_count%s %d\n", fullName, labels, s.hist.count)
		return err
	default:
		return fmt.Errorf("metrics: unsupported metric kind %q", v.kind)
	}
}

// histogram stores non-cumulative bucket counts; the last slot is the +Inf
// overflow bucket.
type histogram struct {
	counts []uint64
	sum    float64
	count  uint64
}

func newHistogram(buckets []float64) *histogram {
	return &histogram{counts: make([]uint64, len(buckets)+1)}
}

// observe adds value to the first bucket whose upper bound is >= value, or to
// the +Inf overflow bucket.
func (h *histogram) observe(value float64, buckets []float64) {
	index := len(h.counts) - 1
	for i, bound := range buckets {
		if value <= bound {
			index = i
			break
		}
	}
	h.counts[index]++
	h.sum += value
	h.count++
}

// appendLabel adds one label to an already-rendered label set.
func appendLabel(rendered, name, value string) string {
	label := name + `="` + escapeLabel(value) + `"`
	if rendered == "" {
		return "{" + label + "}"
	}
	return rendered[:len(rendered)-1] + "," + label + "}"
}

// renderLabels renders sorted label pairs in the canonical Prometheus format.
func renderLabels(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + `="` + escapeLabel(values[i]) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// escapeLabel escapes backslashes, double quotes and newlines per the text
// exposition format.
func escapeLabel(value string) string {
	if !strings.ContainsAny(value, "\\\"\n") {
		return value
	}
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// responseRecorder records the response status for metrics.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}
