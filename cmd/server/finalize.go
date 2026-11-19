package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llmobs"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/search"

	"github.com/jackc/pgx/v5/pgtype"
)

// sourceRetriever returns only the source fragments relevant to a run query.
type sourceRetriever func(ctx context.Context, runID, query string) ([]domain.Source, error)

// finalizeStore is the slice of the persistence layer the post-interview
// pipeline needs. *api.PGStore implements it in production; tests inject a fake
// so the finalizer can be verified without PostgreSQL or a live LLM.
type finalizeStore interface {
	SetStage(id string, stage domain.Stage) error
	ListRunSources(ctx context.Context, runID string) ([]domain.Source, error)
	CreateInterviewSource(ctx context.Context, runID, sourceID, transcript string) error
	SaveTree(ctx context.Context, id string, nodes []domain.Node) error
	SaveRunSnapshot(ctx context.Context, runID string) error
	SaveSynthesisReview(ctx context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error
}

// interviewAnalyst is the analyst step of the pipeline. It is an interface so a
// fake can stand in for the LLM-backed agent in tests.
type interviewAnalyst interface {
	Analyze(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error)
}

// synthesisRunner is the deterministic merge step of the pipeline.
type synthesisRunner interface {
	Synthesize(ctx context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error)
}

// runSourceContentLister is an optional capability of the persistence layer:
// the analyst needs the full content of the run-linked sources to quote them,
// while the synthesizer only needs metadata. *api.PGStore implements it.
type runSourceContentLister interface {
	ListRunSourceContents(ctx context.Context, runID string) ([]domain.Source, error)
}

// runSourceLinker is an optional capability used to attach fallback sources
// (admin/global or retrieved fragments) to the run so their provenance survives
// finalization.
type runSourceLinker interface {
	LinkRunSources(ctx context.Context, runID string, sourceIDs []string) error
}

// finalizerDeps are the collaborators of one finalization. zunClient is
// optional and only used for the best-effort knowledge/skills enrichment.
type finalizerDeps struct {
	store       finalizeStore
	analyst     interviewAnalyst
	synthesizer synthesisRunner
	zunClient   llm.ChatClient
	retrievers  []sourceRetriever
}

// newTreeFinalizer builds the post-interview pipeline: analyst, synthesizer
// and persistence of the resulting tree. An optional retriever injects RAG
// context instead of sending every source to the model.
func newTreeFinalizer(store finalizeStore, analystClient llm.ChatClient, retrievers ...sourceRetriever) chat.Finalizer {
	return newTreeFinalizerWithVersions(store, analystClient, "", "", retrievers...)
}

func newTreeFinalizerWithVersions(store finalizeStore, analystClient llm.ChatClient, analystVersion, synthesizerVersion string, retrievers ...sourceRetriever) chat.Finalizer {
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		analystOptions := []analyst.Option{}
		if analystVersion != "" {
			analystOptions = append(analystOptions, analyst.WithPromptVersion(analystVersion))
		}
		analystAgent, err := analyst.New(analystClient, analystOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyst: %w", err)
		}
		synthesizerOptions := []synthesizer.Option{}
		if synthesizerVersion != "" {
			synthesizerOptions = append(synthesizerOptions, synthesizer.WithPromptVersion(synthesizerVersion))
		}
		synthesizerAgent, err := synthesizer.New(synthesizerOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: synthesizer: %w", err)
		}
		return finalizeInterview(ctx, finalizerDeps{
			store:       store,
			analyst:     analystAgent,
			synthesizer: synthesizerAgent,
			zunClient:   analystClient,
			retrievers:  retrievers,
		}, runID, profession, session)
	}
}

