package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/programwatch"
)

type fakeProgramWatchStore struct {
	Store
	mu         sync.Mutex
	sources    map[string]programwatch.Source
	checks     map[string]programwatch.CheckUpdate
	locks      map[string]string
	busy       map[string]bool
	releases   []string
	drafts     []programwatch.DraftInput
	claimOwner string
	claimLease time.Duration
	seq        int
}

func newFakeProgramWatchStore() *fakeProgramWatchStore {
	return &fakeProgramWatchStore{
		sources: map[string]programwatch.Source{},
		checks:  map[string]programwatch.CheckUpdate{},
		locks:   map[string]string{},
		busy:    map[string]bool{},
	}
}

func (f *fakeProgramWatchStore) add(source programwatch.Source) programwatch.Source {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(source)
}

func (f *fakeProgramWatchStore) addLocked(source programwatch.Source) programwatch.Source {
	f.seq++
	if source.ID == "" {
		source.ID = fmt.Sprintf("aaaaaaaa-aaaa-aaaa-aaaa-%012d", f.seq)
	}
	if source.IntervalHours == 0 {
		source.IntervalHours = 24
	}
	source.Enabled = true
	f.sources[source.ID] = source
	return source
}

func (f *fakeProgramWatchStore) CreateWatchSource(_ context.Context, input programwatch.CreateInput) (programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, source := range f.sources {
		if source.ProgramKey == input.ProgramKey && source.URL == input.URL {
			return programwatch.Source{}, programwatch.ErrDuplicate
		}
	}
	return f.addLocked(programwatch.Source{
		ProgramKey:    input.ProgramKey,
		URL:           input.URL,
		DocFormat:     input.DocFormat,
		IntervalHours: input.IntervalHours,
		Enabled:       true,
	}), nil
}

func (f *fakeProgramWatchStore) ListWatchSources(_ context.Context) ([]programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]programwatch.Source, 0, len(f.sources))
	for _, source := range f.sources {
		items = append(items, source)
	}
	return items, nil
}

func (f *fakeProgramWatchStore) GetWatchSource(_ context.Context, id string) (programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return programwatch.Source{}, programwatch.ErrNotFound
	}
	return source, nil
}

func (f *fakeProgramWatchStore) UpdateWatchSource(_ context.Context, id string, patch programwatch.SourcePatch) (programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return programwatch.Source{}, programwatch.ErrNotFound
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

func (f *fakeProgramWatchStore) DeleteWatchSource(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sources[id]; !ok {
		return programwatch.ErrNotFound
	}
	delete(f.sources, id)
	return nil
}

func (f *fakeProgramWatchStore) DueWatchSources(_ context.Context, now time.Time, limit int) ([]programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]programwatch.Source, 0, len(f.sources))
	for _, source := range f.sources {
		items = append(items, source)
	}
	return programwatch.SelectDue(items, now, limit), nil
}

func (f *fakeProgramWatchStore) ClaimDueWatchSources(_ context.Context, now time.Time, limit int, lease time.Duration, owner string) ([]programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimOwner = owner
	f.claimLease = lease
	items := make([]programwatch.Source, 0, len(f.sources))
	for _, source := range f.sources {
		items = append(items, source)
	}
	claimed := []programwatch.Source{}
	for _, source := range programwatch.SelectDue(items, now, limit) {
		if f.locks[source.ID] != "" {
			continue
		}
		f.locks[source.ID] = owner
		claimed = append(claimed, source)
	}
	return claimed, nil
}

func (f *fakeProgramWatchStore) ClaimWatchSource(_ context.Context, id string, lease time.Duration, owner string) (programwatch.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimOwner = owner
	f.claimLease = lease
	source, ok := f.sources[id]
	if !ok {
		return programwatch.Source{}, programwatch.ErrNotFound
	}
	if f.busy[id] || f.locks[id] != "" {
		return programwatch.Source{}, programwatch.ErrWatchSourceBusy
	}
	f.locks[id] = owner
	return source, nil
}

func (f *fakeProgramWatchStore) ReleaseWatchSource(_ context.Context, id, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, id)
	if f.locks[id] == owner {
		delete(f.locks, id)
	}
	return nil
}

