package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// Подтверждение -> транскрипт интервью (mentor feature #6).
//
// Контракт:
//
//	GET /api/v1/runs/{id}/evidence/{evidence_id}/context
//
// Endpoint отдаёт всё, что нужно фронтенду, чтобы после перезагрузки страницы
// прокрутить чат к сообщению, из которого взята цитата подтверждения, и
// подсветить её:
//
//	{
//	  "run_id": "…",
//	  "evidence": {id, node_id, node_title, source_id, source_title, source_url,
//	               ref_id, quote, status},
//	  "messages": [{id, role, content, sequence, created_at}, …],
//	  "match": {
//	    "message_id": "…",
//	    "sequence": 4,
//	    "role": "user",
//	    "strategy": "answer_index",
//	    "contains_quote": true,
//	    "quote": "…",
//	    "quote_start": 12,     // смещения в рунах в content, если найдены точно
//	    "quote_end": 47
//	  } | null,
//	  "note": "…"            // человекочитаемое объяснение, почему выбран match
//	}
//
// Сопоставление ref_id -> сообщение детерминированное, в порядке приоритета:
//
//  1. ref_id целиком совпадает с id сохранённого сообщения чата
//     (id имеют вид "msg-<run_id>-<sequence>");
//  2. ref_id = answer-N / a-N / ответ-N -> N-я реплика пользователя;
//     ref_id = q-N / question-N / вопрос-N -> N-й вопрос интервьюера;
//  3. ref_id = msg-N / message-N / m-N / просто N -> сообщение с sequence N
//     (если такого sequence нет — N-е сообщение диалога, 1-based);
//  4. иначе — поиск цитаты в тексте сообщений: точное вхождение, затем без
//     учёта регистра, затем без учёта пробелов; при равенстве предпочитается
//     реплика пользователя и более ранняя по sequence;
//  5. иначе — лучшее лексическое совпадение (доля общих значимых токенов,
//     порог 0.6) с той же детерминированной сортировкой;
//  6. иначе match=null.
//
// Если ref_id распознан, но цитаты в его сообщении нет, endpoint всё равно
// возвращает это сообщение (contains_quote=false), чтобы фронтенд мог
// проскроллить к нему; если цитата нашлась в другом сообщении, побеждает
// цитата. mapping-ограничение честно описано в note, а не замаскировано.
//
// Сообщения читаются из durable-таблицы run_chat_messages (PGStore), поэтому
// endpoint работает и после рестарта backend; если хранилище не умеет отдавать
// транскрипт (in-memory demo), используется chat-сервис, а при его отсутствии
// отдаётся пустой список.
//
// Права: любая аутентифицированная роль (admin/teacher/rop/student/guest) —
// это read-only просмотр. Коды: 200 — контекст; 400 — пустой id; 401 — нет
// токена; 403 — роль не распознана; 404 — прогон или подтверждение не найдены;
// 500 — ошибка БД; 501 — хранилище не поддерживает контекст подтверждений.

// Стратегии сопоставления подтверждения с сообщением чата. Значения входят в
// публичный контракт: фронтенд может показывать их в tooltip.
const (
	evidenceMatchStrategyMessageID     = "message_id"
	evidenceMatchStrategyAnswerIndex   = "answer_index"
	evidenceMatchStrategyQuestionIndex = "question_index"
	evidenceMatchStrategySequence      = "sequence"
	evidenceMatchStrategyQuote         = "quote"
	evidenceMatchStrategyTokenOverlap  = "token_overlap"
)

// evidenceMatchTokenThreshold — минимальная доля общих значимых токенов
// цитаты, при которой сообщение считается лексическим совпадением.
const evidenceMatchTokenThreshold = 0.6

var (
	evidenceAnswerRefPattern   = regexp.MustCompile(`^(?:answer|a|ответ)[-_ ]?([0-9]+)$`)
	evidenceQuestionRefPattern = regexp.MustCompile(`^(?:question|q|вопрос)[-_ ]?([0-9]+)$`)
	evidenceSequenceRefPattern = regexp.MustCompile(`^(?:msg|message|m)[-_ ]?([0-9]+)$`)
	evidenceNumberRefPattern   = regexp.MustCompile(`^([0-9]+)$`)
)

