package programwatch

import (
	"bytes"
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

	"activity-descriptor/internal/fgos"
	"activity-descriptor/internal/ingestion"
)

const testCSV = "kind;code;title;parent_code;taxonomy_codes;type\n" +
	"program;P1;Программа;;;\n" +
	"competency;K1;Компетенция;;;ПК\n" +
	"knowledge;K1;Знать стандарт;K1;;\n"

var testNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const testOwner = "test-owner"

type fakeWatchStore struct {
	mu         sync.Mutex
	sources    map[string]Source
	checks     map[string]CheckUpdate
	locks      map[string]string
	releases   []string
	saveErrs   map[string]error
	claim      []Source
	claimOwner string
	claimLease time.Duration
	seq        int
}

func newFakeWatchStore() *fakeWatchStore {
	return &fakeWatchStore{
		sources:  map[string]Source{},
		checks:   map[string]CheckUpdate{},
		locks:    map[string]string{},
		saveErrs: map[string]error{},
	}
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

func (f *fakeWatchStore) ClaimDueWatchSources(_ context.Context, now time.Time, limit int, lease time.Duration, owner string) ([]Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimOwner = owner
	f.claimLease = lease
	candidates := f.claim
	if candidates == nil {
		all := make([]Source, 0, len(f.sources))
		for _, source := range f.sources {
			all = append(all, source)
		}
		candidates = SelectDue(all, now, limit)
	}
	claimed := []Source{}
	for _, source := range candidates {
		if len(claimed) >= limit {
			break
		}
		if f.locks[source.ID] != "" {
			continue
		}
		f.locks[source.ID] = owner
		claimed = append(claimed, source)
	}
	return claimed, nil
}

func (f *fakeWatchStore) ClaimWatchSource(_ context.Context, id string, lease time.Duration, owner string) (Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimOwner = owner
	f.claimLease = lease
	source, ok := f.sources[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	if f.locks[id] != "" {
		return Source{}, ErrWatchSourceBusy
	}
	f.locks[id] = owner
	return source, nil
}

func (f *fakeWatchStore) ReleaseWatchSource(_ context.Context, id, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, id)
	if f.locks[id] == owner {
		delete(f.locks, id)
	}
	return nil
}

func (f *fakeWatchStore) SaveWatchCheck(_ context.Context, id string, update CheckUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.saveErrs[id]; err != nil {
		return err
	}
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
	if update.DocumentURL != nil {
		source.DocumentURL = *update.DocumentURL
	}
	if update.DocumentHash != nil {
		source.DocumentHash = *update.DocumentHash
	}
	if update.DocumentETag != nil {
		source.DocumentETag = *update.DocumentETag
	}
	if update.DocumentLastModified != nil {
		source.DocumentLastModified = *update.DocumentLastModified
	}
	if f.locks[id] == update.Owner {
		delete(f.locks, id)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	if update.Owner != testOwner {
		t.Fatalf("SaveWatchCheck должен получить owner блокировки: %+v", update)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	result, err = checker.CheckAndSave(context.Background(), updated, testOwner)
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
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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

func TestRunOnceChecksOnlyClaimedSources(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	checker.Owner = "worker-test"
	checker.Lease = 2 * time.Minute
	claimed := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.csv", IntervalHours: 24, Enabled: true})
	store.add(Source{ProgramKey: "mirea/09.03.02/2024/full-time", URL: server.URL + "/program.csv", IntervalHours: 24, Enabled: true})
	// Claim вернул только один источник: второй уже занят другим воркером.
	store.claim = []Source{claimed}

	stats, err := checker.RunOnce(context.Background(), 5)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fetched != 1 || stats.Unchanged != 1 {
		t.Fatalf("должен проверяться только claim-результат: %+v", stats)
	}
	store.mu.Lock()
	_, saved := store.checks[claimed.ID]
	savedCount := len(store.checks)
	owner := store.claimOwner
	lease := store.claimLease
	locks := len(store.locks)
	store.mu.Unlock()
	if !saved || savedCount != 1 {
		t.Fatalf("сохранены проверки: %d, нужный источник: %v", savedCount, saved)
	}
	if owner != "worker-test" || lease != 2*time.Minute {
		t.Fatalf("claim должен получить owner и lease: owner=%q lease=%s", owner, lease)
	}
	if locks != 0 {
		t.Fatalf("после успешной проверки блокировки не должно остаться: %d", locks)
	}
}

func TestRunOnceReleasesClaimOnSaveError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	good := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.csv", IntervalHours: 24, Enabled: true})
	bad := store.add(Source{ProgramKey: "mirea/09.03.02/2024/full-time", URL: server.URL + "/program.csv", IntervalHours: 24, Enabled: true})
	store.claim = []Source{good, bad}
	store.saveErrs[bad.ID] = errors.New("db down")

	stats, err := checker.RunOnce(context.Background(), 5)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fetched != 2 || stats.Errors != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	store.mu.Lock()
	_, goodSaved := store.checks[good.ID]
	releases := append([]string(nil), store.releases...)
	locks := len(store.locks)
	store.mu.Unlock()
	if !goodSaved {
		t.Fatal("успешный источник должен сохраниться")
	}
	if len(releases) != 1 || releases[0] != bad.ID {
		t.Fatalf("при ошибке сохранения должен быть Release только проблемного источника: %v", releases)
	}
	if locks != 0 {
		t.Fatalf("блокировки должны быть сняты: %d", locks)
	}
}

func TestCheckAndSaveReleasesLockOnSaveError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.csv", IntervalHours: 24, Enabled: true})
	store.saveErrs[source.ID] = errors.New("db down")

	if _, err := checker.CheckAndSave(context.Background(), source, "owner-x"); err == nil {
		t.Fatal("ожидали ошибку сохранения")
	}
	store.mu.Lock()
	releases := append([]string(nil), store.releases...)
	store.mu.Unlock()
	if len(releases) != 1 || releases[0] != source.ID {
		t.Fatalf("Release после ошибки сохранения: %v", releases)
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

func TestCheckerSavesStatusAndError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken.csv" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write([]byte(testCSV))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)

	changed := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/program.csv",
		IntervalHours: 24,
		Enabled:       true,
	})
	if _, err := checker.CheckAndSave(context.Background(), changed, testOwner); err != nil {
		t.Fatalf("CheckAndSave changed: %v", err)
	}
	update := store.lastCheck(t, changed.ID)
	if update.Status != StatusChanged {
		t.Fatalf("last_status = %q, ожидали %q", update.Status, StatusChanged)
	}
	if update.DraftID == "" {
		t.Fatal("draft_id должен сохраняться при changed")
	}

	broken := store.add(Source{
		ProgramKey:    "mirea/09.03.02/2024/full-time",
		URL:           server.URL + "/broken.csv",
		IntervalHours: 24,
		Enabled:       true,
	})
	if _, err := checker.CheckAndSave(context.Background(), broken, testOwner); err != nil {
		t.Fatalf("CheckAndSave broken: %v", err)
	}
	update = store.lastCheck(t, broken.ID)
	if update.Status != StatusError {
		t.Fatalf("last_status = %q, ожидали %q", update.Status, StatusError)
	}
	if update.Error == "" {
		t.Fatal("last_error должен сохраняться при ошибке")
	}
}

// mutableBody — тело документа и его ETag, которые тест меняет между
// проверками. Мьютекс нужен, потому что handler работает в горутине сервера.
type mutableBody struct {
	mu   sync.Mutex
	body []byte
	etag string
}

func (m *mutableBody) set(body []byte, etag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.body = body
	m.etag = etag
}

func (m *mutableBody) get() ([]byte, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.body, m.etag
}

func hashOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func secondProgramCSV() []byte {
	return []byte(testCSV + "competency;K2;Вторая компетенция;;;ОПК\n")
}

// TestCheckerPageUnchangedDocumentChanged — основной сценарий исправления:
// HTML-страница не меняется, а привязанный документ обновился. Раньше такая
// проверка возвращала unchanged по хэшу страницы и изменение терялось.
func TestCheckerPageUnchangedDocumentChanged(t *testing.T) {
	page := []byte(`<html><body><a href="/files/program.csv">Скачать программу</a></body></html>`)
	document := &mutableBody{}
	document.set([]byte(testCSV), "")
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/files/program.csv", func(w http.ResponseWriter, _ *http.Request) {
		body, etag := document.get()
		w.Header().Set("Content-Type", "text/csv")
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, mux, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/page",
		IntervalHours: 24,
		Enabled:       true,
	})

	first, err := checker.CheckAndSave(context.Background(), source, testOwner)
	if err != nil {
		t.Fatalf("первая проверка: %v", err)
	}
	if first.Status != StatusChanged {
		t.Fatalf("первая проверка: %+v", first)
	}
	afterFirst, err := store.GetWatchSource(context.Background(), source.ID)
	if err != nil {
		t.Fatalf("GetWatchSource: %v", err)
	}
	wantURLCheck := server.URL + "/files/program.csv"
	if afterFirst.DocumentURL != wantURLCheck {
		t.Fatalf("document_url = %q, ожидали %q", afterFirst.DocumentURL, wantURLCheck)
	}
	if afterFirst.DocumentHash != hashOf([]byte(testCSV)) {
		t.Fatalf("document_hash = %q", afterFirst.DocumentHash)
	}
	if afterFirst.LastHash == "" {
		t.Fatal("last_hash страницы должен сохраняться")
	}

	// Документ обновился, страница — нет.
	document.set(secondProgramCSV(), "")
	second, err := checker.CheckAndSave(context.Background(), afterFirst, testOwner)
	if err != nil {
		t.Fatalf("вторая проверка: %v", err)
	}
	if second.Status != StatusChanged {
		t.Fatalf("изменение документа при неизменной странице: %+v", second)
	}
	if len(drafts.inputs) != 2 {
		t.Fatalf("ожидали два черновика, получили %d", len(drafts.inputs))
	}
	update := store.lastCheck(t, source.ID)
	if update.DocumentHash == nil || *update.DocumentHash != hashOf(secondProgramCSV()) {
		t.Fatalf("document_hash должен обновиться: %+v", update.DocumentHash)
	}
}