func (f *fakeProgramWatchStore) SaveWatchCheck(_ context.Context, id string, update programwatch.CheckUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.sources[id]
	if !ok {
		return programwatch.ErrNotFound
	}
	f.checks[id] = update
	source.LastStatus = update.Status
	source.LastError = update.Error
	source.FailureCount = update.FailureCount
	if update.DraftID != "" {
		source.DraftID = update.DraftID
	}
	if update.ETag != nil {
		source.ETag = *update.ETag
	}
	if update.LastHash != nil {
		source.LastHash = *update.LastHash
	}
	if f.locks[id] == update.Owner {
		delete(f.locks, id)
	}
	f.sources[id] = source
	return nil
}

func (f *fakeProgramWatchStore) CreateProgramWatchDraft(_ context.Context, input programwatch.DraftInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drafts = append(f.drafts, input)
	return "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", nil
}

var _ programwatch.Store = (*fakeProgramWatchStore)(nil)
var _ programwatch.DraftCreator = (*fakeProgramWatchStore)(nil)

func doProgramSourceJSON(t *testing.T, handler http.Handler, method, path, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doProgramJSON(t, handler, method, path, role, body)
}

func TestProgramSourcesCRUD(t *testing.T) {
	store := newFakeProgramWatchStore()
	handler := NewServer(store).Routes()

	create := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources", "admin",
		`{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.xlsx","interval_hours":12,"doc_format":"xlsx"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("создание: %d (%s)", create.Code, create.Body.String())
	}
	var created programwatch.Source
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if created.ID == "" || created.IntervalHours != 12 || created.ProgramKey != "mirea/09.03.01/2024/full-time" || !created.Enabled {
		t.Fatalf("источник: %+v", created)
	}
	if created.DocFormat != "xlsx" {
		t.Fatalf("doc_format: %+v", created)
	}

	duplicate := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources", "admin",
		`{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.xlsx"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("дубль: ожидали 409, получили %d (%s)", duplicate.Code, duplicate.Body.String())
	}

	list := doProgramSourceJSON(t, handler, http.MethodGet, "/api/v1/admin/program-sources", "admin", "")
	if list.Code != http.StatusOK {
		t.Fatalf("список: %d (%s)", list.Code, list.Body.String())
	}
	var items []programwatch.Source
	if err := json.Unmarshal(list.Body.Bytes(), &items); err != nil {
		t.Fatalf("список не JSON: %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("список: %+v", items)
	}

	update := doProgramSourceJSON(t, handler, http.MethodPatch, "/api/v1/admin/program-sources/"+created.ID, "admin",
		`{"enabled":false,"interval_hours":6}`)
	if update.Code != http.StatusOK {
		t.Fatalf("обновление: %d (%s)", update.Code, update.Body.String())
	}
	var updated programwatch.Source
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatalf("обновление не JSON: %v", err)
	}
	if updated.Enabled || updated.IntervalHours != 6 {
		t.Fatalf("обновление: %+v", updated)
	}

	remove := doProgramSourceJSON(t, handler, http.MethodDelete, "/api/v1/admin/program-sources/"+created.ID, "admin", "")
	if remove.Code != http.StatusNoContent {
		t.Fatalf("удаление: ожидали 204, получили %d (%s)", remove.Code, remove.Body.String())
	}
	missing := doProgramSourceJSON(t, handler, http.MethodDelete, "/api/v1/admin/program-sources/"+created.ID, "admin", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("повторное удаление: ожидали 404, получили %d", missing.Code)
	}
}

