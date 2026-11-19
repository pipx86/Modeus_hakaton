package api_test

import (
	"net/http"
	"strings"
	"testing"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"

	"github.com/pashagolub/pgxmock/v4"
)

func TestArchiveReadinessIsPublicAndChecksSchema(t *testing.T) {
	pool, state := testPostgresAPI(t)
	handler := api.NewServer(state).Routes()
	for _, test := range []struct {
		schema bool
		err    error
		want   int
		body   string
	}{
		{true, nil, http.StatusOK, `"status":"ok"`},
		{false, nil, http.StatusServiceUnavailable, "schema_not_ready"},
		{false, errTestDB, http.StatusServiceUnavailable, "db_unavailable"},
	} {
		query := pool.ExpectQuery("SELECT to_regclass")
		if test.err != nil {
			query.WillReturnError(test.err)
		} else {
			query.WillReturnRows(pgxmock.NewRows([]string{"check"}).AddRow(test.schema))
		}
		response := callAPI(handler, http.MethodGet, "/api/v1/ready", "", nil)
		if response.Code != test.want || !strings.Contains(response.Body.String(), test.body) {
			t.Fatalf("readiness: %d, %s", response.Code, response.Body.String())
		}
	}
	response := callAPI(api.NewServerForDemo().Routes(), http.MethodGet, "/api/v1/ready", "", nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "db_pool_unavailable") {
		t.Fatalf("readiness без PG: %d, %s", response.Code, response.Body.String())
	}
}

func TestArchiveEndpointsValidateParametersBeforeSQL(t *testing.T) {
	_, state := testPostgresAPI(t)
	handler := api.NewServer(state).Routes()
	admin := testToken(t, auth.RoleAdmin)
	for _, path := range []string{
		"/api/v1/vacancies?limit=501",
		"/api/v1/vacancies?offset=-1",
		"/api/v1/vacancies?experience=unknown",
		"/api/v1/vacancies?date_from=2026-11-19&date_to=2026-11-18",
		"/api/v1/vacancies?date_to=19.11.2026",
		"/api/v1/academic/programs/bad-id",
		"/api/v1/taxonomies?offset=-2",
		"/api/v1/forecast/observed?months=4",
		"/api/v1/forecast/observed?min_freq=NaN",
		"/api/v1/forecast/observed?direction=up",
	} {
		t.Run(path, func(t *testing.T) {
			response := callAPI(handler, http.MethodGet, path, admin, nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("неверный параметр: %d, %s", response.Code, response.Body.String())
			}
		})
	}
	response := callAPI(handler, http.MethodGet, "/api/v1/vacancies", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("каталог без JWT: %d, %s", response.Code, response.Body.String())
	}
	guest := testToken(t, auth.RoleGuest)
	response = callAPI(handler, http.MethodPost, "/api/v1/subscriptions", guest, []byte(`{"topic":"IoT"}`))
	if response.Code != http.StatusForbidden {
		t.Fatalf("гость создал подписку: %d, %s", response.Code, response.Body.String())
	}
	student := testToken(t, auth.RoleStudent)
	response = callAPI(handler, http.MethodPost, "/api/v1/academic/analyze-gap-local", student, []byte(`{}`))
	if response.Code != http.StatusForbidden {
		t.Fatalf("сверка без роли: %d, %s", response.Code, response.Body.String())
	}
}
