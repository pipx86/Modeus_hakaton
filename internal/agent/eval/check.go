package eval

import (
	"fmt"
	"sort"
	"strings"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm/validate"
	"activity-descriptor/internal/pipeline"
)

// CheckResult runs every assertion for one completed pipeline run and returns
// human-readable failures. An empty slice means the case passed.
//
// Besides the optional expectations, the following invariants are always
// checked because they are part of the agent contract:
//   - both model-facing outputs validate against the embedded JSON schemas;
//   - the synthesized tree has exactly one root, the root is a role and every
//     parent/child pair follows role → function → task → action;
//   - every tree node and evidence record carries source id, ref id and quote;
//   - evidence attributed to the interview source appears verbatim in the
//     transcript;
//   - the transcript contains one question and one answer per recorded turn.
func CheckResult(tc Case, result pipeline.Result) []string {
	var failures []string
	failures = append(failures, checkStatus(tc.Expect, result)...)
	failures = append(failures, checkRunCounts(tc.Expect, result)...)
	failures = append(failures, checkTranscript(result)...)
	failures = append(failures, checkTree(tc.Expect, result)...)
	failures = append(failures, checkEvidence(tc, result)...)
	failures = append(failures, checkConflicts(tc.Expect, result)...)
	failures = append(failures, checkContracts(result)...)
	return failures
}

func checkStatus(expect Expect, result pipeline.Result) []string {
	var failures []string
	if expect.Status != nil && string(result.Run.Status) != *expect.Status {
		failures = append(failures, fmt.Sprintf("status = %q, want %q", result.Run.Status, *expect.Status))
	}
	return failures
}

func checkRunCounts(expect Expect, result pipeline.Result) []string {
	var failures []string
	questions, clarifications, fallbacks := 0, 0, 0
	for _, question := range result.Session.Questions {
		questions++
		if question.Clarification {
			clarifications++
		}
		if question.Fallback {
			fallbacks++
		}
	}
	if expect.Questions != nil && questions != *expect.Questions {
		failures = append(failures, fmt.Sprintf("questions = %d, want %d", questions, *expect.Questions))
	}
	if expect.Clarifications != nil && clarifications != *expect.Clarifications {
		failures = append(failures, fmt.Sprintf("clarifications = %d, want %d", clarifications, *expect.Clarifications))
	}
	if expect.Fallbacks != nil && fallbacks != *expect.Fallbacks {
		failures = append(failures, fmt.Sprintf("fallbacks = %d, want %d", fallbacks, *expect.Fallbacks))
	}
	return failures
}

func checkTranscript(result pipeline.Result) []string {
	var failures []string
	questions := len(result.Session.Questions)
	answers := len(result.Session.Answers)
	if questions == 0 {
		failures = append(failures, "transcript has no questions")
	}
	if questions != answers {
		failures = append(failures, fmt.Sprintf("session has %d questions but %d answers", questions, answers))
	}
	if got := strings.Count(result.Transcript, "Вопрос: "); got != questions {
		failures = append(failures, fmt.Sprintf("transcript has %d question lines, want %d", got, questions))
	}
	if got := strings.Count(result.Transcript, "Ответ: "); got != answers {
		failures = append(failures, fmt.Sprintf("transcript has %d answer lines, want %d", got, answers))
	}
	seen := make(map[string]bool, questions)
	for _, question := range result.Session.Questions {
		if strings.TrimSpace(question.Text) == "" {
			failures = append(failures, "question with empty text in session")
		}
		if !question.Level.Valid() {
			failures = append(failures, fmt.Sprintf("question %q has invalid level %q", question.ID, question.Level))
		}
		if seen[question.ID] {
			failures = append(failures, fmt.Sprintf("duplicate question id %q", question.ID))
		}
		seen[question.ID] = true
	}
	return failures
}

