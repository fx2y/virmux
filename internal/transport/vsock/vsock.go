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
	ack, err := sendConnectAndReadAck(ctx, conn, port)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.HasPrefix(ack, "OK ") {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: got=%q want_prefix=%q", ErrConnectAck, ack, "OK ")
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
		attemptCtx, cancel := dialAttemptContext(ctx)
		conn, err := Dial(attemptCtx, udsPath, port)
		cancel()
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

func dialAttemptContext(parent context.Context) (context.Context, context.CancelFunc) {
	const maxAttempt = 2 * time.Second
	if err := parent.Err(); err != nil {
		return parent, func() {}
	}
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return parent, func() {}
		}
		if remaining < maxAttempt {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, maxAttempt)
}

func sendConnectAndReadAck(ctx context.Context, conn net.Conn, port uint32) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return "", err
	}
	type ackResult struct {
		ack string
		err error
	}
	done := make(chan ackResult, 1)
	go func() {
		ack, err := bufio.NewReader(conn).ReadString('\n')
		done <- ackResult{ack: ack, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			return "", res.err
		}
		return strings.TrimSpace(res.ack), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConnectAck) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
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
