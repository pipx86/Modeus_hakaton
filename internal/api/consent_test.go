package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

func consentRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func consentStatusFor(t *testing.T, handler http.Handler, token string) consentStatusResponse {
	t.Helper()
	recorder := consentRequest(t, handler, http.MethodGet, "/api/v1/consent/status", token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /consent/status: %d %s", recorder.Code, recorder.Body.String())
	}
	var status consentStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("статус согласия не JSON: %v", err)
	}
	return status
}

// recordingConsentStore запоминает аргументы регистрации, чтобы проверить,
// что согласие уходит в хранилище вместе с IP и User-Agent.
type recordingConsentStore struct {
	*memStore
	acceptances []ConsentAcceptance
}

func (s *recordingConsentStore) RegisterUserWithConsent(ctx context.Context, email, displayName, passwordHash string, consent ConsentAcceptance) (*domain.User, error) {
	s.acceptances = append(s.acceptances, consent)
	return s.memStore.RegisterUserWithConsent(ctx, email, displayName, passwordHash, consent)
}

func TestRegisterWithoutConsentRejected(t *testing.T) {
	auth.EnsureTestSecret()
	store := newMemStore()
	handler := NewServer(store).Routes()

	for name, body := range map[string]string{
		"поле отсутствует": `{"email":"no-consent@example.com","display_name":"Без согласия","password":"password123"}`,
		"consent=false":    `{"email":"no-consent@example.com","display_name":"Без согласия","password":"password123","consent":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := consentRequest(t, handler, http.MethodPost, "/api/v1/auth/register", "", body)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("статус = %d, ожидался 422 (%s)", recorder.Code, recorder.Body.String())
			}
			var response consentRequiredError
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("тело 422 не JSON: %v", err)
			}
			if response.Code != "CONSENT_REQUIRED" || response.Message != consentRequiredMessage {
				t.Fatalf("тело 422 = %+v", response)
			}
			if response.Details.DocumentURL != consentDocumentPath || response.Details.DocumentVersion != ConsentVersion {
				t.Fatalf("details = %+v, ожидалась ссылка на документ и версия %s", response.Details, ConsentVersion)
			}
			if _, err := store.UserByEmail(context.Background(), "no-consent@example.com"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("пользователь без согласия не должен создаваться: %v", err)
			}
		})
	}
}

func TestRegisterWithConsentRecordsAcceptance(t *testing.T) {
	auth.EnsureTestSecret()
	store := &recordingConsentStore{memStore: newMemStore()}
	handler := NewServer(store).Routes()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"email":"consent@example.com","display_name":"С согласием","password":"password123","consent":true,"consent_version":"v1"}`))
	req.RemoteAddr = "203.0.113.9:4444"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "consent-test/1.0")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("регистрация: %d %s", recorder.Code, recorder.Body.String())
	}

	if len(store.acceptances) != 1 {
		t.Fatalf("регистрация сохранила %d согласий, ожидалось одно", len(store.acceptances))
	}
	accepted := store.acceptances[0]
	if accepted.Version != ConsentVersion {
		t.Fatalf("версия = %q, ожидалась %q", accepted.Version, ConsentVersion)
	}
	if accepted.IP != "203.0.113.9" {
		t.Fatalf("IP = %q, ожидался адрес без порта", accepted.IP)
	}
	if accepted.UserAgent != "consent-test/1.0" {
		t.Fatalf("User-Agent = %q", accepted.UserAgent)
	}

	var registered authResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &registered); err != nil {
		t.Fatalf("ответ регистрации не JSON: %v", err)
	}
	status := consentStatusFor(t, handler, registered.Token)
	if !status.Accepted || status.AcceptedAt == nil {
		t.Fatalf("после регистрации согласие не видно: %+v", status)
	}
	if status.DocumentVersion != ConsentVersion {
		t.Fatalf("document_version = %q, ожидалась %q", status.DocumentVersion, ConsentVersion)
	}
}

