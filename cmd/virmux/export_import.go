package main

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haris/virmux/internal/store"
	"github.com/haris/virmux/internal/trace"
)

type exportManifestEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Link   string `json:"link,omitempty"`
}

type exportBundleMeta struct {
	Version int    `json:"version"`
	Mode    string `json:"mode,omitempty"`
	RunID   string `json:"run_id"`
	EvalID  string `json:"eval_id,omitempty"`
	Task    string `json:"task"`
	Partial bool   `json:"partial,omitempty"`
}

type exportOptions struct {
	Partial bool
}

func cmdExport(args []string) error {
	fs := flagSet("export")
	mode := fs.String("mode", "run", "bundle mode: run|eval")
	runID := fs.String("run-id", "", "run id to export")
	evalID := fs.String("eval-id", "", "eval id to export (mode=eval)")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	outPath := fs.String("out", "", "output bundle path (.tar.zst)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch strings.TrimSpace(*mode) {
	case "", "run":
		if strings.TrimSpace(*runID) == "" {
			return errors.New("--run-id required")
		}
		out := *outPath
		if out == "" {
			out = filepath.Join(*runsDir, *runID+".tar.zst")
		}
		return exportRunBundle(context.Background(), *dbPath, *runsDir, *runID, out, exportOptions{})
	case "eval":
		if strings.TrimSpace(*evalID) == "" {
			return errors.New("--eval-id required when --mode=eval")
		}
		out := *outPath
		if out == "" {
			out = filepath.Join(*runsDir, *evalID+".eval.tar.zst")
		}
		return exportEvalBundle(context.Background(), *dbPath, *runsDir, *evalID, out)
	default:
		return fmt.Errorf("unsupported export mode: %s", *mode)
	}
}

func cmdImport(args []string) error {
	fs := flagSet("import")
	bundle := fs.String("bundle", "", "bundle path (.tar.zst)")
	dbPath := fs.String("db", "runs/virmux.sqlite", "sqlite db path")
	runsDir := fs.String("runs-dir", "runs", "runs directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*bundle) == "" {
		return errors.New("--bundle required")
	}
	return importRunBundle(context.Background(), *bundle, *dbPath, *runsDir)
}

func flagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func exportRunBundle(ctx context.Context, dbPath, runsDir, runID, outPath string, opts exportOptions) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	db := st.DB()
	runDir := filepath.Join(runsDir, runID)
	info, err := os.Stat(runDir)
	if err != nil {
		return fmt.Errorf("stat run dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("run dir not directory: %s", runDir)
	}

	task, err := queryRunTask(db, runID)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "virmux-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	stage := filepath.Join(tmpDir, "bundle")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stage, "db"), 0o755); err != nil {
		return err
	}
	if err := copyTree(runDir, filepath.Join(stage, "run")); err != nil {
		return err
	}
	if err := writeRunSnapshots(db, runID, filepath.Join(stage, "db")); err != nil {
		return err
	}
	meta := exportBundleMeta{Version: 1, Mode: "run", RunID: runID, Task: task, Partial: opts.Partial}
	if err := writeJSONFile(filepath.Join(stage, "meta.json"), meta); err != nil {
		return err
	}
	manifest, err := buildManifest(stage)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return err
	}

	tarPath := filepath.Join(tmpDir, "bundle.tar")
	if err := writeDeterministicTar(stage, tarPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := compressZstd(tarPath, outPath); err != nil {
		return err
	}
	return nil
}

