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

// Store persists and queries vectors.
type Store interface {
	UpsertGroupingRecords(context.Context, []GroupingRecord) error
	SearchSimilar(context.Context, int64, string, []float32, int, float64) ([]SimilarIssue, error)
	DeleteProject(context.Context, int64) (bool, error)
	DeleteHashes(context.Context, int64, []string) (bool, error)
	InsertSupergroup(context.Context, SupergroupRecord) error
	ListSupergroups(context.Context, int64, []int64, int, int) ([]map[string]any, error)
}
