package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
)

func main() {
	fmt.Println("DuckStream Demo Client")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("This demo will:")
	fmt.Println("  1. Create a 'lineitem' table in the server's DuckDB")
	fmt.Println("  2. Register a streaming query: SELECT * FROM lineitem")
	fmt.Println("  3. Continuously insert rows every 2 seconds")
	fmt.Println("  4. Display streamed results over QUIC")
	fmt.Println()
	fmt.Println("Run this AFTER starting the server:")
	fmt.Println("  go run ./cmd/main.go")
	fmt.Println()
	fmt.Println("Then in another terminal:")
	fmt.Println("  go run ./cmd/demo")
	fmt.Println()

	ctx := context.Background()

	go receiveFromQUIC(ctx)
	go setupAndInsert(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nStopping demo...")
}

func setupAndInsert(ctx context.Context) {
	httpClient := &http.Client{}

	time.Sleep(500 * time.Millisecond)

	fmt.Println("=== Step 1: Creating 'lineitem' table ===")
	resp, err := httpClient.Post("http://localhost:8080/exec", "text/plain", nil)
	if err != nil {
		log.Printf("Error creating table: %v", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("CREATE TABLE: %s\n", string(body))

	time.Sleep(200 * time.Millisecond)

	execReq, _ := http.NewRequest("POST", "http://localhost:8080/exec", strings.NewReader("CREATE SEQUENCE IF NOT EXISTS lineitem_id_seq"))
	execReq.Header.Set("Content-Type", "text/plain")
	resp, err = httpClient.Do(execReq)
	if err == nil {
		resp.Body.Close()
	}

	execReq2, _ := http.NewRequest("POST", "http://localhost:8080/exec", strings.NewReader("CREATE TABLE IF NOT EXISTS lineitem (id BIGINT DEFAULT (nextval('lineitem_id_seq')), name TEXT, price DECIMAL(10,2), quantity INTEGER, timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP)"))
	execReq2.Header.Set("Content-Type", "text/plain")
	resp, err = httpClient.Do(execReq2)
	if err == nil {
		resp.Body.Close()
	}

	time.Sleep(200 * time.Millisecond)

	fmt.Println("=== Step 2: Registering streaming query ===")
	queryReq, _ := http.NewRequest("POST", "http://localhost:8080/queries", strings.NewReader(`{"id":"lineitem_stream","sql":"SELECT * FROM lineitem ORDER BY id"}`))
	queryReq.Header.Set("Content-Type", "application/json")
	resp, err = httpClient.Do(queryReq)
	if err != nil {
		log.Printf("Error registering query: %v", err)
		return
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("REGISTER QUERY: %s\n", string(body))

	fmt.Println("=== Step 3: Starting continuous inserts (every 2 seconds) ===")
	fmt.Println("Watch for streamed results in the QUIC output above!")
	fmt.Println()

	items := []map[string]interface{}{
		{"name": "Widget", "price": 19.99, "quantity": 100},
		{"name": "Gadget", "price": 29.99, "quantity": 50},
		{"name": "Gizmo", "price": 9.99, "quantity": 200},
		{"name": "Doohickey", "price": 49.99, "quantity": 25},
		{"name": "Thingamajig", "price": 14.99, "quantity": 150},
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	counter := 0
	for range ticker.C {
		counter++
		item := items[counter%len(items)]

		execReq, _ := http.NewRequest("POST", "http://localhost:8080/exec", strings.NewReader(fmt.Sprintf("INSERT INTO lineitem (name, price, quantity) VALUES ('%s', %v, %d)", item["name"], item["price"], item["quantity"])))
		execReq.Header.Set("Content-Type", "text/plain")
		resp, err = httpClient.Do(execReq)
		if err == nil {
			resp.Body.Close()
			fmt.Printf("[%s] INSERTED: %s (price: $%.2f, qty: %d)\n", time.Now().Format("15:04:05"), item["name"], item["price"], item["quantity"])
		}
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
		fmt.Println("Make sure the server is running!")
		return
	}
	defer func() { _ = conn.CloseWithError(0, "") }()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		log.Printf("Failed to open stream: %v", err)
		return
	}
	defer stream.Close()

	fmt.Println("=== QUIC Connected - Waiting for streamed rows ===")
	fmt.Println()

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
			fmt.Printf("[%s] STREAMED: %v\n", time.Now().Format("15:04:05"), row)
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