// finalizeInterview runs analysis, deterministic synthesis, persistence of the
// review artifacts and the final tree for one finished interview.
func finalizeInterview(ctx context.Context, deps finalizerDeps, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
	if deps.store == nil {
		return domain.RunFailed, fmt.Errorf("finalizer: store is required")
	}
	// Every LLM call of the finalizer (analyst and best-effort ZUN enrichment)
	// is made for this run, so the recorder attributes llm_requests to it.
	ctx = llmobs.WithRunID(ctx, runID)
	transcript := transcriptFromSession(session)
	setFinalizeStage(deps.store, runID, domain.StageAnalysis)

	// The sources attached to this run are the only material the analyst is
	// allowed to quote besides the transcript. Their full content is loaded
	// first so the model can produce evidence-backed standard/vacancy nodes; RAG
	// fragments and admin/global sources remain fallbacks for runs without
	// attached sources.
	analystSources := loadRunSourceContents(ctx, deps.store, runID)
	if len(analystSources) == 0 {
		analystSources = retrieveSources(ctx, deps, runID, profession, transcript)
	}
	if len(analystSources) == 0 {
		analystSources = loadGlobalSources(ctx, deps.store)
	}
	// Fallback sources are attached to the run after the fact so their type and
	// priority reach the synthesizer the same way run-linked sources do.
	linkRunSources(ctx, deps.store, runID, analystSources)

	input := agent.AnalystInput{
		SessionID:  session.ID,
		Profession: profession,
		Transcript: transcript,
		Sources:    analystSources,
	}
	analysis, err := deps.analyst.Analyze(ctx, input)
	if err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: analyze: %w", err)
	}

	// The sources linked to this run are what recovers the provenance of the
	// analyst's entities: every evidence item references a source by id. Loading
	// them is best-effort: without metadata the already supported interview-only
	// path must keep working.
	runSources, err := deps.store.ListRunSources(ctx, runID)
	if err != nil {
		log.Printf("finalizer: load run sources for %s: %v", runID, err)
		runSources = nil
	}
	provenanceSources := mergeSources(runSources, analystSources)
	interviewNodes, standardNodes, vacancyNodes := partitionEntitiesByProvenance(analysis.Entities, provenanceSources)
	log.Printf("finalizer: run %s entities=%d sources=%d interview=%d standard=%d vacancy=%d",
		runID, len(analysis.Entities), len(provenanceSources), len(interviewNodes), len(standardNodes), len(vacancyNodes))

	setFinalizeStage(deps.store, runID, domain.StageSynthesis)
	synthesis, err := deps.synthesizer.Synthesize(ctx, buildSynthesizerInput(profession, synthesisSources(runSources, provenanceSources), interviewNodes, standardNodes, vacancyNodes))
	if err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: synthesize: %w", err)
	}

	// Persist conflicts/messages before the tree so the evidence for expert
	// review is not lost if tree persistence fails afterwards. Review storage is
	// auxiliary: a failure is logged but does not fail an otherwise valid run,
	// same as the automatic snapshot below.
	if persistErr := deps.store.SaveSynthesisReview(ctx, runID, synthesis.Conflicts, synthesis.Messages); persistErr != nil {
		log.Printf("finalizer: persist synthesis review for run %s: %v", runID, persistErr)
	}

	setFinalizeStage(deps.store, runID, domain.StageReview)
	nodes := enrichZun(ctx, deps.zunClient, profession, synthesis.Tree)
	if err := saveInterviewTree(ctx, deps.store, runID, profession, transcript, nodes, knownSourceIDs(provenanceSources)); err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: save tree: %w", err)
	}
	setFinalizeStage(deps.store, runID, domain.StageDone)
	return domain.RunDone, nil
}

// loadRunSourceContents returns the full content of the sources linked to run.
// The capability is optional: without it the finalizer keeps the previous
// metadata-only behaviour.
func loadRunSourceContents(ctx context.Context, store finalizeStore, runID string) []domain.Source {
	lister, ok := store.(runSourceContentLister)
	if !ok {
		return nil
	}
	sources, err := lister.ListRunSourceContents(ctx, runID)
	if err != nil {
		log.Printf("finalizer: load run source contents for %s: %v", runID, err)
		return nil
	}
	for i := range sources {
		sources[i].Content = truncateRunes(sources[i].Content, maxFinalizeSourceRunes)
	}
	return sources
}

// maxFinalizeSourceRunes caps the context handed to the analyst. The analyst
// applies its own, smaller cap; this one protects the finalizer from loading
// huge documents into memory.
const maxFinalizeSourceRunes = 4000

// retrieveSources runs the optional RAG retriever for a plain interview run.
func retrieveSources(ctx context.Context, deps finalizerDeps, runID, profession, transcript string) []domain.Source {
	if len(deps.retrievers) == 0 || deps.retrievers[0] == nil {
		return nil
	}
	query := profession + " " + truncateRunes(transcript, 2000)
	sources, err := deps.retrievers[0](ctx, runID, query)
	if err != nil {
		log.Printf("finalizer: retrieve sources for %s: %v", runID, err)
		return nil
	}
	return sources
}

// loadGlobalSources returns the admin-uploaded materials shared by all runs.
func loadGlobalSources(ctx context.Context, store finalizeStore) []domain.Source {
	lister, ok := store.(interface {
		ListGlobalSources(int) ([]domain.Source, error)
	})
	if !ok {
		return nil
	}
	global, err := lister.ListGlobalSources(5)
	if err != nil {
		log.Printf("finalizer: load global sources: %v", err)
		return nil
	}
	for i := range global {
		global[i].Content = truncateRunes(global[i].Content, maxFinalizeSourceRunes)
	}
	return global
}

