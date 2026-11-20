package api

import (
	"encoding/json"
	"errors"

	"activity-descriptor/internal/learning"

	"github.com/jackc/pgx/v5"
)

// SaveLearningPlan stores the generated plan for a learning run.
func (p *PGStore) SaveLearningPlan(runID string, plan learning.Plan) error {
	ctx, cancel := operation()
	defer cancel()
	payload, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = p.Pool.Exec(ctx, `
		INSERT INTO run_learning_plans(run_id, plan)
		VALUES($1, $2)
		ON CONFLICT (run_id) DO UPDATE
		SET plan=EXCLUDED.plan, updated_at=now()`, runID, payload)
	return translate(err)
}

// GetLearningPlan returns the saved plan for a learning run.
func (p *PGStore) GetLearningPlan(runID string) (learning.Plan, error) {
	ctx, cancel := operation()
	defer cancel()
	var payload []byte
	err := p.Pool.QueryRow(ctx,
		`SELECT plan FROM run_learning_plans WHERE run_id=$1`, runID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return learning.Plan{}, ErrNotFound
	}
	if err != nil {
		return learning.Plan{}, translate(err)
	}
	var plan learning.Plan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return learning.Plan{}, err
	}
	return plan, nil
}
