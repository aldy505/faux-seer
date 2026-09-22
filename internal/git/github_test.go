package git

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aldy505/faux-seer/internal/config"
)

func TestNewGitHubProviderRequiresToken(t *testing.T) {
	if _, err := NewGitHubProvider("https://api.github.com", "  "); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestGitHubProviderListReposFollowsPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/orgs/acme/repos":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/acme/repos?page=2>; rel="next", <%s/orgs/acme/repos?page=2>; rel="last"`, server.URL, server.URL))
				fmt.Fprint(w, `[{"id":1,"name":"widget","full_name":"acme/widget","default_branch":"main","permissions":{"pull":true,"push":false}}]`)
				return
			}
			fmt.Fprint(w, `[{"id":2,"name":"gadget","full_name":"acme/gadget","default_branch":"trunk","permissions":{"admin":true}}]`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider, err := NewGitHubProvider(server.URL, "test-token")
	if err != nil {
		t.Fatalf("create github provider: %v", err)
	}
	repos, err := provider.ListRepos(context.Background(), "acme")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected both pages, got %#v", repos)
	}
	widget, gadget := repos[0], repos[1]
	if widget.Owner != "acme" || widget.Name != "widget" || widget.DefaultBranch != "main" || widget.ExternalID != "1" {
		t.Fatalf("unexpected repo mapping: %#v", widget)
	}
	if !widget.HasReadAccess || widget.HasWriteAccess {
		t.Fatalf("expected pull-only access, got %#v", widget)
	}
	if !gadget.HasWriteAccess {
		t.Fatalf("expected admin to grant write access, got %#v", gadget)
	}
}

func TestGitHubProviderReadFileAndTree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/widget/git/trees/main":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"truncated":false,"tree":[{"path":"main.go","type":"blob","size":12},{"path":"pkg","type":"tree","size":0}]}`)
		case r.URL.Path == "/repos/acme/widget/contents/main.go":
			if r.URL.Query().Get("ref") != "main" {
				t.Errorf("expected ref=main, got %q", r.URL.RawQuery)
			}
			fmt.Fprint(w, "package main")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider, err := NewGitHubProvider(server.URL, "test-token")
	if err != nil {
		t.Fatalf("create github provider: %v", err)
	}
	tree, err := provider.ReadTree(context.Background(), "acme", "widget", "main")
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	if len(tree) != 2 || !tree[0].IsFile() || tree[1].IsFile() || tree[0].Size != 12 {
		t.Fatalf("unexpected tree: %#v", tree)
	}
	content, err := provider.ReadFile(context.Background(), "acme", "widget", "main", "main.go")
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "package main" {
		t.Fatalf("unexpected file content %q", content)
	}
}

func TestGitHubProviderReportsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider, err := NewGitHubProvider(server.URL, "test-token")
	if err != nil {
		t.Fatalf("create github provider: %v", err)
	}
	_, err = provider.GetPR(context.Background(), "acme", "widget", 7)
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGitHubProviderReadsPullRequestFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widget/pulls/7/files" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `[{"filename":"main.go","status":"modified","patch":"@@ -1 +1 @@"}]`)
	}))
	defer server.Close()

	provider, err := NewGitHubProvider(server.URL, "test-token")
	if err != nil {
		t.Fatalf("create github provider: %v", err)
	}
	files, err := provider.GetPRFiles(context.Background(), "acme", "widget", 7)
	if err != nil {
		t.Fatalf("get pr files: %v", err)
	}
	if len(files) != 1 || files[0].Path != "main.go" || !strings.Contains(files[0].Patch, "@@") {
		t.Fatalf("unexpected pr files: %#v", files)
	}
}

func TestNewProviderForUnimplementedProviders(t *testing.T) {
	// Without credentials the provider cannot even be constructed.
	provider, err := NewProvider(&config.Config{GitHubToken: "test-token"}, "gitlab")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured for gitlab without a token, got %v", err)
	}
	if provider != nil {
		t.Fatalf("expected no provider, got %#v", provider)
	}

	// With credentials the provider exists but every call reports that the
	// implementation is still missing, so handlers fall back to simulated work.
	provider, err = NewProvider(&config.Config{GitLabToken: "test-token"}, "gitlab")
	if err != nil {
		t.Fatalf("create gitlab provider: %v", err)
	}
	if provider.Name() != "gitlab" {
		t.Fatalf("expected the gitlab provider, got %q", provider.Name())
	}
	if _, err := provider.ListRepos(context.Background(), "acme"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured from ListRepos, got %v", err)
	}
}
