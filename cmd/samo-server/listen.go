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
	// A bare ":port" (empty host) binds the dual-stack wildcard as-is. Don't
	// rewrite it to "0.0.0.0": Go treats any wildcard the same for "tcp"
	// listens (still a dual-stack [::] socket on Linux), so the rewrite buys
	// nothing and just misleads readers into expecting an IPv4-only bind.
	host, port, err := splitAddr(requested)
	if err != nil {
		return nil, err
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

// sameListenPort reports whether two listen addresses share a port. The
// kernel reports a bare ":6969" bind back as "[::]:6969", so comparing raw
// address strings misreads a successful first-try bind as a port fallback.
func sameListenPort(a, b string) bool {
	_, portA, errA := splitAddr(a)
	_, portB, errB := splitAddr(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return portA == portB
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

// lanURLs are the addresses another machine on the network could open: one per
// IPv4 address on an interface that is up, not loopback, not link-local, and
// not a container or VM bridge (docker0's 172.17.0.1 means nothing from a
// laptop). Falls back to localhost on a box with no network yet.
func lanURLs(port, path string) []string {
	var urls []string
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || virtualInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			urls = append(urls, "http://"+ipnet.IP.To4().String()+port+path)
		}
	}
	if len(urls) == 0 {
		return []string{"http://localhost" + port + path}
	}
	return urls
}

func virtualInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "podman", "lxc", "lxd", "flannel", "cali", "vxlan"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
