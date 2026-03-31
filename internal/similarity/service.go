// Package similarity implements similarity and grouping-record endpoints.
package similarity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/vectorstore"
)

// Service handles similarity and grouping record workflows.
type Service struct {
	cfg        *config.Config
	embeddings embedding.Client
	store      vectorstore.Store
}

// New creates a similarity service.
func New(cfg *config.Config, embeddings embedding.Client, store vectorstore.Store) *Service {
	return &Service{cfg: cfg, embeddings: embeddings, store: store}
}

// SimilarRequest matches Sentry's similarity request.
type SimilarRequest struct {
	ProjectID     int64    `json:"project_id"`
	Stacktrace    string   `json:"stacktrace"`
	ExceptionType *string  `json:"exception_type,omitempty"`
	Hash          string   `json:"hash"`
	K             int      `json:"k,omitempty"`
	Threshold     *float64 `json:"threshold,omitempty"`
	TrainingMode  bool     `json:"training_mode,omitempty"`
}

// SimilarResponse matches Seer's similarity response.
type SimilarResponse struct {
	Responses []vectorstore.SimilarIssue `json:"responses"`
	ModelUsed string                     `json:"model_used,omitempty"`
}

// GroupingRecordData stores a single grouping record request item.
type GroupingRecordData struct {
	GroupID       int64   `json:"group_id"`
	Hash          string  `json:"hash"`
	ProjectID     int64   `json:"project_id"`
	ExceptionType *string `json:"exception_type,omitempty"`
}

// GroupingRecordRequest matches Seer's bulk record endpoint.
type GroupingRecordRequest struct {
	Data                      []GroupingRecordData `json:"data"`
	StacktraceList            []string             `json:"stacktrace_list"`
	EncodeStacktraceBatchSize int                  `json:"encode_stacktrace_batch_size,omitempty"`
	Threshold                 *float64             `json:"threshold,omitempty"`
	K                         int                  `json:"k,omitempty"`
}

// BulkCreateResponse matches Seer's bulk record response.
type BulkCreateResponse struct {
	Success            bool                                `json:"success"`
	GroupsWithNeighbor map[string]vectorstore.SimilarIssue `json:"groups_with_neighbor"`
}

// DeleteByHashRequest matches Seer's delete-by-hash request.
type DeleteByHashRequest struct {
	ProjectID int64    `json:"project_id"`
	HashList  []string `json:"hash_list"`
}

// Similar finds similar issues for a stacktrace.
func (s *Service) Similar(ctx context.Context, raw json.RawMessage) (SimilarResponse, error) {
	var request SimilarRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return SimilarResponse{}, fmt.Errorf("decode similar issues request: %w", err)
	}
	if request.K <= 0 {
		request.K = 1
	}
	threshold := s.cfg.SimilarityThreshold
	if request.Threshold != nil {
		threshold = *request.Threshold
	}
	vectors, err := s.embeddings.EmbedTexts(ctx, []string{request.Stacktrace})
	if err != nil {
		return SimilarResponse{}, err
	}
	if request.TrainingMode {
		record := vectorstore.GroupingRecord{ProjectID: request.ProjectID, Hash: request.Hash, ExceptionType: request.ExceptionType, Vector: vectors[0]}
		if err := s.store.UpsertGroupingRecords(ctx, []vectorstore.GroupingRecord{record}); err != nil {
			return SimilarResponse{}, err
		}
		return SimilarResponse{Responses: []vectorstore.SimilarIssue{}, ModelUsed: s.cfg.EmbeddingModel}, nil
	}
	results, err := s.store.SearchSimilar(ctx, request.ProjectID, request.Hash, vectors[0], request.K, threshold)
	if err != nil {
		return SimilarResponse{}, err
	}
	return SimilarResponse{Responses: results, ModelUsed: s.cfg.EmbeddingModel}, nil
}

// CreateGroupingRecords stores grouping vectors and returns nearest-neighbor hints.
func (s *Service) CreateGroupingRecords(ctx context.Context, raw json.RawMessage) (BulkCreateResponse, error) {
	var request GroupingRecordRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return BulkCreateResponse{}, fmt.Errorf("decode grouping record request: %w", err)
	}
	vectors, err := s.embeddings.EmbedTexts(ctx, request.StacktraceList)
	if err != nil {
		return BulkCreateResponse{}, err
	}
	records := make([]vectorstore.GroupingRecord, 0, len(request.Data))
	neighbors := make(map[string]vectorstore.SimilarIssue, len(request.Data))
	threshold := s.cfg.SimilarityThreshold
	if request.Threshold != nil {
		threshold = *request.Threshold
	}
	for idx, item := range request.Data {
		if idx >= len(vectors) {
			break
		}
		matches, err := s.store.SearchSimilar(ctx, item.ProjectID, item.Hash, vectors[idx], max(1, request.K), threshold)
		if err != nil {
			return BulkCreateResponse{}, err
		}
		if len(matches) > 0 {
			neighbors[item.Hash] = matches[0]
		}
		records = append(records, vectorstore.GroupingRecord{ProjectID: item.ProjectID, Hash: item.Hash, ExceptionType: item.ExceptionType, Vector: vectors[idx]})
	}
	if err := s.store.UpsertGroupingRecords(ctx, records); err != nil {
		return BulkCreateResponse{}, err
	}
	return BulkCreateResponse{Success: true, GroupsWithNeighbor: neighbors}, nil
}

// DeleteProject deletes grouping records for a project.
func (s *Service) DeleteProject(ctx context.Context, projectID int64) (map[string]bool, error) {
	success, err := s.store.DeleteProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"success": success}, nil
}

// DeleteByHash deletes grouping records by hash.
func (s *Service) DeleteByHash(ctx context.Context, raw json.RawMessage) (map[string]bool, error) {
	var request DeleteByHashRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode delete-by-hash request: %w", err)
	}
	success, err := s.store.DeleteHashes(ctx, request.ProjectID, request.HashList)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"success": success}, nil
}

// UpsertSupergroup stores a supergroup artifact.
func (s *Service) UpsertSupergroup(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var request struct {
		OrganizationID int64          `json:"organization_id"`
		GroupID        int64          `json:"group_id"`
		ProjectID      int64          `json:"project_id"`
		ArtifactData   map[string]any `json:"artifact_data"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode supergroup embedding request: %w", err)
	}
	if err := s.store.InsertSupergroup(ctx, vectorstore.SupergroupRecord{OrganizationID: request.OrganizationID, GroupID: request.GroupID, ProjectID: request.ProjectID, Artifact: request.ArtifactData}); err != nil {
		return nil, err
	}
	return map[string]any{"success": true}, nil
}

// ListSupergroups returns stored supergroup artifacts.
func (s *Service) ListSupergroups(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var request struct {
		OrganizationID int64   `json:"organization_id"`
		Offset         *int    `json:"offset,omitempty"`
		Limit          *int    `json:"limit,omitempty"`
		ProjectIDs     []int64 `json:"project_ids,omitempty"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode supergroups list request: %w", err)
	}
	offset, limit := 0, 50
	if request.Offset != nil {
		offset = *request.Offset
	}
	if request.Limit != nil {
		limit = *request.Limit
	}
	items, err := s.store.ListSupergroups(ctx, request.OrganizationID, request.ProjectIDs, offset, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"data": items}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
