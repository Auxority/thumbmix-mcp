// thumbmix-mcp is an MCP server (stdio) that lets Claude Code read and change a Midas M32 over OSC/UDP.
// A companion to Thumbmix, the iPhone remote for the M32. stdout carries MCP only; logs go to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	host := flag.String("host", "", "IP address or name of the desk (required)")
	port := flag.Int("port", 10023, "the desk's OSC/UDP port")
	timeout := flag.Duration("timeout", time.Second, "how long to wait for the desk before refusing a write")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: thumbmix-mcp --host <desk IP> [--port 10023] [--timeout 1s]\n\n"+
			"MCP server over stdio for a Midas M32. Add it to Claude Code with:\n"+
			"  claude mcp add --transport stdio thumbmix -- /path/to/thumbmix-mcp --host <desk IP>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *host == "" {
		flag.Usage()
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	desk, err := Dial(net.JoinHostPort(*host, strconv.Itoa(*port)), *timeout)
	if err != nil {
		slog.Error("cannot open UDP socket", "err", err)
		os.Exit(1)
	}
	model, err := desk.Model()
	switch {
	case err == nil:
		slog.Info("desk found", "model", model)
	case errors.Is(err, errSilent):
		// The desk may be switched on later; every tool call checks again and refuses while it is silent.
		slog.Warn("desk did not answer /xinfo; tools will refuse until it does", "host", *host)
	default:
		slog.Error("refusing to talk to this device", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := newMCP(desk).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
