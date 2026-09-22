package git

import (
	"context"
	"fmt"
	"strings"
)

// bitbucketProvider is the Bitbucket extension seam.
//
// Bitbucket identifies repositories by workspace slug plus a UUID external id
// and nests pull requests under the workspace, so a faithful implementation
// needs its own addressing and pagination. Every method reports
// ErrNotConfigured until that work lands; the type exists so the factory can
// hand callers a provider that names what is missing.
type bitbucketProvider struct {
	baseURL string
	token   string
}

// NewBitbucketProvider creates a Bitbucket client placeholder.
func NewBitbucketProvider(baseURL, token string) (Provider, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%w: BITBUCKET_TOKEN is empty", ErrNotConfigured)
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.bitbucket.org/2.0"
	}
	return &bitbucketProvider{baseURL: strings.TrimRight(baseURL, "/"), token: token}, nil
}

func (p *bitbucketProvider) Name() string { return "bitbucket" }

func (p *bitbucketProvider) unsupported(method string) error {
	return fmt.Errorf("%w: the bitbucket provider does not implement %s yet", ErrNotConfigured, method)
}

func (p *bitbucketProvider) ListRepos(context.Context, string) ([]Repo, error) {
	return nil, p.unsupported("ListRepos")
}

func (p *bitbucketProvider) GetRepo(context.Context, string, string) (Repo, error) {
	return Repo{}, p.unsupported("GetRepo")
}

func (p *bitbucketProvider) GetDefaultBranch(context.Context, string, string) (string, error) {
	return "", p.unsupported("GetDefaultBranch")
}

func (p *bitbucketProvider) ReadTree(context.Context, string, string, string) ([]TreeEntry, error) {
	return nil, p.unsupported("ReadTree")
}

func (p *bitbucketProvider) ReadFile(context.Context, string, string, string, string) ([]byte, error) {
	return nil, p.unsupported("ReadFile")
}

func (p *bitbucketProvider) GetPR(context.Context, string, string, int) (PullRequest, error) {
	return PullRequest{}, p.unsupported("GetPR")
}

func (p *bitbucketProvider) GetPRFiles(context.Context, string, string, int) ([]PRFile, error) {
	return nil, p.unsupported("GetPRFiles")
}

func (p *bitbucketProvider) CreateBranch(context.Context, string, string, string, string) error {
	return p.unsupported("CreateBranch")
}

func (p *bitbucketProvider) CreateCommit(context.Context, string, string, string, string, []FileChange) (string, error) {
	return "", p.unsupported("CreateCommit")
}

func (p *bitbucketProvider) OpenPR(context.Context, string, string, string, string, string, string) (PullRequest, error) {
	return PullRequest{}, p.unsupported("OpenPR")
}

func (p *bitbucketProvider) PostPRReview(context.Context, string, string, int, string, []PRReviewComment) error {
	return p.unsupported("PostPRReview")
}
