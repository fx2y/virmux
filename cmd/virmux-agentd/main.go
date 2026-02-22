package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/haris/virmux/internal/agentd/server"
	"github.com/haris/virmux/internal/agentd/tools"
	mdvsock "github.com/mdlayher/vsock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("virmux-agentd", flag.ContinueOnError)
	port := fs.Int("port", 10001, "AF_VSOCK port")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	ln, err := mdvsock.Listen(uint32(*port), nil)
	if err != nil {
		return err
	}
	defer ln.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	return server.New(tools.NewRegistry(&http.Client{})).ServeConn(ctx, conn)
}
