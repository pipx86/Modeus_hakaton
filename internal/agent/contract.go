// Package agent defines the versioned input/output contract shared by the
// interviewer, analyst and synthesizer agents. The typed Go structs mirror the
// JSON schemas in schema/ so model output is validated before it is trusted.
package agent

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm/validate"
)

// ContractID identifies the agent I/O contract in logs and persisted traces.
const ContractID = "agent.io/v1"

// ContractVersion is the semantic version of the contract. Bump it whenever a
// required field or an invariant changes; prompt versions are tracked
// separately.
const ContractVersion = "1.0.0"

// Kind identifies one agent.
type Kind string

// Supported agent kinds.
const (
	KindInterviewer Kind = "interviewer"
	KindAnalyst     Kind = "analyst"
	KindSynthesizer Kind = "synthesizer"
)

// Kinds returns every supported agent kind in stable order.
func Kinds() []Kind {
	return []Kind{KindInterviewer, KindAnalyst, KindSynthesizer}
}

// Valid reports whether k is a supported agent kind.
func (k Kind) Valid() bool {
	switch k {
	case KindInterviewer, KindAnalyst, KindSynthesizer:
		return true
	default:
		return false
	}
}

// SchemaFS embeds the JSON schema of every agent output.
//
//go:embed schema/*.schema.json
var SchemaFS embed.FS

// Schema returns the output JSON schema for kind. The returned bytes are safe
// to cache and to pass to response_format of an OpenAI-compatible provider.
func Schema(kind Kind) ([]byte, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("agent: unknown kind %q", kind)
	}
	data, err := SchemaFS.ReadFile("schema/" + string(kind) + ".output.schema.json")
	if err != nil {
		return nil, fmt.Errorf("agent: schema for %q: %w", kind, err)
	}
	return data, nil
}

// ValidateOutput validates raw agent output against the kind's JSON schema.
func ValidateOutput(kind Kind, data []byte) error {
	schema, err := Schema(kind)
	if err != nil {
		return err
	}
	return validate.Schema(data, schema)
}

// DecodeOutput validates raw output and decodes it into dst (a pointer).
func DecodeOutput(kind Kind, data []byte, dst any) error {
	if dst == nil {
		return fmt.Errorf("agent: nil decode target")
	}
	if err := ValidateOutput(kind, data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("agent: decode %q output: %w", kind, err)
	}
	return nil
}

// Envelope wraps any agent payload with traceability metadata. The envelope is
// not sent to the model; it is used for persisted agent messages and logs.
type Envelope struct {
	Contract      string          `json:"contract"`
	Kind          Kind            `json:"kind"`
	PromptVersion string          `json:"prompt_version"`
	TraceID       string          `json:"trace_id,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// InterviewerInput is everything the interviewer needs for the next question.
type InterviewerInput struct {
	SessionID         string               `json:"session_id"`
	Respondent        domain.Respondent    `json:"respondent"`
	Profession        string               `json:"profession,omitempty"`
	SourceContext     string               `json:"source_context,omitempty"`
	Asked             []domain.Question    `json:"asked,omitempty"`
	Answers           []domain.Answer      `json:"answers,omitempty"`
	NextLevel         domain.QuestionLevel `json:"next_level"`
	NeedClarification bool                 `json:"need_clarification,omitempty"`
	MaxQuestions      int                  `json:"max_questions,omitempty"`
	// ProfileContext is public GitHub project data supplied by the respondent.
	// It is reference data, not an interview answer.
	ProfileContext string `json:"profile_context,omitempty"`
	// Learning switches the interviewer from an expert activity interview to
	// a beginner learning interview: first find out what the person can do.
	Learning bool `json:"learning,omitempty"`
}

// InterviewerOutput is the next question plus its reasoning. The question level
// must be one of role, function, task or action.
type InterviewerOutput struct {
	Question        domain.Question `json:"question"`
	Rationale       string          `json:"rationale"`
	IsClarification bool            `json:"is_clarification"`
}

// AnalystInput is one transcript to extract entities from, optionally enriched
// with vacancy and standard sources.
type AnalystInput struct {
	SessionID       string          `json:"session_id"`
	Profession      string          `json:"profession,omitempty"`
	Transcript      string          `json:"transcript"`
	Sources         []domain.Source `json:"sources,omitempty"`
	TaxonomyVersion string          `json:"taxonomy_version,omitempty"`
}

// ExtractedEntity is one evidence-backed entity returned by the analyst. It
// embeds domain.Node so the JSON shape is a flat node plus confidence.
type ExtractedEntity struct {
	domain.Node
	Confidence float64 `json:"confidence,omitempty"`
}

// Validate enforces node evidence rules and a valid entity type.
func (e ExtractedEntity) Validate() error {
	if !e.Type.Valid() {
		return fmt.Errorf("agent: entity %q has invalid type %q", e.ID, e.Type)
	}
	if e.Title == "" {
		return fmt.Errorf("agent: entity %q has empty title", e.ID)
	}
	return e.Node.Validate()
}

// AnalystOutput is the full set of entities extracted from one transcript.
type AnalystOutput struct {
	Entities []ExtractedEntity `json:"entities"`
}

// SynthesizerInput holds the three independently extracted role models.
type SynthesizerInput struct {
	Profession string          `json:"profession"`
	Sources    []domain.Source `json:"sources,omitempty"`
	Interview  []domain.Node   `json:"interview,omitempty"`
	Vacancy    []domain.Node   `json:"vacancy,omitempty"`
	Standard   []domain.Node   `json:"standard,omitempty"`
}

// AgentMessage is one memory record emitted by an agent. Messages with kind
// "conflict" carry the resolved conflict in Payload.
type AgentMessage struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SynthesizerOutput is the merged tree, every resolved conflict and the agent
// memory messages produced while merging.
type SynthesizerOutput struct {
	Tree      []domain.Node     `json:"tree"`
	Conflicts []domain.Conflict `json:"conflicts"`
	Messages  []AgentMessage    `json:"messages"`
}

// SortedKinds returns supported kinds sorted alphabetically. It is useful for
// deterministic prompt and schema registries.
func SortedKinds() []Kind {
	kinds := Kinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