func exportEvalBundle(ctx context.Context, dbPath, runsDir, evalID, outPath string) error {
	_ = ctx
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	db := st.DB()

	runDir := filepath.Join(runsDir, evalID)
	info, err := os.Stat(runDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "virmux-export-eval-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	stage := filepath.Join(tmpDir, "bundle")
	if err := os.MkdirAll(filepath.Join(stage, "db"), 0o755); err != nil {
		return err
	}
	if err == nil && info != nil && info.IsDir() {
		if err := copyTree(runDir, filepath.Join(stage, "run")); err != nil {
			return err
		}
	}
	if err := writeEvalSnapshots(db, evalID, filepath.Join(stage, "db")); err != nil {
		return err
	}
	meta := exportBundleMeta{Version: 1, Mode: "eval", EvalID: evalID, Task: "skill:ab"}
	if err := writeJSONFile(filepath.Join(stage, "meta.json"), meta); err != nil {
		return err
	}
	manifest, err := buildManifest(stage)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return err
	}
	tarPath := filepath.Join(tmpDir, "bundle.tar")
	if err := writeDeterministicTar(stage, tarPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := compressZstd(tarPath, outPath); err != nil {
		return err
	}
	return nil
}

func importRunBundle(ctx context.Context, bundlePath, dbPath, runsDir string) error {
	_ = ctx
	tmpDir, err := os.MkdirTemp("", "virmux-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	stage := filepath.Join(tmpDir, "bundle")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	if err := extractZstdTar(bundlePath, stage); err != nil {
		return err
	}
	if err := verifyManifest(stage); err != nil {
		return err
	}
	var meta exportBundleMeta
	if err := readJSONFile(filepath.Join(stage, "meta.json"), &meta); err != nil {
		return err
	}
	if meta.Mode == "eval" {
		st, err := store.Open(dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := importEvalSnapshotsIntoStore(st.DB(), stage); err != nil {
			return err
		}
		destRunDir := filepath.Join(runsDir, meta.EvalID)
		if _, err := os.Lstat(destRunDir); err == nil {
			// Skip copy if already exists
			return nil
		}
		if _, err := os.Stat(filepath.Join(stage, "run")); err == nil {
			if err := copyTree(filepath.Join(stage, "run"), destRunDir); err != nil {
				return err
			}
		}
		return nil
	}
	destRunDir := filepath.Join(runsDir, meta.RunID)
	if _, err := os.Lstat(destRunDir); err == nil {
		return fmt.Errorf("run dir already exists: %s", destRunDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := importSnapshotsIntoStore(st.DB(), stage, bundlePath); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(stage, "run"), destRunDir); err != nil {
		return err
	}
	return nil
}

func queryRunTask(db *sql.DB, runID string) (string, error) {
	var task string
	if err := db.QueryRow(`SELECT task FROM runs WHERE id=?`, runID).Scan(&task); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("run not found: %s", runID)
		}
		return "", err
	}
	return task, nil
}

func writeRunSnapshots(db *sql.DB, runID, outDir string) error {
	if err := snapshotRows(db, filepath.Join(outDir, "runs.json"),
		`SELECT id,task,label,agent_id,image_sha,kernel_sha,rootfs_sha,snapshot_id,cost_est,status,started_at,ended_at,boot_ms,resume_ms,trace_path,source_bundle FROM runs WHERE id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "events.json"),
		`SELECT run_id,ts,kind,payload FROM events WHERE run_id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "artifacts.json"),
		`SELECT run_id,path,sha256,bytes FROM artifacts WHERE run_id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "tool_calls.json"),
		`SELECT run_id,seq,req_id,tool,input_hash,output_hash,input_ref,output_ref,stdout_ref,stderr_ref,rc,dur_ms,bytes_in,bytes_out,error_code FROM tool_calls WHERE run_id=? ORDER BY seq,id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "scores.json"),
		`SELECT run_id,skill,score,pass,critique,judge_cfg_hash,artifact_hash,created_at FROM scores WHERE run_id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "judge_runs.json"),
		`SELECT run_id,skill,rubric_hash,judge_cfg_hash,artifact_hash,metrics_json,critique,score,pass,created_at,model_id,prompt_hash,schema_ver,mode,judge_invalid_count FROM judge_runs WHERE run_id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "refine_runs.json"),
		`SELECT id,run_id,skill,eval_run_id,branch,commit_sha,patch_hash,patch_path,pr_body_path,hunk_count,tools_edit,created_at FROM refine_runs WHERE run_id=? ORDER BY id`,
		runID); err != nil {
		return err
	}
	task, _ := queryRunTask(db, runID)
	if task == "skill:ab" {
		if err := writeEvalSnapshots(db, runID, outDir); err != nil {
			return err
		}
	}
	return nil
}

