package analysis

// Deterministic mentor-insights analysis for a saved competency tree.
//
// The helpers in this file implement the read-only aggregates used by
// GET /api/v1/runs/{id}/insights and GET /api/v1/runs/{id}/coverage:
//
//   - provenance score — how many distinct sources/source types ground a node;
//   - tree health score — a 0..100 deterministic quality score with a
//     groundedness/completeness/duplicates/orphans breakdown;
//   - red flags — exactly five deterministic rules (see flag* constants);
//   - market heat — run skill frequencies matched against node text, with a
//     deterministic fallback extracted from vacancy/standard source content;
//   - coverage matrix — role/function/task rows × source-type columns;
//   - node explanations — template-only "why this node is here" text.
//
// No LLM calls, network access or database access happen here: every value is
// derived from the nodes, the run sources and the optional run skill-frequency
// map. All collections in the returned values are non-nil so the JSON payload
// is stable (`[]`/`{}` instead of `null`).

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"activity-descriptor/internal/domain"
)

// Red-flag codes. Each flagged node reports one or more of these stable
// machine-readable codes together with a human-readable reason.
const (
	// FlagSuspiciousTitle marks an empty, too-short, placeholder-like or
	// non-letter title that cannot identify a real role/function/task.
	FlagSuspiciousTitle = "SUSPICIOUS_TITLE"
	// FlagDuplicateTitle marks a node whose (type, normalized title) repeats
	// for another node of the same type.
	FlagDuplicateTitle = "DUPLICATE_TITLE"
	// FlagSingleWeakSource marks a node grounded by exactly one weak source
	// type (vacancy/document, priority below standard).
	FlagSingleWeakSource = "SINGLE_WEAK_SOURCE"
	// FlagNoChildren marks a role/function/task node without direct children.
	FlagNoChildren = "NO_CHILDREN"
	// FlagNoEvidence marks a node without any evidence record.
	FlagNoEvidence = "NO_EVIDENCE"
)

// InsightsHealthFormula documents the tree health formula. The response also
// carries the weights so the frontend can explain the score without parsing
// this string.
const InsightsHealthFormula = "score = round(0.40*groundedness + 0.30*completeness + 0.20*duplicates + 0.10*orphans). " +
	"All components are 0..100 quality scores where 100 is best: " +
	"groundedness = share of nodes with at least one evidence record; " +
	"completeness = share of structurally complete nodes (role/function/task nodes with at least one child, action leaves are complete by design); " +
	"duplicates = share of nodes whose (type, normalized title) is unique; " +
	"orphans = share of nodes whose parent exists (roots have no parent by design). " +
	"An empty tree scores 0."

// InsightsHealthWeights is the weight of every health component in the score.
var InsightsHealthWeights = map[string]float64{
	"groundedness": 0.40,
	"completeness": 0.30,
	"duplicates":   0.20,
	"orphans":      0.10,
}

// InsightSourceColumns is the fixed, ordered column set of the coverage
// matrix. Every column is always present, even when the run has no source of
// that type (the cells stay 0). Unknown source types are counted in the
// document column.
var InsightSourceColumns = []string{
	string(domain.SourceInterview),
	string(domain.SourceStandard),
	string(domain.SourceVacancy),
	string(domain.SourceDocument),
}

const (
	marketHeatSourceRun     = "run.skill_freq"
	marketHeatSourceSources = "vacancy_sources"
	marketHeatSourceNone    = "none"
	marketHeatTopLimit      = 10
	// marketFallbackContentLimit bounds the rune count extracted from one
	// source document, keeping the deterministic fallback cheap for very large
	// standards/documents. Only the beginning of a document is scanned.
	marketFallbackContentLimit = 20000
)

// NodeProvenance describes how a node is grounded by the run sources.
type NodeProvenance struct {
	// SourceCount is the number of distinct evidence source ids on the node.
	SourceCount int `json:"source_count"`
	// SourceTypes lists the distinct source types in trust-priority order
	// (interview, standard, vacancy, document). Unknown source ids are
	// reported as "document".
	SourceTypes []string `json:"source_types"`
	// EvidenceCount is the number of evidence records on the node after
	// duplicate evidence records are collapsed.
	EvidenceCount int `json:"evidence_count"`
}

// Provenance is the per-node provenance aggregate.
type Provenance struct {
	Nodes map[string]NodeProvenance `json:"nodes"`
}

