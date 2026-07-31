package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// runHealthcheck probes this machine's own /health and maps the result onto an
// exit code, so `samo-server healthcheck` can be a container HEALTHCHECK and a
// systemd ExecStartPost/timer probe without the runtime image needing curl or
// wget (debian:bookworm-slim ships neither).
//
// Exit 0 means the server answered 200. Anything else — connection refused,
// timeout, or the 503 the endpoint returns when Postgres is unreachable — is a
// non-zero exit, which is what lets Docker restart a process that is alive but
// unable to serve.
func runHealthcheck(ctx context.Context, args []string) int {
	addr := healthcheckAddr(args)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: http %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return 1
	}
	return 0
}

// healthcheckAddr resolves what to probe: an explicit argument, else the
// configured listen address with a wildcard host rewritten to loopback (you
// cannot connect to ":6969" or "[::]:6969" as a destination).
func healthcheckAddr(args []string) string {
	raw := ""
	if len(args) > 0 {
		raw = strings.TrimSpace(args[0])
	}
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("SAMO_ADDR"))
	}
	if raw == "" {
		raw = ":6969"
	}

	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		// Bare ":6969" or a bare port number.
		port = strings.TrimPrefix(raw, ":")
		host = ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
