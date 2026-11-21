package programwatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testCSV = "kind;code;title;parent_code;taxonomy_codes;type\n" +
	"program;P1;Программа;;;\n" +
	"competency;K1;Компетенция;;;ПК\n" +
	"knowledge;K1;Знать стандарт;K1;;\n"

var testNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

type fakeWatchStore struct {
	mu      sync.Mutex
	sources map[string]Source
	checks  map[string]CheckUpdate
	seq     int
}

func newFakeWatchStore() *fakeWatchStore {
	return &fakeWatchStore{sources: map[string]Source{}, checks: map[string]CheckUpdate{}}
}

func (f *fakeWatchStore) add(source Source) Source {
	f.mu.Lock()
	defer f.mu.Unlock()
	if source.ID == "" {
		f.seq++
		source.ID = fmt.Sprintf("source-%d", f.seq)
	}
	if source.IntervalHours == 0 {
		source.IntervalHours = 24
	}
	if source.CreatedAt.IsZero() {
		source.CreatedAt = testNow
	}
	f.sources[source.ID] = source
	return source
}

func (f *fakeWatchStore) CreateWatchSource(_ context.Context, input CreateInput) (Source, error) {
	for _, source := range f.sources {
		if source.ProgramKey == input.ProgramKey && source.URL == input.URL {
			return Source{}, ErrDuplicate
		}
	}
	source := Source{
		ProgramKey:    input.ProgramKey,
		URL:           input.URL,
		DocFormat:     input.DocFormat,
		IntervalHours: input.IntervalHours,
		Enabled:       true,
	}
	return f.add(source), nil
}

func (f *fakeWatchStore) ListWatchSources(_ context.Context) ([]Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]Source, 0, len(f.sources))
	for _, source := range f.sources {
		items = append(items, source)
	}
	return items, nil
}

func (f *fakeWatchStore) GetWatchSource(_ context.Context, id string) (Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	return source, nil
}

func (f *fakeWatchStore) UpdateWatchSource(_ context.Context, id string, patch SourcePatch) (Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	if patch.Enabled != nil {
		source.Enabled = *patch.Enabled
	}
	if patch.IntervalHours != nil {
		source.IntervalHours = *patch.IntervalHours
	}
	f.sources[id] = source
	return source, nil
}

func (f *fakeWatchStore) DeleteWatchSource(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sources[id]; !ok {
		return ErrNotFound
	}
	delete(f.sources, id)
	return nil
}

func (f *fakeWatchStore) DueWatchSources(_ context.Context, now time.Time, limit int) ([]Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := make([]Source, 0, len(f.sources))
	for _, source := range f.sources {
		all = append(all, source)
	}
	return SelectDue(all, now, limit), nil
}

func (f *fakeWatchStore) SaveWatchCheck(_ context.Context, id string, update CheckUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return ErrNotFound
	}
	f.checks[id] = update
	source.LastCheckedAt = &update.CheckedAt
	source.LastStatus = update.Status
	source.LastError = update.Error
	source.FailureCount = update.FailureCount
	if update.DraftID != "" {
		source.DraftID = update.DraftID
	}
	if update.ETag != nil {
		source.ETag = *update.ETag
	}
	if update.LastModified != nil {
		source.LastModified = *update.LastModified
	}
	if update.LastHash != nil {
		source.LastHash = *update.LastHash
	}
	f.sources[id] = source
	return nil
}

func (f *fakeWatchStore) lastCheck(t *testing.T, id string) CheckUpdate {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	update, ok := f.checks[id]
	if !ok {
		t.Fatalf("для источника %s не сохранена проверка", id)
	}
	return update
}

type fakeDrafts struct {
	mu     sync.Mutex
	inputs []DraftInput
	err    error
	seq    int
}

func (f *fakeDrafts) CreateProgramWatchDraft(_ context.Context, input DraftInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.inputs = append(f.inputs, input)
	f.seq++
	return fmt.Sprintf("draft-%d", f.seq), nil
}

func newCheckerServer(t *testing.T, handler http.Handler, store Store, drafts DraftCreator) (*Checker, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	checker := NewChecker(store, drafts)
	checker.Now = func() time.Time { return testNow }
	checker.Client = server.Client()
	return checker, server
}

