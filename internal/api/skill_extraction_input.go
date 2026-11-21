package api

import (
	"context"
	"fmt"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store/db"
)

type skillExtractionEvidence struct {
	ID          string                `json:"id"`
	NodeID      string                `json:"node_id"`
	SourceID    string                `json:"source_id"`
	SourceType  domain.SourceType     `json:"source_type"`
	Quote       string                `json:"quote"`
	Status      domain.EvidenceStatus `json:"status,omitempty"`
	ClaimOrigin domain.ClaimOrigin    `json:"claim_origin,omitempty"`
}

type skillExtractionNode struct {
	ID       string                    `json:"id"`
	Type     domain.NodeType           `json:"type"`
	Title    string                    `json:"title"`
	Evidence []skillExtractionEvidence `json:"evidence"`
}

// Short opaque references reduce copying errors and prevent PII redaction from
// altering UUID-shaped references. Source type is included so the model can
// distinguish an employer requirement from an interview statement.
func skillExtractionInput(nodes []domain.Node, sourceTypes map[string]domain.SourceType) ([]skillExtractionNode, map[string]string, map[string]string) {
	input := []skillExtractionNode{}
	nodeIDs := map[string]string{}
	evidenceIDs := map[string]string{}
	evidenceAlias := map[string]string{}
	sourceAlias := map[string]string{}
	for _, n := range nodes {
		alias := fmt.Sprintf("node_%d", len(input)+1)
		evidence := []skillExtractionEvidence{}
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
			evidence = append(evidence, skillExtractionEvidence{
				ID: id, NodeID: alias, SourceID: source, SourceType: sourceTypes[e.SourceID],
				Quote: e.Quote, Status: e.Status, ClaimOrigin: e.ClaimOrigin,
			})
		}
		if len(evidence) > 0 {
			nodeIDs[alias] = n.ID
			input = append(input, skillExtractionNode{ID: alias, Type: n.Type, Title: n.Title, Evidence: evidence})
		}
	}
	return input, nodeIDs, evidenceIDs
}

func loadSkillSourceTypes(ctx context.Context, q db.DBTX, runID string) (map[string]domain.SourceType, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT s.id::text,s.type::text
		FROM sources s
		JOIN evidence e ON e.source_id=s.id
		JOIN nodes n ON n.id=e.node_id
		WHERE n.run_id=$1`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sourceTypes := map[string]domain.SourceType{}
	for rows.Next() {
		var id string
		var sourceType domain.SourceType
		if err := rows.Scan(&id, &sourceType); err != nil {
			return nil, err
		}
		sourceTypes[id] = sourceType
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sourceTypes, nil
}
