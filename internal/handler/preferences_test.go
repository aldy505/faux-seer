package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aldy505/faux-seer/internal/db"
)

// TestProjectPreferenceRemovalsPersistAndStayIdempotent covers the only
// consumer-visible effect of the preference endpoints: the stored repository
// list shrinks, and repeating a removal is still reported as success.
func TestProjectPreferenceRemovalsPersistAndStayIdempotent(t *testing.T) {
	server, store, _, _, _, cleanup := newTestServerWithMocks(t)
	defer cleanup()
	ctx := context.Background()

	seed := db.ProjectPreferenceRecord{
		OrganizationID: 1,
		ReposJSON:      []byte(`[{"repo_provider":"github","repo_external_id":"1","owner":"acme","name":"widget"},{"repo_provider":"gitlab","repo_external_id":"2"}]`),
	}
	integrationID := int64(99)
	seed.IntegrationID = &integrationID
	if err := store.SaveProjectPreference(ctx, seed); err != nil {
		t.Fatalf("seed project preference: %v", err)
	}

	remove := []byte(`{"organization_id":1,"repo_provider":"github","repo_external_id":"1"}`)
	for attempt := 0; attempt < 2; attempt++ {
		resp := issueRequest(server, http.MethodPost, "/v1/project-preference/remove-repository", remove)
		if resp.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d: %s", attempt, resp.Code, resp.Body.String())
		}
		var body map[string]bool
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode remove repository response: %v", err)
		}
		if !body["success"] {
			t.Fatalf("attempt %d: expected success, got %#v", attempt, body)
		}
	}

	stored := storedRepositoryIDs(t, store, 1)
	if len(stored) != 1 || stored[0] != "gitlab/2" {
		t.Fatalf("expected only the gitlab repository to remain, got %#v", stored)
	}

	bulk := []byte(`{"organization_id":1,"repositories":[{"repo_provider":"gitlab","repo_external_id":"2"}]}`)
	resp := issueRequest(server, http.MethodPost, "/v1/project-preference/bulk-remove-repositories", bulk)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	resp = issueRequest(server, http.MethodPost, "/v1/project-preference/bulk-remove-repositories", bulk)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on the repeated bulk removal, got %d: %s", resp.Code, resp.Body.String())
	}
	if stored := storedRepositoryIDs(t, store, 1); len(stored) != 0 {
		t.Fatalf("expected no repositories to remain, got %#v", stored)
	}

	handoffs := []byte(`{"organization_id":1,"integration_id":99}`)
	resp = issueRequest(server, http.MethodPost, "/v1/project-preference/remove-handoffs-for-integration", handoffs)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	cleared, err := store.GetProjectPreference(ctx, 1)
	if err != nil {
		t.Fatalf("read project preference: %v", err)
	}
	if cleared.IntegrationID != nil {
		t.Fatalf("expected the handoff integration to be cleared, got %#v", cleared.IntegrationID)
	}
}

// storedRepositoryIDs renders the stored repositories of an organization.
func storedRepositoryIDs(t *testing.T, store *db.Store, organizationID int64) []string {
	t.Helper()
	record, err := store.GetProjectPreference(context.Background(), organizationID)
	if err != nil {
		t.Fatalf("read project preference: %v", err)
	}
	if record == nil {
		return nil
	}
	var repositories []struct {
		Provider   string `json:"repo_provider"`
		ExternalID string `json:"repo_external_id"`
	}
	if err := json.Unmarshal(record.ReposJSON, &repositories); err != nil {
		t.Fatalf("decode stored repositories: %v", err)
	}
	ids := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		ids = append(ids, repository.Provider+"/"+repository.ExternalID)
	}
	return ids
}
