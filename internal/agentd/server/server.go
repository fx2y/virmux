package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/haris/virmux/internal/agentd/proto"
	"github.com/haris/virmux/internal/agentd/tools"
	trpc "github.com/haris/virmux/internal/transport/rpc"
)

type Server struct{ reg tools.Registry }

func New(reg tools.Registry) *Server { return &Server{reg: reg} }

func (s *Server) ServeConn(ctx context.Context, rw io.ReadWriter) error {
	caps := append([]string(nil), s.reg.Caps()...)
	sort.Strings(caps)
	if _, err := fmt.Fprintf(rw, "READY v0 tools=%s\n", strings.Join(caps, ",")); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		frame, err := trpc.ReadFrame(rw)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		var req proto.Request
		if err := json.Unmarshal(frame, &req); err != nil {
			return err
		}
		allow := map[string]struct{}{}
		for _, name := range req.Allow {
			allow[name] = struct{}{}
		}
		res := s.reg.Handle(ctx, tools.Call{ReqID: req.ReqID, Tool: req.Tool, Args: req.Args, Allow: allow, Base: "/dev/virmux-data"})
		body, err := json.Marshal(res)
		if err != nil {
			return err
		}
		if err := trpc.WriteFrame(rw, body); err != nil {
			return err
		}
	}
}
