package api

import "context"

// rejectZunSuggestion сохраняет отказ эксперта от rule-based предложения.
// Повторный reject идемпотентен: конфликт первичного ключа игнорируется.
func (p *PGStore) rejectZunSuggestion(_ context.Context, runID, nodeID string) error {
	ctx, cancel := operation()
	defer cancel()
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO zun_suggestion_rejections (run_id, node_id)
		 VALUES ($1, $2)
		 ON CONFLICT (run_id, node_id) DO NOTHING`,
		runID, nodeID,
	)
	return translate(err)
}

// rejectedZunNodeIDs возвращает множество узлов, предложения по которым
// отклонены для данного прогона.
func (p *PGStore) rejectedZunNodeIDs(_ context.Context, runID string) (map[string]bool, error) {
	ctx, cancel := operation()
	defer cancel()
	rows, err := p.Pool.Query(ctx,
		`SELECT node_id::text FROM zun_suggestion_rejections WHERE run_id = $1`,
		runID,
	)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, translate(err)
		}
		out[nodeID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}
	return out, nil
}

var _ zunSuggestionRejecter = (*PGStore)(nil)
var _ zunSuggestionRejecter = (*memStore)(nil)
