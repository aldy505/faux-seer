package git

import (
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/httpclient"
)

// NewProvider builds the repository provider selected by name.
//
// GitHub is the only fully implemented provider. The others are constructed from
// their token and base-URL configuration and report ErrNotConfigured from every
// method, so callers fall back to a simulated response instead of failing the
// request.
func NewProvider(cfg *config.Config, provider string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "github":
		return NewGitHubProvider(cfg.GitHubBaseURL, cfg.GitHubToken, httpclient.New(cfg))
	case "gitlab":
		return NewGitLabProvider(cfg.GitLabBaseURL, cfg.GitLabToken)
	case "gitea":
		return NewGiteaProvider(cfg.GiteaBaseURL, cfg.GiteaToken)
	case "bitbucket":
		return NewBitbucketProvider(cfg.BitbucketBaseURL, cfg.BitbucketToken)
	default:
		return nil, fmt.Errorf("%w: unknown git provider %q", ErrNotConfigured, provider)
	}
}
