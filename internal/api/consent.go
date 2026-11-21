package api

import (
	_ "embed"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
)

// ConsentVersion — текущая версия документа согласия на обработку
// персональных данных. Пустая или неизвестная версия сводится к ней.
const ConsentVersion = "v1"

const (
	consentDocumentTitle   = "Политика обработки персональных данных"
	consentDocumentPath    = "/api/v1/consent/document"
	consentRequiredMessage = "нужно согласие на обработку персональных данных"
	consentUserAgentLimit  = 512
)

// knownConsentVersions — версии документа, которые сервис умеет принимать.
// Другие значения не сохраняются: клиент мог прислать устаревшую или
// опечатанную версию, поэтому фиксируется текущая.
var knownConsentVersions = map[string]bool{ConsentVersion: true}

//go:embed consent_document_v1.md
var consentDocumentV1 string

// ConsentAcceptance — подтверждение согласия, которое сохраняется вместе с
// учётной записью при регистрации или отдельным запросом.
type ConsentAcceptance struct {
	Version   string
	IP        string
	UserAgent string
}

// ConsentState — сохранённое согласие пользователя. Пустой Version означает,
// что согласия нет.
type ConsentState struct {
	Version    string
	AcceptedAt time.Time
}

// Accepted сообщает, что пользователь принял именно текущую версию документа.
func (s ConsentState) Accepted() bool { return s.Version == ConsentVersion }

type consentStatusResponse struct {
	DocumentVersion string     `json:"document_version"`
	Accepted        bool       `json:"accepted"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
}

type consentRequiredDetails struct {
	DocumentVersion string `json:"document_version"`
	DocumentURL     string `json:"document_url"`
}

// consentRequiredError — тело 422 при регистрации и 403 при создании
// исследования. details.document_url — ссылка на документ, а не технический id.
type consentRequiredError struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details consentRequiredDetails `json:"details"`
}

type consentDocumentResponse struct {
	Version string `json:"version"`
	Title   string `json:"title"`
	Text    string `json:"text"`
}

func normalizeConsentVersion(version string) string {
	version = strings.TrimSpace(version)
	if knownConsentVersions[version] {
		return version
	}
	return ConsentVersion
}

func writeConsentRequired(w http.ResponseWriter, status int) {
	writeJSON(w, status, consentRequiredError{
		Code:    "CONSENT_REQUIRED",
		Message: consentRequiredMessage,
		Details: consentRequiredDetails{
			DocumentVersion: ConsentVersion,
			DocumentURL:     consentDocumentPath,
		},
	})
}

// remoteIP возвращает адрес клиента без порта: в user_consents.ip хранится
// только адрес, а не r.RemoteAddr целиком.
func remoteIP(r *http.Request) string {
	address := strings.TrimSpace(r.RemoteAddr)
	if address == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(address, "[]")
}

func clipConsentValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func consentStatusFromState(state ConsentState) consentStatusResponse {
	response := consentStatusResponse{
		DocumentVersion: ConsentVersion,
		Accepted:        state.Accepted(),
	}
	if response.Accepted && !state.AcceptedAt.IsZero() {
		acceptedAt := state.AcceptedAt.UTC()
		response.AcceptedAt = &acceptedAt
	}
	return response
}

// requireConsent проверяет согласие перед действиями, создающими данные.
// Чтение, вход и админские операции не блокируются. ErrNotFound означает, что
// хранилищу неизвестен субъект токена (легаси-JWT, демо-фейк), поэтому
// поведение остаётся прежним: проверять согласие не у кого.
func (s *Server) requireConsent(w http.ResponseWriter, r *http.Request, status int) bool {
	store, ok := s.store.(consentStore)
	if !ok {
		return true
	}
	state, err := store.ConsentStatus(r.Context(), auth.Subject(r.Context()))
	if errors.Is(err, ErrNotFound) {
		return true
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "не удалось проверить согласие")
		return false
	}
	if state.Accepted() {
		return true
	}
	writeConsentRequired(w, status)
	return false
}

// handleConsentStatus godoc
// @Summary      Personal data consent status
// @Description  Returns the current document version and whether the authenticated user accepted it.
// @Tags         consent
// @Produce      json
// @Success      200  {object}  consentStatusResponse
// @Failure      401  {object}  errorBody
// @Failure      503  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/consent/status [get]
func (s *Server) handleConsentStatus(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	store, ok := s.store.(consentStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "статус согласия требует хранилище пользователей")
		return
	}
	state, err := store.ConsentStatus(r.Context(), auth.Subject(r.Context()))
	if err != nil && !errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "статус согласия временно недоступен")
		return
	}
	writeJSON(w, http.StatusOK, consentStatusFromState(state))
}

// handleAcceptConsent godoc
// @Summary      Accept personal data processing
// @Description  Records consent of an already registered user for the requested document version. Repeated calls for the same version are idempotent and return the existing acceptance time.
// @Tags         consent
// @Accept       json
// @Produce      json
// @Param        body  body      object{document_version=string}  false  "Accepted document version; defaults to the current one"
// @Success      200   {object}  consentStatusResponse
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      404   {object}  errorBody
// @Failure      503   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/consent [post]
func (s *Server) handleAcceptConsent(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	store, ok := s.store.(consentStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "согласие требует хранилище пользователей")
		return
	}
	var req struct {
		DocumentVersion string `json:"document_version"`
	}
	if err := decodeStrictJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	state, err := store.AcceptConsent(r.Context(), auth.Subject(r.Context()),
		normalizeConsentVersion(req.DocumentVersion), remoteIP(r), clipConsentValue(r.UserAgent(), consentUserAgentLimit))
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "пользователь не найден")
		return
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "не удалось сохранить согласие")
		return
	}
	writeJSON(w, http.StatusOK, consentStatusFromState(state))
}

// handleConsentDocument godoc
// @Summary      Personal data processing policy
// @Description  Public Russian text of the consent document so the frontend can render it without a token.
// @Tags         consent
// @Produce      json
// @Success      200  {object}  consentDocumentResponse
// @Router       /api/v1/consent/document [get]
func (*Server) handleConsentDocument(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, consentDocumentResponse{
		Version: ConsentVersion,
		Title:   consentDocumentTitle,
		Text:    consentDocumentV1,
	})
}
