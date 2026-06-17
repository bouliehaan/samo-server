package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
)

// listenWithFallback tries the requested address; if the port is taken it
// increments until it finds an open one or exhausts the attempt budget. This
// lets `samo-server` keep its memorable default port (6969) even when one is
// already in use — the user just sees the next number up and can guess where
// it landed.
func listenWithFallback(requested string, attempts int) (net.Listener, error) {
	if attempts <= 0 {
		attempts = 20
	}
	host, port, err := splitAddr(requested)
	if err != nil {
		return nil, err
	}

	// If host is empty (bare :port), try IPv4 explicitly first on dual-stack systems
	if host == "" {
		host = "0.0.0.0"
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		addr := joinAddr(host, port+i)
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		if !isAddressInUse(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no free port within %d attempts starting at %s: %w", attempts, requested, lastErr)
}

func splitAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		// Treat bare ":6969" specially.
		if strings.HasPrefix(addr, ":") {
			parsed, parseErr := strconv.Atoi(strings.TrimPrefix(addr, ":"))
			if parseErr == nil {
				return "", parsed, nil
			}
		}
		return "", 0, fmt.Errorf("parse listen address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("parse listen port %q: %w", portStr, err)
	}
	return host, port, nil
}

func joinAddr(host string, port int) string {
	if host == "" {
		return fmt.Sprintf(":%d", port)
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// normalizedDisplayPort takes a listener address like "[::]:6970" and returns
// ":6970" so log lines can be pasted into a browser without IPv6 escaping.
func normalizedDisplayPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return ":" + port
}

func isAddressInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Some platforms wrap the syscall in additional layers; fall back to a
	// substring check on the error message.
	return strings.Contains(err.Error(), "address already in use") ||
		strings.Contains(err.Error(), "bind: only one usage")
}
