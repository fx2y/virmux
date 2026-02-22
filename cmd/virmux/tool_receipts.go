package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/haris/virmux/internal/trace"
	trpc "github.com/haris/virmux/internal/transport/rpc"
)

func buildToolResultPayload(runDir string, req trpc.Request, res trpc.Response) (map[string]any, error) {
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal tool request: %w", err)
	}
	resBytes, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("marshal tool response: %w", err)
	}
	reqRef, resRef, receiptRef, err := writeToolIOArtifacts(runDir, req.ReqID, reqBytes, resBytes)
	if err != nil {
		return nil, err
	}
	errCode := ""
	if res.Error != nil {
		if code, _ := res.Error["code"].(string); code != "" {
			errCode = code
		}
	}
	payload := map[string]any{
		"tool_seq":    req.ReqID,
		"req":         req.ReqID,
		"tool":        req.Tool,
		"args_hash":   trace.SHA256Hex(reqBytes),
		"input_hash":  trace.SHA256Hex(reqBytes),
		"output_hash": trace.SHA256Hex(resBytes),
		"input_ref":   reqRef,
		"output_ref":  resRef,
		"receipt_ref": receiptRef,
		"stdout_ref":  res.StdoutRef,
		"stderr_ref":  res.StderrRef,
		"exit_code":   res.RC,
		"dur_ms":      res.DurMS,
		"bytes_in":    int64(len(reqBytes)),
		"bytes_out":   int64(len(resBytes)),
		"error_code":  errCode,
	}
	return payload, nil
}

func writeToolIOArtifacts(runDir string, reqID int64, reqBytes, resBytes []byte) (reqRef, resRef, receiptRef string, err error) {
	toolDir := filepath.Join(runDir, "toolio")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("mkdir toolio: %w", err)
	}
	base := fmt.Sprintf("%06d", reqID)
	reqRef = filepath.ToSlash(filepath.Join("toolio", base+".req.json"))
	resRef = filepath.ToSlash(filepath.Join("toolio", base+".res.json"))
	receiptRef = filepath.ToSlash(filepath.Join("toolio", base+".receipt.json"))
	if err := os.WriteFile(filepath.Join(runDir, filepath.FromSlash(reqRef)), append(reqBytes, '\n'), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write tool req: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, filepath.FromSlash(resRef)), append(resBytes, '\n'), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write tool res: %w", err)
	}
	receipt := map[string]any{
		"req_id":      reqID,
		"input_hash":  trace.SHA256Hex(reqBytes),
		"output_hash": trace.SHA256Hex(resBytes),
		"input_ref":   reqRef,
		"output_ref":  resRef,
		"bytes_in":    len(reqBytes),
		"bytes_out":   len(resBytes),
	}
	b, err := json.Marshal(receipt)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal tool receipt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, filepath.FromSlash(receiptRef)), append(b, '\n'), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write tool receipt: %w", err)
	}
	return reqRef, resRef, receiptRef, nil
}

func extraRunArtifactPaths(paths []string) []string {
	runDirs := map[string]struct{}{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		base := filepath.Base(p)
		if base == tracePrimaryName || base == traceCompatName || base == runMetaName {
			runDirs[filepath.Dir(p)] = struct{}{}
		}
	}
	var out []string
	for runDir := range runDirs {
		for _, rel := range []string{"toolio", "artifacts"} {
			root := filepath.Join(runDir, rel)
			if _, err := os.Lstat(root); err != nil {
				continue
			}
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				out = append(out, path)
				return nil
			})
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:0]
	var prev string
	for i, s := range in {
		if i == 0 || s != prev {
			out = append(out, s)
			prev = s
		}
	}
	return out
}