// TestCheckerDocumentUnchangedPageChanged — обратный случай: страница
// изменилась, документ тот же. Черновик не создаётся, статус unchanged.
func TestCheckerDocumentUnchangedPageChanged(t *testing.T) {
	firstPage := []byte(`<html><body><a href="/files/program.csv">Скачать</a></body></html>`)
	secondPage := []byte(`<html><body><p>Обновлено сегодня</p><a href="/files/program.csv">Скачать</a></body></html>`)
	document := []byte(testCSV)
	pages := [][]byte{firstPage, secondPage}
	var mu sync.Mutex
	pageIndex := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := pages[pageIndex]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/files/program.csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(document)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, mux, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/page",
		IntervalHours: 24,
		Enabled:       true,
	})
	if _, err := checker.CheckAndSave(context.Background(), source, testOwner); err != nil {
		t.Fatalf("первая проверка: %v", err)
	}
	updated, _ := store.GetWatchSource(context.Background(), source.ID)

	mu.Lock()
	pageIndex = 1
	mu.Unlock()
	result, err := checker.CheckAndSave(context.Background(), updated, testOwner)
	if err != nil {
		t.Fatalf("вторая проверка: %v", err)
	}
	if result.Status != StatusUnchanged {
		t.Fatalf("неизменный документ должен давать unchanged: %+v", result)
	}
	if len(drafts.inputs) != 1 {
		t.Fatalf("новый черновик не должен создаваться: %d", len(drafts.inputs))
	}
	update := store.lastCheck(t, source.ID)
	if update.LastHash == nil || *update.LastHash != hashOf(secondPage) {
		t.Fatalf("last_hash должен обновиться до хэша новой страницы: %+v", update.LastHash)
	}
}

