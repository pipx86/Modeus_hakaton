package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"activity-descriptor/internal/domain"
)

// Проверка результата методистом: разбор ожидаемого результата (карточка 25) и
// границы эндпоинтов проверки (карточки 18 и 23).

func TestNormalizeRunResultDefaultsToActivity(t *testing.T) {
	kind, program, err := normalizeRunResult("", "")
	if err != nil {
		t.Fatalf("пустой режим не должен быть ошибкой: %v", err)
	}
	if kind != string(domain.ResultKindActivity) || program != "" {
		t.Fatalf("ожидался режим activity без программы, получено %q %q", kind, program)
	}
}

func TestNormalizeRunResultActivityIgnoresProgram(t *testing.T) {
	kind, program, err := normalizeRunResult(" ACTIVITY ", "  11111111-1111-1111-1111-111111111111  ")
	if err != nil {
		t.Fatalf("activity не должна требовать ошибки: %v", err)
	}
	if kind != string(domain.ResultKindActivity) || program != "" {
		t.Fatalf("в режиме activity программа не сохраняется: %q %q", kind, program)
	}
}

func TestNormalizeRunResultProgramRequiresProgram(t *testing.T) {
	if _, _, err := normalizeRunResult("program", "   "); !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("ожидалась ErrInvalidProgram, получено %v", err)
	}
	kind, program, err := normalizeRunResult("program", " 11111111-1111-1111-1111-111111111111 ")
	if err != nil {
		t.Fatalf("корректный режим program не должен падать: %v", err)
	}
	if kind != string(domain.ResultKindProgram) || program != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("программа должна сохраниться без пробелов: %q %q", kind, program)
	}
}

func TestNormalizeRunResultRejectsUnknownKind(t *testing.T) {
	if _, _, err := normalizeRunResult("everything", ""); !errors.Is(err, ErrInvalidResultKind) {
		t.Fatalf("ожидалась ErrInvalidResultKind, получено %v", err)
	}
}

func TestReviewNodeRejectsUnknownDecisionBeforeStore(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodPost, "/api/v1/nodes/node-1/review", "specialist",
		`{"status":"pending"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (решение pending недопустимо)", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body["code"] != "INVALID_REVIEW_STATUS" {
		t.Fatalf("code = %q, want INVALID_REVIEW_STATUS", body["code"])
	}
}

func TestReviewNodeRejectsLongComment(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	long := make([]rune, 2001)
	for i := range long {
		long[i] = 'я'
	}
	payload, _ := json.Marshal(map[string]string{"status": "approved", "comment": string(long)})
	rec := do(t, handler, http.MethodPost, "/api/v1/nodes/node-1/review", "specialist", string(payload))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (комментарий длиннее лимита)", rec.Code)
	}
}

func TestReviewNodeForbidsRegularUser(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodPost, "/api/v1/nodes/node-1/review", "user", `{"status":"approved"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (проверять может методист или админ)", rec.Code)
	}
}

func TestReviewNodeRequiresPostgres(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodPost, "/api/v1/nodes/node-1/review", "specialist", `{"status":"approved"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (демо-хранилище не поддерживает проверку)", rec.Code)
	}
}

func TestProgramLinksRequirePostgres(t *testing.T) {
	store := newMemStore()
	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/program-links", "user", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestReviewSummaryRequiresExistingRun(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	rec := do(t, handler, http.MethodGet, "/api/v1/runs/missing/review/summary", "user", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (PG не настроен)", rec.Code)
	}
}
