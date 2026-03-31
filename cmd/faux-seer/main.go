// Command faux-seer runs a local Seer-compatible API service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/handler"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/observability"
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

	autofixService := autofix.New(store, llmClient)
	similarityService := similarity.New(cfg, embeddingClient, vectorStore)
	severityService := severity.New(llmClient)
	issueSummaryService := issuesummary.New(llmClient)
	server := handler.New(cfg, logger, store, autofixService, similarityService, severityService, issueSummaryService)

	httpServer := &http.Server{Addr: cfg.Addr, Handler: obs.WrapHTTP(server.Routes())}
	go func() {
		logger.InfoContext(ctx, "starting faux-seer", "addr", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "http server failed", "error", err)
			obs.Flush(2 * time.Second)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(ctx, "http server shutdown failed", "error", err)
		obs.Flush(2 * time.Second)
		os.Exit(1)
	}
}
