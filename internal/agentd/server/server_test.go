package server

import (
	"context"
	"io"
	"testing"

	"github.com/haris/virmux/internal/agentd/tools"
)

type eofReadWriter struct {
	io.Reader
	io.Writer
}

func TestServeConnEOFIsCleanClose(t *testing.T) {
	t.Parallel()
	rw := eofReadWriter{
		Reader: io.LimitReader(&zeroReader{}, 0),
		Writer: io.Discard,
	}
	err := New(tools.NewRegistry(nil)).ServeConn(context.Background(), rw)
	if err != nil {
		t.Fatalf("expected EOF to be treated as clean close, got %v", err)
	}
}

type zeroReader struct{}

func (*zeroReader) Read(_ []byte) (int, error) { return 0, io.EOF }
