package codeindex

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/vectorstore"
	"github.com/aldy505/faux-seer/internal/vectorstore/sqlitevec"
)

// fakeProvider serves a fixed repository tree without a network.
type fakeProvider struct {
	tree          []git.TreeEntry
	files         map[string]string
	defaultBranch string
}

func (f fakeProvider) Name() string { return "github" }

func (f fakeProvider) ListRepos(context.Context, string) ([]git.Repo, error) { return nil, nil }

func (f fakeProvider) GetRepo(context.Context, string, string) (git.Repo, error) {
	return git.Repo{Owner: "acme", Name: "widget", DefaultBranch: f.defaultBranch}, nil
}

func (f fakeProvider) GetDefaultBranch(context.Context, string, string) (string, error) {
	return f.defaultBranch, nil
}

func (f fakeProvider) ReadTree(context.Context, string, string, string) ([]git.TreeEntry, error) {
	return f.tree, nil
}

func (f fakeProvider) ReadFile(_ context.Context, _, _, _, path string) ([]byte, error) {
	content, ok := f.files[path]
	if !ok {
		return nil, git.ErrNotFound
	}
	return []byte(content), nil
}

func (f fakeProvider) GetPR(context.Context, string, string, int) (git.PullRequest, error) {
	return git.PullRequest{}, errors.New("not implemented")
}

func (f fakeProvider) GetPRFiles(context.Context, string, string, int) ([]git.PRFile, error) {
	return nil, errors.New("not implemented")
}

func (f fakeProvider) CreateBranch(context.Context, string, string, string, string) error {
	return errors.New("not implemented")
}

func (f fakeProvider) CreateCommit(context.Context, string, string, string, string, []git.FileChange) (string, error) {
	return "", errors.New("not implemented")
}

func (f fakeProvider) OpenPR(context.Context, string, string, string, string, string, string) (git.PullRequest, error) {
	return git.PullRequest{}, errors.New("not implemented")
}

func (f fakeProvider) PostPRReview(context.Context, string, string, int, string, []git.PRReviewComment) error {
	return errors.New("not implemented")
}

// bowEmbedder is a deterministic bag-of-words embedding: each token hashes to a
// dimension, so chunks sharing vocabulary with a query are genuinely the
// nearest neighbors. A random vector would make the ranking assertion
// meaningless.
type bowEmbedder struct{ dimensions int }