// TestCheckerDocumentNotModifiedByETag — документ отвечает 304 на
// If-None-Match из сохранённого document_etag, хотя страница изменилась.
func TestCheckerDocumentNotModifiedByETag(t *testing.T) {
	const documentETag = `"doc-v1"`
	document := []byte(testCSV)
	page := []byte(`<html><body><a href="/files/program.csv">Скачать</a></body></html>`)
	newPage := []byte(`<html><body><p>Новость</p><a href="/files/program.csv">Скачать</a></body></html>`)
	pages := [][]byte{page, newPage}
	var mu sync.Mutex
	pageIndex := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := pages[pageIndex]
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/files/program.csv", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == documentETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("ETag", documentETag)
		_, _ = w.Write(document)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, mux, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/page",
		IntervalHours: 24,
		Enabled:       true,
	})
	if _, err := checker.CheckAndSave(context.Background(), source, testOwner); err != nil {
		t.Fatalf("первая проверка: %v", err)
	}
	afterFirst, _ := store.GetWatchSource(context.Background(), source.ID)
	if afterFirst.DocumentETag != documentETag {
		t.Fatalf("document_etag = %q", afterFirst.DocumentETag)
	}

	mu.Lock()
	pageIndex = 1
	mu.Unlock()
	result, err := checker.CheckAndSave(context.Background(), afterFirst, testOwner)
	if err != nil {
		t.Fatalf("вторая проверка: %v", err)
	}
	if result.Status != StatusUnchanged {
		t.Fatalf("304 документа должен давать unchanged: %+v", result)
	}
	if len(drafts.inputs) != 1 {
		t.Fatalf("черновик не должен создаваться при 304 документа: %d", len(drafts.inputs))
	}
}

