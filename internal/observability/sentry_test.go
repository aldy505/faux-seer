package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRequestLoggingMiddlewareLogsRequest(t *testing.T) {
	handler := &recordingHandler{}
	logger := slog.New(handler)
	middleware := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	req.RemoteAddr = "203.0.113.5:4321"
	resp := httptest.NewRecorder()

	middleware.ServeHTTP(resp, req)

	if got := resp.Code; got != http.StatusCreated {
		t.Fatalf("response status = %d, want %d", got, http.StatusCreated)
	}

	records := handler.Records()
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}

	record := records[0]
	if record.Message != "http request" {
		t.Fatalf("record message = %q, want %q", record.Message, "http request")
	}
	if got := record.Attrs["method"]; got != http.MethodPost {
		t.Fatalf("method = %v, want %q", got, http.MethodPost)
	}
	if got := record.Attrs["path"]; got != "/health" {
		t.Fatalf("path = %v, want %q", got, "/health")
	}
	if got := record.Attrs["status"]; got != int64(http.StatusCreated) {
		t.Fatalf("status = %v, want %d", got, http.StatusCreated)
	}
	if got := record.Attrs["remote_addr"]; got != "203.0.113.5" {
		t.Fatalf("remote_addr = %v, want %q", got, "203.0.113.5")
	}
	duration, ok := record.Attrs["duration_ms"].(int64)
	if !ok {
		t.Fatalf("duration_ms type = %T, want int64", record.Attrs["duration_ms"])
	}
	if duration < 0 {
		t.Fatalf("duration_ms = %d, want non-negative", duration)
	}
}

type capturedRecord struct {
	Message string
	Attrs   map[string]any
}

type recordingHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, capturedRecord{
		Message: record.Message,
		Attrs:   attrs,
	})
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(_ string) slog.Handler { return h }

func (h *recordingHandler) Records() []capturedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]capturedRecord, len(h.records))
	copy(out, h.records)
	return out
}
