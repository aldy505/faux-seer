// Package git defines the repository-host abstraction used by faux-seer and a
// GitHub implementation of it.
//
// Sentry identifies a repository by (provider, owner, name) plus the external
// id of the installation it lives in, so every provider method is addressed the
// same way. Providers without credentials report ErrNotConfigured, which
// callers treat as "fall back to a simulated response".
package git

import (
	"context"
	"errors"
)

var (
	// ErrNotConfigured reports that no credentials exist for the provider, so
	// repository access is impossible.
	ErrNotConfigured = errors.New("git provider is not configured")
	// ErrNotFound reports that a repository, ref, or pull request is absent.
	ErrNotFound = errors.New("git resource not found")
)

// Repo describes a repository visible to the configured provider.
type Repo struct {
	Provider       string
	Owner          string
	Name           string
	ExternalID     string
	DefaultBranch  string
	HasReadAccess  bool
	HasWriteAccess bool
}

// TreeEntry is one entry in a repository tree.
type TreeEntry struct {
	Path string
	Type string // "blob" or "tree"
	Size int64
}

// IsFile reports whether the entry is a file rather than a directory.
func (e TreeEntry) IsFile() bool { return e.Type == "blob" }

// FileChange is one file written by CreateCommit.
type FileChange struct {
	Path    string
	Content []byte
}

// PRFile is one file changed by a pull request.
type PRFile struct {
	Path   string
	Status string // "added", "modified", "removed", "renamed"
	Patch  string
}

// PullRequest identifies a pull request and the refs it moves between.
type PullRequest struct {
	Number  int
	URL     string
	Title   string
	Body    string
	State   string
	Head    string
	HeadSHA string
	Base    string
}

// PRReviewComment is one inline review comment.
type PRReviewComment struct {
	Path string
	Line int
	Body string
}

// Provider is the repository-host abstraction.
type Provider interface {
	// Name reports the provider identifier Sentry uses ("github", "gitlab", ...).
	Name() string
	ListRepos(ctx context.Context, org string) ([]Repo, error)
	GetRepo(ctx context.Context, owner, name string) (Repo, error)
	GetDefaultBranch(ctx context.Context, owner, name string) (string, error)
	ReadTree(ctx context.Context, owner, name, ref string) ([]TreeEntry, error)
	ReadFile(ctx context.Context, owner, name, ref, path string) ([]byte, error)
	GetPR(ctx context.Context, owner, name string, number int) (PullRequest, error)
	GetPRFiles(ctx context.Context, owner, name string, number int) ([]PRFile, error)
	CreateBranch(ctx context.Context, owner, name, baseBranch, newBranch string) error
	CreateCommit(ctx context.Context, owner, name, branch, message string, files []FileChange) (string, error)
	OpenPR(ctx context.Context, owner, name, title, body, head, base string) (PullRequest, error)
	PostPRReview(ctx context.Context, owner, name string, number int, body string, comments []PRReviewComment) error
}
