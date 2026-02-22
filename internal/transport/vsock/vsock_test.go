package vsock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestVsockDialConnectAck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "v.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		if string(buf[:n]) == "CONNECT 52\n" {
			_, _ = io.WriteString(conn, "OK 52\n")
		}
	}()

	c, err := Dial(context.Background(), sock, 52)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	_ = c.Close()
}

func TestVsockDialRejectsBadAck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "v.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "OK 99\n")
	}()

	_, err = Dial(context.Background(), sock, 52)
	if !errors.Is(err, ErrConnectAck) {
		t.Fatalf("expected ErrConnectAck, got %v", err)
	}
}

func TestVsockDialWithRetrySucceedsAfterEarlyEOF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "v.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n := attempts.Add(1)
			if n == 1 {
				_ = conn.Close()
				continue
			}
			_, _ = io.WriteString(conn, "OK 52\n")
			_ = conn.Close()
			return
		}
	}()

	res, err := DialWithRetry(context.Background(), sock, 52, RetryPolicy{MaxAttempts: 4, BaseBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond})
	if err != nil {
		t.Fatalf("dial with retry failed: %v", err)
	}
	if res.Stats.Attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", res.Stats.Attempts)
	}
}

func TestVsockDialWithRetryExhausted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "missing.sock")
	_, err := DialWithRetry(context.Background(), sock, 52, RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("expected ErrRetryExhausted, got %v", err)
	}
}

func TestVsockChaos50NoStuck(t *testing.T) {
	t.Parallel()
	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			dir := t.TempDir()
			sock := filepath.Join(dir, "v.sock")
			ln, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			var attempts atomic.Int32
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					a := attempts.Add(1)
					if a <= 2 {
						_ = conn.Close()
						continue
					}
					_, _ = io.WriteString(conn, "OK 10240\n")
					_ = conn.Close()
					return
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err = DialWithRetry(ctx, sock, 10240, RetryPolicy{MaxAttempts: 8, BaseBackoff: time.Millisecond, MaxBackoff: 8 * time.Millisecond})
			if err != nil {
				t.Fatalf("chaos dial failed: %v", err)
			}
		})
	}
}