// TestCheckerPageNotModifiedChecksDocument — страница отвечает 304, но
// сохранённый document_url проверяется отдельно: обновление документа не
// теряется.
func TestCheckerPageNotModifiedChecksDocument(t *testing.T) {
	const pageETag = `"page-v1"`
	page := []byte(`<html><body><a href="/files/program.csv">Скачать</a></body></html>`)
	document := &mutableBody{}
	document.set([]byte(testCSV), "")
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == pageETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", pageETag)
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/files/program.csv", func(w http.ResponseWriter, _ *http.Request) {
		body, etag := document.get()
		w.Header().Set("Content-Type", "text/csv")
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, mux, store, drafts)
	source := store.add(Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           server.URL + "/page",
		IntervalHours: 24,
		Enabled:       true,
	})
	first, err := checker.CheckAndSave(context.Background(), source, testOwner)
	if err != nil || first.Status != StatusChanged {
		t.Fatalf("первая проверка: %+v, %v", first, err)
	}
	afterFirst, _ := store.GetWatchSource(context.Background(), source.ID)

	document.set(secondProgramCSV(), "")
	result, err := checker.CheckAndSave(context.Background(), afterFirst, testOwner)
	if err != nil {
		t.Fatalf("вторая проверка: %v", err)
	}
	if result.Status != StatusChanged {
		t.Fatalf("изменение документа при странице 304: %+v", result)
	}
	if len(drafts.inputs) != 2 {
		t.Fatalf("ожидали второй черновик: %d", len(drafts.inputs))
	}
}

// TestCheckerPDFNeedsParserKeepsDocumentURL — при статусе needs_parser ссылка на
// документ сохраняется, чтобы включённый позже адаптер сразу её использовал.
func TestCheckerPDFNeedsParserKeepsDocumentURL(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	})
	store := newFakeWatchStore()
	checker, server := newCheckerServer(t, handler, store, &fakeDrafts{})
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.pdf", IntervalHours: 24, Enabled: true})
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusNeedsParser {
		t.Fatalf("result: %+v", result)
	}
	update := store.lastCheck(t, source.ID)
	if update.DocumentURL == nil || *update.DocumentURL != source.URL {
		t.Fatalf("document_url должен сохраняться: %+v", update.DocumentURL)
	}
	if update.LastHash != nil || update.ETag != nil {
		t.Fatalf("хэш страницы не должен меняться при needs_parser: %+v", update)
	}
}

// fakePDFExtractor — LLM-адаптер ФГОС без сети: возвращает заранее заданную
// программу и страницы, запоминает входные данные.
type fakePDFExtractor struct {
	mu           sync.Mutex
	pages        []fgos.Page
	program      ingestion.EducationProgram
	warnings     []string
	pagesErr     error
	extractErr   error
	pagesCalls   int
	extractCalls int
	lastName     string
	lastData     []byte
	lastPages    []fgos.Page
}

func (f *fakePDFExtractor) ExtractPages(name string, data []byte) ([]fgos.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pagesCalls++
	f.lastName = name
	f.lastData = append([]byte(nil), data...)
	if f.pagesErr != nil {
		return nil, f.pagesErr
	}
	return f.pages, nil
}

func (f *fakePDFExtractor) Extract(_ context.Context, pages []fgos.Page) (ingestion.EducationProgram, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extractCalls++
	f.lastPages = pages
	if f.extractErr != nil {
		return ingestion.EducationProgram{}, nil, f.extractErr
	}
	return f.program, f.warnings, nil
}

func testFGOSProgram() ingestion.EducationProgram {
	return ingestion.EducationProgram{
		Competencies: []ingestion.ProgramCompetency{{
			Code:  "УК-1",
			Title: "Способен осуществлять поиск и критический анализ информации",
			Type:  "universal",
			KSA:   []ingestion.ProgramKSA{{Kind: "knowledge", Title: "методы системного анализа"}},
		}},
	}
}

