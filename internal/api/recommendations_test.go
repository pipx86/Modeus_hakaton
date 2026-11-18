package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/recommend"
)

const recommendationsProfession = "Аналитик данных (junior)"

// recommendationsRunStore creates a batch run via the mem store, moves it to
// the requested status and seeds a small competency tree with answers.
func recommendationsRunStore(t *testing.T, profession string, status domain.RunStatus) (*memStore, string) {
	t.Helper()

	m := newMemStore()
	run, err := m.CreateRun(profession)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := m.SetStatus(run.ID, status); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := m.SetStage(run.ID, domain.StageDone); err != nil {
		t.Fatalf("SetStage: %v", err)
	}

	m.seedNodes(run.ID,
		domain.Node{
			ID:       "n1",
			Type:     domain.NodeRole,
			Title:    "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "Отвечаю за отчётность"}},
		},
		domain.Node{
			ID:       "n2",
			Type:     domain.NodeFunction,
			Title:    "Работа с данными",
			ParentID: strPtr("n1"),
			Evidence: []domain.Evidence{{ID: "e2", SourceID: "s1", Quote: "Готовлю данные"}},
		},
		domain.Node{
			ID:       "n3",
			Type:     domain.NodeTask,
			Title:    "SQL-запросы",
			ParentID: strPtr("n2"),
			Evidence: []domain.Evidence{{ID: "e3", SourceID: "s1", Quote: "Пишу SQL-запросы"}},
		},
		domain.Node{
			ID:       "n4",
			Type:     domain.NodeAction,
			Title:    "Дашборды",
			ParentID: strPtr("n3"),
			Evidence: []domain.Evidence{{ID: "e4", SourceID: "s1", Quote: "Строю дашборды"}},
		},
	)
	m.seedEvidence(domain.Evidence{ID: "e1", SourceID: "s1", Quote: "Отвечаю за отчётность"}, run.ID)
	m.seedEvidence(domain.Evidence{ID: "e2", SourceID: "s1", Quote: "Готовлю данные"}, run.ID)

	return m, run.ID
}

// TestGetRecommendationsOK checks the direct Recommendation JSON contract for
// every read role, including the newly added guest role.
func TestGetRecommendationsOK(t *testing.T) {
	m, runID := recommendationsRunStore(t, recommendationsProfession, domain.RunDone)
	h := NewServer(m).Routes()

	for _, role := range []string{"admin", "teacher", "rop", "student", "guest"} {
		t.Run(role, func(t *testing.T) {
			rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", role, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("%s: ожидали 200, получили %d (%s)", role, rr.Code, rr.Body.String())
			}

			var rec recommend.Recommendation
			if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil {
				t.Fatalf("тело не распарсилось как Recommendation: %v (%s)", err, rr.Body.String())
			}
			if rec.Profession != recommendationsProfession {
				t.Fatalf("profession: получили %q, ожидали %q", rec.Profession, recommendationsProfession)
			}
			if rec.Level != recommend.LevelJunior {
				t.Fatalf("level: получили %q, ожидали %q", rec.Level, recommend.LevelJunior)
			}
			if rec.Summary == "" {
				t.Fatal("summary не должен быть пустым")
			}
			if len(rec.Courses) != 2 {
				t.Fatalf("курсов: получили %d, ожидали 2", len(rec.Courses))
			}
			for _, course := range rec.Courses {
				if course.Provider != "Stepik" || !course.Free {
					t.Fatalf("курс должен быть бесплатным на Stepik: %+v", course)
				}
				if course.Title == "" || course.URL == "" || course.Reason == "" {
					t.Fatalf("у курса не заполнены поля: %+v", course)
				}
			}
			if len(rec.Plan) < 4 || len(rec.Plan) > 6 {
				t.Fatalf("шагов плана: получили %d, ожидали 4-6", len(rec.Plan))
			}
			for i, step := range rec.Plan {
				if step.Order != i+1 {
					t.Fatalf("шаг %d: order = %d", i, step.Order)
				}
				if step.Done {
					t.Fatalf("шаг %d: done должен быть false", i)
				}
			}
			if rec.GeneratedAt.IsZero() {
				t.Fatal("generated_at не должен быть zero time")
			}
		})
	}
}

