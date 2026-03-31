// Package testutil provides lightweight mocks for internal tests.
package testutil

import (
	"context"
	"sync"

	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/vectorstore"
)

// MockLLMClient implements llm.Client for tests.
type MockLLMClient struct {
	CompleteFunc func(context.Context, llm.CompletionRequest) (string, error)
	Requests     []llm.CompletionRequest
	mu           sync.Mutex
}

// Complete records the request and returns the configured response.
func (m *MockLLMClient) Complete(ctx context.Context, req llm.CompletionRequest) (string, error) {
	m.mu.Lock()
	m.Requests = append(m.Requests, req)
	m.mu.Unlock()
	if m.CompleteFunc != nil {
		return m.CompleteFunc(ctx, req)
	}
	return "", nil
}

var _ llm.Client = (*MockLLMClient)(nil)

// MockEmbeddingClient implements embedding.Client for tests.
type MockEmbeddingClient struct {
	Dimensions int
	EmbedFunc  func(context.Context, []string) ([][]float32, error)
	Requests   [][]string
	mu         sync.Mutex
}

// Complete returns configured embeddings or deterministic zero vectors by default.
func (m *MockEmbeddingClient) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	m.mu.Lock()
	copied := append([]string(nil), texts...)
	m.Requests = append(m.Requests, copied)
	m.mu.Unlock()
	if m.EmbedFunc != nil {
		return m.EmbedFunc(ctx, texts)
	}
	dimensions := m.Dimensions
	if dimensions <= 0 {
		dimensions = 8
	}
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = make([]float32, dimensions)
	}
	return vectors, nil
}

var _ embedding.Client = (*MockEmbeddingClient)(nil)

// MockVectorStore implements vectorstore.Store for tests.
type MockVectorStore struct {
	UpsertGroupingRecordsFunc func(context.Context, []vectorstore.GroupingRecord) error
	SearchSimilarFunc         func(context.Context, int64, string, []float32, int, float64) ([]vectorstore.SimilarIssue, error)
	DeleteProjectFunc         func(context.Context, int64) (bool, error)
	DeleteHashesFunc          func(context.Context, int64, []string) (bool, error)
	InsertSupergroupFunc      func(context.Context, vectorstore.SupergroupRecord) error
	ListSupergroupsFunc       func(context.Context, int64, []int64, int, int) ([]map[string]any, error)

	GroupingRecords []vectorstore.GroupingRecord
	SearchCalls     []SearchCall
	Supergroups     []vectorstore.SupergroupRecord
	mu              sync.Mutex
}

// SearchCall records a search invocation.
type SearchCall struct {
	ProjectID int64
	Hash      string
	Vector    []float32
	K         int
	Threshold float64
}

// UpsertGroupingRecords stores records in memory unless overridden.
func (m *MockVectorStore) UpsertGroupingRecords(ctx context.Context, records []vectorstore.GroupingRecord) error {
	if m.UpsertGroupingRecordsFunc != nil {
		return m.UpsertGroupingRecordsFunc(ctx, records)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GroupingRecords = append(m.GroupingRecords, records...)
	return nil
}

// SearchSimilar records the call and returns configured results.
func (m *MockVectorStore) SearchSimilar(ctx context.Context, projectID int64, hash string, vector []float32, k int, threshold float64) ([]vectorstore.SimilarIssue, error) {
	if m.SearchSimilarFunc != nil {
		return m.SearchSimilarFunc(ctx, projectID, hash, vector, k, threshold)
	}
	m.mu.Lock()
	m.SearchCalls = append(m.SearchCalls, SearchCall{
		ProjectID: projectID,
		Hash:      hash,
		Vector:    append([]float32(nil), vector...),
		K:         k,
		Threshold: threshold,
	})
	m.mu.Unlock()
	if len(m.GroupingRecords) == 0 {
		return nil, nil
	}
	return []vectorstore.SimilarIssue{{
		ParentHash:         m.GroupingRecords[0].Hash,
		StacktraceDistance: 0,
		ShouldGroup:        true,
	}}, nil
}

// DeleteProject deletes records by project unless overridden.
func (m *MockVectorStore) DeleteProject(ctx context.Context, projectID int64) (bool, error) {
	if m.DeleteProjectFunc != nil {
		return m.DeleteProjectFunc(ctx, projectID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.GroupingRecords[:0]
	for _, record := range m.GroupingRecords {
		if record.ProjectID != projectID {
			filtered = append(filtered, record)
		}
	}
	m.GroupingRecords = filtered
	return true, nil
}

// DeleteHashes deletes project/hash records unless overridden.
func (m *MockVectorStore) DeleteHashes(ctx context.Context, projectID int64, hashes []string) (bool, error) {
	if m.DeleteHashesFunc != nil {
		return m.DeleteHashesFunc(ctx, projectID, hashes)
	}
	hashSet := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		hashSet[hash] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.GroupingRecords[:0]
	for _, record := range m.GroupingRecords {
		if record.ProjectID == projectID {
			if _, ok := hashSet[record.Hash]; ok {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	m.GroupingRecords = filtered
	return true, nil
}

// InsertSupergroup stores a supergroup in memory unless overridden.
func (m *MockVectorStore) InsertSupergroup(ctx context.Context, record vectorstore.SupergroupRecord) error {
	if m.InsertSupergroupFunc != nil {
		return m.InsertSupergroupFunc(ctx, record)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Supergroups = append(m.Supergroups, record)
	return nil
}

// ListSupergroups returns the stored artifacts unless overridden.
func (m *MockVectorStore) ListSupergroups(ctx context.Context, organizationID int64, projectIDs []int64, offset, limit int) ([]map[string]any, error) {
	if m.ListSupergroupsFunc != nil {
		return m.ListSupergroupsFunc(ctx, organizationID, projectIDs, offset, limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]map[string]any, 0, len(m.Supergroups))
	for _, record := range m.Supergroups {
		if record.OrganizationID != organizationID {
			continue
		}
		if len(projectIDs) > 0 {
			matched := false
			for _, projectID := range projectIDs {
				if projectID == record.ProjectID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		items = append(items, record.Artifact)
	}
	if offset > len(items) {
		return []map[string]any{}, nil
	}
	items = items[offset:]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

var _ vectorstore.Store = (*MockVectorStore)(nil)
