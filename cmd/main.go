package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/duckstream/duckstream/internal/config"
	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/httpapi"
	"github.com/duckstream/duckstream/internal/query"
	"github.com/duckstream/duckstream/internal/quic"
	"github.com/duckstream/duckstream/internal/repl"
)

type sender struct {
	server *quic.Server
}

func (s *sender) SendToQuery(queryID string, data []byte) error {
	sessions := s.server.GetSessions()
	for _, session := range sessions {
		if err := session.SendToQuery(queryID, data); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no sessions available")
}

func main() {
	cfg := config.Default()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := duckdb.NewClient(cfg.DuckDBPath)
	if err != nil {
		log.Fatalf("Failed to connect to DuckDB: %v", err)
	}
	defer client.Close()

	quicServer := quic.NewServer(cfg.QUICAddr, cfg)
	sender := &sender{server: quicServer}

	manager := query.NewManager(client, sender, cfg.MaxQueries)

	go func() {
		if err := quicServer.Start(ctx); err != nil {
			log.Printf("QUIC server error: %v", err)
		}
	}()

	ingestHandler := duckdb.NewIngestHandler(client, cfg)
	apiHandler := httpapi.NewHandler(manager)

	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", ingestHandler.Handle)
	apiHandler.RegisterRoutes(mux)

	go func() {
		log.Printf("HTTP server listening on %s", cfg.IngestAddr)
		if err := http.ListenAndServe(cfg.IngestAddr, mux); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
		ingestHandler.Flush()
		quicServer.Close()
		client.Close()
		os.Exit(0)
	}()

	_ = repl.NewREPL(ctx, manager).Run()
}