func checkTree(expect Expect, result pipeline.Result) []string {
	nodes := result.Synthesis.Tree
	var failures []string
	if expect.TreeNodes != nil && len(nodes) != *expect.TreeNodes {
		failures = append(failures, fmt.Sprintf("tree nodes = %d, want %d", len(nodes), *expect.TreeNodes))
	}
	if expect.Evidence != nil {
		total := 0
		for _, node := range nodes {
			total += len(node.Evidence)
		}
		if total != *expect.Evidence {
			failures = append(failures, fmt.Sprintf("tree evidence = %d, want %d", total, *expect.Evidence))
		}
	}

	typeCounts := make(map[string]int)
	for _, node := range nodes {
		typeCounts[string(node.Type)]++
	}
	for _, wantedType := range expect.NodeTypes {
		if typeCounts[wantedType] == 0 {
			available := make([]string, 0, len(typeCounts))
			for nodeType := range typeCounts {
				available = append(available, nodeType)
			}
			sort.Strings(available)
			failures = append(failures,
				fmt.Sprintf("tree has no %q nodes (present: %s)", wantedType, strings.Join(available, ",")))
		}
	}

	byID := make(map[string]domain.Node, len(nodes))
	rootIDs := make([]string, 0, 1)
	for _, node := range nodes {
		if err := node.Validate(); err != nil {
			failures = append(failures, fmt.Sprintf("node %q: %v", node.ID, err))
		}
		if strings.TrimSpace(node.ID) == "" {
			failures = append(failures, "tree node with empty id")
		}
		if _, duplicate := byID[node.ID]; duplicate {
			failures = append(failures, fmt.Sprintf("duplicate tree node id %q", node.ID))
		}
		byID[node.ID] = node
		if !node.Type.Valid() {
			failures = append(failures, fmt.Sprintf("node %q has invalid type %q", node.ID, node.Type))
		}
		if strings.TrimSpace(node.Title) == "" {
			failures = append(failures, fmt.Sprintf("node %q has empty title", node.ID))
		}
		if node.ParentID == nil {
			rootIDs = append(rootIDs, node.ID)
		}
		if minimum := expect.MinEvidencePerNode; minimum != nil && len(node.Evidence) < *minimum {
			failures = append(failures, fmt.Sprintf("node %q has %d evidence records, want at least %d",
				node.ID, len(node.Evidence), *minimum))
		}
	}
	if len(rootIDs) != 1 {
		failures = append(failures, fmt.Sprintf("tree has %d root nodes (%s), want exactly 1",
			len(rootIDs), strings.Join(rootIDs, ",")))
	} else if root, ok := byID[rootIDs[0]]; !ok || root.Type != domain.NodeRole {
		failures = append(failures, fmt.Sprintf("root node %q must be a role", rootIDs[0]))
	}
	for _, node := range nodes {
		if node.ParentID == nil {
			continue
		}
		parent, ok := byID[*node.ParentID]
		if !ok {
			failures = append(failures, fmt.Sprintf("node %q references missing parent %q", node.ID, *node.ParentID))
			continue
		}
		if !validTreeParent(node.Type, parent.Type) {
			failures = append(failures,
				fmt.Sprintf("%s node %q cannot be a child of %s node %q", node.Type, node.ID, parent.Type, parent.ID))
		}
	}
	return failures
}

func checkEvidence(tc Case, result pipeline.Result) []string {
	var failures []string
	verbatim := tc.Expect.VerbatimQuotes == nil || *tc.Expect.VerbatimQuotes
	for _, node := range result.Synthesis.Tree {
		for _, evidence := range node.Evidence {
			if strings.TrimSpace(evidence.ID) == "" {
				failures = append(failures, fmt.Sprintf("node %q has evidence with empty id", node.ID))
			}
			if err := evidence.Validate(); err != nil {
				failures = append(failures, fmt.Sprintf("node %q evidence %q: %v", node.ID, evidence.ID, err))
			}
		}
	}
	if !verbatim {
		return failures
	}
	interviewSources := interviewSourceIDs(tc)
	for _, node := range result.Synthesis.Tree {
		for _, evidence := range node.Evidence {
			if len(interviewSources) > 0 && !interviewSources[evidence.SourceID] {
				continue
			}
			if !validate.ContainsQuote(result.Transcript, evidence.Quote) {
				failures = append(failures, fmt.Sprintf("node %q evidence %q: quote is not a verbatim transcript fragment",
					node.ID, evidence.ID))
			}
		}
	}
	return failures
}

