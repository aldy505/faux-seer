package git

import (
	"context"
	"fmt"
	"strings"
)

// gitlabProvider is the GitLab extension seam.
//
// GitLab's API differs enough from GitHub's (project ids instead of owner/name
// paths, merge requests instead of pull requests) that a faithful
// implementation needs its own request and response handling, so every method
// reports ErrNotConfigured until that work lands. The type exists so the
// factory can hand callers a provider that names what is missing.
type gitlabProvider struct {
	baseURL string
	token   string
}

// NewGitLabProvider creates a GitLab client placeholder.
func NewGitLabProvider(baseURL, token string) (Provider, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%w: GITLAB_TOKEN is empty", ErrNotConfigured)
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://gitlab.com/api/v4"
	}
	return &gitlabProvider{baseURL: strings.TrimRight(baseURL, "/"), token: token}, nil
}

func (p *gitlabProvider) Name() string { return "gitlab" }

func (p *gitlabProvider) unsupported(method string) error {
	return fmt.Errorf("%w: the gitlab provider does not implement %s yet", ErrNotConfigured, method)
}

func (p *gitlabProvider) ListRepos(context.Context, string) ([]Repo, error) {
	return nil, p.unsupported("ListRepos")
}

func (p *gitlabProvider) GetRepo(context.Context, string, string) (Repo, error) {
	return Repo{}, p.unsupported("GetRepo")
}

func (p *gitlabProvider) GetDefaultBranch(context.Context, string, string) (string, error) {
	return "", p.unsupported("GetDefaultBranch")
}

func (p *gitlabProvider) ReadTree(context.Context, string, string, string) ([]TreeEntry, error) {
	return nil, p.unsupported("ReadTree")
}

func (p *gitlabProvider) ReadFile(context.Context, string, string, string, string) ([]byte, error) {
	return nil, p.unsupported("ReadFile")
}

func (p *gitlabProvider) GetPR(context.Context, string, string, int) (PullRequest, error) {
	return PullRequest{}, p.unsupported("GetPR")
}

func (p *gitlabProvider) GetPRFiles(context.Context, string, string, int) ([]PRFile, error) {
	return nil, p.unsupported("GetPRFiles")
}

func (p *gitlabProvider) CreateBranch(context.Context, string, string, string, string) error {
	return p.unsupported("CreateBranch")
}

func (p *gitlabProvider) CreateCommit(context.Context, string, string, string, string, []FileChange) (string, error) {
	return "", p.unsupported("CreateCommit")
}

func (p *gitlabProvider) OpenPR(context.Context, string, string, string, string, string, string) (PullRequest, error) {
	return PullRequest{}, p.unsupported("OpenPR")
}

func (p *gitlabProvider) PostPRReview(context.Context, string, string, int, string, []PRReviewComment) error {
	return p.unsupported("PostPRReview")
}