func TestProgramSourcesValidation(t *testing.T) {
	handler := NewServer(newFakeProgramWatchStore()).Routes()
	cases := []struct {
		name string
		body string
	}{
		{"старый ключ", `{"program_key":"prog-key","url":"https://example.com/program.csv"}`},
		{"неполный ключ", `{"program_key":"mirea/09.03.01/2024","url":"https://example.com/program.csv"}`},
		{"не http url", `{"program_key":"mirea/09.03.01/2024/full-time","url":"ftp://example.com/program.csv"}`},
		{"url без схемы", `{"program_key":"mirea/09.03.01/2024/full-time","url":"example.com/program.csv"}`},
		{"interval 0", `{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.csv","interval_hours":0}`},
		{"interval 721", `{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.csv","interval_hours":721}`},
		{"плохой doc_format", `{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.csv","doc_format":"exe"}`},
		{"пустое тело", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources", "admin", tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("ожидали 400, получили %d (%s)", recorder.Code, recorder.Body.String())
			}
		})
	}
	badKey := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources", "admin",
		`{"program_key":"prog-key","url":"https://example.com/program.csv"}`)
	if !strings.Contains(badKey.Body.String(), "mirea/09.03.01/2024/full-time") {
		t.Fatalf("400 должен содержать пример формата: %s", badKey.Body.String())
	}

	forbidden := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources", "user",
		`{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.csv"}`)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("не admin: ожидали 403, получили %d", forbidden.Code)
	}
}

