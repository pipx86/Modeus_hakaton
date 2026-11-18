// Command live-eval runs a tiny, fixed golden set through the real
// interviewer → analyst → synthesizer pipeline using the configured live
// provider. It measures the honest live behaviour of the pipeline: run status,
// JSON contract validity, question/tree/evidence/conflict counts, latency,
// actual model names and fallback usage.
//
// Unlike cmd/agent-eval it needs a provider (LLM_API_KEY etc.) and costs
// tokens, so it is a manual, non-CI check. The provider configuration is read
// from the process environment or from a local ignored dotenv file. Secrets are
// never printed: only model names, the base URL and redacted errors are shown.
//
// Exit codes: 0 — every live case passed, 1 — a case failed or the provider
// could not be configured, 2 — flag misuse.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"activity-descriptor/internal/agent/eval"
	"activity-descriptor/internal/llm/agents"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("live-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	envFile := flags.String("env-file", "", "optional dotenv file with LLM_* provider variables (existing environment variables win)")
	outPath := flags.String("out", "", "optional path for the machine-readable JSON report")
	caseTimeout := flags.Duration("case-timeout", eval.DefaultLiveCaseTimeout, "hard timeout per case, e.g. 10m")
	caseList := flags.String("cases", "", "comma-separated live case names (default: the two built-in cases)")
	dryRun := flags.Bool("dry-run", false, "validate the provider configuration and print the plan without calling the model")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *caseTimeout <= 0 {
		fmt.Fprintln(stderr, "live-eval: -case-timeout must be positive")
		return 2
	}

	cases, err := selectLiveCases(*caseList)
	if err != nil {
		fmt.Fprintf(stderr, "live-eval: %v\n", err)
		return 2
	}
	if err := eval.ValidateLiveCases(cases); err != nil {
		fmt.Fprintf(stderr, "live-eval: invalid built-in live case: %v\n", err)
		return 1
	}

	if *envFile != "" {
		loaded, ignored, err := loadLLMEnvFile(*envFile)
		if err != nil {
			fmt.Fprintf(stderr, "live-eval: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "env file: %s (loaded %d LLM_* variables, ignored %d other/set variables)\n", *envFile, loaded, ignored)
	}

	cfg, err := agents.FromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "live-eval: provider config: %v\n", err)
		return 1
	}
	provider := providerInfo(cfg)

	if *dryRun {
		if err := eval.WriteLivePlan(stdout, provider, cases, *caseTimeout); err != nil {
			fmt.Fprintf(stderr, "live-eval: %v\n", err)
			return 1
		}
		return 0
	}

	// The recorder is attached inside the pool, so it sees the primary client
	// of every agent and the optional fallback client as separate models. That
	// is what makes fallback usage and provider errors measurable.
	recorder := eval.NewCallRecorder()
	pool, err := agents.NewPool(cfg, agents.WithClientDecorator(eval.RecordingDecorator(recorder)))
	if err != nil {
		fmt.Fprintf(stderr, "live-eval: provider pool: %v\n", err)
		return 1
	}
	interviewerClient, err := pool.Client(agents.Interviewer)
	if err != nil {
		fmt.Fprintf(stderr, "live-eval: interviewer client: %v\n", err)
		return 1
	}
	analystClient, err := pool.Client(agents.Analyst)
	if err != nil {
		fmt.Fprintf(stderr, "live-eval: analyst client: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := eval.LiveRunner{
		Clients: eval.LiveClients{
			Interviewer: interviewerClient,
			Analyst:     analystClient,
			Recorder:    recorder,
			Models: map[string]string{
				string(agents.Interviewer): cfg.Agents[agents.Interviewer].Model,
				string(agents.Analyst):     cfg.Agents[agents.Analyst].Model,
			},
		},
		Provider:      provider,
		CaseTimeout:   *caseTimeout,
		RedactSecrets: []string{cfg.APIKey},
	}
	report := runner.RunAll(ctx, cases)
	if err := eval.WriteLiveTable(stdout, report); err != nil {
		fmt.Fprintf(stderr, "live-eval: %v\n", err)
		return 1
	}
	if *outPath != "" {
		var buffer bytes.Buffer
		if err := eval.WriteLiveJSON(&buffer, report); err != nil {
			fmt.Fprintf(stderr, "live-eval: %v\n", err)
			return 1
		}
		if err := os.WriteFile(*outPath, buffer.Bytes(), 0o644); err != nil {
			fmt.Fprintf(stderr, "live-eval: write %s: %v\n", *outPath, err)
			return 1
		}
		fmt.Fprintf(stdout, "report: %s\n", *outPath)
	}
	if report.Failed > 0 {
		fmt.Fprintf(stderr, "live-eval: %d of %d live cases failed\n", report.Failed, report.Passed+report.Failed)
		return 1
	}
	return 0
}

// selectLiveCases resolves the -cases flag against the built-in live set. An
// unknown or empty selection is a usage error rather than silently running a
// different set.
func selectLiveCases(raw string) ([]eval.LiveCase, error) {
	all := eval.DefaultLiveCases()
	if strings.TrimSpace(raw) == "" {
		return all, nil
	}
	selected := make([]eval.LiveCase, 0, len(all))
	seen := make(map[string]bool, len(all))
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		tc, ok := eval.LiveCaseByName(name)
		if !ok {
			available := make([]string, 0, len(all))
			for _, candidate := range all {
				available = append(available, candidate.Name)
			}
			return nil, fmt.Errorf("unknown live case %q (available: %s)", name, strings.Join(available, ", "))
		}
		seen[name] = true
		selected = append(selected, tc)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("-cases selected no live cases")
	}
	return selected, nil
}

// providerInfo projects the provider config into the report header. It never
// copies the API key.
func providerInfo(cfg agents.Config) eval.LiveProviderInfo {
	return eval.LiveProviderInfo{
		BaseURL: cfg.BaseURL,
		Models: map[string]string{
			string(agents.Interviewer): cfg.Agents[agents.Interviewer].Model,
			string(agents.Analyst):     cfg.Agents[agents.Analyst].Model,
		},
		FallbackModels: map[string]string{
			string(agents.Interviewer): cfg.Agents[agents.Interviewer].FallbackModel,
			string(agents.Analyst):     cfg.Agents[agents.Analyst].FallbackModel,
		},
		Synthesizer: "deterministic (internal/agent/synthesizer, no model call)",
	}
}
