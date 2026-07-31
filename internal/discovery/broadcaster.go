package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bouliehaan/samo-server/internal/log"
	"net"
	"strings"
	"time"
)

const (
	// probeMessage is the exact payload a Samo client broadcasts to find us.
	probeMessage = "Who is SamoServer?"

	// maxDatagram bounds a single read. The probe is a short ASCII string;
	// anything larger is not a probe and gets truncated rather than sized for.
	maxDatagram = 1024

	// readErrorBackoff paces retries after a failed read. Without it a socket
	// stuck in a persistently-failing state spins this loop at full CPU and
	// fills the journal — on an appliance that is a disk-full outage caused by
	// a feature nobody was using.
	readErrorBackoff = 250 * time.Millisecond

	// maxConsecutiveReadErrors is when we stop calling it transient. Returning
	// lets the caller log a single clear line instead of an endless stream, and
	// discovery is optional: the server keeps serving without it.
	maxConsecutiveReadErrors = 20

	// minProbeInterval rate-limits replies per source address. install.sh opens
	// 7360/udp in the firewall and the socket binds the wildcard, so on an
	// internet-exposed host an unthrottled responder is a (small) reflection
	// amplifier — the reply is several times the size of the probe, and the
	// source address of a UDP datagram is trivially forged. Real clients probe
	// every few seconds at most.
	minProbeInterval = time.Second

	// maxTrackedProbers caps the rate-limit table so spoofed source addresses
	// can't grow it without bound.
	maxTrackedProbers = 4096
)

type DiscoveryResponse struct {
	Address string `json:"Address"`
	Id      string `json:"Id"`
	Name    string `json:"Name"`
}

type Broadcaster struct {
	Port       int
	ServerPort int
	ServerName string
	ServerID   string
}

// NewBroadcaster answers LAN discovery probes with this server's address and
// stable identity.
//
// serverID is what makes the reply actionable rather than merely informative: a
// client that already knows a server can compare the advertised ID against its
// own and, on a match, talk to the LAN address instead of routing through a
// remote hostname. An empty serverID still advertises the address, but clients
// cannot safely prefer it.
func NewBroadcaster(serverPort int, serverID string) *Broadcaster {
	return &Broadcaster{
		Port:       7360, // Custom port to avoid Jellyfin
		ServerPort: serverPort,
		ServerName: "Samo Server",
		ServerID:   serverID,
	}
}

func (b *Broadcaster) Run(ctx context.Context) error {
	addr := net.UDPAddr{
		Port: b.Port,
		IP:   net.ParseIP("0.0.0.0"),
	}

	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return fmt.Errorf("failed to bind UDP for discovery: %w", err)
	}
	defer conn.Close()

	log.Infof("discovery broadcaster listening on udp :%d", b.Port)

	// Close the connection when the context is done
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	limiter := newProbeLimiter(minProbeInterval, maxTrackedProbers)
	buffer := make([]byte, maxDatagram)
	consecutiveErrors := 0

	for {
		n, remoteAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveReadErrors {
				return fmt.Errorf("discovery socket failed %d consecutive reads, last error: %w", consecutiveErrors, err)
			}
			log.Warnf("discovery read error (%d/%d): %v", consecutiveErrors, maxConsecutiveReadErrors, err)
			// Back off rather than spinning. The old bare `continue` turned any
			// persistent socket error into a busy loop that pinned a core and
			// flooded the journal.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(readErrorBackoff):
			}
			continue
		}
		consecutiveErrors = 0

		if strings.TrimSpace(string(buffer[:n])) != probeMessage {
			continue
		}
		if remoteAddr == nil || !limiter.allow(remoteAddr.IP.String(), time.Now()) {
			continue
		}

		resp := DiscoveryResponse{
			Address: fmt.Sprintf("http://%s:%d", b.getOutboundIP(remoteAddr.IP), b.ServerPort),
			Id:      b.ServerID,
			Name:    b.ServerName,
		}

		respBytes, err := json.Marshal(resp)
		if err != nil {
			log.Warnf("discovery encode error: %v", err)
			continue
		}
		if _, err := conn.WriteToUDP(respBytes, remoteAddr); err != nil {
			log.Warnf("discovery write error: %v", err)
		}
	}
}

// probeLimiter allows one reply per source address per interval. It is
// deliberately tiny: the goal is to blunt a flood, not to authenticate
// anything — a UDP source address can be forged, so this bounds our own
// outbound volume rather than identifying callers.
type probeLimiter struct {
	interval time.Duration
	maxKeys  int
	lastSeen map[string]time.Time
}

func newProbeLimiter(interval time.Duration, maxKeys int) *probeLimiter {
	return &probeLimiter{
		interval: interval,
		maxKeys:  maxKeys,
		lastSeen: make(map[string]time.Time),
	}
}

// allow reports whether a probe from key may be answered now. The loop is
// single-goroutine, so no locking is needed.
func (l *probeLimiter) allow(key string, now time.Time) bool {
	if last, ok := l.lastSeen[key]; ok && now.Sub(last) < l.interval {
		return false
	}
	if len(l.lastSeen) >= l.maxKeys {
		// Table full: drop entries that are already past their interval. If a
		// spoofing flood keeps it full anyway, reset — losing rate-limit state
		// costs one extra reply per address, whereas unbounded growth costs
		// the whole process.
		for k, seen := range l.lastSeen {
			if now.Sub(seen) >= l.interval {
				delete(l.lastSeen, k)
			}
		}
		if len(l.lastSeen) >= l.maxKeys {
			l.lastSeen = make(map[string]time.Time)
		}
	}
	l.lastSeen[key] = now
	return true
}

func (b *Broadcaster) getOutboundIP(remoteIP net.IP) string {
	conn, err := net.Dial("udp", fmt.Sprintf("%s:80", remoteIP.String()))
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
