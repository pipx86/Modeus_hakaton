// Package analyst extracts evidence-backed role-model entities from interview
// transcripts and links them to the curated taxonomy.
package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/repair"
	"activity-descriptor/internal/llm/validate"
	"activity-descriptor/internal/prompts"
)

// PromptVersion is the default prompt version used by the analyst agent.
const PromptVersion = prompts.DefaultVersion

// ErrModel marks a model, prompt or validation failure that survived repair.
var ErrModel = errors.New("analyst: model failure")

// Agent extracts entities from one transcript and links them to the taxonomy.
type Agent struct {
	client     llm.ChatClient
	version    string
	maxRepairs int
	taxonomy   Taxonomy
}

// Option customises the analyst Agent.
type Option func(*Agent)

// WithTaxonomy replaces the embedded taxonomy fixture.
func WithTaxonomy(taxonomy Taxonomy) Option {
	return func(a *Agent) { a.taxonomy = taxonomy }
}

// WithPromptVersion pins the prompt version loaded from the embedded prompts.
func WithPromptVersion(version string) Option {
	return func(a *Agent) {
		if version != "" {
			a.version = version
		}
	}
}

// WithMaxRepairs sets the number of repair attempts for invalid model output.
// Negative values are ignored.
func WithMaxRepairs(maxRepairs int) Option {
	return func(a *Agent) {
		if maxRepairs >= 0 {
			a.maxRepairs = maxRepairs
		}
	}
}

// New creates an analyst agent. client is required; the embedded taxonomy is
// used unless WithTaxonomy is passed.
func New(client llm.ChatClient, opts ...Option) (*Agent, error) {
	if client == nil {
		return nil, errors.New("analyst: chat client is required")
	}
	a := &Agent{client: client, version: PromptVersion, maxRepairs: 1}
	for _, opt := range opts {
		opt(a)
	}
	if a.taxonomy.Version == "" {
		taxonomy, err := DefaultTaxonomy()
		if err != nil {
			return nil, fmt.Errorf("analyst: %w", err)
		}
		a.taxonomy = taxonomy
	}
	if _, err := prompts.Load(prompts.KindAnalyst, a.version); err != nil {
		return nil, fmt.Errorf("analyst: %w", err)
	}
	return a, nil
}

// Version returns the pinned prompt version.
func (a *Agent) Version() string { return a.version }

// Taxonomy returns the taxonomy used for linking.
func (a *Agent) Taxonomy() Taxonomy { return a.taxonomy }

// Extract asks the model for entities. The validator enforces the output schema
// and that every quote is a verbatim transcript fragment; invalid answers are
// repaired before a terminal ErrModel is returned.
func (a *Agent) Extract(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error) {
	out, err := a.extract(ctx, in, "", func(out *agent.AnalystOutput) error {
		if err := ResolveEvidenceSources(out, in); err != nil {
			return err
		}
		return ValidateEntitiesWithSources(*out, in.Transcript, in.Sources)
	})
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, ErrQuoteMismatch) && !strings.Contains(err.Error(), ErrQuoteMismatch.Error()) {
		return agent.AnalystOutput{}, err
	}
	salvaged, salvageErr := a.extract(ctx, in, "", func(out *agent.AnalystOutput) error {
		return ValidateEntityShape(*out)
	})
	if salvageErr != nil {
		return agent.AnalystOutput{}, err
	}
	cleaned := SalvageEntities(salvaged, in)
	if len(cleaned.Entities) == 0 {
		return agent.AnalystOutput{}, err
	}
	if resolveErr := ResolveEvidenceSources(&cleaned, in); resolveErr == nil {
		return cleaned, nil
	}
	return agent.AnalystOutput{}, err
}

