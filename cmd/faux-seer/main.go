// Command faux-seer runs a local Seer-compatible API service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aldy505/faux-seer/internal/assistedquery"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/explorer"
	"github.com/aldy505/faux-seer/internal/feedback"
	"github.com/aldy505/faux-seer/internal/generation"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/handler"
	"github.com/aldy505/faux-seer/internal/investigations"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/monitoring"
	"github.com/aldy505/faux-seer/internal/observability"
	"github.com/aldy505/faux-seer/internal/preferences"
	"github.com/aldy505/faux-seer/internal/replay"
	"github.com/aldy505/faux-seer/internal/runmgr"
	"github.com/aldy505/faux-seer/internal/runs"
	"github.com/aldy505/faux-seer/internal/severity"
	"github.com/aldy505/faux-seer/internal/similarity"
	vectorstorefactory "github.com/aldy505/faux-seer/internal/vectorstorefactory"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	obs, err := observability.Initialize(ctx, cfg)
	if err != nil {
		slog.Error("initialize sentry", "error", err)
		os.Exit(1)
	}
	defer obs.Flush(2 * time.Second)
	logger := obs.Logger

	store, err := db.New(ctx, cfg.DatabasePath)
	if err != nil {
		logger.ErrorContext(ctx, "open database", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
	defer store.Close()

	llmClient, err := llm.New(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "create llm client", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
	embeddingClient, err := embedding.New(cfg)
	if err != nil {
		logger.ErrorContext(ctx, "create embedding client", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
	vectorStore, err := vectorstorefactory.New(ctx, cfg, store)
	if err != nil {
		logger.ErrorContext(ctx, "create vector store", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
	if closer, ok := vectorStore.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	// A provider needs credentials; without them repository-backed endpoints
	// answer with a simulated response instead of failing.
	gitProvider, err := git.NewProvider(cfg, "github")
	if err != nil {
		logger.InfoContext(ctx, "repository provider unavailable; repository-backed endpoints are simulated", "error", err)
		gitProvider = nil
	}

	runManager := runmgr.New(ctx, logger)
	codeIndex := codeindex.New(gitProvider, embeddingClient, vectorStore, cfg.CodeIndexChunkSize, cfg.CodeIndexChunkOverlap)
	runService := runs.New(store, llmClient, runManager)

	server := handler.New(handler.Services{
		Config:         cfg,
		Logger:         logger,
		Store:          store,
		LLM:            llmClient,
		Git:            gitProvider,
		CodeIndex:      codeIndex,
		Runs:           runManager,
		Autofix:        autofix.New(store, llmClient, runManager),
		Explorer:       explorer.New(store, llmClient, codeIndex, runManager),
		Similarity:     similarity.New(cfg, embeddingClient, vectorStore),
		Severity:       severity.New(llmClient),
		IssueSummary:   issuesummary.New(llmClient),
		Feedback:       feedback.New(llmClient),
		Replay:         replay.New(store, llmClient),
		Generation:     generation.New(llmClient, gitProvider, cfg),
		RunStore:       runService,
		Investigations: investigations.New(runService),
		AssistedQuery:  assistedquery.New(runService, codeIndex),
		Preferences:    preferences.New(store),
		Monitoring:     monitoring.New(cfg),
	})

	httpServer := &http.Server{Addr: cfg.Addr, Handler: obs.WrapHTTP(server.Routes())}
	go func() {
		logger.InfoContext(ctx, startupMessage(cfg.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "http server failed", "error", err)
			obs.Flush(2 * time.Second)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Background runs are cancelled before the process exits so an in-flight run
	// persists its terminal "shutdown" state instead of being killed mid-write.
	runManager.CancelAll()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(ctx, "http server shutdown failed", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
	if err := runManager.WaitContext(shutdownCtx); err != nil {
		logger.ErrorContext(ctx, "background runs did not finish", "error", err)
	}
}

// startupMessage renders the single startup log line for the configured bind
// address, expanding an empty host to all interfaces.
func startupMessage(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", addr
	}
	if host == "" {
		host = "0.0.0.0"
	}
	return fmt.Sprintf("Server started on %s:%s", host, port)
}
