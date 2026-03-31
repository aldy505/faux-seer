package sqlitevec

import (
	"context"
	"testing"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/vectorstore"
)

func TestStoreUpsertAndSearchRoundTrip(t *testing.T) {
	storeDB, err := db.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = storeDB.Close() })

	store, err := New(context.Background(), storeDB, 2)
	if err != nil {
		t.Fatalf("create sqlitevec store: %v", err)
	}
	if err := store.UpsertGroupingRecords(context.Background(), []vectorstore.GroupingRecord{
		{ProjectID: 1, Hash: "hash-a", Vector: []float32{1, 0}},
		{ProjectID: 1, Hash: "hash-b", Vector: []float32{0, 1}},
	}); err != nil {
		t.Fatalf("upsert grouping records: %v", err)
	}

	results, err := store.SearchSimilar(context.Background(), 1, "hash-query", []float32{1, 0}, 1, 0.1)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].ParentHash != "hash-a" {
		t.Fatalf("expected hash-a, got %#v", results[0])
	}
}
