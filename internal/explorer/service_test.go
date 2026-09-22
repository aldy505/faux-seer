package explorer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/runmgr"
)

// blockingLLM waits for cancellation before answering, so a test can drive the
// shutdown path deterministically.
type blockingLLM struct{ started chan struct{} }

func (b blockingLLM) Complete(ctx context.Context, _ llm.CompletionRequest) (string, error) {
	if b.started != nil {
		close(b.started)
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func TestChatRunRecordsShutdownWhenCancelled(t *testing.T) {
	ctx := context.Background()
	store, err := db.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	started := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := runmgr.New(ctx, logger)
	service := New(store, blockingLLM{started: started}, nil, manager)

	response, err := service.Chat(ctx, json.RawMessage(`{"organization_id":1,"query":"why is checkout slow?"}`))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	// Wait until the background reply generation is actually in flight, then
	// shut the process down the way main does.
	<-started
	manager.CancelAll()
	manager.Wait()

	state, err := service.GetState(ctx, mustMarshal(map[string]any{"organization_id": 1, "run_id": response.RunID}))
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Session == nil {
		t.Fatal("expected the run to remain readable after shutdown")
	}
	if state.Session.Status != db.RunStatusError {
		t.Fatalf("expected status %q, got %q", db.RunStatusError, state.Session.Status)
	}
	if state.Session.FailureReason == nil || *state.Session.FailureReason != "shutdown" {
		t.Fatalf("expected failure_reason %q, got %#v", "shutdown", state.Session.FailureReason)
	}
	if len(state.Session.Blocks) != 1 {
		t.Fatalf("expected only the user block to be persisted, got %#v", state.Session.Blocks)
	}
	if loading := loadingBlocks(state.Session.Blocks); len(loading) != 0 {
		t.Fatalf("expected no block to still be loading, got %#v", loading)
	}
}

// controlledLLM blocks until the test releases it, so a test can observe the run
// while its reply is still in flight.
type controlledLLM struct {
	started chan struct{}
	release chan struct{}
	reply   string
}

func (c controlledLLM) Complete(ctx context.Context, _ llm.CompletionRequest) (string, error) {
	close(c.started)
	select {
	case <-c.release:
		return c.reply, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// failingLLM answers every request with a fixed error.
type failingLLM struct{ err error }

func (f failingLLM) Complete(context.Context, llm.CompletionRequest) (string, error) {
	return "", f.err
}

// TestChatRunCarriesLoadingBlockUntilTheReplyLands covers what Sentry polls for:
// a run in flight has to show a pending assistant block, and the reply has to
// fill that block in rather than arrive as a second one.
func TestChatRunCarriesLoadingBlockUntilTheReplyLands(t *testing.T) {
	ctx := context.Background()
	store, err := db.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	started := make(chan struct{})
	release := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(store, controlledLLM{started: started, release: release, reply: "checkout.go handles refunds"}, nil, runmgr.New(ctx, logger))

	response, err := service.Chat(ctx, json.RawMessage(`{"organization_id":1,"query":"why is checkout slow?"}`))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	<-started

	pending := sessionFor(t, service, ctx, response.RunID)
	if pending.Status != db.RunStatusProcessing {
		t.Fatalf("expected status %q while the reply is in flight, got %q", db.RunStatusProcessing, pending.Status)
	}
	if len(pending.Blocks) != 2 {
		t.Fatalf("expected the user block and a pending assistant block, got %#v", pending.Blocks)
	}
	placeholder := pending.Blocks[1]
	if !placeholder.Loading || placeholder.Message.Role != "assistant" || placeholder.Message.Content != "" {
		t.Fatalf("expected an empty loading assistant block, got %#v", placeholder)
	}

	close(release)
	done := waitForTerminal(t, service, ctx, response.RunID)
	if done.Status != db.RunStatusCompleted {
		t.Fatalf("expected status %q, got %q (%#v)", db.RunStatusCompleted, done.Status, done.FailureReason)
	}
	if len(done.Blocks) != 2 {
		t.Fatalf("expected the reply to replace the pending block, got %#v", done.Blocks)
	}
	if done.Blocks[1].Loading || done.Blocks[1].Message.Content != "checkout.go handles refunds" {
		t.Fatalf("expected the pending block to be filled in, got %#v", done.Blocks[1])
	}
	if done.Blocks[1].ID != placeholder.ID {
		t.Fatalf("expected block %q to be reused, got %q", placeholder.ID, done.Blocks[1].ID)
	}
}

// TestChatRunReportsTimeoutWhenProviderDoesNotAnswer covers the failure a
// bounded outbound call produces, reported with the reason Sentry classifies.
func TestChatRunReportsTimeoutWhenProviderDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	store, err := db.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	providerErr := fmt.Errorf("call chat completion API: %w", context.DeadlineExceeded)
	service := New(store, failingLLM{err: providerErr}, nil, runmgr.New(ctx, logger))

	response, err := service.Chat(ctx, json.RawMessage(`{"organization_id":1,"query":"why is checkout slow?"}`))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	state := waitForTerminal(t, service, ctx, response.RunID)
	if state.Status != db.RunStatusError {
		t.Fatalf("expected status %q, got %q", db.RunStatusError, state.Status)
	}
	if state.FailureReason == nil || *state.FailureReason != "timeout" {
		t.Fatalf("expected failure_reason %q, got %#v", "timeout", state.FailureReason)
	}
	if loading := loadingBlocks(state.Blocks); len(loading) != 0 {
		t.Fatalf("expected no block to still be loading, got %#v", loading)
	}
}

// sessionFor reads a run's state the way the state endpoint does.
func sessionFor(t *testing.T, service *Service, ctx context.Context, runID int64) *RunState {
	t.Helper()
	state, err := service.GetState(ctx, mustMarshal(map[string]any{"organization_id": 1, "run_id": runID}))
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Session == nil {
		t.Fatalf("expected a session for run %d", runID)
	}
	return state.Session
}

// waitForTerminal polls a run until it leaves "processing", which is what
// Sentry's poller does.
func waitForTerminal(t *testing.T, service *Service, ctx context.Context, runID int64) *RunState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := sessionFor(t, service, ctx, runID)
		if state.Status != db.RunStatusProcessing {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d stayed in %q", runID, state.Status)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// loadingBlocks returns the blocks a client would still render as pending.
func loadingBlocks(blocks []MemoryBlock) []MemoryBlock {
	var loading []MemoryBlock
	for _, block := range blocks {
		if block.Loading {
			loading = append(loading, block)
		}
	}
	return loading
}

func mustMarshal(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