// TestGetRecommendationsRouteRegistered verifies that the route is registered
// and returns the expected JSON object keys.
func TestGetRecommendationsRouteRegistered(t *testing.T) {
	m, runID := recommendationsRunStore(t, recommendationsProfession, domain.RunDone)
	h := NewServer(m).Routes()

	rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", "teacher", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("маршрут рекомендаций не зарегистрирован: получили %d (%s)", rr.Code, rr.Body.String())
	}
	if contentType := rr.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type: получили %q, ожидали application/json", contentType)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("тело не JSON-объект: %v", err)
	}
	for _, key := range []string{"profession", "level", "summary", "courses", "plan", "generated_at"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
	if got := strings.TrimSpace(string(payload["courses"])); !strings.HasPrefix(got, "[") {
		t.Fatalf("courses должны быть JSON-массивом, получили %q", got)
	}
	if got := strings.TrimSpace(string(payload["plan"])); !strings.HasPrefix(got, "[") {
		t.Fatalf("plan должны быть JSON-массивом, получили %q", got)
	}
}

// TestGetRecommendationsNotReady checks that pending, running and failed runs
// are rejected with 409 RUN_NOT_READY.
func TestGetRecommendationsNotReady(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunPending, domain.RunRunning, domain.RunFailed} {
		t.Run(string(status), func(t *testing.T) {
			m, runID := recommendationsRunStore(t, "Backend Developer (junior)", status)
			h := NewServer(m).Routes()

			rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", "student", "")
			if rr.Code != http.StatusConflict {
				t.Fatalf("ожидали 409 для статуса %q, получили %d (%s)", status, rr.Code, rr.Body.String())
			}
			assertErrorCode(t, rr, "RUN_NOT_READY")
		})
	}
}

// TestGetRecommendationsDegradedRun checks that degraded runs still get
// recommendations.
func TestGetRecommendationsDegradedRun(t *testing.T) {
	m, runID := recommendationsRunStore(t, recommendationsProfession, domain.RunDegraded)
	h := NewServer(m).Routes()

	rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", "guest", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200 для degraded, получили %d (%s)", rr.Code, rr.Body.String())
	}

	var rec recommend.Recommendation
	if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil {
		t.Fatalf("тело не распарсилось как Recommendation: %v", err)
	}
	if len(rec.Courses) != 2 {
		t.Fatalf("курсов: получили %d, ожидали 2", len(rec.Courses))
	}
}

// TestGetRecommendationsNotFound checks the 404 path.
func TestGetRecommendationsNotFound(t *testing.T) {
	h := NewServer(newMemStore()).Routes()

	rr := do(t, h, http.MethodGet, "/api/v1/runs/missing/recommendations", "teacher", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ожидали 404, получили %d (%s)", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "NOT_FOUND")
}

// TestGetRecommendationsUnauthorized checks that the auth middleware protects
// the new route.
func TestGetRecommendationsUnauthorized(t *testing.T) {
	m, runID := recommendationsRunStore(t, recommendationsProfession, domain.RunDone)
	h := NewServer(m).Routes()

	rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 без токена, получили %d (%s)", rr.Code, rr.Body.String())
	}
}

// TestGetRecommendationsAnswersFallback checks that an answers-store failure
// still produces a plan from the profession and tree.
func TestGetRecommendationsAnswersFallback(t *testing.T) {
	base, runID := recommendationsRunStore(t, "Backend Developer (junior)", domain.RunDone)
	st := &failingRunStore{
		Store:         base,
		getAnswersErr: errors.New("answers unavailable"),
	}
	h := NewServer(st).Routes()

	rr := do(t, h, http.MethodGet, "/api/v1/runs/"+runID+"/recommendations", "teacher", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200 при ошибке GetAnswers, получили %d (%s)", rr.Code, rr.Body.String())
	}

	var rec recommend.Recommendation
	if err := json.Unmarshal(rr.Body.Bytes(), &rec); err != nil {
		t.Fatalf("тело не распарсилось как Recommendation: %v", err)
	}
	if rec.Profession != "Backend Developer (junior)" {
		t.Fatalf("profession: получили %q", rec.Profession)
	}
	if len(rec.Courses) != 2 {
		t.Fatalf("курсов: получили %d, ожидали 2", len(rec.Courses))
	}
	if len(rec.Plan) < 4 || len(rec.Plan) > 6 {
		t.Fatalf("шагов плана: получили %d, ожидали 4-6", len(rec.Plan))
	}
}
