package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
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

func NewBroadcaster(serverPort int) *Broadcaster {
	return &Broadcaster{
		Port:       7360, // Custom port to avoid Jellyfin
		ServerPort: serverPort,
		ServerName: "Samo Server",
		ServerID:   "samo-server", // You could fetch a real ID from DB if needed
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

	log.Printf("discovery broadcaster listening on udp :%d", b.Port)

	// Close the connection when the context is done
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buffer := make([]byte, 1024)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("discovery read error: %v", err)
				continue
			}
		}

		message := strings.TrimSpace(string(buffer[:n]))
		if message == "Who is SamoServer?" {
			localIP := b.getOutboundIP(remoteAddr.IP)

			resp := DiscoveryResponse{
				Address: fmt.Sprintf("http://%s:%d", localIP, b.ServerPort),
				Id:      b.ServerID,
				Name:    b.ServerName,
			}

			respBytes, err := json.Marshal(resp)
			if err != nil {
				log.Printf("discovery encode error: %v", err)
				continue
			}

			_, err = conn.WriteToUDP(respBytes, remoteAddr)
			if err != nil {
				log.Printf("discovery write error: %v", err)
			}
		}
	}
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
