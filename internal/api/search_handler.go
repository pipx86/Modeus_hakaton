package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/search"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// sourceIndexFunc индексирует один источник: строит фрагменты и векторы.
// Поле-функция позволяет тестам подменить модель и PostgreSQL, а cmd/server
// включает фоновую индексацию новых источников через EnableSourceAutoIndex.
type sourceIndexFunc func(ctx context.Context, sourceID string) error

// sourceAutoIndexTimeout ограничивает фоновую индексацию одного источника:
// большой документ и холодная модель не должны занимать обработчик вечно.
const sourceAutoIndexTimeout = 2 * time.Minute

// searchService собирает сервис поиска из пула и параметров Ollama
// (OLLAMA_URL/OLLAMA_EMBED_MODEL с локальными умолчаниями). Без PostgreSQL
// (in-memory store) поиск недоступен.
func (s *Server) searchService() (search.Service, bool) {
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		return search.Service{}, false
	}
	return search.ConfigFromEnv().Service(pg.Pool), true
}

// newSourceIndexer creates the indexer used by the source HTTP handlers.
func (s *Server) newSourceIndexer() (sourceIndexFunc, bool) {
	service, ok := s.searchService()
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, sourceID string) error {
		var id pgtype.UUID
		if err := id.Scan(strings.TrimSpace(sourceID)); err != nil || !id.Valid {
			return fmt.Errorf("некорректный ID источника: %q", sourceID)
		}
		return service.IndexSource(ctx, id)
	}, true
}

// EnableSourceAutoIndex включает фоновую индексацию источников, созданных
// через POST /sources и POST /sources/upload. Вызывается из cmd/server после
// создания сервера; тесты и demo API с in-memory store остаются без индексации.
// Возвращает false, если индексация недоступна (нет PostgreSQL).
func (s *Server) EnableSourceAutoIndex() bool {
	indexer, ok := s.newSourceIndexer()
	if !ok {
		s.indexSource = nil
		return false
	}
	s.indexSource = indexer
	return true
}

// SetSourceIndexer подменяет фоновый индексатор (тесты, альтернативное wiring).
func (s *Server) SetSourceIndexer(index func(ctx context.Context, sourceID string) error) {
	s.indexSource = index
}

// indexSourceAsync запускает индексацию в фоне: загрузка/создание источника не
// должны ждать холодную модель. Ошибки логируются и не влияют на ответ API;
// повторный запуск возможен через POST /sources/{id}/index или backfill.
func (s *Server) indexSourceAsync(sourceID string) {
	index := s.indexSource
	if index == nil || strings.TrimSpace(sourceID) == "" {
		return
	}
	go func() {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), sourceAutoIndexTimeout)
		defer cancel()
		if err := index(ctx, sourceID); err != nil {
			logging.L().Warn("source_auto_index_failed",
				"source_id", sourceID,
				"error", err,
				"duration_ms", time.Since(started).Milliseconds())
			return
		}
		logging.L().Info("source_auto_indexed",
			"source_id", sourceID,
			"duration_ms", time.Since(started).Milliseconds())
	}()
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	if !s.allowInterviewAccess(w, r, r.PathValue("id")) {
		return
	}
	var id pgtype.UUID
	if err := id.Scan(r.PathValue("id")); err != nil {
		writeErr(w, 400, "BAD_REQUEST", "ожидается UUID")
		return
	}
	limit := 5
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeErr(w, 400, "BAD_REQUEST", "limit должен быть от 1 до 100")
			return
		}
	}
	service, ok := s.searchService()
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "нужна PostgreSQL")
		return
	}
	searchFunc := service.Search
	strategy := r.URL.Query().Get("strategy")
	if strategy == "hybrid" {
		searchFunc = service.HybridSearch
	} else if strategy != "" && strategy != "vector" {
		writeErr(w, 400, "BAD_REQUEST", "strategy должен быть vector или hybrid")
		return
	}
	hits, mode, err := searchFunc(r.Context(), id, r.URL.Query().Get("q"), limit)
	if err != nil {
		logging.L().Warn("search_failed", "run_id", r.PathValue("id"), "strategy", strategy, "error", err)
		writeErr(w, 400, "SEARCH_FAILED", "не удалось выполнить поиск; проверьте запрос и пакет поиска")
		return
	}
	nodeByQuote := map[string]string{}
	if answers, answerErr := s.store.GetAnswers(r.PathValue("id")); answerErr == nil {
		for _, answer := range answers {
			if answer.NodeID != "" && answer.Quote != "" {
				nodeByQuote[answer.Quote] = answer.NodeID
			}
		}
	}
	enriched := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		item := map[string]any{
			"ref_id":    hit.RefID,
			"source_id": hit.SourceID,
			"quote":     hit.Quote,
			"score":     hit.Score,
		}
		if nodeID := nodeByQuote[hit.Quote]; nodeID != "" {
			item["node_id"] = nodeID
		}
		enriched = append(enriched, item)
	}
	writeJSON(w, 200, map[string]any{"mode": mode, "hits": enriched})
}

