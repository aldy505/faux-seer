package pgvector

import (
	"context"
	"strings"
	"testing"
)

func TestNewRejectsInvalidDSN(t *testing.T) {
	_, err := New(context.Background(), "not a valid dsn", 8)
	if err == nil {
		t.Fatal("expected invalid DSN error")
	}
	if !strings.Contains(err.Error(), "parse pgvector DSN") {
		t.Fatalf("expected parse error, got %v", err)
	}
}
