package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haris/virmux/internal/agent"
	"github.com/haris/virmux/internal/skill/research"
	"github.com/haris/virmux/internal/store"
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
	case "reduce":
		return cmdResearchReduce(args[1:])
	case "run":
		return cmdResearchRun(args[1:])
	case "replay":
		return cmdResearchReplay(args[1:])
	case "timeline":
		return cmdResearchTimeline(args[1:])
	default:
		return fmt.Errorf("unknown research subcommand: %s", args[0])
	}
}

func cmdResearchTimeline(args []string) error {
	fs, cfg, _ := commonFlags("research timeline")
	runID := fs.String("run", "", "run id to view timeline (last one if empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		latest, err := findLatestRunDirWithRequired(cfg.runsDir, "trace.ndjson")
		if err != nil {
			return err
		}
		*runID = filepath.Base(latest)
	}

	tracePath := filepath.Join(cfg.runsDir, *runID, "trace.ndjson")
	data, err := os.ReadFile(tracePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	fmt.Printf("Timeline for run %s:\n", *runID)
	for _, line := range lines {
		if line == "" {
			continue
		}
		var ev struct {
			TS      time.Time      `json:"ts"`
			Type    string         `json:"type"`
			Event   string         `json:"event"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err == nil {
			if strings.HasPrefix(ev.Event, "research.") {
				fmt.Printf("[%s] %-30s %v\n", ev.TS.Format("15:04:05"), ev.Event, ev.Payload)
			}
		} else {
			// fallback if ts is not standard
			var ev2 struct {
				Event   string         `json:"event"`
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &ev2); err == nil {
				if strings.HasPrefix(ev2.Event, "research.") {
					fmt.Printf("[--:--:--] %-30s %v\n", ev2.Event, ev2.Payload)
				}
			}
		}
	}
	return nil
}

func cmdResearchReplay(args []string) error {
	fs, cfg, _ := commonFlags("research replay")
	runID := fs.String("run", "", "run id to replay (last one if empty)")
	only := fs.String("only", "", "comma-separated list of track ids to rerun")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		latest, err := findLatestRunDirWithRequired(cfg.runsDir, "plan.yaml")
		if err != nil {
			return err
		}
		*runID = filepath.Base(latest)
	}

	onlyList := []string{}
	if *only != "" {
		onlyList = strings.Split(*only, ",")
	}

	return runWithStore(cfg, "research:replay", map[string]any{"target_run_id": *runID, "only": onlyList}, func(ctx context.Context, st *store.Store, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "virmux", "research")
		mapper := &research.DefaultMapper{
			RunsDir:  cfg.runsDir,
			CacheDir: cacheDir,
			Store:    st,
		}
		scheduler := &research.DefaultScheduler{
			Mapper: mapper,
			Emitter: func(event string, payload map[string]any) error {
				return emitVM(event, payload)
			},
			TrackArtifactExists: researchTrackArtifactProbe(cfg.runsDir),
		}

		replayer := &research.DefaultReplay{
			RunsDir:   cfg.runsDir,
			Store:     st,
			Scheduler: scheduler,
			Emitter: func(event string, payload map[string]any) error {
				return emitVM(event, payload)
			},
		}

		output, err := replayer.Run(ctx, research.ReplayInput{RunID: *runID, Only: onlyList})
		if err != nil {
			return vm.Outcome{}, nil, err
		}
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, *runID); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist target artifacts: %w", err)
		}

		return vm.Outcome{}, map[string]any{"run_id": output.RunID}, nil
	}, defaultRunRuntime)
}

func cmdResearchRun(args []string) error {
	fs, cfg, timeoutSec := commonFlags("research run")
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

	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "virmux", "research")

	return runWithStore(cfg, "research:run", map[string]any{"query": *query}, func(ctx context.Context, st *store.Store, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		// 1. Plan
		planner := &research.DefaultPlanner{Hints: &research.DefaultHintProvider{}}
		planOutput, err := planner.Compile(ctx, research.PlanInput{Query: *query})
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("planner: %w", err)
		}
		planData, _ := yaml.Marshal(planOutput.Plan)
		if err := os.WriteFile(filepath.Join(runDir, "plan.yaml"), planData, 0644); err != nil {
			return vm.Outcome{}, nil, err
		}
		_ = emitVM("research.plan.created", map[string]any{"plan_id": planOutput.PlanID, "path": "plan.yaml"})
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, filepath.Base(runDir)); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist research artifacts: %w", err)
		}

		// 2. Map
		mapper := &research.DefaultMapper{RunsDir: cfg.runsDir, CacheDir: cacheDir, Store: st}
		scheduler := &research.DefaultScheduler{
			Mapper: mapper,
			Emitter: func(event string, payload map[string]any) error {
				return emitVM(event, payload)
			},
			TrackArtifactExists: researchTrackArtifactProbe(cfg.runsDir),
		}
		runID := filepath.Base(runDir)
		_, err = scheduler.Run(ctx, planOutput.Plan, runID, nil)
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("scheduler: %w", err)
		}

		// 3. Reduce
		reducer := &research.DefaultReducer{RunsDir: cfg.runsDir, Store: st}
		_, err = reducer.Run(ctx, research.ReduceInput{RunID: runID})
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("reducer: %w", err)
		}
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, runID); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist research artifacts: %w", err)
		}

		return vm.Outcome{}, map[string]any{
			"plan_id":    planOutput.PlanID,
			"reduce_dir": filepath.Join(runDir, "reduce"),
		}, nil
	}, defaultRunRuntime)
}

func cmdResearchReduce(args []string) error {
	fs, cfg, _ := commonFlags("research reduce")
	runID := fs.String("run", "", "run id to reduce (last one if empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		latest, err := findLatestRunDirWithRequired(cfg.runsDir, "map")
		if err != nil {
			return err
		}
		*runID = filepath.Base(latest)
	}

	return runWithStore(cfg, "research:reduce", map[string]any{"target_run_id": *runID}, func(ctx context.Context, st *store.Store, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
		reducer := &research.DefaultReducer{
			RunsDir: cfg.runsDir,
			Store:   st,
		}

		output, err := reducer.Run(ctx, research.ReduceInput{RunID: *runID})
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("reducer run: %w", err)
		}
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, *runID); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist target artifacts: %w", err)
		}

		// Prepare summary details
		details := map[string]any{
			"run_id":     output.RunID,
			"reduce_dir": filepath.Join(cfg.runsDir, *runID, "reduce"),
		}

		return vm.Outcome{}, details, nil
	}, defaultRunRuntime)
}

func cmdResearchMap(args []string) error {
	fs, cfg, _ := commonFlags("research map")
	runID := fs.String("run", "", "run id to map (last one if empty)")
	only := fs.String("only", "", "comma-separated list of track ids to run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" {
		latest, err := findLatestRunDirWithRequired(cfg.runsDir, "plan.yaml")
		if err != nil {
			return err
		}
		*runID = filepath.Base(latest)
	}

	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "virmux", "research")
	onlyList := []string{}
	if *only != "" {
		onlyList = strings.Split(*only, ",")
	}

	return runWithStore(cfg, "research:map", map[string]any{"target_run_id": *runID, "only": onlyList}, func(ctx context.Context, st *store.Store, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
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
		mapper := &research.DefaultMapper{
			RunsDir:  cfg.runsDir,
			CacheDir: cacheDir,
			Store:    st,
		}
		scheduler := &research.DefaultScheduler{
			Mapper: mapper,
			Emitter: func(event string, payload map[string]any) error {
				return emitVM(event, payload)
			},
			TrackArtifactExists: researchTrackArtifactProbe(cfg.runsDir),
		}

		// 3. Run scheduler
		fmt.Printf("Starting research map for run %s (plan %s)\n", *runID, plan.PlanID)
		states, err := scheduler.Run(ctx, plan, *runID, onlyList)
		if err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("scheduler run: %w", err)
		}
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, *runID); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist target artifacts: %w", err)
		}

		// 4. Report status
		for _, s := range states {
			fmt.Printf("Track %-15s status=%-10v error=%v\n", s.TrackID, s.Status, s.Error)
		}

		// Prepare summary details
		details := map[string]any{
			"plan_id": plan.PlanID,
			"tracks":  len(plan.Tracks),
			"only":    onlyList,
			"results": states,
		}

		return vm.Outcome{}, details, nil
	}, defaultRunRuntime)
}

func researchTrackArtifactProbe(runsDir string) func(runID, trackID string) (bool, error) {
	return func(runID, trackID string) (bool, error) {
		p := filepath.Join(runsDir, runID, "map", trackID+".jsonl")
		_, err := os.Stat(p)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
}

func findLatestRunDir(runsDir string) (string, error) {
	return findLatestRunDirWithRequired(runsDir)
}

func findLatestRunDirWithRequired(runsDir string, required ...string) (string, error) {
	ents, err := os.ReadDir(runsDir)
	if err != nil {
		return "", err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() > ents[j].Name() })
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(runsDir, e.Name())
		ok := true
		for _, rel := range required {
			if _, statErr := os.Stat(filepath.Join(path, rel)); statErr != nil {
				ok = false
				break
			}
		}
		if ok {
			return path, nil
		}
	}
	if len(required) == 0 {
		return "", errors.New("no runs found")
	}
	return "", fmt.Errorf("no runs found with required artifacts: %s", strings.Join(required, ","))
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

	planner := &research.DefaultPlanner{
		Hints: &research.DefaultHintProvider{},
	}

	err := runWithStore(cfg, "research:plan", map[string]any{"query": *query}, func(ctx context.Context, st *store.Store, art vm.Artifacts, meta agent.Meta, runDir string, emitVM vmEventEmitter) (vm.Outcome, map[string]any, error) {
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
		if err := persistResearchTargetArtifacts(ctx, st, cfg.runsDir, filepath.Base(runDir)); err != nil {
			return vm.Outcome{}, nil, fmt.Errorf("persist research artifacts: %w", err)
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

func persistResearchTargetArtifacts(ctx context.Context, st *store.Store, runsDir, runID string) error {
	if st == nil {
		return nil
	}
	var exists int
	if err := st.DB().QueryRowContext(ctx, `SELECT 1 FROM runs WHERE id=? LIMIT 1`, runID).Scan(&exists); err != nil {
		// Allow filesystem-only target operations (e.g. cloned throwaway replay targets) without DB rows.
		return nil
	}
	runDir := filepath.Join(runsDir, runID)
	return persistRunArtifacts(ctx, st, runID, []string{
		filepath.Join(runDir, "plan.yaml"),
		filepath.Join(runDir, "map"),
		filepath.Join(runDir, "reduce"),
		filepath.Join(runDir, "mismatch.json"),
	})
}
