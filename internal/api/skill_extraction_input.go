package api

import (
	"activity-descriptor/internal/domain"
	"fmt"
)

// Short opaque references reduce copying errors and prevent PII redaction from
// altering UUID-shaped references. Only exact mappings are accepted on return.
func skillExtractionInput(nodes []domain.Node) ([]domain.Node, map[string]string, map[string]string) {
	input := []domain.Node{}
	nodeIDs := map[string]string{}
	evidenceIDs := map[string]string{}
	evidenceAlias := map[string]string{}
	sourceAlias := map[string]string{}
	for _, n := range nodes {
		alias := fmt.Sprintf("node_%d", len(input)+1)
		evidence := []domain.Evidence{}
		for _, e := range n.Evidence {
			if e.Status == domain.EvidenceRejected {
				continue
			}
			id, ok := evidenceAlias[e.ID]
			if !ok {
				id = fmt.Sprintf("evidence_%d", len(evidenceAlias)+1)
				evidenceAlias[e.ID] = id
				evidenceIDs[id] = e.ID
			}
			source, ok := sourceAlias[e.SourceID]
			if !ok {
				source = fmt.Sprintf("source_%d", len(sourceAlias)+1)
				sourceAlias[e.SourceID] = source
			}
			evidence = append(evidence, domain.Evidence{ID: id, NodeID: alias, SourceID: source, RefID: id, Quote: e.Quote, Status: e.Status, ClaimOrigin: e.ClaimOrigin})
		}
		if len(evidence) > 0 {
			nodeIDs[alias] = n.ID
			input = append(input, domain.Node{ID: alias, Type: n.Type, Title: n.Title, Evidence: evidence})
		}
	}
	return input, nodeIDs, evidenceIDs
}
