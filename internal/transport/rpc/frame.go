package rpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const MaxFrameBytes uint32 = 1 << 20

var ErrFrameTooLarge = errors.New("rpc frame exceeds max bytes")

func WriteFrame(w io.Writer, payload []byte) error {
	if uint32(len(payload)) > MaxFrameBytes {
		return fmt.Errorf("%w: len=%d max=%d", ErrFrameTooLarge, len(payload), MaxFrameBytes)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	if n > MaxFrameBytes {
		return nil, fmt.Errorf("%w: len=%d max=%d", ErrFrameTooLarge, n, MaxFrameBytes)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
