package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/store/db"
)

func (s *Server) subscriptionQueries(w http.ResponseWriter, r *http.Request) (*db.Queries, string, bool) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return nil, "", false
	}
	subject := auth.Subject(r.Context())
	if subject == "" {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "нужен идентификатор пользователя")
		return nil, "", false
	}
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "подписки требуют PostgreSQL")
		return nil, "", false
	}
	return db.New(pg.Pool), subject, true
}

func decodeSubscriptionBody(w http.ResponseWriter, r *http.Request, payload any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := decoder.Decode(payload); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен один JSON-объект")
		return false
	}
	return true
}

func publicationText(value string, maxRunes int) bool {
	return utf8.RuneCountInString(value) <= maxRunes && !strings.ContainsRune(value, 0)
}

func (s *Server) handleCreatePublicationSubscription(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	var input struct {
		Topic    string `json:"topic"`
		Source   string `json:"source"`
		Telegram string `json:"telegram_chat_id"`
		Email    string `json:"email"`
	}
	if !decodeSubscriptionBody(w, r, &input, 8<<10) {
		return
	}
	topic := strings.TrimSpace(input.Topic)
	source := strings.TrimSpace(input.Source)
	telegram := strings.TrimSpace(input.Telegram)
	email := strings.TrimSpace(input.Email)
	if source == "" {
		source = "openalex+arxiv"
	}
	_, telegramErr := strconv.ParseInt(telegram, 10, 64)
	address, emailErr := mail.ParseAddress(email)
	if topic == "" || !publicationText(topic, 200) ||
		(source != "openalex" && source != "arxiv" && source != "openalex+arxiv") ||
		(telegram != "" && telegramErr != nil) ||
		(email != "" && (!publicationText(email, 254) || emailErr != nil || address.Address != email)) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "неверная тема, канал публикаций или получатель")
		return
	}
	id, err := queries.CreatePublicationSubscription(r.Context(), db.CreatePublicationSubscriptionParams{
		OwnerID: owner, Topic: topic, Source: source, TelegramChatID: telegram, Email: email,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": id})
}

func (s *Server) handlePublicationSubscriptions(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	list, err := queries.ListPublicationSubscriptions(r.Context(), owner)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if list == nil {
		list = []db.ListPublicationSubscriptionsRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": list, "total": len(list)})
}

func (s *Server) handleDeletePublicationSubscription(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	id, ok := academicUUID(w, r.PathValue("id"), "subscription_id", false)
	if !ok {
		return
	}
	changed, err := queries.DeletePublicationSubscription(r.Context(), db.DeletePublicationSubscriptionParams{
		ID: id, OwnerID: owner, IsAdmin: auth.RoleFromCtx(r.Context()) == auth.RoleAdmin,
	})
	writePublicationMutation(w, changed, err)
}

func (s *Server) handlePublicationNotifications(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	limit, err := queryInt(r.URL.Query(), "limit", 50, 1, 200)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var unread bool
	if query := r.URL.Query().Get("unread_only"); query != "" {
		unread, err = strconv.ParseBool(query)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "unread_only должен быть логическим")
			return
		}
	}
	list, err := queries.ListPublicationNotifications(r.Context(), db.ListPublicationNotificationsParams{
		OwnerID: owner, UnreadOnly: unread, PageSize: limit,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if list == nil {
		list = []db.ListPublicationNotificationsRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": list, "total": len(list)})
}

func (s *Server) handlePublicationUnreadCount(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	count, err := queries.CountUnreadPublicationNotifications(r.Context(), owner)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": count})
}

func (s *Server) handleReadPublicationNotification(w http.ResponseWriter, r *http.Request) {
	queries, owner, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	id, ok := academicUUID(w, r.PathValue("id"), "notification_id", false)
	if !ok {
		return
	}
	changed, err := queries.MarkPublicationNotificationRead(r.Context(), db.MarkPublicationNotificationReadParams{
		ID: id, OwnerID: owner, IsAdmin: auth.RoleFromCtx(r.Context()) == auth.RoleAdmin,
	})
	writePublicationMutation(w, changed, err)
}

func writePublicationMutation(w http.ResponseWriter, changed int64, err error) {
	switch {
	case err != nil:
		sourceEvidenceError(w, translate(err))
	case changed == 0:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "запись не найдена")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *Server) handlePublicationPollingTargets(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	queries, _, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	after, ok := academicUUID(w, r.URL.Query().Get("after_id"), "after_id", true)
	if !ok {
		return
	}
	limit, err := queryInt(r.URL.Query(), "limit", 100, 1, 200)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	list, err := queries.ListPublicationPollingTargets(r.Context(), db.ListPublicationPollingTargetsParams{
		AfterID: after, PageSize: limit,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if list == nil {
		list = []db.ListPublicationPollingTargetsRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": list, "total": len(list)})
}

type publicationUpdate struct {
	Title    string `json:"title"`
	Abstract string `json:"abstract"`
	URL      string `json:"url"`
	Source   string `json:"source"`
}

func (s *Server) handlePublicationUpdates(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	queries, _, ok := s.subscriptionQueries(w, r)
	if !ok {
		return
	}
	var input struct {
		SubscriptionID string              `json:"subscription_id"`
		Articles       []publicationUpdate `json:"articles"`
	}
	if !decodeSubscriptionBody(w, r, &input, 256<<10) {
		return
	}
	id, ok := academicUUID(w, input.SubscriptionID, "subscription_id", false)
	if !ok {
		return
	}
	if input.Articles == nil || len(input.Articles) > 20 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен массив до 20 публикаций")
		return
	}
	for i := range input.Articles {
		article := &input.Articles[i]
		article.Title = strings.TrimSpace(article.Title)
		article.URL = strings.TrimSpace(article.URL)
		link, err := url.Parse(article.URL)
		if article.Title == "" || !publicationText(article.Title, 500) || !publicationText(article.Abstract, 2000) ||
			!publicationText(article.URL, 2048) || err != nil || link.Hostname() == "" || link.User != nil ||
			(link.Scheme != "https" && link.Scheme != "http") || (article.Source != "openalex" && article.Source != "arxiv") {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимые поля публикации")
			return
		}
	}
	body, err := json.Marshal(input.Articles)
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	result, err := queries.RecordPublicationUpdates(r.Context(), db.RecordPublicationUpdatesParams{
		SubscriptionID: id, Articles: body,
	})
	if err != nil {
		sourceEvidenceError(w, translate(err))
		return
	}
	if !result.Found {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "подписка не найдена")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": result.Created})
}