// HealthBreakdown is the tree health score split into its four 0..100
// components (higher is always better).
type HealthBreakdown struct {
	Groundedness int `json:"groundedness"`
	Completeness int `json:"completeness"`
	Duplicates   int `json:"duplicates"`
	Orphans      int `json:"orphans"`
}

// HealthCounts exposes the raw counters behind HealthBreakdown so the UI can
// render "9 of 12 grounded" style captions.
type HealthCounts struct {
	NodeCount      int `json:"node_count"`
	GroundedNodes  int `json:"grounded_nodes"`
	DuplicateNodes int `json:"duplicate_nodes"`
	OrphanNodes    int `json:"orphan_nodes"`
	ChildlessNodes int `json:"childless_nodes"`
}

// Health is the deterministic tree health aggregate.
type Health struct {
	Score     int                `json:"score"`
	Breakdown HealthBreakdown    `json:"breakdown"`
	Weights   map[string]float64 `json:"weights"`
	Counts    HealthCounts       `json:"counts"`
	Formula   string             `json:"formula"`
}

// Flag aggregates all red flags of one node. Codes and Reasons are parallel
// arrays: reasons[i] explains codes[i].
type Flag struct {
	NodeID  string   `json:"node_id"`
	Codes   []string `json:"codes"`
	Reasons []string `json:"reasons"`
}

// MarketSkill is one row of the market-heat top list.
type MarketSkill struct {
	Skill     string `json:"skill"`
	Frequency int    `json:"frequency"`
}

// MarketNodeHeat is the market heat matched to one tree node.
type MarketNodeHeat struct {
	// Score is the sum of the frequencies of the matched skills.
	Score int `json:"score"`
	// Matches lists the matched skill names in top-list order.
	Matches []string `json:"matches"`
}

// MarketHeat overlays the market skill frequencies on the tree.
type MarketHeat struct {
	// Source reports where the frequencies came from:
	//   - "run.skill_freq" — the persisted/analysed Run.SkillFreq map;
	//   - "vacancy_sources" — deterministic fallback extracted from the
	//     content of the run's vacancy/standard sources;
	//   - "none" — no market data available, Top and Nodes are empty.
	Source string `json:"source"`
	// SampleSize is the number of vacancy/standard source documents scanned
	// for the "vacancy_sources" fallback. It is omitted for the other sources.
	SampleSize int                       `json:"sample_size,omitempty"`
	Top        []MarketSkill             `json:"top"`
	Nodes      map[string]MarketNodeHeat `json:"nodes"`
}

// CoverageRow is one role/function/task row of the coverage matrix.
type CoverageRow struct {
	NodeID string         `json:"node_id"`
	Title  string         `json:"title"`
	Type   string         `json:"type"`
	Cells  map[string]int `json:"cells"`
}

// Coverage is the role/function/task × source-type evidence matrix.
type Coverage struct {
	Columns []string      `json:"columns"`
	Rows    []CoverageRow `json:"rows"`
}