func TestCheckerPDFWithExtractorCreatesDraft(t *testing.T) {
	body := []byte("%PDF-1.7 fake")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	extractor := &fakePDFExtractor{
		pages:    []fgos.Page{{Number: 1, Text: "УК-1. Способен осуществлять поиск."}},
		program:  testFGOSProgram(),
		warnings: []string{"фрагмент 1: предупреждение адаптера"},
	}
	checker.PDFExtractor = extractor
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.pdf", IntervalHours: 24, Enabled: true})

	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
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
	if input.SourceFormat != "pdf" || input.SourceName != "program.pdf" {
		t.Fatalf("draft input: %+v", input)
	}
	if input.Program.Title != "program" {
		t.Fatalf("title должен браться из имени файла: %q", input.Program.Title)
	}
	if input.Program.Code != source.ProgramKey {
		t.Fatalf("код программы: %q", input.Program.Code)
	}
	if input.Competencies != 1 || input.KSA != 1 {
		t.Fatalf("счётчики: %+v", input)
	}
	if len(input.Warnings) != 1 || input.Warnings[0] != "фрагмент 1: предупреждение адаптера" {
		t.Fatalf("предупреждения: %v", input.Warnings)
	}
	if extractor.pagesCalls != 1 || extractor.extractCalls != 1 {
		t.Fatalf("вызовы адаптера: pages=%d extract=%d", extractor.pagesCalls, extractor.extractCalls)
	}
	if extractor.lastName != "program.pdf" || !bytes.Equal(extractor.lastData, body) {
		t.Fatalf("адаптер получил name=%q data=%q", extractor.lastName, extractor.lastData)
	}
	update := store.lastCheck(t, source.ID)
	if update.DocumentURL == nil || *update.DocumentURL != source.URL {
		t.Fatalf("document_url: %+v", update.DocumentURL)
	}
	if update.DocumentHash == nil || *update.DocumentHash != hashOf(body) {
		t.Fatalf("document_hash: %+v", update.DocumentHash)
	}
	if update.LastHash == nil || *update.LastHash != hashOf(body) {
		t.Fatalf("last_hash: %+v", update.LastHash)
	}
}

func TestCheckerDOCXWithExtractorAddsPageNumberWarning(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		_, _ = w.Write([]byte("PK fake docx"))
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	checker.PDFExtractor = &fakePDFExtractor{
		pages:   []fgos.Page{{Number: 1, Text: "ОПК-2. Способен разрабатывать ПО."}},
		program: testFGOSProgram(),
	}
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/program.docx", IntervalHours: 24, Enabled: true})
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusChanged {
		t.Fatalf("result: %+v", result)
	}
	input := drafts.inputs[0]
	if input.SourceFormat != "docx" {
		t.Fatalf("source_format: %q", input.SourceFormat)
	}
	if len(input.Warnings) == 0 || input.Warnings[0] != fgos.ApproximatePageNumbersWarning {
		t.Fatalf("нет предупреждения о приблизительных страницах: %v", input.Warnings)
	}
}

func TestCheckerPDFExtractorErrors(t *testing.T) {
	body := []byte("%PDF-1.7 fake")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	})
	store := newFakeWatchStore()
	drafts := &fakeDrafts{}
	checker, server := newCheckerServer(t, handler, store, drafts)
	checker.PDFExtractor = &fakePDFExtractor{pagesErr: errors.New("нет текстового слоя, нужен OCR")}
	source := store.add(Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: server.URL + "/scan.pdf", IntervalHours: 24, Enabled: true})
	result, err := checker.CheckAndSave(context.Background(), source, testOwner)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusError || !strings.Contains(result.Error, "не удалось прочитать PDF") {
		t.Fatalf("result: %+v", result)
	}
	if len(drafts.inputs) != 0 {
		t.Fatalf("черновик не должен создаваться: %+v", drafts.inputs)
	}

	checker.PDFExtractor = &fakePDFExtractor{extractErr: errors.New("не удалось извлечь компетенции")}
	updated, getErr := store.GetWatchSource(context.Background(), source.ID)
	if getErr != nil {
		t.Fatalf("GetWatchSource: %v", getErr)
	}
	result, err = checker.CheckAndSave(context.Background(), updated, testOwner)
	if err != nil {
		t.Fatalf("CheckAndSave: %v", err)
	}
	if result.Status != StatusError || !strings.Contains(result.Error, "не удалось извлечь программу") {
		t.Fatalf("result: %+v", result)
	}
}
