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
	case "map":
		return cmdResearchMap(args[1:])
	default:
		return fmt.Errorf("unknown research subcommand: %s", args[0])
	}
}

func cmdResearchMap(args []string) error {
	fs, cfg, _ := commonFlags("research map")
	runID := fs.String("run", "", "run id to map (last one if empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		latest, err := findLatestRunDir(cfg.runsDir)
		if err != nil {
			return err
		}
		*runID = filepath.Base(latest)
	}

	return runWithStore(cfg, "research:map", map[string]any{"target_run_id": *runID}, func(ctx context.Context, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		// 1. Load plan.yaml from runs/<runID>/plan.yaml
		planPath := filepath.Join(cfg.runsDir, *runID, "plan.yaml")
		data, err := os.ReadFile(planPath)
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("read plan from %s: %w", planPath, err)
		}
		plan, err := research.ParsePlan(data)
		if err != nil {
			return vm.Outcome{}, nil, err
		}

		// 2. Setup services
		mapper := &research.DefaultMapper{RunsDir: cfg.runsDir}
		scheduler := &research.DefaultScheduler{
			Mapper: mapper,
			Emitter: func(event string, payload map[string]any) error {
				return emitVM(event, payload)
			},
		}

		// 3. Run scheduler
		fmt.Printf("Starting research map for run %s (plan %s)\n", *runID, plan.PlanID)
		states, err := scheduler.Run(ctx, plan, *runID)
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("scheduler run: %w", err)
		}

		// 4. Report status
		for _, s := range states {
			fmt.Printf("Track %-15s status=%-10v error=%v\n", s.TrackID, s.Status, s.Error)
		}

		// Prepare summary details
		details := map[string]any{
			"plan_id": plan.PlanID,
			"tracks":  len(plan.Tracks),
			"results": states,
		}

		return vm.Outcome{}, details, nil
	}, defaultRunRuntime)
}

func findLatestRunDir(runsDir string) (string, error) {
	ents, err := os.ReadDir(runsDir)
	if err != nil {
		return "", err
	}
	var latest os.DirEntry
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if latest == nil || e.Name() > latest.Name() {
			latest = e
		}
	}
	if latest == nil {
		return "", errors.New("no runs found")
	}
	return filepath.Join(runsDir, latest.Name()), nil
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

		plan := output.Plan
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
