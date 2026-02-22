package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

type Request struct {
	ReqID int64          `json:"req"`
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args,omitempty"`
	Allow []string       `json:"allow,omitempty"`
}

type Response struct {
	ReqID     int64          `json:"req"`
	OK        bool           `json:"ok"`
	RC        int            `json:"rc,omitempty"`
	StdoutRef string         `json:"stdout_ref,omitempty"`
	StderrRef string         `json:"stderr_ref,omitempty"`
	OHHash    string         `json:"ohash,omitempty"`
	DurMS     int64          `json:"dur_ms,omitempty"`
	Error     map[string]any `json:"error,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

var ErrReqIDInUse = errors.New("rpc req_id already inflight")

type Client struct {
	rw      io.ReadWriter
	mu      sync.Mutex
	pending map[int64]chan Response
	stopCh  chan struct{}
	once    sync.Once
	readErr error
}

func NewClient(rw io.ReadWriter) *Client {
	c := &Client{rw: rw, pending: map[int64]chan Response{}, stopCh: make(chan struct{})}
	go c.readLoop()
	return c
}

func (c *Client) Close() error {
	c.once.Do(func() { close(c.stopCh) })
	return nil
}

func (c *Client) Call(ctx context.Context, req Request) (Response, error) {
	ch := make(chan Response, 1)
	c.mu.Lock()
	if _, exists := c.pending[req.ReqID]; exists {
		c.mu.Unlock()
		return Response{}, fmt.Errorf("%w: %d", ErrReqIDInUse, req.ReqID)
	}
	c.pending[req.ReqID] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ReqID)
		c.mu.Unlock()
	}()

	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if err := WriteFrame(c.rw, body); err != nil {
		return Response{}, err
	}

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-c.stopCh:
		if c.readErr != nil {
			return Response{}, c.readErr
		}
		return Response{}, io.EOF
	case res := <-ch:
		return res, nil
	}
}

func (c *Client) readLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}
		payload, err := ReadFrame(c.rw)
		if err != nil {
			c.readErr = err
			c.Close()
			return
		}
		var res Response
		if err := json.Unmarshal(payload, &res); err != nil {
			c.readErr = err
			c.Close()
			return
		}
		c.mu.Lock()
		ch := c.pending[res.ReqID]
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}
