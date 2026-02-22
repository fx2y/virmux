package rpc

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestVsockMuxInterleavedResponsesPreserveReqID(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := NewClient(clientConn)
	defer client.Close()

	go func() {
		defer serverConn.Close()
		reqs := make([]Request, 0, 2)
		for i := 0; i < 2; i++ {
			payload, err := ReadFrame(serverConn)
			if err != nil {
				return
			}
			var req Request
			if err := json.Unmarshal(payload, &req); err != nil {
				return
			}
			reqs = append(reqs, req)
		}
		if len(reqs) != 2 {
			return
		}
		var first, second Request
		if reqs[0].ReqID == 1 {
			first, second = reqs[1], reqs[0]
		} else {
			first, second = reqs[0], reqs[1]
		}
		for _, req := range []Request{first, second} {
			res := Response{ReqID: req.ReqID, OK: true}
			out, _ := json.Marshal(res)
			_ = WriteFrame(serverConn, out)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		_, err := client.Call(ctx, Request{ReqID: 1, Tool: "shell.exec"})
		errCh <- err
	}()
	go func() {
		_, err := client.Call(ctx, Request{ReqID: 2, Tool: "shell.exec"})
		errCh <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("call failed: %v", err)
		}
	}
}
