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
// ModelUsed reports the primary configured embedding model, not necessarily the
// model that served a given request, because embedding calls load-balance.
type SimilarResponse struct {
	Responses []vectorstore.SimilarIssue `json:"responses"`
	ModelUsed string                     `json:"model_used,omitempty"`
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
		return SimilarResponse{Responses: []vectorstore.SimilarIssue{}, ModelUsed: s.primaryEmbeddingModel()}, nil
	}
	results, err := s.store.SearchSimilar(ctx, request.ProjectID, request.Hash, vectors[0], request.K, threshold)
	if err != nil {
		return SimilarResponse{}, err
	}
	// Sentry iterates this list directly, so it must serialize as [] rather
	// than null when the store reports no matches.
	if results == nil {
		results = []vectorstore.SimilarIssue{}
	}
	return SimilarResponse{Responses: results, ModelUsed: s.primaryEmbeddingModel()}, nil
}

// primaryEmbeddingModel returns the first configured embedding model name.
func (s *Service) primaryEmbeddingModel() string {
	if len(s.cfg.EmbeddingModel) == 0 {
		return ""
	}
	return s.cfg.EmbeddingModel[0]
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
	// Sentry iterates this list directly, so an empty result must serialize as
	// [] rather than null.
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"data": items}, nil
}
