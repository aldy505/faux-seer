// Package vectorstore defines vector search and supergroup storage interfaces.
package vectorstore

import "context"

// SimilarIssue represents a nearest-neighbor search result.
type SimilarIssue struct {
	ParentHash         string  `json:"parent_hash"`
	StacktraceDistance float64 `json:"stacktrace_distance"`
	ShouldGroup        bool    `json:"should_group"`
}

// GroupingRecord stores a project/hash embedding.
type GroupingRecord struct {
	ProjectID     int64
	Hash          string
	ExceptionType *string
	Vector        []float32
}

// SupergroupRecord stores raw supergroup artifact data.
type SupergroupRecord struct {
	OrganizationID int64
	GroupID        int64
	ProjectID      int64
	Artifact       map[string]any
}

// CodeChunk is one indexed unit of repository source text.
type CodeChunk struct {
	OrganizationID int64
	Provider       string
	Owner          string
	Name           string
	Ref            string
	Path           string
	ChunkIndex     int
	StartLine      int
	EndLine        int
	Text           string
	Vector         []float32
}

// CodeResult is a code chunk matched by a semantic search.
type CodeResult struct {
	Provider   string  `json:"provider"`
	Owner      string  `json:"owner"`
	Name       string  `json:"name"`
	Ref        string  `json:"ref"`
	Path       string  `json:"path"`
	ChunkIndex int     `json:"chunk_index"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
	Text       string  `json:"text"`
	Distance   float64 `json:"distance"`
}

// CodeFilters narrows a code search. Empty fields match any value, except
// OrganizationID, which always scopes the search to one organization; two
// organizations can hold an index of the same repository without colliding.
type CodeFilters struct {
	OrganizationID int64
	Provider       string
	Owner          string
	Name           string
	Ref            string
}

// Store persists and queries vectors.
type Store interface {
	UpsertGroupingRecords(context.Context, []GroupingRecord) error
	SearchSimilar(context.Context, int64, string, []float32, int, float64) ([]SimilarIssue, error)
	DeleteProject(context.Context, int64) (bool, error)
	DeleteHashes(context.Context, int64, []string) (bool, error)
	InsertSupergroup(context.Context, SupergroupRecord) error
	ListSupergroups(context.Context, int64, []int64, int, int) ([]map[string]any, error)
	UpsertCodeChunks(context.Context, []CodeChunk) error
	SearchCode(context.Context, []float32, int, CodeFilters) ([]CodeResult, error)
	DeleteCodeRepo(context.Context, int64, string, string, string) (bool, error)
	HasCodeChunks(context.Context, int64) (bool, error)
}
