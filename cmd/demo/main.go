package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
)

var eventCounter int

func main() {
	fmt.Println("DuckStream Demo Client")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("Prerequisites:")
	fmt.Println("1. Start the server: go run ./cmd/main.go")
	fmt.Println("2. In the server REPL, register a query: REGISTER QUERY q1 AS SELECT * FROM events")
	fmt.Println("3. Run this demo in another terminal: go run ./cmd/demo")
	fmt.Println()
	fmt.Println("This demo will:")
	fmt.Println("  - Continuously ingest events every 5 seconds")
	fmt.Println("  - Connect to QUIC and display streaming results")
	fmt.Println("  - Press Ctrl+C to stop")
	fmt.Println()

	if len(os.Args) > 1 && os.Args[1] == "--help" {
		return
	}

	ctx := context.Background()

	go receiveFromQUIC(ctx)
	go continuousIngest(ctx)

	time.Sleep(100 * time.Millisecond)

	fmt.Println("=== Starting continuous event ingestion (every 5 seconds) ===")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nStopping demo...")
}

func continuousIngest(ctx context.Context) {
	client := &http.Client{}

	events := []string{
		`{"event":"click","x":100,"y":200}`,
		`{"event":"view","page":"/home"}`,
		`{"event":"click","x":150,"y":250}`,
		`{"event":"scroll","position":500}`,
		`{"event":"click","x":300,"y":400}`,
		`{"event":"view","page":"/about"}`,
		`{"event":"click","x":50,"y":100}`,
		`{"event":"scroll","position":1000}`,
		`{"event":"view","page":"/contact"}`,
		`{"event":"click","x":400,"y":300}`,
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		eventCounter++
		e := events[eventCounter%len(events)]
		data := map[string]string{"data": e}
		jsonBytes, _ := json.Marshal(data)
		resp, err := client.Post("http://localhost:8080/ingest", "application/json", nil)
		if err != nil {
			log.Printf("Error: %v", err)
			continue
		}
		fmt.Printf("[%s] Ingested: %s -> %d\n", time.Now().Format("15:04:05"), e, resp.StatusCode)
		resp.Body.Close()
		_ = jsonBytes
	}
}

func receiveFromQUIC(ctx context.Context) {
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"duckstream"},
	}

	conn, err := quic.DialAddr(ctx, "localhost:4242", tlsConf, nil)
	if err != nil {
		log.Printf("QUIC connection failed: %v", err)
		fmt.Println("Make sure the server is running and you have registered a query!")
		return
	}
	defer func() { _ = conn.CloseWithError(0, "") }()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		log.Printf("Failed to open stream: %v", err)
		return
	}
	defer stream.Close()

	fmt.Println("=== Connected to QUIC, waiting for results ===")

	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if err != nil {
			return
		}
		parseMessage(buf[:n])
	}
}

func parseMessage(data []byte) {
	if len(data) < 5 {
		fmt.Printf("Raw: %s\n", string(data))
		return
	}

	msgType := data[0]
	payloadLen := uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	payload := data[5 : 5+payloadLen]

	switch msgType {
	case 0x01:
		var rows []map[string]interface{}
		if err := json.Unmarshal(payload, &rows); err != nil {
			fmt.Printf("Row Batch (raw): %s\n", payload)
			return
		}
		for _, row := range rows {
			fmt.Printf("[%s] Row: %v\n", time.Now().Format("15:04:05"), row)
		}
	case 0x02:
		fmt.Println("Query completed")
	case 0x03:
		fmt.Printf("Error: %s\n", payload)
	case 0x04:
		fmt.Println("Heartbeat")
	default:
		fmt.Printf("Unknown message type: %d, payload: %s\n", msgType, payload)
	}
}