// interviewSourceIDs collects the source ids that the interview branch of the
// pipeline produced: every source used by a scripted analyst entity, plus any
// declared interview source. Their quotes must be verbatim transcript fragments;
// standard/vacancy evidence quotes come from their own documents.
func interviewSourceIDs(tc Case) map[string]bool {
	ids := make(map[string]bool)
	for _, entity := range tc.Script.Analyst.Entities {
		for _, evidence := range entity.Evidence {
			ids[evidence.SourceID] = true
		}
	}
	for _, source := range tc.Sources {
		if source.Type == domain.SourceInterview {
			ids[source.ID] = true
		}
	}
	return ids
}

func checkConflicts(expect Expect, result pipeline.Result) []string {
	conflicts := result.Synthesis.Conflicts
	var failures []string
	if expect.Conflicts != nil && len(conflicts) != *expect.Conflicts {
		failures = append(failures, fmt.Sprintf("conflicts = %d, want %d", len(conflicts), *expect.Conflicts))
	}
	nodeIDs := make(map[string]bool, len(result.Synthesis.Tree))
	for _, node := range result.Synthesis.Tree {
		nodeIDs[node.ID] = true
	}
	for _, conflict := range conflicts {
		if strings.TrimSpace(conflict.ID) == "" {
			failures = append(failures, "conflict with empty id")
		}
		if !nodeIDs[conflict.NodeID] {
			failures = append(failures, fmt.Sprintf("conflict %q references node %q missing from the tree",
				conflict.ID, conflict.NodeID))
		}
		if conflict.Field != "title" && conflict.Field != "description" {
			failures = append(failures, fmt.Sprintf("conflict %q has unsupported field %q", conflict.ID, conflict.Field))
		}
		if len(conflict.Values) < 2 {
			failures = append(failures, fmt.Sprintf("conflict %q has %d values, want at least 2", conflict.ID, len(conflict.Values)))
			continue
		}
		winner, _ := domain.ResolveConflict(conflict.Values)
		if winner.Value != conflict.Winner.Value || winner.SourceType != conflict.Winner.SourceType {
			failures = append(failures, fmt.Sprintf("conflict %q winner = %s/%q, want highest-priority %s/%q",
				conflict.ID, conflict.Winner.SourceType, conflict.Winner.Value, winner.SourceType, winner.Value))
		}
		if strings.TrimSpace(conflict.Reason) == "" {
			failures = append(failures, fmt.Sprintf("conflict %q has empty reason", conflict.ID))
		}
	}
	if len(expect.ConflictWinners) > 0 {
		if len(conflicts) != len(expect.ConflictWinners) {
			failures = append(failures, fmt.Sprintf("conflicts = %d, want %d winners", len(conflicts), len(expect.ConflictWinners)))
		}
		for i := 0; i < len(conflicts) && i < len(expect.ConflictWinners); i++ {
			if got := string(conflicts[i].Winner.SourceType); got != expect.ConflictWinners[i] {
				failures = append(failures, fmt.Sprintf("conflict[%d] winner source = %q, want %q",
					i, got, expect.ConflictWinners[i]))
			}
		}
	}
	if len(expect.ConflictValues) > 0 {
		if len(conflicts) != len(expect.ConflictValues) {
			failures = append(failures, fmt.Sprintf("conflicts = %d, want %d value lists", len(conflicts), len(expect.ConflictValues)))
		}
		for i := 0; i < len(conflicts) && i < len(expect.ConflictValues); i++ {
			if got := len(conflicts[i].Values); got != expect.ConflictValues[i] {
				failures = append(failures, fmt.Sprintf("conflict[%d] values = %d, want %d",
					i, got, expect.ConflictValues[i]))
			}
		}
	}
	return failures
}

func checkContracts(result pipeline.Result) []string {
	if contractsValid(result) {
		return nil
	}
	return []string{"analyst or synthesizer output violates the embedded agent JSON schema"}
}

func validTreeParent(child, parent domain.NodeType) bool {
	switch child {
	case domain.NodeFunction:
		return parent == domain.NodeRole
	case domain.NodeTask:
		return parent == domain.NodeFunction
	case domain.NodeAction:
		return parent == domain.NodeTask || parent == domain.NodeAction
	default:
		return false
	}
}