func writeEvalSnapshots(db *sql.DB, evalID, outDir string) error {
	// Include the primary eval_run and any curated_eval_run_id referenced by its canary runs.
	q := `SELECT id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at 
	      FROM eval_runs 
	      WHERE id=? 
	      OR id IN (SELECT curated_eval_run_id FROM canary_runs WHERE eval_run_id=? AND curated_eval_run_id != '')
	      ORDER BY id`
	if err := snapshotRows(db, filepath.Join(outDir, "eval_runs.json"), q, evalID, evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "eval_cases.json"),
		`SELECT eval_run_id,fixture_id,base_score,head_score,base_pass,head_pass,base_cost,head_cost,created_at FROM eval_cases WHERE eval_run_id=? ORDER BY id`,
		evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "promotions.json"),
		`SELECT id,skill,tag,base_ref,head_ref,from_ref,to_ref,reason,metrics_json,commit_sha,op,eval_run_id,verdict_sha256,actor,created_at FROM promotions WHERE eval_run_id=? ORDER BY created_at,id`,
		evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "experiments.json"),
		`SELECT id,eval_run_id,skill,cohort,base_ref,head_ref,judge_mode,created_at FROM experiments WHERE eval_run_id=? ORDER BY id`,
		evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "comparisons.json"),
		`SELECT c.experiment_id,c.fixture_id,c.winner,c.rationale,c.created_at
		 FROM comparisons c
		 WHERE c.experiment_id IN (SELECT e.id FROM experiments e WHERE e.eval_run_id=?)
		 ORDER BY c.experiment_id,c.id`,
		evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "canary_runs.json"),
		`SELECT id,skill,eval_run_id,curated_eval_run_id,dset_path,dset_sha256,dset_count,candidate_ref,baseline_ref,gate_verdict_json,action,action_ref,caught_by_canary,backlog_path,summary_path,created_at FROM canary_runs WHERE eval_run_id=? ORDER BY created_at,id`,
		evalID); err != nil {
		return err
	}
	if err := snapshotRows(db, filepath.Join(outDir, "suggest_runs.json"),
		`SELECT id,skill,eval_run_id,motif_key,branch,commit_sha,pr_body_hash,pr_body_path,run_ids_json,created_at FROM suggest_runs WHERE eval_run_id=? ORDER BY created_at,id`,
		evalID); err != nil {
		return err
	}
	return nil
}

