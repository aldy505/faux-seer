package explorer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

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
}

func mustMarshal(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