// linkRunSources attaches fallback sources to the run so the run_sources table
// reflects every material that actually fed the analyst. Errors are non-fatal.
func linkRunSources(ctx context.Context, store finalizeStore, runID string, sources []domain.Source) {
	linker, ok := store.(runSourceLinker)
	if !ok || len(sources) == 0 {
		return
	}
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source.ID) != "" {
			ids = append(ids, source.ID)
		}
	}
	if err := linker.LinkRunSources(ctx, runID, ids); err != nil {
		log.Printf("finalizer: link fallback sources for %s: %v", runID, err)
	}
}

// mergeSources returns the authoritative metadata of the run sources plus any
// analyst source the run does not link yet. Run sources win on id conflicts
// because they are the persisted rows.
func mergeSources(runSources, analystSources []domain.Source) []domain.Source {
	merged := make([]domain.Source, 0, len(runSources)+len(analystSources))
	seen := make(map[string]bool, len(runSources)+len(analystSources))
	for _, source := range runSources {
		if seen[source.ID] {
			continue
		}
		seen[source.ID] = true
		merged = append(merged, source)
	}
	for _, source := range analystSources {
		if seen[source.ID] {
			continue
		}
		seen[source.ID] = true
		merged = append(merged, source)
	}
	return merged
}

// synthesisSources is the metadata list handed to the deterministic
// synthesizer. Run sources are preferred; when the store has none (for example
// an in-memory test double) the analyst sources are used so priorities and
// source types are still available.
func synthesisSources(runSources, provenanceSources []domain.Source) []domain.Source {
	if len(runSources) > 0 {
		return runSources
	}
	return provenanceSources
}

// knownSourceIDs returns the lower-cased ids of sources that exist in the
// database, so the saved tree can keep real source references instead of
// rewriting every evidence item to the interview transcript.
func knownSourceIDs(sources []domain.Source) map[string]bool {
	ids := make(map[string]bool, len(sources))
	for _, source := range sources {
		if validSourceUUID(source.ID) {
			ids[strings.ToLower(source.ID)] = true
		}
	}
	return ids
}

// validSourceUUID reports whether value is a canonical 36-character UUID. The
// finalizer uses it to decide which evidence source ids are real database rows.
func validSourceUUID(value string) bool {
	var id pgtype.UUID
	return id.Scan(value) == nil && id.Valid
}

// partitionEntitiesByProvenance routes the analyst entities into the three
// synthesizer inputs using the declared type of the run sources their evidence
// references.
//
// An entity belongs to Standard (or Vacancy) only when every evidence item is
// backed by a source of that type. Mixed provenance, interview or document
// sources, missing source metadata, unknown ids and evidence-less nodes all fall
// back to the Interview list, which keeps the already supported interview-only
// path working when provenance cannot be established. Node ids, hierarchy and
// evidence are passed through unchanged: partitioning only chooses a slot.
func partitionEntitiesByProvenance(entities []agent.ExtractedEntity, sources []domain.Source) (interview, standard, vacancy []domain.Node) {
	sourceTypes := make(map[string]domain.SourceType, len(sources))
	for _, source := range sources {
		sourceTypes[source.ID] = source.Type
	}
	for _, entity := range entities {
		node := entity.Node
		switch classifyEntityProvenance(node, sourceTypes) {
		case domain.SourceStandard:
			standard = append(standard, node)
		case domain.SourceVacancy:
			vacancy = append(vacancy, node)
		default:
			interview = append(interview, node)
		}
	}
	return interview, standard, vacancy
}

// classifyEntityProvenance reports the single non-interview source type shared
// by all evidence of the node. Anything mixed, unknown or interview-backed is
// reported as interview so the caller keeps the safe default.
func classifyEntityProvenance(node domain.Node, sourceTypes map[string]domain.SourceType) domain.SourceType {
	if len(node.Evidence) == 0 {
		return domain.SourceInterview
	}
	provenance := domain.SourceType("")
	for _, evidence := range node.Evidence {
		sourceType, ok := sourceTypes[evidence.SourceID]
		if !ok {
			return domain.SourceInterview
		}
		if sourceType != domain.SourceStandard && sourceType != domain.SourceVacancy {
			return domain.SourceInterview
		}
		if provenance == "" {
			provenance = sourceType
			continue
		}
		if provenance != sourceType {
			return domain.SourceInterview
		}
	}
	return provenance
}

