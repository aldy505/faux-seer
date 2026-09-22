// Package preferences maintains Seer's per-organization repository preferences.
//
// Sentry only ever asks Seer to drop repositories or integration handoffs, so
// the stored preference is a list of repositories the organization has told Seer
// it should no longer consider.
package preferences

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aldy505/faux-seer/internal/db"
)

// Service maintains project preferences.
type Service struct {
	store *db.Store
}

// New creates a preferences service.
func New(store *db.Store) *Service { return &Service{store: store} }

// Repository identifies a repository in preference requests.
type Repository struct {
	ProjectIDs    []int64 `json:"project_ids,omitempty"`
	Provider      string  `json:"repo_provider"`
	ExternalID    string  `json:"repo_external_id"`
	Owner         string  `json:"owner,omitempty"`
	Name          string  `json:"name,omitempty"`
	IntegrationID *int64  `json:"integration_id,omitempty"`
}

type removeRepositoryRequest struct {
	OrganizationID int64  `json:"organization_id"`
	RepoProvider   string `json:"repo_provider"`
	RepoExternalID string `json:"repo_external_id"`
}

type bulkRemoveRequest struct {
	OrganizationID int64        `json:"organization_id"`
	Repositories   []Repository `json:"repositories"`
}

type removeHandoffsRequest struct {
	OrganizationID int64 `json:"organization_id"`
	IntegrationID  int64 `json:"integration_id"`
}

// RemovedSet returns the repositories the organization has told Seer to drop,
// keyed by "provider\x00external_id". Indexing consults it so a repository that
// was removed from Seer is not indexed again.
func (s *Service) RemovedSet(ctx context.Context, organizationID int64) (map[string]struct{}, error) {
	if organizationID == 0 {
		return map[string]struct{}{}, nil
	}
	record, err := s.store.GetProjectPreference(ctx, organizationID)
	if err != nil || record == nil {
		return map[string]struct{}{}, err
	}
	var repositories []Repository
	if err := json.Unmarshal(record.ReposJSON, &repositories); err != nil {
		return nil, fmt.Errorf("decode stored repositories: %w", err)
	}
	removed := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		removed[repositoryKey(repository.Provider, repository.ExternalID)] = struct{}{}
	}
	return removed, nil
}

// repositoryKey identifies a repository by the pair Sentry removes it by.
func repositoryKey(provider, externalID string) string {
	return provider + "\x00" + externalID
}

// RemoveRepository drops one repository from the stored preference.
func (s *Service) RemoveRepository(ctx context.Context, raw json.RawMessage) error {
	var request removeRepositoryRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode remove repository request: %w", err)
	}
	if request.OrganizationID == 0 {
		return fmt.Errorf("organization_id is required")
	}
	return s.filter(ctx, request.OrganizationID, func(repository Repository) bool {
		return repository.Provider == request.RepoProvider && repository.ExternalID == request.RepoExternalID
	})
}

// BulkRemoveRepositories drops several repositories from the stored preference.
func (s *Service) BulkRemoveRepositories(ctx context.Context, raw json.RawMessage) error {
	var request bulkRemoveRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode bulk remove repositories request: %w", err)
	}
	if request.OrganizationID == 0 {
		return fmt.Errorf("organization_id is required")
	}
	removed := make(map[string]struct{}, len(request.Repositories))
	for _, repository := range request.Repositories {
		removed[repositoryKey(repository.Provider, repository.ExternalID)] = struct{}{}
	}
	return s.filter(ctx, request.OrganizationID, func(repository Repository) bool {
		_, drop := removed[repositoryKey(repository.Provider, repository.ExternalID)]
		return drop
	})
}

// RemoveHandoffsForIntegration clears the stored integration handoff.
func (s *Service) RemoveHandoffsForIntegration(ctx context.Context, raw json.RawMessage) error {
	var request removeHandoffsRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode remove handoffs request: %w", err)
	}
	if request.OrganizationID == 0 {
		return fmt.Errorf("organization_id is required")
	}
	record, err := s.store.GetProjectPreference(ctx, request.OrganizationID)
	if err != nil {
		return err
	}
	if record == nil || record.IntegrationID == nil {
		return nil
	}
	if request.IntegrationID != 0 && *record.IntegrationID != request.IntegrationID {
		return nil
	}
	record.IntegrationID = nil
	return s.store.SaveProjectPreference(ctx, *record)
}

// filter rewrites the stored repository list, dropping every entry that drop
// reports as removed.
func (s *Service) filter(ctx context.Context, organizationID int64, drop func(Repository) bool) error {
	record, err := s.store.GetProjectPreference(ctx, organizationID)
	if err != nil {
		return err
	}
	repositories := []Repository{}
	if record != nil {
		if err := json.Unmarshal(record.ReposJSON, &repositories); err != nil {
			return fmt.Errorf("decode stored repositories: %w", err)
		}
	}
	kept := make([]Repository, 0, len(repositories))
	for _, repository := range repositories {
		if drop(repository) {
			continue
		}
		kept = append(kept, repository)
	}
	payload, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("marshal repositories: %w", err)
	}
	saved := db.ProjectPreferenceRecord{OrganizationID: organizationID, ReposJSON: payload}
	if record != nil {
		saved.IntegrationID = record.IntegrationID
	}
	return s.store.SaveProjectPreference(ctx, saved)
}