func TestRegisterFallsBackToCurrentConsentVersion(t *testing.T) {
	auth.EnsureTestSecret()
	store := &recordingConsentStore{memStore: newMemStore()}
	handler := NewServer(store).Routes()

	recorder := consentRequest(t, handler, http.MethodPost, "/api/v1/auth/register", "",
		`{"email":"default-version@example.com","display_name":"Версия по умолчанию","password":"password123","consent":true,"consent_version":"v999"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("регистрация: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(store.acceptances) != 1 || store.acceptances[0].Version != ConsentVersion {
		t.Fatalf("версия по умолчанию не применилась: %+v", store.acceptances)
	}
}

func TestConsentStatusAndAcceptAreIdempotent(t *testing.T) {
	auth.EnsureTestSecret()
	store := newMemStore()
	user, err := store.CreateUser(context.Background(), "legacy@example.com", "Легаси", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store).Routes()
	token := auth.TokenFor(user.ID, auth.RoleUser)

	before := consentStatusFor(t, handler, token)
	if before.Accepted || before.DocumentVersion != ConsentVersion || before.AcceptedAt != nil {
		t.Fatalf("до принятия статус = %+v", before)
	}

	accepted := consentRequest(t, handler, http.MethodPost, "/api/v1/consent", token, `{"document_version":"v1"}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("POST /consent: %d %s", accepted.Code, accepted.Body.String())
	}
	var first consentStatusResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if !first.Accepted || first.AcceptedAt == nil {
		t.Fatalf("согласие не зафиксировано: %+v", first)
	}

	repeated := consentRequest(t, handler, http.MethodPost, "/api/v1/consent", token, `{"document_version":"v1"}`)
	if repeated.Code != http.StatusOK {
		t.Fatalf("повторный POST /consent: %d %s", repeated.Code, repeated.Body.String())
	}
	var second consentStatusResponse
	if err := json.Unmarshal(repeated.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.AcceptedAt == nil || !second.AcceptedAt.Equal(*first.AcceptedAt) {
		t.Fatalf("повторный accept изменил accepted_at: %v vs %v", first.AcceptedAt, second.AcceptedAt)
	}

	store.mu.RLock()
	rows := len(store.consents[user.ID])
	store.mu.RUnlock()
	if rows != 1 {
		t.Fatalf("строк согласия = %d, ожидалась одна (идемпотентность)", rows)
	}

	after := consentStatusFor(t, handler, token)
	if !after.Accepted || after.AcceptedAt == nil || !after.AcceptedAt.Equal(*first.AcceptedAt) {
		t.Fatalf("статус после принятия = %+v", after)
	}
}

func TestCreateRunRequiresConsent(t *testing.T) {
	auth.EnsureTestSecret()
	store := newMemStore()
	user, err := store.CreateUser(context.Background(), "runner@example.com", "Исследователь", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store).Routes()
	token := auth.TokenFor(user.ID, auth.RoleUser)

	reading := consentRequest(t, handler, http.MethodGet, "/api/v1/runs", token, "")
	if reading.Code != http.StatusOK {
		t.Fatalf("чтение без согласия заблокировано: %d %s", reading.Code, reading.Body.String())
	}

	blocked := consentRequest(t, handler, http.MethodPost, "/api/v1/runs", token, `{"profession":"cook"}`)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("POST /runs без согласия: %d %s", blocked.Code, blocked.Body.String())
	}
	var response consentRequiredError
	if err := json.Unmarshal(blocked.Body.Bytes(), &response); err != nil {
		t.Fatalf("тело 403 не JSON: %v", err)
	}
	if response.Code != "CONSENT_REQUIRED" || response.Message != consentRequiredMessage {
		t.Fatalf("тело 403 = %+v", response)
	}
	if response.Details.DocumentURL != consentDocumentPath {
		t.Fatalf("в details нет ссылки на документ: %+v", response.Details)
	}

	accepted := consentRequest(t, handler, http.MethodPost, "/api/v1/consent", token, `{"document_version":"v1"}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("POST /consent: %d %s", accepted.Code, accepted.Body.String())
	}
	created := consentRequest(t, handler, http.MethodPost, "/api/v1/runs", token, `{"profession":"cook"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /runs с согласием: %d %s", created.Code, created.Body.String())
	}
}

func TestConsentDocumentIsPublic(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	recorder := consentRequest(t, handler, http.MethodGet, "/api/v1/consent/document", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /consent/document без токена: %d %s", recorder.Code, recorder.Body.String())
	}
	var document consentDocumentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("документ не JSON: %v", err)
	}
	if document.Version != ConsentVersion || document.Title == "" {
		t.Fatalf("документ = %+v", document)
	}
	for _, fragment := range []string{"152-ФЗ", "персональных данных", "privacy@modeus.local"} {
		if !strings.Contains(document.Text, fragment) {
			t.Fatalf("в тексте политики нет фрагмента %q", fragment)
		}
	}
}

func TestConsentStatusRequiresAuth(t *testing.T) {
	handler := NewServer(newMemStore()).Routes()
	recorder := consentRequest(t, handler, http.MethodGet, "/api/v1/consent/status", "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("GET /consent/status без токена: %d, ожидался 401", recorder.Code)
	}
}