func TestSelectDue(t *testing.T) {
	older := testNow.Add(-48 * time.Hour)
	recent := testNow.Add(-2 * time.Hour)
	future := testNow.Add(2 * time.Hour)
	sources := []Source{
		{ID: "disabled", Enabled: false},
		{ID: "future", Enabled: true, IntervalHours: 24, LastCheckedAt: &future},
		{ID: "new-b", Enabled: true, IntervalHours: 24},
		{ID: "new-a", Enabled: true, IntervalHours: 24},
		{ID: "older", Enabled: true, IntervalHours: 24, LastCheckedAt: &older},
		{ID: "recent", Enabled: true, IntervalHours: 1, LastCheckedAt: &recent},
	}
	due := SelectDue(sources, testNow, 3)
	if len(due) != 3 {
		t.Fatalf("ожидали 3 источника, получили %d: %+v", len(due), due)
	}
	if due[0].ID != "new-a" || due[1].ID != "new-b" || due[2].ID != "older" {
		t.Fatalf("порядок due-выборки: %+v", due)
	}
	if sources[1].Due(testNow) {
		t.Fatal("future не должен быть due")
	}
	if got := SelectDue(sources, testNow, 0); len(got) != 4 {
		t.Fatalf("без лимита ожидали 4 источника, получили %d", len(got))
	}
}

func TestCheckerUnchangedByETag(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"v1"` {
			t.Errorf("If-None-Match = %q", got)
		}
		if got := r.Header.Get("If-Modified-Since"); got != "Mon, 20 Nov 2026 10:00:00 GMT" {
			t.Errorf("If-Modified-Since = %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/program.csv",
		IntervalHours: 24,
		Enabled:       true,
		ETag:          `"v1"`,
		LastModified:  "Mon, 20 Nov 2026 10:00:00 GMT",
		LastHash:      "old-hash",
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusUnchanged {
		t.Fatalf("status = %q", result.Status)
	}
	if len(drafts.inputs) != 0 {
		t.Fatalf("черновик не должен создаваться: %+v", drafts.inputs)
	}
	update := store.lastCheck(t, source.ID)
	if update.ETag != nil || update.LastModified != nil || update.LastHash != nil {
		t.Fatalf("при 304 старые валидаторы должны сохраняться: %+v", update)
	}
	if !update.CheckedAt.Equal(testNow) || update.FailureCount != 0 {
		t.Fatalf("update: %+v", update)
	}
}

func TestCheckerUnchangedByHash(t *testing.T) {
	body := []byte(testCSV)
	sum := sha256.Sum256(body)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/program.csv",
		IntervalHours: 24,
		Enabled:       true,
		LastHash:      hex.EncodeToString(sum[:]),
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusUnchanged {
		t.Fatalf("status = %q", result.Status)
	}
	update := store.lastCheck(t, source.ID)
	if update.ETag == nil || *update.ETag != `"v2"` {
		t.Fatalf("ETag должен обновиться: %+v", update.ETag)
	}
	if update.LastHash == nil || *update.LastHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("LastHash должен обновиться: %+v", update.LastHash)
	}
}

func TestCheckerChangedCreatesDraft(t *testing.T) {
	body := []byte(testCSV)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/files/program.csv",
		IntervalHours: 24,
		Enabled:       true,
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusChanged || result.DraftID != "draft-1" {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 1 {
		t.Fatalf("ожидали один черновик: %+v", drafts.inputs)
	}
	input := drafts.inputs[0]
	if input.ProgramKey != source.ProgramKey || input.SourceName != "program.csv" || input.SourceFormat != "csv" {
		t.Fatalf("draft input: %+v", input)
	}
	if input.Competencies != 1 || input.KSA != 1 {
		t.Fatalf("counts: %+v", input)
	}
	update := store.lastCheck(t, source.ID)
	if update.DraftID != "draft-1" || update.LastHash == nil {
		t.Fatalf("update: %+v", update)
	}
}

func TestCheckerHTMLFollowsRelativeLink(t *testing.T) {
	page := `<html><body><a href="files/program.csv">Скачать программу</a></body></html>`
	mux := http.NewServeMux()
	mux.HandleFunc("/page/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/page/files/program.csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(testCSV))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, mux, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/page/",
		IntervalHours: 24,
		Enabled:       true,
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusChanged {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 1 || drafts.inputs[0].SourceName != "program.csv" {
		t.Fatalf("draft input: %+v", drafts.inputs)
	}
}

func TestCheckerHTMLWithoutLinkNeedsAttention(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><a href="/about">О программе</a></body></html>`))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL, IntervalHours: 24, Enabled: true})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusNeedsAttention || result.Error != "на странице нет ссылки на документ" {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 0 {
		t.Fatalf("черновик не должен создаваться: %+v", drafts.inputs)
	}
}