// handleIndexSource синхронно перестраивает индекс одного источника.
// Успех — 204; ошибки различают отсутствующий/пустой источник, изменённый во
// время индексации текст и недоступную модель/схему pgvector.
func (s *Server) handleIndexSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	var id pgtype.UUID
	if err := id.Scan(r.PathValue("id")); err != nil || !id.Valid {
		writeErr(w, 400, "BAD_REQUEST", "ожидается UUID")
		return
	}
	service, ok := s.searchService()
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "нужна PostgreSQL")
		return
	}
	// Отдельная проверка существования даёт 404 вместо невнятной ошибки
	// «источник не найден» из транзакции индексации.
	if pg, isPG := s.store.(*PGStore); isPG && pg.Pool != nil {
		var exists bool
		if err := pg.Pool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM sources WHERE id=$1)", id).Scan(&exists); err != nil {
			logging.L().Warn("source_index_check_failed", "source_id", id.String(), "error", err)
			writeErr(w, http.StatusBadGateway, "INDEX_FAILED", "не удалось проверить источник: "+err.Error())
			return
		}
		if !exists {
			writeErr(w, http.StatusNotFound, "SOURCE_NOT_FOUND", "источник не найден")
			return
		}
	}
	started := time.Now()
	if err := service.IndexSource(r.Context(), id); err != nil {
		status, code, message := indexSourceError(err)
		logging.L().Warn("source_index_failed",
			"source_id", id.String(),
			"error", err,
			"duration_ms", time.Since(started).Milliseconds())
		writeErr(w, status, code, message)
		return
	}
	logging.L().Info("source_indexed",
		"source_id", id.String(),
		"model", service.Model.Model,
		"duration_ms", time.Since(started).Milliseconds())
	w.WriteHeader(http.StatusNoContent)
}

// indexSourceError переводит ошибки IndexSource в HTTP-статусы и коды.
func indexSourceError(err error) (int, string, string) {
	switch {
	case errors.Is(err, search.ErrSourceNotFound):
		return http.StatusNotFound, "SOURCE_NOT_FOUND", "источник не найден"
	case errors.Is(err, search.ErrEmptySource):
		return http.StatusUnprocessableEntity, "EMPTY_SOURCE", "источник не содержит текста: добавьте содержимое и повторите индексацию"
	case errors.Is(err, search.ErrSourceChanged):
		return http.StatusConflict, "SOURCE_CHANGED", "источник изменился во время векторизации; повторите индексацию"
	default:
		// Сбой модели или отсутствие таблиц migrations_optional/vector:
		// прежний индекс не тронут, повтор после исправления безопасен.
		return http.StatusBadGateway, "INDEX_FAILED", "не удалось получить векторы; прежний индекс сохранён: " + err.Error()
	}
}
