package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/haris/virmux/internal/agentd/proto"
)

type Call struct {
	ReqID int64
	Tool  string
	Args  json.RawMessage
	Allow map[string]struct{}
	Base  string
}

type Runner interface {
	Run(context.Context, Call) proto.Response
}

type Registry struct{ runners map[string]Runner }

func NewRegistry(httpc *http.Client) Registry {
	if httpc == nil {
		httpc = &http.Client{}
	}
	return Registry{runners: map[string]Runner{
		"shell.exec": shellRunner{},
		"fs.read":    fsReadRunner{},
		"fs.write":   fsWriteRunner{},
		"http.fetch": httpRunner{client: httpc},
	}}
}

func (r Registry) Caps() []string { return []string{"shell.exec", "fs.read", "fs.write", "http.fetch"} }

func (r Registry) Handle(ctx context.Context, c Call) proto.Response {
	if _, ok := c.Allow[c.Tool]; !ok {
		return errResp(c.ReqID, "DENIED", "tool not allowlisted")
	}
	run, ok := r.runners[c.Tool]
	if !ok {
		return errResp(c.ReqID, "DENIED", "unknown tool")
	}
	return run.Run(ctx, c)
}

func errResp(req int64, code, msg string) proto.Response {
	return proto.Response{ReqID: req, OK: false, Error: &proto.Error{Code: code, Msg: msg, Retryable: false, ReqID: req}}
}

type shellArgs struct {
	Cmd       string `json:"cmd"`
	Cwd       string `json:"cwd"`
	TimeoutMS int    `json:"timeout_ms"`
}

type shellRunner struct{}

func (shellRunner) Run(ctx context.Context, c Call) proto.Response {
	var a shellArgs
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return errResp(c.ReqID, "INTERNAL", err.Error())
	}
	if strings.TrimSpace(a.Cmd) == "" {
		return errResp(c.ReqID, "DENIED", "empty cmd")
	}
	if a.TimeoutMS <= 0 {
		a.TimeoutMS = 2000
	}
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(a.TimeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-lc", a.Cmd)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	var outb, errb strings.Builder
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start).Milliseconds()
	stdout := outb.String()
	stderr := errb.String()
	stdoutRef, stderrRef, refs, hash := outputRefsAndHash(c.ReqID, stdout, stderr)
	res := proto.Response{
		ReqID:        c.ReqID,
		OK:           err == nil,
		RC:           0,
		StdoutRef:    stdoutRef,
		StderrRef:    stderrRef,
		OHHash:       hash,
		DurMS:        dur,
		ArtifactRefs: refs,
		Data:         map[string]any{"stdout": stdout, "stderr": stderr},
	}
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			res.Error = &proto.Error{Code: "TIMEOUT", Msg: err.Error(), Retryable: false, ReqID: c.ReqID}
		} else {
			res.Error = &proto.Error{Code: "CRASH", Msg: err.Error(), Retryable: false, ReqID: c.ReqID}
		}
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			res.RC = ee.ExitCode()
		} else if res.Error.Code == "TIMEOUT" {
			res.RC = 124
		} else {
			res.RC = 1
		}
	}
	return res
}

type fsReadRunner struct{}

func (fsReadRunner) Run(ctx context.Context, c Call) proto.Response {
	_ = ctx
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return errResp(c.ReqID, "INTERNAL", err.Error())
	}
	p, derr := guardDataPath(a.Path, false)
	if derr != nil {
		return errResp(c.ReqID, "DENIED", derr.Error())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return errResp(c.ReqID, "CRASH", err.Error())
	}
	return proto.Response{ReqID: c.ReqID, OK: true, Data: map[string]any{"bytes": string(b), "path": p}, OHHash: sha256Hex(b)}
}

type fsWriteRunner struct{}

