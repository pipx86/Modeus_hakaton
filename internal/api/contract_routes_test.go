package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The public HTTP contract required by the demo is documented in openapi.yaml
// and served to Swagger UI from api-docs/. This table is the protected /api/v1
// route surface the demo must expose; keep it in sync with openapi.yaml when
// the contract changes. GET /api/v1/health is public and has its own check.
//
// These checks intentionally cover only the route surface (method, path, auth
// boundary and static Swagger mount). Handler behaviour is covered by the
// scenario tests: full_flow_test.go, guest_access_test.go, runs_errors_test.go.

const contractID = "00000000-0000-0000-0000-000000000000"

type contractRoute struct {
	method string
	path   string
}

var demoContractRoutes = []contractRoute{
	{http.MethodGet, "/api/v1/sources"},
	{http.MethodPost, "/api/v1/sources"},
	{http.MethodGet, "/api/v1/sources/" + contractID},
	{http.MethodDelete, "/api/v1/sources/" + contractID},
	{http.MethodGet, "/api/v1/evidences"},
	{http.MethodPost, "/api/v1/evidences"},
	{http.MethodPost, "/api/v1/evidences/" + contractID + "/approve"},
	{http.MethodGet, "/api/v1/runs"},
	{http.MethodPost, "/api/v1/runs"},
	{http.MethodGet, "/api/v1/runs/" + contractID},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/tree"},
	{http.MethodPut, "/api/v1/runs/" + contractID + "/tree"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/answers"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/export"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/events"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/search"},
	{http.MethodPatch, "/api/v1/nodes/" + contractID},
	{http.MethodPost, "/api/v1/sources/" + contractID + "/index"},
	{http.MethodPost, "/api/v1/runs/" + contractID + "/snapshots"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/snapshots"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/snapshots/" + contractID + "/diff"},
	{http.MethodGet, "/api/v1/reviews/evidence"},
	{http.MethodGet, "/api/v1/runs/" + contractID + "/snapshots/" + contractID},
}

func contractHandler() http.Handler {
	return NewServer(newMemStore()).Routes()
}

// TestContractProtectedRoutesRequireAuth verifies that none of the documented
// demo routes accidentally became public.
func TestContractProtectedRoutesRequireAuth(t *testing.T) {
	h := contractHandler()
	for _, route := range demoContractRoutes {
		rec := do(t, h, route.method, route.path, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: status = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

// TestContractRoutesRegistered verifies that every documented route is actually
// mounted with the documented method. A request to a registered route with an
// unknown method yields 405 + Allow (401 would mean an auth-only catch-all, 404
// a missing route), so the Allow header is the registration signal.
func TestContractRoutesRegistered(t *testing.T) {
	h := contractHandler()
	const probeMethod = "CONTRACT" // a valid HTTP token, not a real API method
	for _, route := range demoContractRoutes {
		rec := do(t, h, probeMethod, route.path, "admin", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s is not registered (probe %s): status = %d, want 405",
				route.method, route.path, probeMethod, rec.Code)
			continue
		}
		if allow := rec.Header().Get("Allow"); !strings.Contains(allow, route.method) {
			t.Errorf("%s %s: Allow = %q, want it to list %s", route.method, route.path, allow, route.method)
		}
	}
}

// TestContractPublicHealth covers the only public /api/v1 endpoint.
func TestContractPublicHealth(t *testing.T) {
	rec := do(t, contractHandler(), http.MethodGet, "/api/v1/health", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/health without token: status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health response is not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("health status = %q, want %q", body["status"], "ok")
	}
}

// TestContractAuthBoundary distinguishes "route exists but needs a token" from
// the catch-all: unknown /api/v1 paths are 401 without a token and 404 with it.
func TestContractAuthBoundary(t *testing.T) {
	h := contractHandler()
	const missing = "/api/v1/contract-does-not-exist"
	if rec := do(t, h, http.MethodGet, missing, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown route without token: status = %d, want 401", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, missing, "admin", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown route with token: status = %d, want 404", rec.Code)
	}
}

// TestContractSwaggerUIServed proves that Routes() mounts the static Swagger UI
// from the repository's api-docs/ directory, which is what cmd/api serves at
// /swagger/ when started from the repo root. demo_swagger.go is redundant: it
// used the same Routes() mount (api.NewServerForDemo() == NewServer(memStore)).
func TestContractSwaggerUIServed(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	t.Chdir(repoRoot) // api-docs/ is resolved relative to the process cwd

	h := contractHandler()
	index := do(t, h, http.MethodGet, "/swagger/", "", "")
	if index.Code != http.StatusOK {
		t.Fatalf("GET /swagger/: status = %d, want 200 (is api-docs/ missing?)", index.Code)
	}
	if !strings.Contains(index.Body.String(), "SwaggerUIBundle") {
		t.Error("GET /swagger/ does not return the Swagger UI index.html")
	}
	spec := do(t, h, http.MethodGet, "/swagger/swagger.json", "", "")
	if spec.Code != http.StatusOK {
		t.Fatalf("GET /swagger/swagger.json: status = %d, want 200", spec.Code)
	}
	if !json.Valid(spec.Body.Bytes()) {
		t.Error("GET /swagger/swagger.json does not return valid JSON")
	}
}