// chatTranscriptMessage — одно сообщение сохранённого диалога. Это публичное
// представление run_chat_messages/chat.Message: sequence — 1-based позиция в
// диалоге, по ней фронтенд находит DOM-узел сообщения.
type chatTranscriptMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Sequence  int       `json:"sequence"`
	CreatedAt time.Time `json:"created_at"`
}

// evidenceContextItem — подтверждение вместе с контекстом узла/источника.
// SourceTitle/SourceURL дублируют то, что уже отдаёт очередь проверки, чтобы
// панель «почему этот узел» не делала дополнительные запросы.
type evidenceContextItem struct {
	ID             string                `json:"id"`
	NodeID         string                `json:"node_id"`
	NodeTitle      string                `json:"node_title,omitempty"`
	SourceID       string                `json:"source_id"`
	SourceTitle    string                `json:"source_title,omitempty"`
	SourceType     domain.SourceType     `json:"source_type,omitempty"`
	SourceURL      string                `json:"source_url,omitempty"`
	RefID          string                `json:"ref_id,omitempty"`
	Quote          string                `json:"quote"`
	Status         domain.EvidenceStatus `json:"status"`
	ClaimOrigin    domain.ClaimOrigin    `json:"claim_origin,omitempty"`
	InterviewID    string                `json:"interview_id,omitempty"`
	RespondentName string                `json:"respondent_name,omitempty"`
}

// evidenceTranscriptMatch — найденное сообщение и позиция цитаты в нём.
// QuoteStart/QuoteEnd заданы в рунах content только когда цитата найдена
// дословно (с учётом регистра или без): frontend подсвечивает срез
// content[quote_start:quote_end]. Для лексического совпадения смещений нет.
type evidenceTranscriptMatch struct {
	MessageID     string  `json:"message_id"`
	Sequence      int     `json:"sequence"`
	Role          string  `json:"role"`
	Strategy      string  `json:"strategy"`
	ContainsQuote bool    `json:"contains_quote"`
	Quote         string  `json:"quote"`
	QuoteStart    int     `json:"quote_start,omitempty"`
	QuoteEnd      int     `json:"quote_end,omitempty"`
	Score         float64 `json:"score,omitempty"`
}

// evidenceContextResponse — публичный DTO endpoint'а.
type evidenceContextResponse struct {
	RunID    string                   `json:"run_id"`
	Evidence evidenceContextItem      `json:"evidence"`
	Messages []chatTranscriptMessage  `json:"messages"`
	Match    *evidenceTranscriptMatch `json:"match"`
	Note     string                   `json:"note"`
}

// evidenceContextReader — optional-возможность хранилища: подтверждение и
// сохранённый диалог прогона. Реализуется *PGStore (SQL) и *memStore (память;
// диалог пуст, его подхватывает chat-сервис).
type evidenceContextReader interface {
	getRunEvidenceContext(ctx context.Context, runID, evidenceID string) (evidenceContextItem, error)
}

// evidenceTranscriptReader — optional-возможность хранилища отдавать durable
// историю чата без chat-сервиса.
type evidenceTranscriptReader interface {
	listEvidenceTranscriptMessages(ctx context.Context, runID string) ([]chatTranscriptMessage, error)
}

type interviewTranscriptReader interface {
	listInterviewTranscriptMessages(context.Context, string, string) ([]chatTranscriptMessage, error)
}

// getRunEvidenceContext ищет подтверждение строго внутри прогона: evidence.id
// и evidence.run_id. Подтверждение другого прогона неотличимо от
// несуществующего (404), чтобы endpoint нельзя было использовать для чтения
// чужих цитат.
func (p *PGStore) getRunEvidenceContext(ctx context.Context, runID, evidenceID string) (evidenceContextItem, error) {
	var item evidenceContextItem
	if p == nil || p.Pool == nil {
		return item, ErrServiceUnavailable
	}
	if !validUUID(runID) || !validUUID(evidenceID) {
		return item, ErrNotFound
	}
	var status string
	err := p.Pool.QueryRow(ctx, `SELECT e.id::text,e.node_id::text,COALESCE(n.title,''),
	e.source_id::text,COALESCE(s.title,''),s.type,COALESCE(s.source_url,''),
	COALESCE(e.ref_id,''),e.quote,COALESCE(e.status,'pending'),COALESCE(e.claim_origin,'unclassified'),COALESCE(e.interview_id::text,''),COALESCE(i.respondent_name,'')
FROM evidence e
JOIN nodes n ON n.id=e.node_id
JOIN sources s ON s.id=e.source_id
LEFT JOIN run_interviews i ON i.id=e.interview_id
WHERE e.id=$1 AND e.run_id=$2`, evidenceID, runID).
		Scan(&item.ID, &item.NodeID, &item.NodeTitle, &item.SourceID, &item.SourceTitle, &item.SourceType,
			&item.SourceURL, &item.RefID, &item.Quote, &status, &item.ClaimOrigin, &item.InterviewID, &item.RespondentName)
	if err != nil {
		return item, translate(err)
	}
	item.Status = domain.EvidenceStatus(status)
	if item.Status == "" {
		item.Status = domain.EvidencePending
	}
	return item, nil
}

