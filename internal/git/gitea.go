package git

import (
	"context"
	"fmt"
	"strings"
)

// giteaProvider is the Gitea extension seam.
//
// Gitea's REST API resembles GitHub's but is self-hosted and paginates
// differently, so a faithful implementation needs its own request handling.
// Every method reports ErrNotConfigured until that work lands; the type exists
// so the factory can hand callers a provider that names what is missing.
type giteaProvider struct {
	baseURL string
	token   string
}

// NewGiteaProvider creates a Gitea client placeholder. Gitea is self-hosted, so
// it requires a base URL as well as a token.
func NewGiteaProvider(baseURL, token string) (Provider, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("%w: GITEA_TOKEN and GITEA_BASE_URL are required", ErrNotConfigured)
	}
	return &giteaProvider{baseURL: strings.TrimRight(baseURL, "/"), token: token}, nil
}

func (p *giteaProvider) Name() string { return "gitea" }

func (p *giteaProvider) unsupported(method string) error {
	return fmt.Errorf("%w: the gitea provider does not implement %s yet", ErrNotConfigured, method)
}

func (p *giteaProvider) ListRepos(context.Context, string) ([]Repo, error) {
	return nil, p.unsupported("ListRepos")
}

func (p *giteaProvider) GetRepo(context.Context, string, string) (Repo, error) {
	return Repo{}, p.unsupported("GetRepo")
}

func (p *giteaProvider) GetDefaultBranch(context.Context, string, string) (string, error) {
	return "", p.unsupported("GetDefaultBranch")
}

func (p *giteaProvider) ReadTree(context.Context, string, string, string) ([]TreeEntry, error) {
	return nil, p.unsupported("ReadTree")
}

func (p *giteaProvider) ReadFile(context.Context, string, string, string, string) ([]byte, error) {
	return nil, p.unsupported("ReadFile")
}

func (p *giteaProvider) GetPR(context.Context, string, string, int) (PullRequest, error) {
	return PullRequest{}, p.unsupported("GetPR")
}

func (p *giteaProvider) GetPRFiles(context.Context, string, string, int) ([]PRFile, error) {
	return nil, p.unsupported("GetPRFiles")
}

func (p *giteaProvider) CreateBranch(context.Context, string, string, string, string) error {
	return p.unsupported("CreateBranch")
}

func (p *giteaProvider) CreateCommit(context.Context, string, string, string, string, []FileChange) (string, error) {
	return "", p.unsupported("CreateCommit")
}

func (p *giteaProvider) OpenPR(context.Context, string, string, string, string, string, string) (PullRequest, error) {
	return PullRequest{}, p.unsupported("OpenPR")
}

func (p *giteaProvider) PostPRReview(context.Context, string, string, int, string, []PRReviewComment) error {
	return p.unsupported("PostPRReview")
}