// buildSynthesizerInput assembles the deterministic merge input. The analyst
// nodes keep the source slot their evidence proves (interview, standard or
// vacancy) so the merge can resolve priorities and actually detect cross-source
// conflicts; the run sources provide the ids, types and priorities for that.
func buildSynthesizerInput(profession string, sources []domain.Source, interviewNodes, standardNodes, vacancyNodes []domain.Node) agent.SynthesizerInput {
	return agent.SynthesizerInput{
		Profession: profession,
		Sources:    sources,
		Interview:  interviewNodes,
		Standard:   standardNodes,
		Vacancy:    vacancyNodes,
	}
}

// setFinalizeStage обновляет этап прогона, чтобы UI видел прогресс пайплайна.
func setFinalizeStage(store finalizeStore, runID string, stage domain.Stage) {
	if store == nil {
		return
	}
	_ = store.SetStage(runID, stage)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// ragSettings — параметры RAG-ретривера из окружения. Отдельная структура
// позволяет проверить умолчания и выбор стратегии без PostgreSQL и модели.
type ragSettings struct {
	OllamaURL  string
	EmbedModel string
	Strategy   string // "vector" или "hybrid"
	Limit      int
}

// ragSettingsFromEnv читает OLLAMA_URL, OLLAMA_EMBED_MODEL, RAG_SEARCH_STRATEGY
// и RAG_SEARCH_LIMIT. Пустые значения дают локальную Ollama (nomic-embed-text)
// и векторный поиск с top-8 фрагментов — совместимо с прежним поведением.
func ragSettingsFromEnv() ragSettings {
	cfg := search.ConfigFromEnv()
	settings := ragSettings{
		OllamaURL:  cfg.OllamaURL,
		EmbedModel: cfg.EmbedModel,
		Strategy:   "vector",
		Limit:      8,
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RAG_SEARCH_STRATEGY"))) {
	case "", "vector":
		settings.Strategy = "vector"
	case "hybrid":
		settings.Strategy = "hybrid"
	default:
		// Неизвестная стратегия не должна ломать finalizer: остаёмся на vector.
		logging.Logger.Warn("rag_search_strategy_unknown",
			"value", os.Getenv("RAG_SEARCH_STRATEGY"),
			"fallback", "vector")
	}
	if raw := strings.TrimSpace(os.Getenv("RAG_SEARCH_LIMIT")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 1 && value <= 50 {
			settings.Limit = value
		} else {
			logging.Logger.Warn("rag_search_limit_invalid",
				"value", raw,
				"fallback", settings.Limit)
		}
	}
	return settings
}

// newRAGRetriever builds a lexical/vector retriever. It degrades to BM25 when
// Ollama is unavailable; an empty index simply returns no sources. The search
// strategy (vector or hybrid) and fragment limit come from the environment so a
// deployment can trade latency for recall without a code change.
func newRAGRetriever(pgStore *api.PGStore) sourceRetriever {
	if pgStore == nil || pgStore.Pool == nil {
		return nil
	}
	settings := ragSettingsFromEnv()
	service := search.Service{
		Pool:  pgStore.Pool,
		Model: embeddings.Ollama{BaseURL: settings.OllamaURL, Model: settings.EmbedModel},
	}
	logging.Logger.Info("rag_retriever_configured",
		"ollama_url", settings.OllamaURL,
		"embed_model", settings.EmbedModel,
		"strategy", settings.Strategy,
		"limit", settings.Limit)
	return func(ctx context.Context, runID, query string) ([]domain.Source, error) {
		var id pgtype.UUID
		if err := id.Scan(runID); err != nil || !id.Valid {
			return nil, fmt.Errorf("invalid run id: %w", err)
		}
		started := time.Now()
		searchFn := service.Search
		if settings.Strategy == "hybrid" {
			searchFn = service.HybridSearch
		}
		hits, mode, err := searchFn(ctx, id, query, settings.Limit)
		if err != nil {
			return nil, err
		}
		bySource := make(map[string]*domain.Source)
		for _, hit := range hits {
			sourceID := hit.SourceID.String()
			item := bySource[sourceID]
			if item == nil {
				// Keep the real source type so the finalizer can still route
				// evidence from a retrieved standard/vacancy fragment to the
				// matching synthesizer slot instead of treating everything as
				// an untyped document.
				source := domain.Source{ID: sourceID, Type: domain.SourceDocument, Title: "Retrieved source"}
				var storedType, title string
				if metaErr := pgStore.Pool.QueryRow(ctx, "SELECT type,title FROM sources WHERE id=$1", hit.SourceID).Scan(&storedType, &title); metaErr == nil {
					source.Type = domain.SourceType(storedType)
					source.Title = title
				}
				if source.Type == "" {
					source.Type = domain.SourceDocument
				}
				source.Priority = domain.SourcePriority(source.Type)
				bySource[sourceID] = &source
				item = &source
			}
			if item.Content != "" {
				item.Content += "\n"
			}
			item.Content += hit.Quote
		}
		out := make([]domain.Source, 0, len(bySource))
		for _, item := range bySource {
			out = append(out, *item)
		}
		logging.Logger.Info("rag_retrieved",
			"run_id", runID,
			"strategy", settings.Strategy,
			"mode", mode,
			"hits", len(hits),
			"sources", len(out),
			"duration_ms", time.Since(started).Milliseconds())
		return out, nil
	}
}

func transcriptFromSession(session domain.InterviewSession) string {
	var b strings.Builder
	for i, question := range session.Questions {
		if i >= len(session.Answers) {
			break
		}
		b.WriteString("Вопрос: ")
		b.WriteString(question.Text)
		b.WriteString("\nОтвет: ")
		b.WriteString(session.Answers[i].Text)
		b.WriteString("\n")
	}
	return b.String()
}

// saveInterviewTree assigns database-safe UUIDs to the AI tree, creates the
// interview source and persists the tree through the finalize store. Evidence
// that already references a real run source keeps that source id; every other
// evidence item (interview quotes, synthetic ids) is attributed to the created
// interview source.
func saveInterviewTree(ctx context.Context, store finalizeStore, runID, profession, transcript string, nodes []domain.Node, keepSources map[string]bool) error {
	if store == nil {
		return fmt.Errorf("postgres store is not configured")
	}
	sourceID := newUUID()
	if err := store.CreateInterviewSource(ctx, runID, sourceID.String(), transcript); err != nil {
		return fmt.Errorf("create interview source: %w", err)
	}

	repaired, err := domain.RepairTree(nodes)
	if err != nil {
		return fmt.Errorf("repair tree: %w", err)
	}
	converted := assignUUIDsPreservingSources(repaired, sourceID, keepSources)
	rootCount := 0
	for _, node := range converted {
		if node.ParentID == nil {
			rootCount++
		}
	}
	if rootCount != 1 {
		return fmt.Errorf("tree must have exactly one root, got %d", rootCount)
	}
	if err := store.SaveTree(ctx, runID, converted); err != nil {
		return fmt.Errorf("store tree: %w", err)
	}
	// Автоснимок дерева: вкладка «Снимки и проверка» получает базовую точку
	// для diff, без ручного POST /snapshots.
	if snapErr := store.SaveRunSnapshot(ctx, runID); snapErr != nil {
		log.Printf("chat: snapshot failed for run %s: %v", runID, snapErr)
	}
	return nil
}

// assignUUIDsPreservingSources is assignUUIDs with provenance retention: an
// evidence item whose source id is a known real source keeps it, so the saved
// tree still shows which standard/vacancy material backs the node.
func assignUUIDsPreservingSources(nodes []domain.Node, interviewSourceID pgtype.UUID, keepSources map[string]bool) []domain.Node {
	ids := make(map[string]string, len(nodes))
	for _, node := range nodes {
		ids[node.ID] = newUUID().String()
	}
	out := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		converted := node
		converted.ID = ids[node.ID]
		if node.ParentID != nil {
			if parent, ok := ids[*node.ParentID]; ok {
				parentID := parent
				converted.ParentID = &parentID
			} else {
				converted.ParentID = nil
			}
		}
		converted.Evidence = make([]domain.Evidence, 0, len(node.Evidence))
		for _, evidence := range node.Evidence {
			source := interviewSourceID.String()
			if keepSources != nil && keepSources[strings.ToLower(strings.TrimSpace(evidence.SourceID))] {
				source = strings.ToLower(strings.TrimSpace(evidence.SourceID))
			}
			converted.Evidence = append(converted.Evidence, domain.Evidence{
				ID:       newUUID().String(),
				SourceID: source,
				RefID:    evidence.RefID,
				Quote:    evidence.Quote,
			})
		}
		out = append(out, converted)
	}
	return out
}

func assignUUIDs(nodes []domain.Node, sourceID pgtype.UUID) []domain.Node {
	return assignUUIDsPreservingSources(nodes, sourceID, nil)
}

func newUUID() pgtype.UUID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return pgtype.UUID{Bytes: b, Valid: true}
}