func (fsWriteRunner) Run(ctx context.Context, c Call) proto.Response {
	_ = ctx
	var a struct {
		Path  string `json:"path"`
		Bytes string `json:"bytes"`
		Mode  int    `json:"mode"`
	}
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return errResp(c.ReqID, "INTERNAL", err.Error())
	}
	p, derr := guardDataPath(a.Path, true)
	if derr != nil {
		return errResp(c.ReqID, "DENIED", derr.Error())
	}
	if a.Mode == 0 {
		a.Mode = 0644
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return errResp(c.ReqID, "CRASH", err.Error())
	}
	if err := os.WriteFile(p, []byte(a.Bytes), os.FileMode(a.Mode)); err != nil {
		return errResp(c.ReqID, "CRASH", err.Error())
	}
	return proto.Response{ReqID: c.ReqID, OK: true, OHHash: sha256Hex([]byte(a.Bytes)), Data: map[string]any{"path": p, "bytes": len(a.Bytes)}}
}

type httpRunner struct{ client *http.Client }

func (h httpRunner) Run(ctx context.Context, c Call) proto.Response {
	var a struct {
		URL, Method, Body string
		Headers           map[string]string `json:"headers"`
		TimeoutMS         int               `json:"timeout_ms"`
	}
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return errResp(c.ReqID, "INTERNAL", err.Error())
	}
	if a.TimeoutMS <= 0 {
		a.TimeoutMS = 2000
	}
	if a.Method == "" {
		a.Method = http.MethodGet
	}
	hc := *h.client
	hc.Timeout = time.Duration(a.TimeoutMS) * time.Millisecond
	req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, strings.NewReader(a.Body))
	if err != nil {
		return errResp(c.ReqID, "CRASH", err.Error())
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := hc.Do(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		code := "CRASH"
		if os.IsTimeout(err) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			code = "TIMEOUT"
		}
		return proto.Response{ReqID: c.ReqID, OK: false, DurMS: dur, Error: &proto.Error{Code: code, Msg: err.Error(), Retryable: false, ReqID: c.ReqID}}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return proto.Response{ReqID: c.ReqID, OK: true, DurMS: dur, OHHash: sha256Hex(body), Data: map[string]any{"status": resp.StatusCode, "body": string(body)}}
}

func guardDataPath(p string, allowCreate bool) (string, error) {
	return guardDataPathWithRoot(p, "/dev/virmux-data", allowCreate)
}

func guardDataPathWithRoot(p, hostRoot string, allowCreate bool) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("path empty")
	}
	if strings.TrimSpace(hostRoot) == "" {
		return "", fmt.Errorf("path denied: empty host root")
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		clean = filepath.Join("/data", clean)
	}
	if clean != "/data" && !strings.HasPrefix(clean, "/data/") {
		return clean, fmt.Errorf("path denied: %s", clean)
	}
	if clean == "/data" {
		if err := denySymlinkComponents(hostRoot, hostRoot, allowCreate); err != nil {
			return "", err
		}
		return hostRoot, nil
	}
	hostPath := filepath.Join(hostRoot, strings.TrimPrefix(clean, "/data/"))
	if err := denySymlinkComponents(hostRoot, hostPath, allowCreate); err != nil {
		return "", err
	}
	return hostPath, nil
}

func denySymlinkComponents(root, target string, allowCreate bool) error {
	rootClean := filepath.Clean(root)
	targetClean := filepath.Clean(target)
	if !filepath.IsAbs(rootClean) || !filepath.IsAbs(targetClean) {
		return fmt.Errorf("path denied: non-absolute root/target")
	}
	if targetClean != rootClean && !strings.HasPrefix(targetClean, rootClean+string(filepath.Separator)) {
		return fmt.Errorf("path denied: %s", targetClean)
	}
	if err := denyIfSymlink(rootClean); err != nil {
		return err
	}
	rel, err := filepath.Rel(rootClean, targetClean)
	if err != nil {
		return fmt.Errorf("path denied: %w", err)
	}
	if rel == "." {
		return nil
	}
	cur := rootClean
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if allowCreate {
					return nil
				}
				return err
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path denied: symlink component %s", cur)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("path denied: non-directory component %s", cur)
		}
	}
	return nil
}

func denyIfSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path denied: symlink component %s", path)
	}
	return nil
}

func outputRefsAndHash(reqID int64, stdout, stderr string) (string, string, []string, string) {
	outRel := fmt.Sprintf("artifacts/%d.out", reqID)
	errRel := fmt.Sprintf("artifacts/%d.err", reqID)
	combined := append([]byte(stdout), []byte(stderr)...)
	return outRel, errRel, []string{outRel, errRel}, sha256Hex(combined)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
