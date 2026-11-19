package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/learning"
	"activity-descriptor/internal/logging"

	"github.com/jackc/pgx/v5"
)

const learningPlanTimeout = 2 * time.Minute

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

// runLearningPlan generates and persists one learning run in the background.
func (s *Server) runLearningPlan(runID, profession string) {
	if s.learningPlan == nil {
		_ = s.store.SetStage(runID, domain.StageFailed)
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok {
		_ = s.store.SetStage(runID, domain.StageFailed)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), learningPlanTimeout)
		defer cancel()
		plan, err := s.learningPlan.Plan(ctx, profession)
		if err != nil {
			logging.Logger.Error("learning_plan_failed", "run_id", runID, "error", err)
			_ = s.store.SetStage(runID, domain.StageFailed)
			return
		}
		if err := pgStore.SaveLearningPlan(runID, plan); err != nil {
			logging.Logger.Error("learning_plan_save_failed", "run_id", runID, "error", err)
			_ = s.store.SetStage(runID, domain.StageFailed)
			return
		}
		if err := s.store.SetStage(runID, domain.StageDone); err != nil {
			logging.Logger.Error("learning_plan_stage_failed", "run_id", runID, "error", err)
		}
	}()
}
