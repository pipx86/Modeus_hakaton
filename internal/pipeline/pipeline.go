// Package pipeline wires the interviewer, analyst and synthesizer agents into
// one end-to-end activity-description run.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// AnswerFunc returns the respondent's answer for a question.
type AnswerFunc func(domain.Question) string

// Config is the input of one pipeline run.
type Config struct {
	Client            llm.ChatClient
	InterviewerClient llm.ChatClient
	AnalystClient     llm.ChatClient
	Respondent        domain.Respondent
	Profession        string
	Answers           AnswerFunc
	Sources           []domain.Source
	Vacancy           []domain.Node
	Standard          []domain.Node
}

// Result is everything produced by one pipeline run.
type Result struct {
	Run        domain.Run
	Session    domain.InterviewSession
	Transcript string
	Analyst    agent.AnalystOutput
	Synthesis  agent.SynthesizerOutput
	Duration   time.Duration
}

// Run executes the full interview → analysis → synthesis pipeline. All model
// calls go through cfg.Client, so the run can be executed deterministically in
// tests and demos with a fake client.
func Run(ctx context.Context, cfg Config) (Result, error) {
	start := time.Now()

	if ctx == nil {
		return Result{}, fmt.Errorf("pipeline: nil context")
	}
	if cfg.Client == nil && cfg.InterviewerClient == nil && cfg.AnalystClient == nil {
		return Result{}, fmt.Errorf("pipeline: chat client is required")
	}
	interviewerClient := cfg.InterviewerClient
	if interviewerClient == nil {
		interviewerClient = cfg.Client
	}
	analystClient := cfg.AnalystClient
	if analystClient == nil {
		analystClient = cfg.Client
	}
	if interviewerClient == nil || analystClient == nil {
		return Result{}, fmt.Errorf("pipeline: both interviewer and analyst clients are required")
	}
	if cfg.Answers == nil {
		return Result{}, fmt.Errorf("pipeline: answer function is required")
	}
	if strings.TrimSpace(cfg.Profession) == "" {
		return Result{}, fmt.Errorf("pipeline: profession is required")
	}

	interviewerAgent, err := interviewer.New(interviewerClient)
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: interviewer: %w", err)
	}
	session, err := interviewer.NewSession(interviewerAgent, "run-"+cfg.Profession, cfg.Respondent)
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: session: %w", err)
	}

	var transcript strings.Builder
	for !session.Done() {
		question, err := session.Next(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("pipeline: next question: %w", err)
		}
		answer := cfg.Answers(question)
		if err := session.Answer(answer); err != nil {
			return Result{}, fmt.Errorf("pipeline: answer: %w", err)
		}
		transcript.WriteString("Вопрос: ")
		transcript.WriteString(question.Text)
		transcript.WriteString("\nОтвет: ")
		transcript.WriteString(answer)
		transcript.WriteString("\n")
	}
	state := session.Close()

	analystAgent, err := analyst.New(analystClient)
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: analyst: %w", err)
	}
	analysis, err := analystAgent.Analyze(ctx, agent.AnalystInput{
		SessionID:  state.ID,
		Profession: cfg.Profession,
		Transcript: transcript.String(),
		Sources:    cfg.Sources,
	})
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: analysis: %w", err)
	}

	interviewNodes := make([]domain.Node, 0, len(analysis.Entities))
	for _, entity := range analysis.Entities {
		interviewNodes = append(interviewNodes, entity.Node)
	}

	synthesizerAgent, err := synthesizer.New()
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: synthesizer: %w", err)
	}
	synthesis, err := synthesizerAgent.Synthesize(ctx, agent.SynthesizerInput{
		Profession: cfg.Profession,
		Sources:    cfg.Sources,
		Interview:  interviewNodes,
		Vacancy:    cfg.Vacancy,
		Standard:   cfg.Standard,
	})
	if err != nil {
		return Result{}, fmt.Errorf("pipeline: synthesis: %w", err)
	}
	for _, node := range synthesis.Tree {
		if err := node.Validate(); err != nil {
			return Result{}, fmt.Errorf("pipeline: tree node %q: %w", node.ID, err)
		}
	}

	status := domain.RunDone
	if state.Status == domain.SessionDegraded {
		status = domain.RunDegraded
	}
	finish := time.Now()
	run := domain.Run{
		ID:         state.ID,
		Status:     status,
		Profession: cfg.Profession,
		StartedAt:  &start,
		FinishedAt: &finish,
	}

	return Result{
		Run:        run,
		Session:    state,
		Transcript: transcript.String(),
		Analyst:    analysis,
		Synthesis:  synthesis,
		Duration:   finish.Sub(start),
	}, nil
}