func snapshotRows(db *sql.DB, outPath, q string, args ...any) error {
	rows, err := db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	var all []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		holders := make([]any, len(cols))
		for i := range holders {
			holders[i] = &dest[i]
		}
		if err := rows.Scan(holders...); err != nil {
			return err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := dest[i].(type) {
			case []byte:
				m[c] = string(v)
			default:
				m[c] = v
			}
		}
		all = append(all, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writeJSONFile(outPath, all)
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func buildManifest(stage string) ([]exportManifestEntry, error) {
	var entries []exportManifestEntry
	err := filepath.Walk(stage, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == "manifest.json" {
			return nil
		}
		rel = filepath.ToSlash(rel)
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entries = append(entries, exportManifestEntry{Path: rel, Kind: "symlink", SHA256: "meta:symlink", Link: target})
		case mode.IsDir():
			entries = append(entries, exportManifestEntry{Path: rel, Kind: "dir", SHA256: "meta:dir"})
		case mode.IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entries = append(entries, exportManifestEntry{Path: rel, Kind: "file", SHA256: trace.SHA256Hex(b), Bytes: info.Size()})
		default:
			entries = append(entries, exportManifestEntry{Path: rel, Kind: "other", SHA256: fmt.Sprintf("meta:mode:%#o", uint32(mode.Type()))})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func writeDeterministicTar(stageDir, tarPath string) error {
	out, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer out.Close()
	tw := tar.NewWriter(out)
	defer tw.Close()

	var paths []string
	if err := filepath.Walk(stageDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == stageDir {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)

	epoch := time.Unix(0, 0).UTC()
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		hdr.ModTime = epoch
		hdr.AccessTime = epoch
		hdr.ChangeTime = epoch
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Linkname = target
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, f); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}
	return nil
}

func compressZstd(tarPath, outPath string) error {
	cmd := exec.Command("zstd", "-q", "-f", "-19", tarPath, "-o", outPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zstd compress: %w", err)
	}
	return nil
}

func extractZstdTar(bundlePath, dest string) error {
	cmd := exec.Command("zstd", "-dc", bundlePath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(stdout)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			_ = cmd.Wait()
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				_ = cmd.Wait()
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				_ = cmd.Wait()
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				_ = cmd.Wait()
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				_ = cmd.Wait()
				return err
			}
			_ = f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				_ = cmd.Wait()
				return err
			}
			linkName, err := validateSymlinkTarget(dest, target, hdr.Linkname)
			if err != nil {
				_ = cmd.Wait()
				return err
			}
			if err := os.Symlink(linkName, target); err != nil {
				_ = cmd.Wait()
				return err
			}
		default:
			_ = cmd.Wait()
			return fmt.Errorf("unsupported tar entry type %d for %s", hdr.Typeflag, hdr.Name)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("zstd extract: %w", err)
	}
	return nil
}

func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." {
		return root, nil
	}
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("unsafe bundle path: %s", name)
	}
	return filepath.Join(root, clean), nil
}

func validateSymlinkTarget(root, linkPath, linkName string) (string, error) {
	cleanRoot := filepath.Clean(root)
	if filepath.IsAbs(linkName) {
		return "", fmt.Errorf("unsafe symlink target: %s", linkName)
	}
	cleanLink := filepath.Clean(linkName)
	if cleanLink == "." || cleanLink == "" {
		return "", fmt.Errorf("unsafe symlink target: %s", linkName)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), cleanLink))
	if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe symlink target: %s", linkName)
	}
	return cleanLink, nil
}

func verifyManifest(stage string) error {
	var manifest []exportManifestEntry
	if err := readJSONFile(filepath.Join(stage, "manifest.json"), &manifest); err != nil {
		return err
	}
	actual, err := buildManifest(stage)
	if err != nil {
		return err
	}
	got, _ := json.Marshal(actual)
	want, _ := json.Marshal(manifest)
	if !bytes.Equal(got, want) {
		return fmt.Errorf("bundle manifest mismatch")
	}
	return nil
}