func (b bowEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vector := make([]float32, b.dimensions)
		for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_'
		}) {
			hasher := fnv.New32a()
			_, _ = hasher.Write([]byte(token))
			vector[hasher.Sum32()%uint32(b.dimensions)]++
		}
		var sum float64
		for _, value := range vector {
			sum += float64(value * value)
		}
		if sum > 0 {
			magnitude := float32(math.Sqrt(sum))
			for index := range vector {
				vector[index] /= magnitude
			}
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func newTestService(t *testing.T) (*Service, vectorstore.Store) {
	t.Helper()
	store, err := db.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	vectorStore, err := sqlitevec.New(context.Background(), store, 64)
	if err != nil {
		t.Fatalf("create sqlitevec store: %v", err)
	}
	provider := fakeProvider{
		defaultBranch: "main",
		tree: []git.TreeEntry{
			{Path: "checkout.go", Type: "blob", Size: 2048},
			{Path: "README.md", Type: "blob", Size: 128},
			{Path: "node_modules/left-pad/index.js", Type: "blob", Size: 64},
			{Path: "assets/logo.png", Type: "blob", Size: 64},
			{Path: "data/huge.txt", Type: "blob", Size: maxCodeFileBytes + 1},
			{Path: "pkg", Type: "tree"},
		},
		files: map[string]string{
			"checkout.go": "package checkout\n\n// ValidateCart validates the shopper cart before payment.\nfunc ValidateCart(cart Cart) error {\n\treturn nil\n}\n",
			"README.md":   "# widget\n\nInstall instructions.\n",
		},
	}
	return New(provider, bowEmbedder{dimensions: 64}, vectorStore, 400, 80), vectorStore
}

func TestIndexRepoIndexesTextAndSkipsUnindexableEntries(t *testing.T) {
	service, _ := newTestService(t)
	stats, err := service.IndexRepo(context.Background(), IndexRequest{OrganizationID: 42, Owner: "acme", Name: "widget"})
	if err != nil {
		t.Fatalf("index repo: %v", err)
	}
	if stats.Ref != "main" {
		t.Fatalf("expected the default branch to be indexed, got %q", stats.Ref)
	}
	if stats.FilesIndexed != 2 {
		t.Fatalf("expected the two text files to be indexed, got %#v", stats)
	}
	if stats.ChunksStored == 0 {
		t.Fatalf("expected stored chunks, got %#v", stats)
	}
	if stats.FilesSkipped != 4 {
		t.Fatalf("expected node_modules, binary, oversized and tree entries to be skipped, got %#v", stats)
	}

	indexed, err := service.HasOrgIndex(context.Background(), 42)
	if err != nil {
		t.Fatalf("check org index: %v", err)
	}
	if !indexed {
		t.Fatal("expected the organization to report an index")
	}
	other, err := service.HasOrgIndex(context.Background(), 7)
	if err != nil {
		t.Fatalf("check other org index: %v", err)
	}
	if other {
		t.Fatal("expected another organization to report no index")
	}
}

func TestIndexRepoSearchFindsMatchingChunk(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.IndexRepo(context.Background(), IndexRequest{OrganizationID: 42, Owner: "acme", Name: "widget"}); err != nil {
		t.Fatalf("index repo: %v", err)
	}
	results, err := service.Search(context.Background(), "ValidateCart checkout", SearchOptions{OrganizationID: 42, K: 1})
	if err != nil {
		t.Fatalf("search code: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %#v", results)
	}
	result := results[0]
	if result.Path != "checkout.go" {
		t.Fatalf("expected the matching file, got %#v", result)
	}
	if !strings.Contains(result.Text, "ValidateCart") {
		t.Fatalf("expected the chunk text to contain the symbol, got %q", result.Text)
	}
	if result.StartLine < 1 || result.EndLine < result.StartLine {
		t.Fatalf("expected a line span, got %#v", result)
	}
	if result.Owner != "acme" || result.Name != "widget" || result.Ref != "main" {
		t.Fatalf("expected repository identity on the result, got %#v", result)
	}

	// Another organization must not see this index.
	other, err := service.Search(context.Background(), "ValidateCart", SearchOptions{OrganizationID: 7, K: 1})
	if err != nil {
		t.Fatalf("search other org: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected no results for another organization, got %#v", other)
	}
}

func TestDeleteRepoRemovesIndex(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.IndexRepo(context.Background(), IndexRequest{OrganizationID: 42, Owner: "acme", Name: "widget"}); err != nil {
		t.Fatalf("index repo: %v", err)
	}
	if err := service.DeleteRepo(context.Background(), 42, "github", "acme", "widget"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	indexed, err := service.HasOrgIndex(context.Background(), 42)
	if err != nil {
		t.Fatalf("check org index: %v", err)
	}
	if indexed {
		t.Fatal("expected the deleted repository to leave no index")
	}
}

// TestIndexKeepsOrganizationsSeparate covers two organizations indexing the same
// repository: each keeps its own chunks, and deleting one index leaves the other.
func TestIndexKeepsOrganizationsSeparate(t *testing.T) {
	service, _ := newTestService(t)
	for _, organizationID := range []int64{42, 43} {
		if _, err := service.IndexRepo(context.Background(), IndexRequest{OrganizationID: organizationID, Owner: "acme", Name: "widget"}); err != nil {
			t.Fatalf("index repo for org %d: %v", organizationID, err)
		}
	}
	for _, organizationID := range []int64{42, 43} {
		indexed, err := service.HasOrgIndex(context.Background(), organizationID)
		if err != nil {
			t.Fatalf("check org %d index: %v", organizationID, err)
		}
		if !indexed {
			t.Fatalf("expected org %d to keep its index", organizationID)
		}
		results, err := service.Search(context.Background(), "ValidateCart", SearchOptions{OrganizationID: organizationID, K: 1})
		if err != nil {
			t.Fatalf("search org %d: %v", organizationID, err)
		}
		if len(results) != 1 {
			t.Fatalf("expected org %d to find its own chunk, got %#v", organizationID, results)
		}
	}

	if err := service.DeleteRepo(context.Background(), 42, "github", "acme", "widget"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if indexed, err := service.HasOrgIndex(context.Background(), 42); err != nil || indexed {
		t.Fatalf("expected org 42's index to be gone (err %v, indexed %v)", err, indexed)
	}
	if indexed, err := service.HasOrgIndex(context.Background(), 43); err != nil || !indexed {
		t.Fatalf("expected org 43's index to survive (err %v, indexed %v)", err, indexed)
	}
}

func TestIndexRepoWithoutProviderIsNotConfigured(t *testing.T) {
	service := New(nil, bowEmbedder{dimensions: 8}, nil, 100, 10)
	if _, err := service.IndexRepo(context.Background(), IndexRequest{Owner: "acme", Name: "widget"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if service.Indexed() {
		t.Fatal("expected a provider-less service to report no index capability")
	}
}
