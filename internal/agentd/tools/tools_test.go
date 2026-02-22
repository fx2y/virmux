package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func allowAll() map[string]struct{} {
	return map[string]struct{}{"shell.exec": {}, "fs.read": {}, "fs.write": {}, "http.fetch": {}}
}

func TestDenyByDefault(t *testing.T) {
	r := NewRegistry(nil)
	res := r.Handle(context.Background(), Call{ReqID: 1, Tool: "shell.exec", Allow: map[string]struct{}{}, Args: json.RawMessage(`{"cmd":"echo hi"}`), Base: t.TempDir()})
	if res.OK || res.Error == nil || res.Error.Code != "DENIED" {
		t.Fatalf("want DENIED, got %+v", res)
	}
}

func TestFSGuardNoLeak(t *testing.T) {
	r := NewRegistry(nil)
	res := r.Handle(context.Background(), Call{ReqID: 1, Tool: "fs.write", Allow: allowAll(), Args: json.RawMessage(`{"path":"/etc/pwn","bytes":"x"}`), Base: t.TempDir()})
	if res.OK || res.Error == nil || res.Error.Code != "DENIED" {
		t.Fatalf("want DENIED got %+v", res)
	}
}

func TestGuardDataPathRejectsSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	_, err := guardDataPathWithRoot("/data/link/passwd", root, true)
	if err == nil || err.Error() == "" {
		t.Fatalf("expected symlink traversal denial, got err=%v", err)
	}
}

func TestGuardDataPathAllowsCreateInsideDataRoot(t *testing.T) {
	root := t.TempDir()
	got, err := guardDataPathWithRoot("/data/new/dir/file.txt", root, true)
	if err != nil {
		t.Fatalf("expected allow create path, got %v", err)
	}
	want := filepath.Join(root, "new", "dir", "file.txt")
	if got != want {
		t.Fatalf("path mismatch got=%q want=%q", got, want)
	}
}

func TestShellTimeoutTyped(t *testing.T) {
	r := NewRegistry(nil)
	res := r.Handle(context.Background(), Call{ReqID: 1, Tool: "shell.exec", Allow: allowAll(), Args: json.RawMessage(`{"cmd":"sleep 1","timeout_ms":10}`), Base: t.TempDir()})
	if res.Error == nil || res.Error.Code != "TIMEOUT" {
		t.Fatalf("want TIMEOUT got %+v", res)
	}
}

func TestHTTPFetch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer ts.Close()
	r := NewRegistry(nil)
	res := r.Handle(context.Background(), Call{ReqID: 1, Tool: "http.fetch", Allow: allowAll(), Args: json.RawMessage(`{"url":"` + ts.URL + `"}`), Base: t.TempDir()})
	if !res.OK {
		t.Fatalf("http fetch failed: %+v", res)
	}
	if got := res.Data["status"]; got != float64(200) && got != 200 {
		t.Fatalf("status=%v", got)
	}
}
