// Package eval provides a deterministic, offline evaluation harness for the
// interviewer → analyst → synthesizer pipeline. It replays small golden cases
// through internal/pipeline with a scripted LLM client, then checks the
// structural guarantees of the result (status, tree hierarchy, evidence,
// verbatim quotes, conflicts, question count) and reports comparable metrics.
//
// No network, database or provider credentials are required: every model call
// is served by the in-process scripted client in fake.go.
package eval

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// SchemaVersion is the version of the golden-case JSON document. Bump it when a
// fixture field changes meaning; every loaded fixture must declare this value.
const SchemaVersion = 1

// embeddedCases holds the default golden set so the command works from any
// directory without extra files.
//
//go:embed testdata/cases/*.json
var embeddedCases embed.FS

// Case is one golden evaluation case: the pipeline input (profession, answers,
// optional standard/vacancy nodes) plus the scripted analyst answer and the
// expectations asserted by the harness.
type Case struct {
	SchemaVersion int               `json:"schema_version"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Profession    string            `json:"profession"`
	Respondent    domain.Respondent `json:"respondent,omitempty"`
	Answers       Answers           `json:"answers"`
	Sources       []domain.Source   `json:"sources,omitempty"`
	Standard      []domain.Node     `json:"standard,omitempty"`
	Vacancy       []domain.Node     `json:"vacancy,omitempty"`
	Script        Script            `json:"script"`
	Expect        Expect            `json:"expect"`
}

// Answers is the scripted respondent. The interviewer plan asks one question per
// level; the harness answers by the question level. Default is the fallback for
// unknown levels.
type Answers struct {
	Role     string `json:"role"`
	Function string `json:"function"`
	Task     string `json:"task"`
	Action   string `json:"action"`
	Default  string `json:"default,omitempty"`
}

// For returns the scripted answer for q. Answers are intentionally specific
// (more than four words and no vague markers) so the interviewer session does
// not ask clarification questions unless a case scripts them.
func (a Answers) For(q domain.Question) string {
	var text string
	switch q.Level {
	case domain.QuestionRole:
		text = a.Role
	case domain.QuestionFunction:
		text = a.Function
	case domain.QuestionTask:
		text = a.Task
	case domain.QuestionAction:
		text = a.Action
	default:
		text = a.Default
	}
	if strings.TrimSpace(text) == "" {
		text = a.Default
	}
	return text
}

// Script is the fake model behavior of one case.
type Script struct {
	Interviewer InterviewerScript   `json:"interviewer,omitempty"`
	Analyst     agent.AnalystOutput `json:"analyst"`
}

// InterviewerScript customises the fake interviewer. ErrorOnCalls lists 1-based
// interviewer call numbers that must fail, which exercises the fallback path and
// the degraded run status. Questions overrides the deterministic question text
// for a level (role, function, task or action).
type InterviewerScript struct {
	ErrorOnCalls []int             `json:"error_on_calls,omitempty"`
	Questions    map[string]string `json:"questions,omitempty"`
}

// Expect is the assertion set of one case. Pointer fields are optional: nil
// means "do not assert this value". The always-on invariants are documented on
// CheckResult.
type Expect struct {
	// Status is the expected run status, for example "done" or "degraded".
	Status *string `json:"status,omitempty"`
	// Questions is the expected number of transcript questions. The default
	// interviewer plan has six.
	Questions *int `json:"questions,omitempty"`
	// Clarifications is the expected number of clarification questions.
	Clarifications *int `json:"clarifications,omitempty"`
	// Fallbacks is the expected number of deterministic fallback questions.
	Fallbacks *int `json:"fallbacks,omitempty"`
	// TreeNodes is the expected number of nodes in the synthesized tree.
	TreeNodes *int `json:"tree_nodes,omitempty"`
	// NodeTypes lists node types that must be present at least once.
	NodeTypes []string `json:"node_types,omitempty"`
	// MinEvidencePerNode is the minimum number of evidence records per tree node.
	MinEvidencePerNode *int `json:"min_evidence_per_node,omitempty"`
	// Evidence is the expected total number of evidence records in the tree.
	Evidence *int `json:"evidence,omitempty"`
	// Conflicts is the expected number of resolved conflicts.
	Conflicts *int `json:"conflicts,omitempty"`
	// ConflictWinners is the expected winner source type per conflict, in output
	// order. When Conflicts is also set, its count must match this list.
	ConflictWinners []string `json:"conflict_winners,omitempty"`
	// ConflictValues is the expected number of competing values per conflict.
	ConflictValues []int `json:"conflict_values,omitempty"`
	// ModelCalls is the expected total number of fake model calls.
	ModelCalls *int `json:"model_calls,omitempty"`
	// VerbatimQuotes controls the transcript quote check for interview-sourced
	// evidence. It is enabled by default (nil means true).
	VerbatimQuotes *bool `json:"verbatim_quotes,omitempty"`
}

// DefaultCases loads the golden set embedded in the binary.
func DefaultCases() ([]Case, error) {
	return loadCasesFS(embeddedCases, "testdata/cases")
}

// LoadCasesDir loads every "*.json" case file from dir, sorted by file name.
func LoadCasesDir(dir string) ([]Case, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: cases dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("eval: cases path %s is not a directory", dir)
	}
	return loadCasesFS(os.DirFS(dir), ".")
}

// LoadCaseFile loads and validates one case file.
func LoadCaseFile(path string) (Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Case{}, fmt.Errorf("eval: read case: %w", err)
	}
	return decodeCase(path, data)
}

func loadCasesFS(fsys fs.FS, dir string) ([]Case, error) {
	pattern := "*.json"
	if dir != "." {
		pattern = dir + "/*.json"
	}
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, fmt.Errorf("eval: glob cases: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("eval: no case files in %s", dir)
	}
	sort.Strings(files)
	cases := make([]Case, 0, len(files))
	seen := make(map[string]string, len(files))
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("eval: read case %s: %w", file, err)
		}
		tc, err := decodeCase(file, data)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := seen[tc.Name]; duplicate {
			return nil, fmt.Errorf("eval: duplicate case name %q in %s and %s", tc.Name, previous, file)
		}
		seen[tc.Name] = file
		cases = append(cases, tc)
	}
	return cases, nil
}

func decodeCase(source string, data []byte) (Case, error) {
	var tc Case
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&tc); err != nil {
		return Case{}, fmt.Errorf("eval: parse case %s: %w", source, err)
	}
	if err := tc.Validate(); err != nil {
		return Case{}, fmt.Errorf("eval: case %s: %w", source, err)
	}
	return tc, nil
}

// Validate checks that the fixture is complete enough to be reproducible and
// that no expectation can silently never fire.
func (c Case) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version = %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(c.Profession) == "" {
		return fmt.Errorf("profession is required")
	}
	for _, level := range []struct {
		name string
		text string
	}{
		{"role", c.Answers.Role},
		{"function", c.Answers.Function},
		{"task", c.Answers.Task},
		{"action", c.Answers.Action},
	} {
		if strings.TrimSpace(level.text) == "" {
			return fmt.Errorf("answers.%s is required", level.name)
		}
	}
	if len(c.Script.Analyst.Entities) == 0 {
		return fmt.Errorf("script.analyst.entities is required")
	}
	for level, questionText := range c.Script.Interviewer.Questions {
		if !domain.QuestionLevel(level).Valid() {
			return fmt.Errorf("script.interviewer.questions has unknown level %q", level)
		}
		if strings.TrimSpace(questionText) == "" {
			return fmt.Errorf("script.interviewer.questions[%q] is empty", level)
		}
	}
	for _, interviewerCall := range c.Script.Interviewer.ErrorOnCalls {
		if interviewerCall < 1 {
			return fmt.Errorf("script.interviewer.error_on_calls must be 1-based, got %d", interviewerCall)
		}
	}
	for i, entity := range c.Script.Analyst.Entities {
		if err := entity.Validate(); err != nil {
			return fmt.Errorf("script.analyst.entities[%d]: %w", i, err)
		}
		for j, evidence := range entity.Evidence {
			if err := evidence.Validate(); err != nil {
				return fmt.Errorf("script.analyst.entities[%d].evidence[%d]: %w", i, j, err)
			}
			if strings.TrimSpace(evidence.ID) == "" {
				return fmt.Errorf("script.analyst.entities[%d].evidence[%d] has empty id", i, j)
			}
		}
	}
	if err := validateNodes("standard", c.Standard); err != nil {
		return err
	}
	if err := validateNodes("vacancy", c.Vacancy); err != nil {
		return err
	}
	return c.Expect.Validate()
}

// Validate checks the expectation block for impossible or contradictory values.
func (e Expect) Validate() error {
	if e.Status != nil && !domain.RunStatus(*e.Status).Valid() {
		return fmt.Errorf("expect.status %q is not a run status", *e.Status)
	}
	for name, value := range map[string]*int{
		"questions":             e.Questions,
		"clarifications":        e.Clarifications,
		"fallbacks":             e.Fallbacks,
		"tree_nodes":            e.TreeNodes,
		"min_evidence_per_node": e.MinEvidencePerNode,
		"evidence":              e.Evidence,
		"conflicts":             e.Conflicts,
		"model_calls":           e.ModelCalls,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("expect.%s must be >= 0", name)
		}
	}
	for _, nodeType := range e.NodeTypes {
		if !domain.NodeType(nodeType).Valid() {
			return fmt.Errorf("expect.node_types contains unknown type %q", nodeType)
		}
	}
	for _, winner := range e.ConflictWinners {
		if !validSourceType(winner) {
			return fmt.Errorf("expect.conflict_winners contains unknown source type %q", winner)
		}
	}
	for _, count := range e.ConflictValues {
		if count < 2 {
			return fmt.Errorf("expect.conflict_values entries must be >= 2, got %d", count)
		}
	}
	if len(e.ConflictWinners) > 0 && len(e.ConflictValues) > 0 && len(e.ConflictWinners) != len(e.ConflictValues) {
		return fmt.Errorf("expect.conflict_winners and expect.conflict_values must have the same length")
	}
	if e.Conflicts != nil && len(e.ConflictWinners) > 0 && *e.Conflicts != len(e.ConflictWinners) {
		return fmt.Errorf("expect.conflicts = %d but %d conflict winners are listed", *e.Conflicts, len(e.ConflictWinners))
	}
	if e.Conflicts != nil && len(e.ConflictValues) > 0 && *e.Conflicts != len(e.ConflictValues) {
		return fmt.Errorf("expect.conflicts = %d but %d conflict value counts are listed", *e.Conflicts, len(e.ConflictValues))
	}
	return nil
}

func validateNodes(label string, nodes []domain.Node) error {
	for i, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("%s[%d] has empty id", label, i)
		}
		if !node.Type.Valid() {
			return fmt.Errorf("%s[%d] has invalid type %q", label, i, node.Type)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("%s[%d] has empty title", label, i)
		}
		if len(node.Evidence) == 0 {
			return fmt.Errorf("%s[%d] has no evidence", label, i)
		}
		for j, evidence := range node.Evidence {
			if err := evidence.Validate(); err != nil {
				return fmt.Errorf("%s[%d].evidence[%d]: %w", label, i, j, err)
			}
			if strings.TrimSpace(evidence.ID) == "" {
				return fmt.Errorf("%s[%d].evidence[%d] has empty id", label, i, j)
			}
		}
	}
	return nil
}

func validSourceType(value string) bool {
	switch domain.SourceType(value) {
	case domain.SourceVacancy, domain.SourceInterview, domain.SourceStandard, domain.SourceDocument:
		return true
	default:
		return false
	}
}