// extract runs one model extraction with the supplied validator. The validator
// is applied inside the repair loop and once more to the returned answer so
// deterministic attribution always applies to the final entities.
func (a *Agent) extract(ctx context.Context, in agent.AnalystInput, extraInstruction string, validate func(*agent.AnalystOutput) error) (agent.AnalystOutput, error) {
	doc, err := prompts.Load(prompts.KindAnalyst, a.version)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}
	messages, err := buildMessages(doc, in, extraInstruction)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}

	validator := func(answer string) error {
		if err := agent.ValidateOutput(agent.KindAnalyst, []byte(answer)); err != nil {
			return err
		}
		var out agent.AnalystOutput
		if err := json.Unmarshal([]byte(answer), &out); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		return validate(&out)
	}
	repaired, err := repair.New(a.client, validator, repair.WithMaxRepairs(a.maxRepairs))
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}
	answer, err := repaired.Chat(ctx, messages)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %w", ErrModel, err)
	}

	var out agent.AnalystOutput
	if err := json.Unmarshal([]byte(answer), &out); err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: decode: %v", ErrModel, err)
	}
	if err := validate(&out); err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %w", ErrModel, err)
	}
	return out, nil
}

// extractFromSource is a focused extraction pass over one standard/vacancy
// source. Live models tend to quote only the interview when both are present, so
// the analyst runs one extra, explicitly source-scoped request per material
// source. Evidence from this pass is forced to the source id and validated
// against the source content, which is what makes real standard/vacancy nodes.
func (a *Agent) extractFromSource(ctx context.Context, in agent.AnalystInput, source domain.Source) ([]agent.ExtractedEntity, error) {
	if strings.TrimSpace(source.Content) == "" {
		return nil, nil
	}
	focused := in
	focused.Transcript = ""
	focused.Sources = []domain.Source{source}
	out, err := a.extract(ctx, focused, sourceExtractionInstruction(source), func(out *agent.AnalystOutput) error {
		return validateSourceEntities(out, source)
	})
	if err != nil {
		return nil, err
	}
	return out.Entities, nil
}

// evidenceSourceIDs returns the set of source ids referenced by any evidence.
func evidenceSourceIDs(entities []agent.ExtractedEntity) map[string]bool {
	ids := make(map[string]bool, len(entities))
	for _, entity := range entities {
		for _, evidence := range entity.Evidence {
			ids[evidence.SourceID] = true
		}
	}
	return ids
}

// classifyEvidenceOrigin makes the distinction explicit for every extracted
// claim. Only a title reproduced inside its quoted source is marked as a
// source statement; a paraphrase remains a model interpretation grounded by
// the verbatim quote. Unknown historical/legacy values stay unclassified.
func classifyEvidenceOrigin(entities []agent.ExtractedEntity, in agent.AnalystInput) {
	contents := sourceContents(in)
	for i := range entities {
		for j := range entities[i].Evidence {
			evidence := &entities[i].Evidence[j]
			if evidence.ClaimOrigin.Valid() && evidence.ClaimOrigin != "" && evidence.ClaimOrigin != domain.ClaimUnclassified {
				continue
			}
			_, ok := contents[evidence.SourceID]
			if !ok {
				evidence.ClaimOrigin = domain.ClaimUnclassified
				continue
			}
			claim := strings.TrimSpace(entities[i].Title)
			if claim != "" && strings.Contains(strings.ToLower(evidence.Quote), strings.ToLower(claim)) {
				evidence.ClaimOrigin = domain.ClaimSourceStatement
			} else {
				evidence.ClaimOrigin = domain.ClaimModelInterpretation
			}
		}
	}
}

// Analyze extracts entities, normalizes them, removes duplicates and links them
// to the taxonomy. The result is a connected role → function → task → action
// tree in which every node has evidence.
//
// Besides the main extraction it runs one focused pass per standard/vacancy
// source so material sources always contribute evidence-backed nodes. Evidence is
// then split, deduplicated and taxonomy-linked per source-provenance group
// (interview / standard / vacancy). Keeping the groups separate means the
// deterministically linked taxonomy ids are shared across groups — which is what
// lets the synthesizer detect conflicting titles — while the evidence of a
// standard node can never be silently merged into an Interview node.
func (a *Agent) Analyze(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error) {
	extracted, err := a.Extract(ctx, in)
	if err != nil {
		return agent.AnalystOutput{}, err
	}
	entities := extracted.Entities
	for _, source := range in.Sources {
		if provenanceClass(source.Type) == domain.SourceInterview {
			continue
		}
		extra, sourceErr := a.extractFromSource(ctx, in, source)
		if sourceErr != nil {
			// The interview analysis stays valid when one material source cannot
			// be extracted; losing the whole run would be worse.
			log.Printf("analyst: focused extraction from source %q failed: %v", source.ID, sourceErr)
			continue
		}
		entities = append(entities, extra...)
	}
	classifyEvidenceOrigin(entities, in)
	entities = NormalizeEntities(entities)
	nodes := make([]domain.Node, 0, len(entities))
	for _, group := range splitEntitiesByProvenance(entities, in.Sources) {
		linked := a.taxonomy.Link(ToNodes(DedupEntities(group.entities)))
		nodes = append(nodes, linked...)
	}
	if len(nodes) == 0 {
		return agent.AnalystOutput{}, ErrNoEntities
	}
	return agent.AnalystOutput{Entities: NodesToEntities(nodes)}, nil
}

