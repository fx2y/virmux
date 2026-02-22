package rpc

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()
	buf := new(bytes.Buffer)
	in := []byte(`{"req":7,"ok":true}`)
	if err := WriteFrame(buf, in); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	out, err := ReadFrame(buf)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("payload mismatch got=%q want=%q", string(out), string(in))
	}
}

func TestFrameRejectsOversize(t *testing.T) {
	t.Parallel()
	buf := new(bytes.Buffer)
	tooBig := bytes.Repeat([]byte{'a'}, int(MaxFrameBytes)+1)
	err := WriteFrame(buf, tooBig)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}