func importSnapshotsIntoStore(db *sql.DB, stage, bundlePath string) error {
	var runsRows []map[string]any
	var eventRows []map[string]any
	var artRows []map[string]any
	var toolRows []map[string]any
	var scoreRows []map[string]any
	var judgeRows []map[string]any
	var refineRows []map[string]any
	if err := readJSONFile(filepath.Join(stage, "db", "runs.json"), &runsRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "events.json"), &eventRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "artifacts.json"), &artRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "tool_calls.json"), &toolRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "scores.json"), &scoreRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "judge_runs.json"), &judgeRows); err != nil {
		return err
	}
	if err := readJSONFile(filepath.Join(stage, "db", "refine_runs.json"), &refineRows); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		refineRows = nil
	}
	if len(runsRows) != 1 {
		return fmt.Errorf("bundle runs.json must contain exactly 1 row")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	r := runsRows[0]
	if _, err := tx.Exec(`INSERT INTO runs(id,task,label,agent_id,image_sha,kernel_sha,rootfs_sha,snapshot_id,cost_est,status,started_at,ended_at,boot_ms,resume_ms,trace_path,source_bundle)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		str(r["id"]),
		str(r["task"]),
		str(r["label"]),
		str(r["agent_id"]),
		str(r["image_sha"]),
		str(r["kernel_sha"]),
		str(r["rootfs_sha"]),
		str(r["snapshot_id"]),
		numf(r["cost_est"]),
		str(r["status"]),
		str(r["started_at"]),
		nilIfEmpty(str(r["ended_at"])),
		numi(r["boot_ms"]),
		numi(r["resume_ms"]),
		str(r["trace_path"]),
		bundlePath,
	); err != nil {
		return fmt.Errorf("insert imported run: %w", err)
	}
	for _, row := range eventRows {
		if _, err := tx.Exec(`INSERT INTO events(run_id,ts,kind,payload) VALUES(?,?,?,?)`,
			str(row["run_id"]), str(row["ts"]), str(row["kind"]), str(row["payload"])); err != nil {
			return fmt.Errorf("insert imported event: %w", err)
		}
	}
	for _, row := range artRows {
		if _, err := tx.Exec(`INSERT INTO artifacts(run_id,path,sha256,bytes) VALUES(?,?,?,?)`,
			str(row["run_id"]), str(row["path"]), str(row["sha256"]), numi(row["bytes"])); err != nil {
			return fmt.Errorf("insert imported artifact: %w", err)
		}
	}
	for _, row := range toolRows {
		if _, err := tx.Exec(`INSERT INTO tool_calls(run_id,seq,req_id,tool,input_hash,output_hash,input_ref,output_ref,stdout_ref,stderr_ref,rc,dur_ms,bytes_in,bytes_out,error_code)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			str(row["run_id"]), numi(row["seq"]), numi(row["req_id"]), str(row["tool"]), str(row["input_hash"]), str(row["output_hash"]),
			str(row["input_ref"]), str(row["output_ref"]), str(row["stdout_ref"]), str(row["stderr_ref"]),
			numi(row["rc"]), numi(row["dur_ms"]), numi(row["bytes_in"]), numi(row["bytes_out"]), str(row["error_code"])); err != nil {
			return fmt.Errorf("insert imported tool_call: %w", err)
		}
	}
	for _, row := range scoreRows {
		if _, err := tx.Exec(`INSERT INTO scores(run_id,skill,score,pass,critique,judge_cfg_hash,artifact_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			str(row["run_id"]), str(row["skill"]), numf(row["score"]), numi(row["pass"]), str(row["critique"]),
			str(row["judge_cfg_hash"]), str(row["artifact_hash"]), str(row["created_at"])); err != nil {
			return fmt.Errorf("insert imported score: %w", err)
		}
	}
	for _, row := range judgeRows {
		if _, err := tx.Exec(`INSERT INTO judge_runs(run_id,skill,rubric_hash,judge_cfg_hash,artifact_hash,metrics_json,critique,score,pass,created_at,model_id,prompt_hash,schema_ver,mode,judge_invalid_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			str(row["run_id"]), str(row["skill"]), str(row["rubric_hash"]), str(row["judge_cfg_hash"]),
			str(row["artifact_hash"]), str(row["metrics_json"]), str(row["critique"]), numf(row["score"]), numi(row["pass"]), str(row["created_at"]),
			str(row["model_id"]), str(row["prompt_hash"]), str(row["schema_ver"]), str(row["mode"]), numi(row["judge_invalid_count"])); err != nil {
			return fmt.Errorf("insert imported judge_run: %w", err)
		}
	}
		for _, row := range refineRows {
			if _, err := tx.Exec(`INSERT INTO refine_runs(id,run_id,skill,eval_run_id,branch,commit_sha,patch_hash,patch_path,pr_body_path,hunk_count,tools_edit,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
				str(row["id"]), str(row["run_id"]), str(row["skill"]), str(row["eval_run_id"]), str(row["branch"]), str(row["commit_sha"]), str(row["patch_hash"]), str(row["patch_path"]), str(row["pr_body_path"]), numi(row["hunk_count"]), numi(row["tools_edit"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported refine_run: %w", err)
			}
		}
	
		task := str(r["task"])
		if task == "skill:ab" {
			if err := importEvalSnapshotsIntoTx(tx, stage); err != nil {
				return err
			}
		}
	
		return tx.Commit()
	}
	
	func importEvalSnapshotsIntoStore(db *sql.DB, stage string) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := importEvalSnapshotsIntoTx(tx, stage); err != nil {
			return err
		}
		return tx.Commit()
	}
	
	func importEvalSnapshotsIntoTx(tx *sql.Tx, stage string) error {
		evalRows, err := readSnapshotRows(filepath.Join(stage, "db", "eval_runs.json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		evalCaseRows, err := readSnapshotRows(filepath.Join(stage, "db", "eval_cases.json"))
		if err != nil {
			return err
		}
		promoRows, err := readSnapshotRows(filepath.Join(stage, "db", "promotions.json"))
		if err != nil {
			return err
		}
		expRows, err := readSnapshotRows(filepath.Join(stage, "db", "experiments.json"))
		if err != nil {
			return err
		}
		compRows, err := readSnapshotRows(filepath.Join(stage, "db", "comparisons.json"))
		if err != nil {
			return err
		}
		canaryRows, err := readSnapshotRows(filepath.Join(stage, "db", "canary_runs.json"))
		if err != nil {
			return err
		}
		suggestRows, err := readSnapshotRows(filepath.Join(stage, "db", "suggest_runs.json"))
		if err != nil {
			return err
		}
	
		for _, r := range evalRows {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO eval_runs(id,skill,cohort,base_ref,head_ref,provider,fixtures_hash,cfg_sha256,cfg_path,results_sha256,results_path,verdict_sha256,verdict_path,score_p50_base,score_p50_head,fail_rate_base,fail_rate_head,cost_total_base,cost_total_head,score_p50_delta,fail_rate_delta,cost_delta,pass,verdict_json,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				str(r["id"]), str(r["skill"]), str(r["cohort"]), str(r["base_ref"]), str(r["head_ref"]), str(r["provider"]), str(r["fixtures_hash"]),
				str(r["cfg_sha256"]), str(r["cfg_path"]), str(r["results_sha256"]), str(r["results_path"]), str(r["verdict_sha256"]), str(r["verdict_path"]),
				numf(r["score_p50_base"]), numf(r["score_p50_head"]), numf(r["fail_rate_base"]), numf(r["fail_rate_head"]), numf(r["cost_total_base"]), numf(r["cost_total_head"]),
				numf(r["score_p50_delta"]), numf(r["fail_rate_delta"]), numf(r["cost_delta"]), numi(r["pass"]), str(r["verdict_json"]), str(r["created_at"])); err != nil {
				return fmt.Errorf("insert imported eval_run: %w", err)
			}
		}
		for _, row := range evalCaseRows {
			if _, err := tx.Exec(`INSERT INTO eval_cases(eval_run_id,fixture_id,base_score,head_score,base_pass,head_pass,base_cost,head_cost,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				str(row["eval_run_id"]), str(row["fixture_id"]), numf(row["base_score"]), numf(row["head_score"]), numi(row["base_pass"]), numi(row["head_pass"]), numf(row["base_cost"]), numf(row["head_cost"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported eval_case: %w", err)
			}
		}
		for _, row := range promoRows {
			var evalRunID sql.NullString
			if v := str(row["eval_run_id"]); v != "" {
				evalRunID = sql.NullString{String: v, Valid: true}
			}
			if _, err := tx.Exec(`INSERT INTO promotions(id,skill,tag,base_ref,head_ref,from_ref,to_ref,reason,metrics_json,commit_sha,op,eval_run_id,verdict_sha256,actor,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				str(row["id"]), str(row["skill"]), str(row["tag"]), str(row["base_ref"]), str(row["head_ref"]), str(row["from_ref"]), str(row["to_ref"]),
				str(row["reason"]), str(row["metrics_json"]), str(row["commit_sha"]), str(row["op"]), evalRunID, str(row["verdict_sha256"]), str(row["actor"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported promotion: %w", err)
			}
		}
		for _, row := range expRows {
			var evalRunID sql.NullString
			if v := str(row["eval_run_id"]); v != "" {
				evalRunID = sql.NullString{String: v, Valid: true}
			}
			if _, err := tx.Exec(`INSERT INTO experiments(id,eval_run_id,skill,cohort,base_ref,head_ref,judge_mode,created_at) VALUES(?,?,?,?,?,?,?,?)`,
				str(row["id"]), evalRunID, str(row["skill"]), str(row["cohort"]), str(row["base_ref"]), str(row["head_ref"]), str(row["judge_mode"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported experiment: %w", err)
			}
		}
		for _, row := range compRows {
			if _, err := tx.Exec(`INSERT INTO comparisons(experiment_id,fixture_id,winner,rationale,created_at) VALUES(?,?,?,?,?)`,
				str(row["experiment_id"]), str(row["fixture_id"]), str(row["winner"]), str(row["rationale"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported comparison: %w", err)
			}
		}
		for _, row := range canaryRows {
			var curated sql.NullString
			if v := str(row["curated_eval_run_id"]); v != "" {
				curated = sql.NullString{String: v, Valid: true}
			}
			if _, err := tx.Exec(`INSERT INTO canary_runs(id,skill,eval_run_id,curated_eval_run_id,dset_path,dset_sha256,dset_count,candidate_ref,baseline_ref,gate_verdict_json,action,action_ref,caught_by_canary,backlog_path,summary_path,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				str(row["id"]), str(row["skill"]), str(row["eval_run_id"]), curated, str(row["dset_path"]), str(row["dset_sha256"]), numi(row["dset_count"]),
				str(row["candidate_ref"]), str(row["baseline_ref"]), str(row["gate_verdict_json"]), str(row["action"]), str(row["action_ref"]), numi(row["caught_by_canary"]),
				str(row["backlog_path"]), str(row["summary_path"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported canary_run: %w", err)
			}
		}
		for _, row := range suggestRows {
			var evalRunID sql.NullString
			if v := str(row["eval_run_id"]); v != "" {
				evalRunID = sql.NullString{String: v, Valid: true}
			}
			if _, err := tx.Exec(`INSERT INTO suggest_runs(id,skill,eval_run_id,motif_key,branch,commit_sha,pr_body_hash,pr_body_path,run_ids_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				str(row["id"]), str(row["skill"]), evalRunID, str(row["motif_key"]), str(row["branch"]), str(row["commit_sha"]), str(row["pr_body_hash"]), str(row["pr_body_path"]), str(row["run_ids_json"]), str(row["created_at"])); err != nil {
				return fmt.Errorf("insert imported suggest_run: %w", err)
			}
		}
		return nil
	}
	

func readSnapshotRows(path string) ([]map[string]any, error) {
	var rows []map[string]any
	if err := readJSONFile(path, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		mode := info.Mode()
		switch {
		case rel == ".":
			return os.MkdirAll(dst, 0o755)
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		case mode.IsDir():
			return os.MkdirAll(target, mode.Perm())
		case mode.IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				_ = out.Close()
				return err
			}
			return out.Close()
		default:
			return nil
		}
	})
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func numi(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case nil:
		return 0
	default:
		var n int64
		fmt.Sscan(fmt.Sprint(x), &n)
		return n
	}
}

func numf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		n, _ := x.Float64()
		return n
	case nil:
		return 0
	default:
		var n float64
		fmt.Sscan(fmt.Sprint(x), &n)
		return n
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
