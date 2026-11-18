package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// guestChat — заглушка chatStarter, чтобы GET /messages отдавал 200 без LLM.
type guestChat struct{}

func (guestChat) Start(string, string) error { return nil }

func (guestChat) Messages(string) ([]chat.Message, error) {
	return []chat.Message{{ID: "m1", Role: "agent", Content: "Расскажите о задачах"}}, nil
}

func (guestChat) Post(string, string) (chat.Message, error) { return chat.Message{}, nil }

func (guestChat) Subscribe(string) (<-chan chat.Event, func(), error) {
	return nil, func() {}, nil
}

const (
	guestRunID      = "11111111-1111-1111-1111-111111111111"
	guestSnapshotID = "22222222-2222-2222-2222-222222222222"
	guestSourceID   = "33333333-3333-3333-3333-333333333333"
	guestEvidenceID = "44444444-4444-4444-4444-444444444444"
)

// guestServer собирает сервер с готовым демонстрационным исследованием.
func guestServer() http.Handler {
	m := newMemStore()
	m.seedRun(domain.Run{ID: guestRunID, Status: domain.RunDone, Profession: "cook", Stage: domain.StageDone})
	m.seedNodes(guestRunID, domain.Node{
		ID:       "n1",
		Type:     domain.NodeRole,
		Title:    "Cook",
		Evidence: []domain.Evidence{{ID: guestEvidenceID, SourceID: guestSourceID, Quote: "q1"}},
	})
	m.seedEvidence(domain.Evidence{ID: guestEvidenceID, SourceID: guestSourceID, Quote: "q1"}, guestRunID)
	return NewServerWithChat(m, guestChat{}).Routes()
}

// TestGuestCanReadDemoData проверяет, что гость проходит чтение GET-маршрутов.
// Для маршрутов, которым нужен PostgreSQL, ожидается 501: 403 означал бы, что
// гостя не пустили на уровне роли.
func TestGuestCanReadDemoData(t *testing.T) {
	h := guestServer()
	tests := []struct {
		name string
		path string
		want int
	}{
		{"list runs", "/api/v1/runs", http.StatusOK},
		{"get run", "/api/v1/runs/" + guestRunID, http.StatusOK},
		{"get tree", "/api/v1/runs/" + guestRunID + "/tree", http.StatusOK},
		{"get answers", "/api/v1/runs/" + guestRunID + "/answers", http.StatusOK},
		{"get messages", "/api/v1/runs/" + guestRunID + "/messages", http.StatusOK},
		{"export run", "/api/v1/runs/" + guestRunID + "/export", http.StatusOK},
		// Ниже нужен PGStore, поэтому успешный проход роли даёт 501.
		{"list snapshots", "/api/v1/runs/" + guestRunID + "/snapshots", http.StatusNotImplemented},
		{"get snapshot", "/api/v1/runs/" + guestRunID + "/snapshots/" + guestSnapshotID, http.StatusNotImplemented},
		{"search run", "/api/v1/runs/" + guestRunID + "/search?q=cook", http.StatusNotImplemented},
		{"list sources", "/api/v1/sources", http.StatusNotImplemented},
		{"get source", "/api/v1/sources/" + guestSourceID, http.StatusNotImplemented},
		{"list evidences", "/api/v1/evidences", http.StatusNotImplemented},
		{"get evidence", "/api/v1/evidences/" + guestEvidenceID, http.StatusNotImplemented},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, h, http.MethodGet, tc.path, "guest", "")
			if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
				t.Fatalf("guest должен читать %s, получили %d (%s)", tc.path, rr.Code, rr.Body.String())
			}
			if rr.Code != tc.want {
				t.Fatalf("%s: ожидали %d, получили %d (%s)", tc.path, tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestGuestCannotMutate доказывает, что на всех изменяющих маршрутах гость
// получает 403, даже если данные для операции существуют.
func TestGuestCannotMutate(t *testing.T) {
	h := guestServer()
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create run", http.MethodPost, "/api/v1/runs", `{"profession":"cook"}`},
		{"save tree", http.MethodPut, "/api/v1/runs/" + guestRunID + "/tree", `[]`},
		{"patch node", http.MethodPatch, "/api/v1/nodes/n1", `{"title":"Other"}`},
		{"create snapshot", http.MethodPost, "/api/v1/runs/" + guestRunID + "/snapshots", ""},
		{"approve evidence", http.MethodPost, "/api/v1/evidences/" + guestEvidenceID + "/approve", ""},
		{"reject evidence", http.MethodPost, "/api/v1/evidences/" + guestEvidenceID + "/reject", ""},
		{"bulk approve", http.MethodPost, "/api/v1/evidences/bulk-approve", `{"ids":["` + guestEvidenceID + `"]}`},
		{"create evidence", http.MethodPost, "/api/v1/evidences", `{"node_id":"n1","source_id":"s1","quote":"q"}`},
		{"create source", http.MethodPost, "/api/v1/sources", `{"type":"document","title":"x"}`},
		{"delete source", http.MethodDelete, "/api/v1/sources/" + guestSourceID, ""},
		{"index source", http.MethodPost, "/api/v1/sources/" + guestSourceID + "/index", ""},
		{"post message", http.MethodPost, "/api/v1/runs/" + guestRunID + "/messages", `{"content":"hi"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, h, tc.method, tc.path, "guest", tc.body)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s: гость не должен изменять данные, ожидали 403, получили %d (%s)", tc.path, rr.Code, rr.Body.String())
			}
			var body errorBody
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s: тело ответа не JSON: %v", tc.path, err)
			}
			if body.Code != "FORBIDDEN" {
				t.Fatalf("%s: ожидали код FORBIDDEN, получили %q", tc.path, body.Code)
			}
		})
	}
}

// TestAdminOnlySourceDeleteNotWeakened проверяет, что удаление источника
// осталось доступно только admin.
func TestAdminOnlySourceDeleteNotWeakened(t *testing.T) {
	h := guestServer()
	for _, role := range []string{"guest", "teacher", "rop", "student"} {
		rr := do(t, h, http.MethodDelete, "/api/v1/sources/"+guestSourceID, role, "")
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s не должен удалять источник: ожидали 403, получили %d", role, rr.Code)
		}
	}
	rr := do(t, h, http.MethodDelete, "/api/v1/sources/"+guestSourceID, "admin", "")
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("admin должен проходить проверку роли на удаление источника, получили %d (%s)", rr.Code, rr.Body.String())
	}
}
