package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldy505/faux-seer/internal/auth"
)

func TestHealthEndpoint(t *testing.T) {
	server := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected ok status, got %#v", body)
	}
}

func TestProtectedEndpointRequiresValidSignature(t *testing.T) {
	server := newTestServer(t)
	payload := []byte(`{"message":"panic", "has_stacktrace":1}`)
	req := httptest.NewRequest(http.MethodPost, "/v0/issues/severity-score", bytesReader(payload))
	req.Header.Set("Authorization", auth.SignRequestBody(payload, "test-secret"))
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func bytesReader(payload []byte) *bytes.Reader { return bytes.NewReader(payload) }
