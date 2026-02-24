package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/haris/virmux/internal/agent"
	"github.com/haris/virmux/internal/skill/research"
	"github.com/haris/virmux/internal/vm"
	yaml "gopkg.in/yaml.v2"
)

func cmdResearch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: virmux research <plan|map|reduce|replay|run>")
	}
	switch args[0] {
	case "plan":
		return cmdResearchPlan(args[1:])
	default:
		return fmt.Errorf("unknown research subcommand: %s", args[0])
	}
}

func cmdResearchPlan(args []string) error {
	fs, cfg, timeoutSec := commonFlags("research plan")
	query := fs.String("query", "", "research query")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *query == "" && fs.NArg() > 0 {
		*query = fs.Arg(0)
	}
	if *query == "" {
		return errors.New("query required")
	}
	cfg.timeout = timeDuration(*timeoutSec)

	planner := &research.DefaultPlanner{}

	err := runWithStore(cfg, "research:plan", map[string]any{"query": *query}, func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		output, err := planner.Compile(ctx, research.PlanInput{Query: *query})
		if err != nil {
			return vm.Outcome{}, nil, err
		}

		// Save plan.yaml to runDir
		// In a real implementation, Planner might return the Plan struct too.
		// For now, let's just create a stub plan.yaml as per spec-06.
		plan := research.Plan{
			PlanID:       output.PlanID,
			Goal:         *query,
			DimsDidntAsk: []string{"dims you didn't ask"},
			Tracks: []research.Track{
				{
					ID:   "track-1",
					Q:    fmt.Sprintf("Research %s", *query),
					Kind: "deep",
					Budget: research.PlanBudget{USD: 1.0, Mins: 5},
					StopRule: "found 1 source",
				},
			},
		}
		planPath := filepath.Join(runDir, "plan.yaml")
		data, _ := yaml.Marshal(plan)
		if err := os.WriteFile(planPath, data, 0644); err != nil {
			return vm.Outcome{}, nil, err
		}

		if err := emitVM("research.plan.created", map[string]any{"plan_id": output.PlanID, "path": planPath}); err != nil {
			return vm.Outcome{}, nil, err
		}

		return vm.Outcome{}, map[string]any{"plan_id": output.PlanID, "plan_path": planPath}, nil
	}, defaultRunRuntime)

	if err != nil {
		return err
	}

	return nil
}

func timeDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}