func TestCheckerPDFNeedsParser(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.pdf", IntervalHours: 24, Enabled: true})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusNeedsParser || result.Error != "PDF и DOCX требуют LLM-адаптера" {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 0 {
		t.Fatalf("для PDF черновик не создаётся: %+v", drafts.inputs)
	}
	update := store.lastCheck(t, source.ID)
	if update.LastHash != nil || update.ETag != nil {
		t.Fatalf("валидаторы не должны меняться: %+v", update)
	}
}

func TestCheckerNetworkErrorKeepsOldDataAndBacksOff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/program.csv"
	server.Close()
	store := newFakeWatchStore()
	checker := NewChecker(store, &fakeDrafts{})
	checker.Now = func() time.Time { return testNow }
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           url,
		IntervalHours: 24,
		Enabled:       true,
		ETag:          `"keep-me"`,
		LastHash:      "keep-hash",
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusError || result.Error == "" {
		t.Fatalf("result: %+v", result)
	}
	update := store.lastCheck(t, source.ID)
	if update.ETag != nil || update.LastHash != nil {
		t.Fatalf("old data должны сохраняться: %+v", update)
	}
	if update.FailureCount != 1 || !update.CheckedAt.Equal(testNow) {
		t.Fatalf("первая ошибка: %+v", update)
	}

	updated, _ := store.GetWatchSource(context.Background(), source.ID)
	result, err = checker.CheckAndSave(context.Background(), updated)
	if err != nil {
		t.Fatalf("вторая CheckAndSave: %v", err)
	}
	update = store.lastCheck(t, source.ID)
	if result.Status != StatusError || update.FailureCount != 2 {
		t.Fatalf("вторая ошибка: result=%+v update=%+v", result, update)
	}
	if want := testNow.Add(24 * time.Hour); !update.CheckedAt.Equal(want) {
		t.Fatalf("экспоненциальная пауза: CheckedAt=%s, ожидали %s", update.CheckedAt, want)
	}
}

func TestCheckerParseError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("ETag", `"bad"`)
		_, _ = w.Write([]byte("kind;code;title\nprogram;P1;Программа\ncompetency;K1;A\ncompetency;K1;B\n"))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/program.csv",
		IntervalHours: 24,
		Enabled:       true,
		ETag:          `"keep"`,
	})
	result, err := checker.CheckAndSave(context.Background(), source)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusError || !strings.Contains(result.Error, "разобрать") {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 0 {
		t.Fatalf("при ошибке разбора черновик не создаётся: %+v", drafts.inputs)
	}
	update := store.lastCheck(t, source.ID)
	if update.ETag != nil || update.LastHash != nil {
		t.Fatalf("old data не должны меняться: %+v", update)
	}
	if update.FailureCount != 1 {
		t.Fatalf("failure count: %+v", update)
	}
}

func TestCheckerRunOnceRespectsLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	for i := 0; i < 7; i++ {
		store.add(Source{
			ID:            fmt.Sprintf("source-%02d", i),
			ProgramKey:    "mirea/09.03.01/2024/full-time",
			URL:           server.URL + "/program.csv",
			IntervalHours: 24,
			Enabled:       true,
		})
	}
	stats, err := checker.RunOnce(context.Background(), 5)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fetched != 5 || stats.Unchanged != 5 {
		t.Fatalf("stats: %+v", stats)
	}
	store.mu.Lock()
	saved := len(store.checks)
	store.mu.Unlock()
	if saved != 5 {
		t.Fatalf("сохранено проверок: %d", saved)
	}
}

func TestNormalizeCreateInput(t *testing.T) {
	input, err := NormalizeCreateInput(CreateInput{ProgramKey: "mirea/09.03.01/2024/full-time", URL: " https://example.com/program.xlsx "})
	if err != nil {
		t.Fatalf("NormalizeCreateInput: %v", err)
	}
	if input.URL != "https://example.com/program.xlsx" || input.IntervalHours != 24 {
		t.Fatalf("input: %+v", input)
	}
	for _, raw := range []string{"ftp://example.com/program.csv", "example.com/program.csv", "https:///program.csv"} {
		if _, err := NormalizeCreateInput(CreateInput{ProgramKey: "mirea/09.03.01/2024/full-time", URL: raw}); err == nil {
			t.Errorf("URL %q должен быть отклонён", raw)
		}
	}
	if _, err := NormalizeCreateInput(CreateInput{ProgramKey: "mirea/09.03.01/2024/full-time", URL: "https://example.com/a", IntervalHours: 721}); err == nil {
		t.Error("interval_hours 721 должен быть отклонён")
	}
}

func TestErrorsAreDistinguishable(t *testing.T) {
	if !errors.Is(fmt.Errorf("wrap: %w", ErrNotFound), ErrNotFound) {
		t.Fatal("errors.Is должен видеть ErrNotFound")
	}
}
