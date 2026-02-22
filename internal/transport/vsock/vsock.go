package vsock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

var (
	ErrConnectAck      = errors.New("vsock CONNECT ack mismatch")
	ErrRetryExhausted  = errors.New("vsock retry budget exhausted")
	ErrNonRetryableAck = errors.New("vsock non-retryable handshake failure")
)

type Stats struct {
	Attempts    int
	HandshakeMS int64
}

type DialResult struct {
	Conn  net.Conn
	Stats Stats
}

type RetryPolicy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 12,
		BaseBackoff: 25 * time.Millisecond,
		MaxBackoff:  800 * time.Millisecond,
	}
}

func Dial(ctx context.Context, udsPath string, port uint32) (net.Conn, error) {
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", udsPath)
	if err != nil {
		return nil, err
	}
	ack, err := sendConnectAndReadAck(conn, port)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	want := fmt.Sprintf("OK %d", port)
	if ack != want {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: got=%q want=%q", ErrConnectAck, ack, want)
	}
	return conn, nil
}

func DialWithRetry(ctx context.Context, udsPath string, port uint32, policy RetryPolicy) (DialResult, error) {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	if policy.BaseBackoff <= 0 {
		policy.BaseBackoff = 25 * time.Millisecond
	}
	if policy.MaxBackoff < policy.BaseBackoff {
		policy.MaxBackoff = policy.BaseBackoff
	}

	started := time.Now()
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		conn, err := Dial(ctx, udsPath, port)
		if err == nil {
			return DialResult{Conn: conn, Stats: Stats{Attempts: attempt, HandshakeMS: time.Since(started).Milliseconds()}}, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return DialResult{Stats: Stats{Attempts: attempt, HandshakeMS: time.Since(started).Milliseconds()}}, fmt.Errorf("%w: %v", ErrNonRetryableAck, err)
		}
		if attempt == policy.MaxAttempts {
			break
		}
		if err := sleepWithContext(ctx, backoffForAttempt(policy, attempt)); err != nil {
			return DialResult{Stats: Stats{Attempts: attempt, HandshakeMS: time.Since(started).Milliseconds()}}, err
		}
	}
	return DialResult{Stats: Stats{Attempts: policy.MaxAttempts, HandshakeMS: time.Since(started).Milliseconds()}}, fmt.Errorf("%w: attempts=%d last=%v", ErrRetryExhausted, policy.MaxAttempts, lastErr)
}

func sendConnectAndReadAck(conn net.Conn, port uint32) (string, error) {
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return "", err
	}
	ack, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(ack), nil
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConnectAck) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return true
		}
		var errno syscall.Errno
		if errors.As(opErr.Err, &errno) {
			return errno == syscall.ECONNREFUSED || errno == syscall.ENOENT || errno == syscall.EPIPE || errno == syscall.ECONNRESET
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "refused") || strings.Contains(msg, "no such file")
}
func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoffForAttempt(policy RetryPolicy, attempt int) time.Duration {
	step := time.Duration(attempt*attempt) * policy.BaseBackoff
	if step > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return step
}