// InsightSource is the run source metadata shown next to the insights.
type InsightSource struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// ExplanationEvidence is one evidence item of a node explanation.
type ExplanationEvidence struct {
	Quote      string `json:"quote"`
	SourceID   string `json:"source_id"`
	SourceType string `json:"source_type"`
	Priority   int    `json:"priority"`
	RefID      string `json:"ref_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

// NodeExplanation explains why a node exists in the tree. Summary is
// template-based (no LLM); Evidence is never empty for a node with evidence
// and explains the missing-data state for a node without it.
type NodeExplanation struct {
	Summary  string                `json:"summary"`
	Evidence []ExplanationEvidence `json:"evidence"`
}

// Insights is the full payload of GET /api/v1/runs/{id}/insights.
type Insights struct {
	RunID            string                     `json:"run_id"`
	Provenance       Provenance                 `json:"provenance"`
	Health           Health                     `json:"health"`
	Flags            []Flag                     `json:"flags"`
	MarketHeat       MarketHeat                 `json:"market_heat"`
	Coverage         Coverage                   `json:"coverage"`
	Sources          []InsightSource            `json:"sources"`
	NodeExplanations map[string]NodeExplanation `json:"node_explanations"`
}

// insightTree indexes the tree for the aggregates below.
//
// Evidence is deduplicated per node: records with the same evidence id are
// collapsed, and id-less records are collapsed by (source_id, ref_id, quote).
// Fully empty evidence records are ignored. Source ids are compared
// case-insensitively, which matches UUID semantics and keeps in-memory
// fixtures and PostgreSQL data consistent.
type insightTree struct {
	nodes    []domain.Node
	byID     map[string]domain.Node
	sources  map[string]domain.Source
	children map[string][]string
	evidence map[string][]domain.Evidence
}

// MarketInput is the optional market-heat source for BuildInsightsWithMarket.
//
// SkillFreq has the highest precedence and is normally Run.SkillFreq.
// FallbackFreq/FallbackSampleSize carry the deterministic frequencies derived
// from the content of vacancy/standard run sources (see
// MarketSkillFrequencyFromSources) and are used only when SkillFreq yields no
// usable skills.
type MarketInput struct {
	SkillFreq          map[string]int
	FallbackFreq       map[string]int
	FallbackSampleSize int
}

// BuildInsights computes all mentor insights for the given tree.
//
// nodes is the flat tree returned by GET /runs/{id}/tree (root plus
// descendants in any order), sources are the run sources (see
// PGStore.ListRunSources) and skillFreq is the optional Run.SkillFreq map.
// Market heat comes from skillFreq only; use BuildInsightsWithMarket when a
// deterministic source-content fallback is available. The returned RunID is
// empty: HTTP handlers set it from the get-run response.
func BuildInsights(nodes []domain.Node, sources []domain.Source, skillFreq map[string]int) Insights {
	return BuildInsightsWithMarket(nodes, sources, MarketInput{SkillFreq: skillFreq})
}

// BuildInsightsWithMarket is BuildInsights plus the market-heat fallback: when
// SkillFreq yields no usable skills, the FallbackFreq map is used and the
// response reports MarketHeat.Source "vacancy_sources" with SampleSize.
func BuildInsightsWithMarket(nodes []domain.Node, sources []domain.Source, market MarketInput) Insights {
	tree := newInsightTree(nodes, sources)
	return Insights{
		Provenance:       tree.buildProvenance(),
		Health:           tree.buildHealth(),
		Flags:            tree.buildFlags(),
		MarketHeat:       tree.buildMarketHeat(market),
		Coverage:         tree.buildCoverage(),
		Sources:          buildInsightSources(sources),
		NodeExplanations: tree.buildNodeExplanations(),
	}
}

// BuildCoverageMatrix computes the role/function/task × source-type coverage
// matrix used by GET /api/v1/runs/{id}/coverage. Rows keep the input node
// order (PostgreSQL returns nodes ordered by level and id), actions are
// excluded and every cell of InsightSourceColumns is present.
func BuildCoverageMatrix(nodes []domain.Node, sources []domain.Source) Coverage {
	return newInsightTree(nodes, sources).buildCoverage()
}

// WriteCoverageCSV writes the coverage matrix as CSV with the header
// node_id,title,type,interview,standard,vacancy,document. Titles are quoted
// according to RFC 4180 by encoding/csv.
func WriteCoverageCSV(w io.Writer, coverage Coverage) error {
	writer := csv.NewWriter(w)
	header := make([]string, 0, 3+len(coverage.Columns))
	header = append(header, "node_id", "title", "type")
	header = append(header, coverage.Columns...)
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, row := range coverage.Rows {
		record := make([]string, 0, 3+len(coverage.Columns))
		record = append(record, row.NodeID, row.Title, row.Type)
		for _, column := range coverage.Columns {
			record = append(record, strconv.Itoa(row.Cells[column]))
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func newInsightTree(nodes []domain.Node, sources []domain.Source) *insightTree {
	tree := &insightTree{
		nodes:    nodes,
		byID:     make(map[string]domain.Node, len(nodes)),
		sources:  make(map[string]domain.Source, len(sources)),
		children: make(map[string][]string, len(nodes)),
		evidence: make(map[string][]domain.Evidence, len(nodes)),
	}
	for _, source := range sources {
		if key := insightNormalizeID(source.ID); key != "" {
			tree.sources[key] = source
		}
	}
	for _, node := range nodes {
		key := insightNormalizeID(node.ID)
		if key != "" {
			tree.byID[key] = node
		}
		tree.evidence[node.ID] = insightDedupeEvidence(node.Evidence)
		if node.ParentID != nil {
			parentKey := insightNormalizeID(*node.ParentID)
			tree.children[parentKey] = append(tree.children[parentKey], node.ID)
		}
	}
	return tree
}

func insightNormalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func insightDedupeEvidence(items []domain.Evidence) []domain.Evidence {
	out := make([]domain.Evidence, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" && strings.TrimSpace(item.SourceID) == "" &&
			strings.TrimSpace(item.RefID) == "" && strings.TrimSpace(item.Quote) == "" {
			continue
		}
		key := "id|" + insightNormalizeID(item.ID)
		if insightNormalizeID(item.ID) == "" {
			key = "src|" + insightNormalizeID(item.SourceID) + "|ref|" + insightNormalizeID(item.RefID) + "|quote|" + strings.TrimSpace(item.Quote)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

// insightSourceType maps an evidence source id to a known source type.
// Unknown ids (for example evidence saved before run_sources was populated)
// are reported as documents, so no evidence ever disappears from the counts.
func (t *insightTree) insightSourceType(sourceID string) domain.SourceType {
	source, ok := t.sources[insightNormalizeID(sourceID)]
	if !ok {
		return domain.SourceDocument
	}
	switch source.Type {
	case domain.SourceInterview, domain.SourceStandard, domain.SourceVacancy, domain.SourceDocument:
		return source.Type
	default:
		return domain.SourceDocument
	}
}

func (t *insightTree) insightSourcePriority(sourceID string) int {
	source, ok := t.sources[insightNormalizeID(sourceID)]
	if !ok {
		return domain.SourcePriority(domain.SourceDocument)
	}
	return source.EffectivePriority()
}

func insightSortedSourceTypes(set map[domain.SourceType]struct{}) []string {
	out := make([]string, 0, len(set))
	for _, candidate := range []domain.SourceType{
		domain.SourceInterview, domain.SourceStandard, domain.SourceVacancy, domain.SourceDocument,
	} {
		if _, ok := set[candidate]; ok {
			out = append(out, string(candidate))
		}
	}
	return out
}

func (t *insightTree) buildProvenance() Provenance {
	provenance := Provenance{Nodes: make(map[string]NodeProvenance, len(t.nodes))}
	for _, node := range t.nodes {
		item := NodeProvenance{SourceTypes: []string{}}
		types := make(map[domain.SourceType]struct{})
		sourceIDs := make(map[string]struct{})
		for _, evidence := range t.evidence[node.ID] {
			item.EvidenceCount++
			sourceIDs[insightNormalizeID(evidence.SourceID)] = struct{}{}
			types[t.insightSourceType(evidence.SourceID)] = struct{}{}
		}
		item.SourceCount = len(sourceIDs)
		item.SourceTypes = insightSortedSourceTypes(types)
		provenance.Nodes[node.ID] = item
	}
	return provenance
}

// buildHealth implements InsightsHealthFormula. Every component is a 0..100
// quality score, the score is their weighted average rounded to an integer.
// A tree without nodes scores 0 with a zero breakdown.
func (t *insightTree) buildHealth() Health {
	weights := make(map[string]float64, len(InsightsHealthWeights))
	for name, weight := range InsightsHealthWeights {
		weights[name] = weight
	}
	health := Health{
		Weights: weights,
		Formula: InsightsHealthFormula,
	}
	total := len(t.nodes)
	if total == 0 {
		return health
	}

	titleGroups := insightTitleGroups(t.nodes)
	grounded, duplicates, orphans, childless := 0, 0, 0, 0
	for _, node := range t.nodes {
		if len(t.evidence[node.ID]) > 0 {
			grounded++
		}
		if len(t.children[insightNormalizeID(node.ID)]) == 0 && insightExpectsChildren(node.Type) {
			childless++
		}
		if len(titleGroups[insightTitleKey(node)]) > 1 {
			duplicates++
		}
		if node.ParentID != nil {
			if _, ok := t.byID[insightNormalizeID(*node.ParentID)]; !ok {
				orphans++
			}
		}
	}

	health.Counts = HealthCounts{
		NodeCount:      total,
		GroundedNodes:  grounded,
		DuplicateNodes: duplicates,
		OrphanNodes:    orphans,
		ChildlessNodes: childless,
	}
	health.Breakdown = HealthBreakdown{
		Groundedness: insightPercent(grounded, total),
		Completeness: insightPercent(total-childless, total),
		Duplicates:   insightPercent(total-duplicates, total),
		Orphans:      insightPercent(total-orphans, total),
	}
	health.Score = insightHealthScore(health.Breakdown)
	return health
}

func insightPercent(part, total int) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(100 * float64(part) / float64(total)))
}

func insightHealthScore(breakdown HealthBreakdown) int {
	score := 0.40*float64(breakdown.Groundedness) +
		0.30*float64(breakdown.Completeness) +
		0.20*float64(breakdown.Duplicates) +
		0.10*float64(breakdown.Orphans)
	rounded := int(math.Round(score))
	if rounded < 0 {
		return 0
	}
	if rounded > 100 {
		return 100
	}
	return rounded
}

// insightExpectsChildren reports whether a node type is expected to be
// decomposed one level further. Actions are tree leaves by design.
func insightExpectsChildren(nodeType domain.NodeType) bool {
	switch nodeType {
	case domain.NodeRole, domain.NodeFunction, domain.NodeTask:
		return true
	default:
		return false
	}
}

// buildFlags applies the five red-flag rules to every node. A node with
// several problems gets one Flag with parallel Codes/Reasons arrays, in the
// fixed rule order: title, duplicates, weak source, children, evidence.
//
// Root handling is explicit: the root is not special-cased. It gets a
// NO_EVIDENCE flag when it has no evidence and a NO_CHILDREN flag when it has
// no children (a decomposition tree should always start from a role with at
// least one function). There are no false positives for the root beyond what
// the same rules report for any other node.
func (t *insightTree) buildFlags() []Flag {
	titleGroups := insightTitleGroups(t.nodes)
	flags := make([]Flag, 0)
	for _, node := range t.nodes {
		flag := Flag{NodeID: node.ID, Codes: []string{}, Reasons: []string{}}
		add := func(code, reason string) {
			flag.Codes = append(flag.Codes, code)
			flag.Reasons = append(flag.Reasons, reason)
		}
		if reason, ok := insightSuspiciousTitleReason(node.Title); ok {
			add(FlagSuspiciousTitle, reason)
		}
		if group := titleGroups[insightTitleKey(node)]; len(group) > 1 {
			add(FlagDuplicateTitle, fmt.Sprintf(
				"Название «%s» повторяется у %d узлов типа %s — вероятная копия, узлы нужно объединить или переименовать",
				node.Title, len(group), node.Type))
		}
		if reason, ok := t.insightWeakSourceReason(node.ID); ok {
			add(FlagSingleWeakSource, reason)
		}
		if len(t.children[insightNormalizeID(node.ID)]) == 0 && insightExpectsChildren(node.Type) {
			add(FlagNoChildren, fmt.Sprintf(
				"У узла типа %s нет дочерних узлов — уровень не раскрыт, добавьте декомпозицию", node.Type))
		}
		if len(t.evidence[node.ID]) == 0 {
			add(FlagNoEvidence, "У узла нет подтверждений (evidence) — происхождение не установлено")
		}
		if len(flag.Codes) > 0 {
			flags = append(flags, flag)
		}
	}
	return flags
}

func (t *insightTree) insightWeakSourceReason(nodeID string) (string, bool) {
	evidence := t.evidence[nodeID]
	if len(evidence) == 0 {
		return "", false
	}
	types := make(map[domain.SourceType]struct{})
	for _, item := range evidence {
		types[t.insightSourceType(item.SourceID)] = struct{}{}
	}
	if len(types) != 1 {
		return "", false
	}
	var only domain.SourceType
	for sourceType := range types {
		only = sourceType
	}
	priority := domain.SourcePriority(only)
	if priority >= domain.PriorityStandard {
		return "", false
	}
	return fmt.Sprintf(
		"Узел подтверждён только одним слабым источником (%s, приоритет %d) — для надёжного обоснования нужен interview или standard",
		only, priority), true
}

func insightTitleKey(node domain.Node) string {
	title := insightNormalizeTitle(node.Title)
	if title == "" {
		return ""
	}
	return string(node.Type) + "\x00" + title
}

func insightTitleGroups(nodes []domain.Node) map[string][]string {
	groups := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		key := insightTitleKey(node)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], node.ID)
	}
	return groups
}

func insightNormalizeTitle(title string) string {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.ReplaceAll(normalized, "ё", "е")
	return strings.Join(strings.Fields(normalized), " ")
}

var insightGenericTitles = map[string]struct{}{
	"роль": {}, "функция": {}, "задача": {}, "действие": {}, "узел": {},
	"элемент": {}, "пункт": {}, "другое": {}, "прочее": {}, "разное": {},
	"новый узел": {}, "новая роль": {}, "новая функция": {}, "новая задача": {},
	"заглушка": {}, "пусто": {}, "нет": {}, "тест": {},
	"role": {}, "function": {}, "task": {}, "action": {}, "node": {},
	"item": {}, "other": {}, "misc": {}, "todo": {}, "tbd": {},
	"n/a": {}, "na": {}, "none": {}, "null": {}, "test": {},
}

// insightSuspiciousTitleReason implements the generic/suspicious title rule.
// Empty titles, titles shorter than three runes, placeholder words and titles
// without a single letter are suspicious.
func insightSuspiciousTitleReason(title string) (string, bool) {
	normalized := insightNormalizeTitle(title)
	if normalized == "" {
		return "Название узла пустое — узел нельзя однозначно идентифицировать", true
	}
	if utf8.RuneCountInString(normalized) < 3 {
		return fmt.Sprintf("Название «%s» короче трёх символов — вероятно, заглушка", title), true
	}
	if _, generic := insightGenericTitles[normalized]; generic {
		return fmt.Sprintf("Название «%s» шаблонное и не описывает конкретную роль, функцию или задачу", title), true
	}
	hasLetter := false
	for _, r := range normalized {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return fmt.Sprintf("Название «%s» не содержит букв — вероятно, заглушка", title), true
	}
	return "", false
}

// buildMarketHeat matches the market skill frequencies against node titles,
// descriptions and ZUN competencies.
//
// Precedence: Run.SkillFreq ("run.skill_freq") first, then the deterministic
// vacancy/standard source fallback ("vacancy_sources", with SampleSize). When
// neither yields usable skills the result is empty with Source "none".
func (t *insightTree) buildMarketHeat(market MarketInput) MarketHeat {
	heat := MarketHeat{
		Source: marketHeatSourceNone,
		Top:    []MarketSkill{},
		Nodes:  make(map[string]MarketNodeHeat),
	}
	skills := insightMergeSkillFrequency(market.SkillFreq)
	if len(skills) > 0 {
		heat.Source = marketHeatSourceRun
	} else if market.FallbackSampleSize > 0 {
		if fallback := insightMergeSkillFrequency(market.FallbackFreq); len(fallback) > 0 {
			skills = fallback
			heat.Source = marketHeatSourceSources
			heat.SampleSize = market.FallbackSampleSize
		}
	}
	if len(skills) == 0 {
		return heat
	}
	for index, skill := range skills {
		if index >= marketHeatTopLimit {
			break
		}
		heat.Top = append(heat.Top, MarketSkill{Skill: skill.Name, Frequency: skill.Frequency})
	}
	for _, node := range t.nodes {
		text := insightNodeSearchText(node)
		matches := make([]string, 0)
		score := 0
		for _, skill := range skills {
			if insightContainsTokenFold(text, skill.Name) {
				matches = append(matches, skill.Name)
				score += skill.Frequency
			}
		}
		if len(matches) > 0 {
			heat.Nodes[node.ID] = MarketNodeHeat{Score: score, Matches: matches}
		}
	}
	return heat
}

// marketFallbackSkills supplements DefaultTaxonomy with common market and
// analytics terms that appear in vacancy/standard text but are not taxonomy
// definitions. They are plain string terms: extraction stays rule-based and
// deterministic (no LLM, no external calls).
var marketFallbackSkills = []string{
	"analytics", "аналитика", "dashboard", "dashboards", "дашборд", "дашборды",
	"bi", "business intelligence", "бизнес-аналитика",
	"data visualization", "визуализация данных", "визуализация",
	"reporting", "отчетность", "отчеты", "отчет",
	"data quality", "качество данных", "data cleaning", "очистка данных",
	"requirements analysis", "анализ требований", "сбор требований",
	"statistics", "статистика", "метрики", "metrics", "product metrics",
	"etl", "airflow", "spark", "pyspark", "hadoop", "sql", "python",
	"postgresql", "excel", "tableau", "power bi", "ab testing", "a/b testing",
	"machine learning", "data science", "pandas", "numpy", "jupyter",
	"выгрузка", "выгрузки", "презентация результатов",
}

// marketFallbackCatalog is the deterministic extractor catalog used by
// MarketSkillFrequencyFromSources: the standard taxonomy plus the extra
// analytics terms above. Built once at package init.
var marketFallbackCatalog = NewTaxonomyCatalog(marketFallbackSkills, DefaultTaxonomy())

// MarketSkillFrequencyFromSources derives market skill frequencies from the
// content of the run's vacancy and standard sources.
//
// The extraction is fully deterministic (analysis.ExtractSkills over a fixed
// catalog; no LLM, no network, no schema change). The returned frequency of a
// skill is the number of source documents whose content contains it, with the
// taxonomy canonicalizing aliases (for example Postgres/PostgreSQL). The
// second return value is the sample size: the number of non-empty
// vacancy/standard documents that were scanned.
//
// Interview and document sources are ignored because they are not market
// evidence. An empty result (nil sources, no vacancy/standard sources or no
// extractable skills) returns an empty map and sample size 0; callers must
// treat it as "no data", not as an error.
func MarketSkillFrequencyFromSources(sources []domain.Source) (map[string]int, int) {
	texts := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.Type != domain.SourceVacancy && source.Type != domain.SourceStandard {
			continue
		}
		if strings.TrimSpace(source.Content) == "" {
			continue
		}
		content := source.Content
		if utf8.RuneCountInString(content) > marketFallbackContentLimit {
			content = string([]rune(content)[:marketFallbackContentLimit])
		}
		// The extractor normalizes "ё" to "е" in catalog terms but tokenizes
		// raw text as-is; normalize the document too so both spellings match.
		content = strings.ReplaceAll(content, "ё", "е")
		content = strings.ReplaceAll(content, "Ё", "Е")
		texts = append(texts, content)
	}
	if len(texts) == 0 {
		return map[string]int{}, 0
	}
	triple := ExtractSkills(texts, marketFallbackCatalog)
	return triple.Freq, len(texts)
}

type insightMarketSkill struct {
	Name      string
	Frequency int
}

// insightMergeSkillFrequency normalizes the skill-frequency map
// case-insensitively, merges duplicate spellings (summing their frequencies)
// and returns skills ordered by frequency desc, then name asc. Non-positive
// frequencies are ignored.
func insightMergeSkillFrequency(skillFreq map[string]int) []insightMarketSkill {
	if len(skillFreq) == 0 {
		return nil
	}
	keys := make([]string, 0, len(skillFreq))
	for key := range skillFreq {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	merged := make(map[string]*insightMarketSkill, len(keys))
	for _, key := range keys {
		frequency := skillFreq[key]
		normalized := normalizeSkill(key)
		if frequency <= 0 || normalized == "" {
			continue
		}
		if existing, ok := merged[normalized]; ok {
			existing.Frequency += frequency
			if key < existing.Name {
				existing.Name = key
			}
			continue
		}
		merged[normalized] = &insightMarketSkill{Name: key, Frequency: frequency}
	}
	out := make([]insightMarketSkill, 0, len(merged))
	for _, skill := range merged {
		out = append(out, *skill)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Frequency != out[j].Frequency {
			return out[i].Frequency > out[j].Frequency
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func insightNodeSearchText(node domain.Node) string {
	var builder strings.Builder
	builder.WriteString(node.Title)
	if node.Description != "" {
		builder.WriteString(" ")
		builder.WriteString(node.Description)
	}
	if node.Zun != nil {
		for _, values := range [][]string{node.Zun.Knowledge, node.Zun.Skills, node.Zun.Abilities} {
			if len(values) == 0 {
				continue
			}
			builder.WriteString(" ")
			builder.WriteString(strings.Join(values, " "))
		}
	}
	return builder.String()
}

// insightContainsTokenFold reports whether needle occurs in text
// case-insensitively as a whole token: if the needle starts/ends with a word
// rune, the adjacent text rune must not be a word rune. This prevents skill
// "R" from matching "Report" while still matching "SQL-запросы". Both sides
// are folded to lowercase with "ё" normalized to "е" so extracted canonical
// terms match tree wording with either spelling.
func insightContainsTokenFold(text, needle string) bool {
	haystack := []rune(insightFoldMatchText(text))
	needleRunes := []rune(insightFoldMatchText(needle))
	if len(needleRunes) == 0 || len(needleRunes) > len(haystack) {
		return false
	}
	for start := 0; start+len(needleRunes) <= len(haystack); start++ {
		if string(haystack[start:start+len(needleRunes)]) != string(needleRunes) {
			continue
		}
		if start > 0 && insightWordRune(haystack[start-1]) && insightWordRune(needleRunes[0]) {
			continue
		}
		end := start + len(needleRunes)
		if end < len(haystack) && insightWordRune(haystack[end]) && insightWordRune(needleRunes[len(needleRunes)-1]) {
			continue
		}
		return true
	}
	return false
}

// insightFoldMatchText lowercases text and normalizes "ё" to "е".
func insightFoldMatchText(text string) string {
	text = strings.ToLower(text)
	return strings.ReplaceAll(text, "ё", "е")
}

func insightWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (t *insightTree) buildCoverage() Coverage {
	columns := append([]string(nil), InsightSourceColumns...)
	rows := make([]CoverageRow, 0, len(t.nodes))
	for _, node := range t.nodes {
		if !insightCoverageRow(node.Type) {
			continue
		}
		cells := make(map[string]int, len(columns))
		for _, column := range columns {
			cells[column] = 0
		}
		for _, evidence := range t.evidence[node.ID] {
			cells[insightCoverageColumn(t.insightSourceType(evidence.SourceID))]++
		}
		rows = append(rows, CoverageRow{
			NodeID: node.ID,
			Title:  node.Title,
			Type:   string(node.Type),
			Cells:  cells,
		})
	}
	return Coverage{Columns: columns, Rows: rows}
}

func insightCoverageRow(nodeType domain.NodeType) bool {
	switch nodeType {
	case domain.NodeRole, domain.NodeFunction, domain.NodeTask:
		return true
	default:
		return false
	}
}

// insightCoverageColumn maps a source type to its coverage column. Unknown
// types are counted as documents, mirroring the provenance rules.
func insightCoverageColumn(sourceType domain.SourceType) string {
	value := string(sourceType)
	for _, column := range InsightSourceColumns {
		if column == value {
			return column
		}
	}
	return string(domain.SourceDocument)
}

func buildInsightSources(sources []domain.Source) []InsightSource {
	out := make([]InsightSource, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		key := insightNormalizeID(source.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, InsightSource{
			ID:       source.ID,
			Type:     string(source.Type),
			Title:    source.Title,
			Priority: source.EffectivePriority(),
		})
	}
	return out
}

func (t *insightTree) buildNodeExplanations() map[string]NodeExplanation {
	explanations := make(map[string]NodeExplanation, len(t.nodes))
	for _, node := range t.nodes {
		evidence := t.evidence[node.ID]
		explanation := NodeExplanation{
			Summary:  t.insightNodeSummary(node, evidence),
			Evidence: make([]ExplanationEvidence, 0, len(evidence)),
		}
		for _, item := range evidence {
			sourceType := t.insightSourceType(item.SourceID)
			explanation.Evidence = append(explanation.Evidence, ExplanationEvidence{
				Quote:      item.Quote,
				SourceID:   item.SourceID,
				SourceType: string(sourceType),
				Priority:   t.insightSourcePriority(item.SourceID),
				RefID:      item.RefID,
				Status:     string(item.Status),
			})
		}
		explanations[node.ID] = explanation
	}
	return explanations
}

// insightNodeSummary renders the template-only "why this node" text. It never
// returns an empty string: a node without evidence explains the missing-data
// state instead.
func (t *insightTree) insightNodeSummary(node domain.Node, evidence []domain.Evidence) string {
	nodeType := string(node.Type)
	if nodeType == "" {
		nodeType = "unknown"
	}
	if len(evidence) == 0 {
		return fmt.Sprintf(
			"Узел «%s» (%s) не подтверждён ни одним источником: происхождение не установлено, нужны evidence.",
			node.Title, nodeType)
	}
	types := make(map[domain.SourceType]struct{})
	sourceIDs := make(map[string]struct{})
	for _, item := range evidence {
		types[t.insightSourceType(item.SourceID)] = struct{}{}
		sourceIDs[insightNormalizeID(item.SourceID)] = struct{}{}
	}
	sortedTypes := insightSortedSourceTypes(types)
	strongest := domain.SourceType("")
	if len(sortedTypes) > 0 {
		strongest = domain.SourceType(sortedTypes[0])
	}
	summary := fmt.Sprintf(
		"Узел «%s» (%s) включён в дерево по подтверждениям: цитат %d, источников %d, типы источников %s; максимальный приоритет — %s (%d).",
		node.Title, nodeType, len(evidence), len(sourceIDs), strings.Join(sortedTypes, ", "),
		strongest, domain.SourcePriority(strongest))
	if len(sortedTypes) == 1 && domain.SourcePriority(strongest) < domain.PriorityStandard {
		summary += " Единственный тип источника слабый — для надёжного обоснования нужен interview или standard."
	}
	return summary
}