// listEvidenceTranscriptMessages читает durable-историю интервью. Сообщения
// упорядочены по sequence и id, лимит 500 защищает ответ от аномально
// длинного диалога.
func (p *PGStore) listEvidenceTranscriptMessages(ctx context.Context, runID string) ([]chatTranscriptMessage, error) {
	if p == nil || p.Pool == nil {
		return nil, ErrServiceUnavailable
	}
	if !validUUID(runID) {
		return nil, ErrNotFound
	}
	rows, err := p.Pool.Query(ctx, `SELECT id,role,content,sequence,created_at
FROM run_chat_messages WHERE run_id=$1 ORDER BY sequence,id LIMIT 500`, runID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	messages := make([]chatTranscriptMessage, 0, 32)
	for rows.Next() {
		var message chatTranscriptMessage
		if err = rows.Scan(&message.ID, &message.Role, &message.Content, &message.Sequence, &message.CreatedAt); err != nil {
			return nil, translate(err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (p *PGStore) listInterviewTranscriptMessages(ctx context.Context, runID, interviewID string) ([]chatTranscriptMessage, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id,role,content,sequence,created_at FROM run_chat_messages WHERE run_id=$1 AND interview_id=$2 ORDER BY sequence,id LIMIT 500`, runID, interviewID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	messages := make([]chatTranscriptMessage, 0, 32)
	for rows.Next() {
		var m chatTranscriptMessage
		if err = rows.Scan(&m.ID, &m.Role, &m.Content, &m.Sequence, &m.CreatedAt); err != nil {
			return nil, translate(err)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// getRunEvidenceContext — in-memory реализация: подтверждение ищется среди
// evidence прогона, заголовки узла/источника подтягиваются из memStore.
func (m *memStore) getRunEvidenceContext(_ context.Context, runID, evidenceID string) (evidenceContextItem, error) {
	var item evidenceContextItem
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.runs[runID]; !ok {
		return item, ErrNotFound
	}
	var evidence domain.Evidence
	found := false
	for _, id := range m.runEvidenceIDs[runID] {
		if id != evidenceID {
			continue
		}
		if value, ok := m.evidences[id]; ok {
			evidence = value
			found = true
			break
		}
	}
	if !found {
		return item, ErrNotFound
	}
	item = evidenceContextItem{
		ID:          evidence.ID,
		NodeID:      evidence.NodeID,
		SourceID:    evidence.SourceID,
		RefID:       evidence.RefID,
		Quote:       evidence.Quote,
		Status:      m.evidenceStatusLocked(evidence.ID, evidence),
		ClaimOrigin: evidence.ClaimOrigin,
		InterviewID: evidence.InterviewID,
	}
	for _, node := range m.nodes[runID] {
		if node.ID == evidence.NodeID {
			item.NodeTitle = node.Title
			break
		}
	}
	if source, ok := m.sources[evidence.SourceID]; ok {
		item.SourceTitle = source.Title
		item.SourceType = source.Type
		item.SourceURL = source.SourceURL
	}
	return item, nil
}

// listEvidenceTranscriptMessages — in-memory хранилище не сохраняет диалог;
// хендлер подхватит сообщения из chat-сервиса.
func (m *memStore) listEvidenceTranscriptMessages(_ context.Context, _ string) ([]chatTranscriptMessage, error) {
	return []chatTranscriptMessage{}, nil
}

// chatMessagesToTranscript конвертирует сообщения chat-сервиса в публичный DTO,
// сохраняя sequence: по нему страница находит сообщение после перезагрузки.
func chatMessagesToTranscript(messages []chat.Message) []chatTranscriptMessage {
	out := make([]chatTranscriptMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, chatTranscriptMessage{
			ID:        message.ID,
			Role:      message.Role,
			Content:   message.Content,
			Sequence:  message.Sequence,
			CreatedAt: message.CreatedAt,
		})
	}
	return out
}

// resolveEvidenceTranscriptMessages выбирает источник истории диалога:
// durable-таблица, затем chat-сервис (активная in-memory сессия), затем
// пустой список. Ошибка БД не маскируется: она вернёт 500.
func (s *Server) resolveEvidenceTranscriptMessages(ctx context.Context, runID string) ([]chatTranscriptMessage, error) {
	if reader, ok := s.store.(evidenceTranscriptReader); ok {
		messages, err := reader.listEvidenceTranscriptMessages(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			return messages, nil
		}
	}
	if s.chat != nil {
		messages, err := s.chat.Messages(runID)
		switch {
		case err == nil:
			return chatMessagesToTranscript(messages), nil
		case errors.Is(err, chat.ErrNotFound):
			// У прогона нет чата — для контекста подтверждения это пустота.
		case err != nil:
			return nil, err
		}
	}
	return []chatTranscriptMessage{}, nil
}

// locateEvidenceQuote ищет цитату в тексте сообщения: дословно, затем без
// учёта регистра (со смещениями в рунах), затем по нормализованным пробелам
// (без смещений, подсветка вычисляется фронтендом). Located=false при
// нормализованном совпадении: смещения в исходном тексте не определены.
type evidenceQuoteLocation struct {
	Contains bool
	Located  bool
	Start    int
	End      int
}

func locateEvidenceQuote(quote, content string) evidenceQuoteLocation {
	quote = strings.TrimSpace(quote)
	if quote == "" || content == "" {
		return evidenceQuoteLocation{}
	}
	if index := strings.Index(content, quote); index >= 0 {
		start := utf8.RuneCountInString(content[:index])
		return evidenceQuoteLocation{Contains: true, Located: true, Start: start, End: start + utf8.RuneCountInString(quote)}
	}
	lowerContent := strings.ToLower(content)
	lowerQuote := strings.ToLower(quote)
	if index := strings.Index(lowerContent, lowerQuote); index >= 0 {
		start := utf8.RuneCountInString(lowerContent[:index])
		return evidenceQuoteLocation{Contains: true, Located: true, Start: start, End: start + utf8.RuneCountInString(lowerQuote)}
	}
	if normalizedQuote := normalizeEvidenceText(quote); normalizedQuote != "" &&
		strings.Contains(normalizeEvidenceText(content), normalizedQuote) {
		return evidenceQuoteLocation{Contains: true}
	}
	return evidenceQuoteLocation{}
}

// normalizeEvidenceText схлопывает пробелы и приводит к нижнему регистру —
// используется для «мягкого» поиска цитаты в сообщении.
func normalizeEvidenceText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

// parseEvidenceRefNumber вытаскивает N из ref-паттерна; 0 означает «не число».
func parseEvidenceRefNumber(pattern *regexp.Regexp, value string) int {
	match := pattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number < 1 {
		return 0
	}
	return number
}

// resolveEvidenceRef сопоставляет ref_id с сообщением по документированным
// соглашениям. Возвращает nil, если ref_id не распознан — тогда решение
// принимает поиск по тексту цитаты.
func resolveEvidenceRef(refID string, messages []chatTranscriptMessage) (*chatTranscriptMessage, string) {
	refID = strings.TrimSpace(refID)
	if refID == "" || len(messages) == 0 {
		return nil, ""
	}
	for index := range messages {
		if messages[index].ID == refID {
			copied := messages[index]
			return &copied, evidenceMatchStrategyMessageID
		}
	}
	// UUID внутри id может прийти из модели в другом регистре: сравниваем ещё
	// раз без учёта регистра, прежде чем переходить к номерным соглашениям.
	for index := range messages {
		if strings.EqualFold(messages[index].ID, refID) {
			copied := messages[index]
			return &copied, evidenceMatchStrategyMessageID
		}
	}

	lower := strings.ToLower(refID)
	var ordinal int
	var strategy, role string
	switch {
	case evidenceAnswerRefPattern.MatchString(lower):
		ordinal = parseEvidenceRefNumber(evidenceAnswerRefPattern, lower)
		strategy, role = evidenceMatchStrategyAnswerIndex, "user"
	case evidenceQuestionRefPattern.MatchString(lower):
		ordinal = parseEvidenceRefNumber(evidenceQuestionRefPattern, lower)
		strategy, role = evidenceMatchStrategyQuestionIndex, "agent"
	case evidenceSequenceRefPattern.MatchString(lower):
		ordinal = parseEvidenceRefNumber(evidenceSequenceRefPattern, lower)
		strategy = evidenceMatchStrategySequence
	case evidenceNumberRefPattern.MatchString(lower):
		ordinal = parseEvidenceRefNumber(evidenceNumberRefPattern, lower)
		strategy = evidenceMatchStrategySequence
	default:
		return nil, ""
	}
	if ordinal == 0 {
		return nil, ""
	}

	if strategy == evidenceMatchStrategyAnswerIndex || strategy == evidenceMatchStrategyQuestionIndex {
		seen := 0
		for index := range messages {
			if !strings.EqualFold(messages[index].Role, role) {
				continue
			}
			seen++
			if seen == ordinal {
				copied := messages[index]
				return &copied, strategy
			}
		}
		return nil, ""
	}

	// msg-N: сначала точный sequence, затем N-е сообщение диалога (1-based).
	for index := range messages {
		if messages[index].Sequence == ordinal {
			copied := messages[index]
			return &copied, strategy
		}
	}
	if ordinal <= len(messages) {
		copied := messages[ordinal-1]
		return &copied, strategy
	}
	return nil, ""
}

// findQuoteMessage ищет сообщение, в тексте которого есть цитата. Сначала
// проверяются реплики пользователя (ответы), затем сообщения агента; внутри
// группы побеждает более ранняя по sequence. Возвращает nil, если цитаты нет
// ни в одном сообщении.
func findQuoteMessage(quote string, messages []chatTranscriptMessage) (*chatTranscriptMessage, evidenceQuoteLocation) {
	var userMatch, agentMatch *chatTranscriptMessage
	var userLocation, agentLocation evidenceQuoteLocation
	for index := range messages {
		location := locateEvidenceQuote(quote, messages[index].Content)
		if !location.Contains {
			continue
		}
		if strings.EqualFold(messages[index].Role, "user") {
			if userMatch == nil {
				copied := messages[index]
				userMatch, userLocation = &copied, location
			}
			continue
		}
		if agentMatch == nil {
			copied := messages[index]
			agentMatch, agentLocation = &copied, location
		}
	}
	if userMatch != nil {
		return userMatch, userLocation
	}
	return agentMatch, agentLocation
}

// evidenceMatchTokens — значимые токены цитаты/сообщения для лексического
// fallback: нижний регистр, ё->е, без стоп-слов и токенов короче 3 символов.
func evidenceMatchTokens(text string) map[string]struct{} {
	tokens := make(map[string]struct{}, 16)
	for _, token := range zunTokens(text) {
		if utf8.RuneCountInString(token) < 3 {
			continue
		}
		if _, stop := evidenceTranscriptStopwords[token]; stop {
			continue
		}
		tokens[token] = struct{}{}
	}
	return tokens
}

// evidenceTranscriptStopwords — частотные слова, не несущие смысла для
// сравнения цитаты и реплики.
var evidenceTranscriptStopwords = map[string]struct{}{
	"для": {}, "или": {}, "при": {}, "про": {}, "над": {}, "под": {}, "без": {},
	"что": {}, "как": {}, "это": {}, "его": {}, "она": {}, "они": {},
	"все": {}, "весь": {}, "был": {}, "была": {}, "были": {}, "быть": {},
	"так": {}, "там": {}, "тут": {}, "где": {}, "когда": {}, "если": {},
	"чтобы": {}, "также": {}, "только": {}, "более": {}, "менее": {},
	"этот": {}, "эта": {}, "эти": {}, "свой": {}, "свои": {}, "свое": {},
	"который": {}, "которая": {}, "которые": {}, "которое": {},
	"очень": {}, "могу": {}, "может": {}, "можно": {}, "нужно": {},
	"есть": {}, "нет": {}, "the": {}, "and": {}, "for": {}, "with": {},
	"что-то": {}, "каждый": {}, "каждую": {}, "день": {}, "работа": {},
	"работе": {}, "работаю": {}, "делаю": {}, "делать": {}, "свою": {},
}

// findTokenOverlapMessage выбирает сообщение с максимальной долей общих
// значимых токенов. Детерминизм: score сравнивается строго, при равенстве
// предпочитается реплика пользователя, затем более ранняя по sequence.
func findTokenOverlapMessage(quote string, messages []chatTranscriptMessage) (*chatTranscriptMessage, float64) {
	quoteTokens := evidenceMatchTokens(quote)
	if len(quoteTokens) == 0 {
		return nil, 0
	}
	var best *chatTranscriptMessage
	var bestScore float64
	bestIsUser := false
	for index := range messages {
		messageTokens := evidenceMatchTokens(messages[index].Content)
		if len(messageTokens) == 0 {
			continue
		}
		intersection := 0
		for token := range quoteTokens {
			if _, ok := messageTokens[token]; ok {
				intersection++
			}
		}
		if intersection == 0 || (intersection < 2 && len(quoteTokens) > 1) {
			continue
		}
		containment := float64(intersection) / float64(len(quoteTokens))
		union := len(quoteTokens) + len(messageTokens) - intersection
		jaccard := float64(intersection) / float64(union)
		score := 0.7*containment + 0.3*jaccard
		if score < evidenceMatchTokenThreshold {
			continue
		}
		isUser := strings.EqualFold(messages[index].Role, "user")
		better := best == nil || score > bestScore
		if !better && best != nil && score == bestScore && isUser && !bestIsUser {
			better = true
		}
		if better {
			copied := messages[index]
			best, bestScore, bestIsUser = &copied, score, isUser
		}
	}
	if best == nil {
		return nil, 0
	}
	return best, math.Round(bestScore*1000) / 1000
}

// buildEvidenceTranscriptMatch собирает публичное представление совпадения.
func buildEvidenceTranscriptMatch(message chatTranscriptMessage, strategy string, location evidenceQuoteLocation, quote string, score float64) *evidenceTranscriptMatch {
	match := &evidenceTranscriptMatch{
		MessageID:     message.ID,
		Sequence:      message.Sequence,
		Role:          message.Role,
		Strategy:      strategy,
		ContainsQuote: location.Contains,
		Quote:         quote,
		Score:         score,
	}
	if location.Located {
		match.QuoteStart = location.Start
		match.QuoteEnd = location.End
	}
	return match
}

// evidenceTranscriptNote объясняет выбранную стратегию человекочитаемым
// текстом. Строки стабильны: на них опираются тесты и tooltip фронтенда.
func evidenceTranscriptNote(strategy string) string {
	switch strategy {
	case evidenceMatchStrategyMessageID:
		return "ref_id совпадает с идентификатором сообщения интервью"
	case evidenceMatchStrategyAnswerIndex:
		return "ref_id указывает на номер ответа (answer-N): выбрана N-я реплика пользователя"
	case evidenceMatchStrategyQuestionIndex:
		return "ref_id указывает на номер вопроса (q-N): выбран N-й вопрос интервьюера"
	case evidenceMatchStrategySequence:
		return "ref_id указывает на порядковый номер сообщения в диалоге (msg-N)"
	case evidenceMatchStrategyQuote:
		return "точная привязка по ref_id не удалась: цитата найдена в тексте сообщения"
	case evidenceMatchStrategyTokenOverlap:
		return "точное совпадение не найдено: выбрано сообщение с максимальным лексическим совпадением"
	default:
		return "не удалось сопоставить подтверждение с сообщениями интервью"
	}
}

// matchEvidenceToTranscript вычисляет соответствие «подтверждение -> сообщение
// диалога» по документированным правилам (см. комментарий выше). Возвращает
// match=nil и объяснение, если сопоставить не удалось.
func matchEvidenceToTranscript(refID, quote string, messages []chatTranscriptMessage) (*evidenceTranscriptMatch, string) {
	if len(messages) == 0 {
		return nil, "у прогона нет сохранённых сообщений интервью"
	}
	quote = strings.TrimSpace(quote)

	if refMessage, refStrategy := resolveEvidenceRef(refID, messages); refMessage != nil {
		location := locateEvidenceQuote(quote, refMessage.Content)
		if location.Contains {
			return buildEvidenceTranscriptMatch(*refMessage, refStrategy, location, quote, 0), evidenceTranscriptNote(refStrategy)
		}
		if found, foundLocation := findQuoteMessage(quote, messages); found != nil {
			return buildEvidenceTranscriptMatch(*found, evidenceMatchStrategyQuote, foundLocation, quote, 0),
				"ref_id указывает на другое сообщение: цитата найдена в тексте чата"
		}
		if found, score := findTokenOverlapMessage(quote, messages); found != nil {
			return buildEvidenceTranscriptMatch(*found, evidenceMatchStrategyTokenOverlap, evidenceQuoteLocation{}, quote, score),
				evidenceTranscriptNote(evidenceMatchStrategyTokenOverlap)
		}
		return buildEvidenceTranscriptMatch(*refMessage, refStrategy, evidenceQuoteLocation{}, quote, 0),
			"ref_id сопоставлен с сообщением, но цитата в нём не найдена"
	}

	if found, location := findQuoteMessage(quote, messages); found != nil {
		return buildEvidenceTranscriptMatch(*found, evidenceMatchStrategyQuote, location, quote, 0),
			evidenceTranscriptNote(evidenceMatchStrategyQuote)
	}
	if found, score := findTokenOverlapMessage(quote, messages); found != nil {
		return buildEvidenceTranscriptMatch(*found, evidenceMatchStrategyTokenOverlap, evidenceQuoteLocation{}, quote, score),
			evidenceTranscriptNote(evidenceMatchStrategyTokenOverlap)
	}
	return nil, evidenceTranscriptNote("")
}

// handleEvidenceContext godoc
// @Summary      Evidence context in the interview transcript
// @Description  Returns the evidence item (quote, ref_id, node/source titles) plus the full saved chat transcript of the run and the message that best matches the evidence, so the UI can scroll to and highlight the quote after reload. Matching is deterministic: message id, answer-N/user reply, q-N/question, msg-N/sequence, then quote text, then best lexical overlap; match is null when nothing matches and note explains the chosen strategy. Read roles only.
// @Tags         evidences
// @Produce      json
// @Param        id           path  string  true  "Run ID"
// @Param        evidence_id  path  string  true  "Evidence ID"
// @Success      200  {object}  evidenceContextResponse
// @Failure      400  {object}  errorBody
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      500  {object}  errorBody
// @Failure      501  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/evidence/{evidence_id}/context [get]
func (s *Server) handleEvidenceContext(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	runID := strings.TrimSpace(r.PathValue("id"))
	evidenceID := strings.TrimSpace(r.PathValue("evidence_id"))
	if runID == "" || evidenceID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужны идентификаторы исследования и подтверждения")
		return
	}
	reader, ok := s.store.(evidenceContextReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "контекст подтверждения не поддерживается хранилищем")
		return
	}
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	evidence, err := reader.getRunEvidenceContext(r.Context(), runID, evidenceID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "подтверждение не найдено")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось получить подтверждение")
		return
	}
	var messages []chatTranscriptMessage
	if evidence.InterviewID != "" {
		if scoped, ok := s.store.(interviewTranscriptReader); ok {
			messages, err = scoped.listInterviewTranscriptMessages(r.Context(), runID, evidence.InterviewID)
		} else {
			messages = []chatTranscriptMessage{}
		}
	} else if evidence.SourceType == domain.SourceInterview {
		if _, isPG := s.store.(*PGStore); isPG {
			messages = []chatTranscriptMessage{}
		} else {
			messages, err = s.resolveEvidenceTranscriptMessages(r.Context(), runID)
		}
	} else {
		messages = []chatTranscriptMessage{}
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить историю интервью")
		return
	}
	match, note := matchEvidenceToTranscript(evidence.RefID, evidence.Quote, messages)
	writeJSON(w, http.StatusOK, evidenceContextResponse{
		RunID:    runID,
		Evidence: evidence,
		Messages: messages,
		Match:    match,
		Note:     note,
	})
}