func TestProgramSourcePatchValidation(t *testing.T) {
	store := newFakeProgramWatchStore()
	source := store.add(programwatch.Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: "https://example.com/program.csv"})
	handler := NewServer(store).Routes()

	for _, body := range []string{`{}`, `{"interval_hours":0}`, `{"interval_hours":999}`, `not-json`} {
		recorder := doProgramSourceJSON(t, handler, http.MethodPatch, "/api/v1/admin/program-sources/"+source.ID, "admin", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q: ожидали 400, получили %d (%s)", body, recorder.Code, recorder.Body.String())
		}
	}
	missing := doProgramSourceJSON(t, handler, http.MethodPatch, "/api/v1/admin/program-sources/cccccccc-cccc-cccc-cccc-cccccccccccc", "admin", `{"enabled":true}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("неизвестный источник: ожидали 404, получили %d", missing.Code)
	}
}

func TestProgramSourceCheckCreatesDraft(t *testing.T) {
	document := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(validProgramCSV))
	}))
	defer document.Close()

	store := newFakeProgramWatchStore()
	source := store.add(programwatch.Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           document.URL + "/program.csv",
		IntervalHours: 24,
		Enabled:       true,
	})
	handler := NewServer(store).Routes()
	recorder := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources/"+source.ID+"/check", "admin", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("проверка: %d (%s)", recorder.Code, recorder.Body.String())
	}
	var result programwatch.CheckResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if result.Status != programwatch.StatusChanged || result.DraftID == "" {
		t.Fatalf("результат: %+v", result)
	}
	store.mu.Lock()
	drafts := append([]programwatch.DraftInput(nil), store.drafts...)
	checks := len(store.checks)
	store.mu.Unlock()
	if len(drafts) != 1 || drafts[0].ProgramKey != source.ProgramKey || checks != 1 {
		t.Fatalf("drafts=%+v checks=%d", drafts, checks)
	}
	store.mu.Lock()
	lease := store.claimLease
	locks := len(store.locks)
	store.mu.Unlock()
	if lease != programSourceCheckLease {
		t.Fatalf("ручная проверка должна брать lease %s, получили %s", programSourceCheckLease, lease)
	}
	if locks != 0 {
		t.Fatalf("после успешной проверки блокировка должна быть снята: %d", locks)
	}

	missing := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources/cccccccc-cccc-cccc-cccc-cccccccccccc/check", "admin", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("неизвестный источник: ожидали 404, получили %d", missing.Code)
	}
}

func TestProgramSourceCheckBusyReturns409(t *testing.T) {
	store := newFakeProgramWatchStore()
	source := store.add(programwatch.Source{
		ProgramKey:    "mirea/09.03.01/2024/full-time",
		URL:           "https://example.com/program.csv",
		IntervalHours: 24,
		Enabled:       true,
	})
	store.mu.Lock()
	store.busy[source.ID] = true
	store.mu.Unlock()
	handler := NewServer(store).Routes()
	recorder := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources/"+source.ID+"/check", "admin", "")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("занятый источник: ожидали 409, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Code != "PROGRAM_SOURCE_BUSY" {
		t.Fatalf("code = %q", body.Code)
	}
	if !strings.Contains(body.Message, "источник уже проверяется") {
		t.Fatalf("message = %q", body.Message)
	}
	store.mu.Lock()
	checks := len(store.checks)
	drafts := len(store.drafts)
	store.mu.Unlock()
	if checks != 0 || drafts != 0 {
		t.Fatalf("занятый источник не должен проверяться: checks=%d drafts=%d", checks, drafts)
	}
}

func TestProgramSourcesRequirePostgres(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/admin/program-sources", `{"program_key":"mirea/09.03.01/2024/full-time","url":"https://example.com/program.csv"}`},
		{http.MethodGet, "/api/v1/admin/program-sources", ""},
		{http.MethodPatch, "/api/v1/admin/program-sources/cccccccc-cccc-cccc-cccc-cccccccccccc", `{"enabled":false}`},
		{http.MethodDelete, "/api/v1/admin/program-sources/cccccccc-cccc-cccc-cccc-cccccccccccc", ""},
		{http.MethodPost, "/api/v1/admin/program-sources/cccccccc-cccc-cccc-cccc-cccccccccccc/check", ""},
	}
	for _, route := range routes {
		recorder := doProgramSourceJSON(t, handler, route.method, route.path, "admin", route.body)
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s: ожидали 501, получили %d (%s)", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProgramSourceCheckWithoutDraftCreatorReturns501(t *testing.T) {
	watch := newFakeProgramWatchStore()
	store := &watchStoreOnly{watch: watch}
	source := watch.add(programwatch.Source{ProgramKey: "mirea/09.03.01/2024/full-time", URL: "https://example.com/program.csv"})
	handler := NewServer(store).Routes()
	recorder := doProgramSourceJSON(t, handler, http.MethodPost, "/api/v1/admin/program-sources/"+source.ID+"/check", "admin", "")
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("без создателя черновиков: ожидали 501, получили %d (%s)", recorder.Code, recorder.Body.String())
	}
}

// watchStoreOnly намеренно не реализует programwatch.DraftCreator: он
// пробрасывает только методы реестра.
type watchStoreOnly struct {
	Store
	watch *fakeProgramWatchStore
}

func (w *watchStoreOnly) CreateWatchSource(ctx context.Context, input programwatch.CreateInput) (programwatch.Source, error) {
	return w.watch.CreateWatchSource(ctx, input)
}
func (w *watchStoreOnly) ListWatchSources(ctx context.Context) ([]programwatch.Source, error) {
	return w.watch.ListWatchSources(ctx)
}
func (w *watchStoreOnly) GetWatchSource(ctx context.Context, id string) (programwatch.Source, error) {
	return w.watch.GetWatchSource(ctx, id)
}
func (w *watchStoreOnly) UpdateWatchSource(ctx context.Context, id string, patch programwatch.SourcePatch) (programwatch.Source, error) {
	return w.watch.UpdateWatchSource(ctx, id, patch)
}
func (w *watchStoreOnly) DeleteWatchSource(ctx context.Context, id string) error {
	return w.watch.DeleteWatchSource(ctx, id)
}
func (w *watchStoreOnly) DueWatchSources(ctx context.Context, now time.Time, limit int) ([]programwatch.Source, error) {
	return w.watch.DueWatchSources(ctx, now, limit)
}
func (w *watchStoreOnly) ClaimDueWatchSources(ctx context.Context, now time.Time, limit int, lease time.Duration, owner string) ([]programwatch.Source, error) {
	return w.watch.ClaimDueWatchSources(ctx, now, limit, lease, owner)
}
func (w *watchStoreOnly) ClaimWatchSource(ctx context.Context, id string, lease time.Duration, owner string) (programwatch.Source, error) {
	return w.watch.ClaimWatchSource(ctx, id, lease, owner)
}
func (w *watchStoreOnly) ReleaseWatchSource(ctx context.Context, id, owner string) error {
	return w.watch.ReleaseWatchSource(ctx, id, owner)
}
func (w *watchStoreOnly) SaveWatchCheck(ctx context.Context, id string, update programwatch.CheckUpdate) error {
	return w.watch.SaveWatchCheck(ctx, id, update)
}