const (
	maxAnalystTranscriptRunes = 12000
	maxAnalystSources         = 8
	maxAnalystSourceRunes     = 2000
)

// fitAnalystInput keeps the analyst prompt inside a predictable token budget.
// Retrieval already limits the number of fragments; this is a final guard for
// direct callers that pass full documents or very long transcripts.
func fitAnalystInput(in agent.AnalystInput) agent.AnalystInput {
	in.Transcript = truncateRunes(in.Transcript, maxAnalystTranscriptRunes)
	if len(in.Sources) > maxAnalystSources {
		in.Sources = in.Sources[:maxAnalystSources]
	}
	sources := make([]domain.Source, len(in.Sources))
	copy(sources, in.Sources)
	for i := range sources {
		sources[i].Content = truncateRunes(sources[i].Content, maxAnalystSourceRunes)
	}
	in.Sources = sources
	return in
}

// promptInput is the exact payload sent to the model: the fitted input plus the
// synthetic interview source the model can cite. One source slot is reserved for
// the transcript so respondent quotes have a stable source_id.
func promptInput(in agent.AnalystInput) agent.AnalystInput {
	in = fitAnalystInput(in)
	limit := maxAnalystSources - 1
	if len(in.Sources) > limit {
		in.Sources = in.Sources[:limit]
	}
	sources := make([]domain.Source, 0, len(in.Sources)+1)
	sources = append(sources, in.Sources...)
	sources = append(sources, domain.Source{
		ID:      InterviewSourceID,
		Type:    domain.SourceInterview,
		Title:   "Транскрипт интервью",
		Content: in.Transcript,
	})
	in.Sources = sources
	return in
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n...[truncated]"
}

func buildMessages(doc prompts.Document, in agent.AnalystInput, extraInstruction string) ([]llm.Message, error) {
	in = promptInput(in)
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}
	system := doc.System + "\n\n" + sourceAttributionRules
	examples := "# Examples\n\n" + doc.Examples
	user := "Вход аналитика (JSON):\n" + string(payload)
	if strings.TrimSpace(extraInstruction) != "" {
		user += "\n\n" + extraInstruction
	}
	user += "\n\nВерни JSON по схеме analyst.output.schema.json. " +
		"Каждая цитата должна дословно присутствовать в transcript или в content источника из sources, " +
		"на который ссылается evidence.source_id."
	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: examples},
		{Role: llm.RoleUser, Content: user},
	}, nil
}

// sourceExtractionInstruction is the extra prompt of the focused per-source
// pass. It overrides the transcript-first wording of the base prompt because the
// transcript is intentionally empty in that request.
func sourceExtractionInstruction(source domain.Source) string {
	return `## Обязательный проход по одному источнику

Сейчас передан РОВНО ОДИН источник (` + string(source.Type) + `, id=` + source.ID + `).
Извлеки из его content все элементы деятельности (роль, функции, задачи,
	 действия), которые в нём описаны. НЕ используй транскрипт и не придумывай
цитаты: каждый evidence.quote — дословный фрагмент content этого источника,
evidence.source_id=` + source.ID + `, evidence.ref_id — номер строки или пункта.
Формулировки title бери дословно из источника: если роль названа «` +
		firstContentLine(source.Content) + `», title должен сохранять это название.
Для каждого утверждения укажи evidence.claim_origin: source_statement, если
формулировка title буквально присутствует в цитате, иначе model_interpretation.
Разделяй функцию (повторяющаяся область ответственности), задачу (ограниченный
результат с несколькими шагами) и действие (один наблюдаемый шаг с результатом).
Извлекай конкретные действия и задачи, если source это описывает. Не додумывай
инструменты, шаги и результаты, которых нет в цитате. Не объединяй дублирующие
формулировки, если у них разные объекты или результаты.
Верни минимум одну сущность (например, роль), если в источнике описана
профессиональная деятельность.`
}

