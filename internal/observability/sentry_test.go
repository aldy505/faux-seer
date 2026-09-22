package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRequestLoggingMiddlewareLogsRequest(t *testing.T) {
	handler := &recordingHandler{}
	logger := slog.New(handler)
	middleware := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/health?verbose=1", nil)
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
	if got := record.Attrs["url"]; got != "/health?verbose=1" {
		t.Fatalf("url = %v, want %q", got, "/health?verbose=1")
	}
	if got := record.Attrs["status"]; got != int64(http.StatusCreated) {
		t.Fatalf("status = %v, want %d", got, http.StatusCreated)
	}
	if _, ok := record.Attrs["remote_addr"]; ok {
		t.Fatalf("remote_addr should not be logged, got %v", record.Attrs["remote_addr"])
	}
	duration, ok := record.Attrs["duration_ms"].(int64)
	if !ok {
		t.Fatalf("duration_ms type = %T, want int64", record.Attrs["duration_ms"])
	}
	if duration < 0 {
		t.Fatalf("duration_ms = %d, want non-negative", duration)
	}
}

func TestRequestLoggingMiddlewareLogsHeadersAndBody(t *testing.T) {
	handler := &recordingHandler{}
	logger := slog.New(handler)
	payload := `{"message":"panic"}`
	var handlerBody string
	middleware := requestLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read handler body: %v", err)
		}
		handlerBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v0/issues/severity-score", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Rpcsignature rpc0:deadbeef")
	resp := httptest.NewRecorder()

	middleware.ServeHTTP(resp, req)

	if handlerBody != payload {
		t.Fatalf("handler body = %q, want %q (body must be restored for downstream handlers)", handlerBody, payload)
	}

	records := handler.Records()
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	record := records[0]
	if got := record.Attrs["body"]; got != payload {
		t.Fatalf("body = %v, want %q", got, payload)
	}
	headers, ok := record.Attrs["headers"].(http.Header)
	if !ok {
		t.Fatalf("headers type = %T, want http.Header", record.Attrs["headers"])
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("logged Content-Type = %q, want %q", got, "application/json")
	}
	if got := headers.Get("Authorization"); got != "Rpcsignature rpc0:deadbeef" {
		t.Fatalf("logged Authorization = %q, want %q", got, "Rpcsignature rpc0:deadbeef")
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