// firstContentLine returns a short excerpt of the source, used to make the
// focused extraction prompt concrete without inventing content.
func firstContentLine(content string) string {
	line := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	runes := []rune(line)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return line
}

// validateSourceEntities enforces that a focused source extraction only cites
// the given source and that every quote is a verbatim fragment of its content.
// It also forces the evidence source id, so provenance can never be lost.
func validateSourceEntities(out *agent.AnalystOutput, source domain.Source) error {
	if out == nil {
		return errors.New("analyst: nil output")
	}
	if len(out.Entities) == 0 {
		return ErrNoEntities
	}
	for i := range out.Entities {
		entity := &out.Entities[i]
		if err := entity.Validate(); err != nil {
			return fmt.Errorf("analyst: entity %q: %w", entity.ID, err)
		}
		for j := range entity.Evidence {
			evidence := &entity.Evidence[j]
			if !validate.ContainsQuote(source.Content, evidence.Quote) {
				return fmt.Errorf("%w: entity %q quote %q is not in source %q", ErrQuoteMismatch, entity.ID, evidence.Quote, source.ID)
			}
			evidence.SourceID = source.ID
		}
	}
	return nil
}

// sourceAttributionRules is appended to the analyst system prompt when run
// sources are available. It teaches the model to quote per source, cite the real
// source id and keep conflicting source wording apart, which is what makes the
// deterministic synthesizer able to detect and persist cross-source conflicts.
const sourceAttributionRules = `## Источники и атрибуция цитат

В поле sources передан массив материалов: стандарт (type=standard), вакансия
(type=vacancy) и синтетический транскрипт интервью (id=` + InterviewSourceID + `).
Для каждого элемента evidence:
- evidence.source_id обязан совпадать с полем id источника, из content которого
  взята цитата; для цитат из интервью используй source_id=` + InterviewSourceID + `.
- evidence.quote — дословный фрагмент content выбранного источника (или
  transcript у источника интервью).
- Для каждого evidence укажи claim_origin: source_statement, если title
  дословно есть внутри quote; model_interpretation, если title обобщает или
  интерпретирует дословную цитату. Никогда не выдавай интерпретацию за цитату.
- Утверждение — это title узла с уровнем type (role/function/task/action).
  Сохраняй дословный fragment в evidence.quote и не переписывай его под title.
- Функция — устойчивая повторяющаяся область ответственности; задача —
  ограниченный результат с несколькими шагами; действие — один наблюдаемый
  шаг с проверяемым результатом. Извлекай конкретные задачи и действия, когда
  они описаны в материале. Не придумывай отсутствующие шаги, инструменты,
  исполнителей или результаты.
- Дедуплицируй одинаковые формулировки только если совпадают объект и результат;
  сохраняй отдельные утверждения, если отличаются объект, условия или результат.
- ОБЯЗАТЕЛЬНО извлеки элементы деятельности из КАЖДОГО standard/vacancy
  источника, если они там описаны. Не ограничивайся транскриптом: standard и
  vacancy — равноправные источники evidence.
- Не смешивай цитаты разных источников в одной сущности: если один и тот же
  элемент подтверждён и стандартом, и вакансией, верни две сущности с одинаковым
  id, но с evidence из соответствующего источника.
- Формулировки title/description бери из того источника, на который ссылается
  evidence: если стандарт и вакансия называют один элемент по-разному, верни две
  сущности с одинаковым id и разными title — синтезатор зафиксирует конфликт.
- Если в sources нет стандарта или вакансии, извлекай evidence только из
  transcript и не придумывай цитаты.`
