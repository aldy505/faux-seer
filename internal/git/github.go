package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aldy505/faux-seer/internal/httpclient"
)

// maxResponseBytes bounds a single provider response so a misconfigured base URL
// cannot exhaust memory.
const maxResponseBytes = 8 << 20

// githubProvider talks to GitHub's REST API.
type githubProvider struct {
	client   *http.Client
	baseURL  string
	token    string
	perPage  int
	maxPages int
}

// NewGitHubProvider creates a GitHub REST API client. An empty token reports
// ErrNotConfigured, because unauthenticated GitHub access cannot list a private
// installation's repositories.
func NewGitHubProvider(baseURL, token string, httpClient *http.Client) (Provider, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%w: GITHUB_TOKEN is empty", ErrNotConfigured)
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.github.com"
	}
	if httpClient == nil {
		httpClient = httpclient.New(nil)
	}
	return &githubProvider{
		client:   httpClient,
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		perPage:  100,
		maxPages: 50,
	}, nil
}

func (p *githubProvider) Name() string { return "github" }

type githubRepo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Permissions   struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
	} `json:"permissions"`
}

func (p *githubProvider) repoFromAPI(raw githubRepo) Repo {
	owner := ""
	if index := strings.Index(raw.FullName, "/"); index > 0 {
		owner = raw.FullName[:index]
	}
	readAccess := raw.Permissions.Pull || raw.Permissions.Push || raw.Permissions.Admin
	writeAccess := raw.Permissions.Push || raw.Permissions.Admin
	return Repo{
		Provider:       p.Name(),
		Owner:          owner,
		Name:           raw.Name,
		ExternalID:     strconv.FormatInt(raw.ID, 10),
		DefaultBranch:  raw.DefaultBranch,
		HasReadAccess:  readAccess,
		HasWriteAccess: writeAccess,
	}
}

func (p *githubProvider) ListRepos(ctx context.Context, org string) ([]Repo, error) {
	path := "/user/repos"
	if strings.TrimSpace(org) != "" {
		path = "/orgs/" + url.PathEscape(org) + "/repos"
	}
	query := url.Values{"per_page": {strconv.Itoa(p.perPage)}, "sort": {"updated"}}

	var repos []githubRepo
	for page := 1; page <= p.maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var batch []githubRepo
		next, err := p.doJSON(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &batch)
		if err != nil {
			return nil, err
		}
		repos = append(repos, batch...)
		if next == "" {
			break
		}
		path, err = p.pathFromNext(next)
		if err != nil {
			return nil, err
		}
		query = url.Values{}
	}
	out := make([]Repo, 0, len(repos))
	for _, repo := range repos {
		out = append(out, p.repoFromAPI(repo))
	}
	return out, nil
}

func (p *githubProvider) GetRepo(ctx context.Context, owner, name string) (Repo, error) {
	var raw githubRepo
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name), nil, &raw); err != nil {
		return Repo{}, err
	}
	return p.repoFromAPI(raw), nil
}

func (p *githubProvider) GetDefaultBranch(ctx context.Context, owner, name string) (string, error) {
	repo, err := p.GetRepo(ctx, owner, name)
	if err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "", fmt.Errorf("%w: repository %s/%s has no default branch", ErrNotFound, owner, name)
	}
	return repo.DefaultBranch, nil
}

type githubTree struct {
	Truncated bool `json:"truncated"`
	SHA       string
	Entries   []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size"`
		SHA  string `json:"sha"`
	} `json:"tree"`
}

// ReadTree returns the full recursive tree for a ref.
//
// GitHub truncates recursive listings above a size limit, so truncated results
// are completed by walking the subtrees the first response listed.
func (p *githubProvider) ReadTree(ctx context.Context, owner, name, ref string) ([]TreeEntry, error) {
	if ref == "" {
		defaultBranch, err := p.GetDefaultBranch(ctx, owner, name)
		if err != nil {
			return nil, err
		}
		ref = defaultBranch
	}
	var root githubTree
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", nil, &root); err != nil {
		return nil, err
	}
	entries := make([]TreeEntry, 0, len(root.Entries))
	var pending []string
	for _, item := range root.Entries {
		entries = append(entries, TreeEntry{Path: item.Path, Type: item.Type, Size: item.Size})
		if root.Truncated && item.Type == "tree" {
			pending = append(pending, item.SHA)
		}
	}
	for len(pending) > 0 {
		sha := pending[0]
		pending = pending[1:]
		var subtree githubTree
		if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/git/trees/"+url.PathEscape(sha), nil, &subtree); err != nil {
			return nil, err
		}
		for _, item := range subtree.Entries {
			entries = append(entries, TreeEntry{Path: item.Path, Type: item.Type, Size: item.Size})
			if item.Type == "tree" {
				pending = append(pending, item.SHA)
			}
		}
	}
	return entries, nil
}

func (p *githubProvider) ReadFile(ctx context.Context, owner, name, ref, path string) ([]byte, error) {
	target := p.repoPath(owner, name) + "/contents/" + escapePath(path)
	if ref != "" {
		target += "?ref=" + url.QueryEscape(ref)
	}
	body, _, err := p.do(ctx, http.MethodGet, target, "application/vnd.github.raw", nil)
	if err != nil {
		return nil, err
	}
	return body, nil
}

type githubPR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

func (p *githubProvider) pullFromAPI(raw githubPR) PullRequest {
	return PullRequest{
		Number:  raw.Number,
		URL:     raw.HTMLURL,
		Title:   raw.Title,
		Body:    raw.Body,
		State:   raw.State,
		Head:    raw.Head.Ref,
		HeadSHA: raw.Head.SHA,
		Base:    raw.Base.Ref,
	}
}

func (p *githubProvider) GetPR(ctx context.Context, owner, name string, number int) (PullRequest, error) {
	var raw githubPR
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/pulls/"+strconv.Itoa(number), nil, &raw); err != nil {
		return PullRequest{}, err
	}
	return p.pullFromAPI(raw), nil
}

func (p *githubProvider) GetPRFiles(ctx context.Context, owner, name string, number int) ([]PRFile, error) {
	path := p.repoPath(owner, name) + "/pulls/" + strconv.Itoa(number) + "/files"
	var files []PRFile
	for page := 1; page <= p.maxPages; page++ {
		target := path + "?per_page=" + strconv.Itoa(p.perPage) + "&page=" + strconv.Itoa(page)
		var batch []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"`
			Patch    string `json:"patch"`
		}
		next, err := p.doJSON(ctx, http.MethodGet, target, nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, item := range batch {
			files = append(files, PRFile{Path: item.Filename, Status: item.Status, Patch: item.Patch})
		}
		if next == "" {
			break
		}
	}
	return files, nil
}

func (p *githubProvider) CreateBranch(ctx context.Context, owner, name, baseBranch, newBranch string) error {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/git/ref/heads/"+escapePath(baseBranch), nil, &ref); err != nil {
		return err
	}
	payload := map[string]string{"ref": "refs/heads/" + newBranch, "sha": ref.Object.SHA}
	if _, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/git/refs", payload, nil); err != nil {
		return err
	}
	return nil
}

func (p *githubProvider) CreateCommit(ctx context.Context, owner, name, branch, message string, files []FileChange) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/git/ref/heads/"+escapePath(branch), nil, &ref); err != nil {
		return "", err
	}
	baseCommitSHA := ref.Object.SHA

	var baseCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := p.doJSON(ctx, http.MethodGet, p.repoPath(owner, name)+"/git/commits/"+url.PathEscape(baseCommitSHA), nil, &baseCommit); err != nil {
		return "", err
	}

	tree := make([]map[string]any, 0, len(files))
	for _, file := range files {
		var blob struct {
			SHA string `json:"sha"`
		}
		payload := map[string]any{"content": string(file.Content), "encoding": "utf-8"}
		if _, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/git/blobs", payload, &blob); err != nil {
			return "", err
		}
		tree = append(tree, map[string]any{"path": file.Path, "mode": "100644", "type": "blob", "sha": blob.SHA})
	}
	var newTree struct {
		SHA string `json:"sha"`
	}
	if _, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/git/trees", map[string]any{"base_tree": baseCommit.Tree.SHA, "tree": tree}, &newTree); err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/git/commits", map[string]any{"message": message, "tree": newTree.SHA, "parents": []string{baseCommitSHA}}, &commit); err != nil {
		return "", err
	}
	if _, err := p.doJSON(ctx, http.MethodPatch, p.repoPath(owner, name)+"/git/refs/heads/"+escapePath(branch), map[string]any{"sha": commit.SHA, "force": false}, nil); err != nil {
		return "", err
	}
	return commit.SHA, nil
}

func (p *githubProvider) OpenPR(ctx context.Context, owner, name, title, body, head, base string) (PullRequest, error) {
	payload := map[string]string{"title": title, "body": body, "head": head, "base": base}
	var raw githubPR
	if _, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/pulls", payload, &raw); err != nil {
		return PullRequest{}, err
	}
	return p.pullFromAPI(raw), nil
}

func (p *githubProvider) PostPRReview(ctx context.Context, owner, name string, number int, body string, comments []PRReviewComment) error {
	payload := map[string]any{"body": body, "event": "COMMENT"}
	if len(comments) > 0 {
		rendered := make([]map[string]any, 0, len(comments))
		for _, comment := range comments {
			rendered = append(rendered, map[string]any{"path": comment.Path, "line": comment.Line, "body": comment.Body})
		}
		payload["comments"] = rendered
	}
	_, err := p.doJSON(ctx, http.MethodPost, p.repoPath(owner, name)+"/pulls/"+strconv.Itoa(number)+"/reviews", payload, nil)
	return err
}

func (p *githubProvider) repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// pathFromNext extracts the request path from a Link header's next URL.
func (p *githubProvider) pathFromNext(next string) (string, error) {
	parsed, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("parse github pagination link %q: %w", next, err)
	}
	target := parsed.Path
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	return target, nil
}

// doJSON performs a JSON request and decodes the response into out, returning
// the Link header's next URL when the response is paginated.
func (p *githubProvider) doJSON(ctx context.Context, method, path string, payload any, out any) (string, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal github request payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	raw, next, err := p.do(ctx, method, path, "application/vnd.github+json", body)
	if err != nil {
		return "", err
	}
	if out == nil {
		return next, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return "", fmt.Errorf("decode github response for %s %s: %w", method, path, err)
	}
	return next, nil
}

// do performs an authenticated request against the configured base URL.
func (p *githubProvider) do(ctx context.Context, method, path, accept string, payload io.Reader) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, payload)
	if err != nil {
		return nil, "", fmt.Errorf("build github request for %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read github response for %s %s: %w", method, path, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("%w: github %s %s", ErrNotFound, method, path)
	}
	if resp.StatusCode >= 400 {
		return nil, "", &HTTPError{StatusCode: resp.StatusCode, Method: method, Path: path, Body: strings.TrimSpace(string(raw))}
	}
	return raw, nextLink(resp.Header.Get("Link")), nil
}

// HTTPError describes a non-2xx provider response.
type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("github %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is a provider 404.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// nextLink extracts the rel="next" URL from a Link header.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		sections := strings.Split(part, ";")
		if len(sections) < 2 {
			continue
		}
		if !strings.Contains(sections[1], `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(sections[0]), "<>")
	}
	return ""
}

// escapePath escapes each path segment, preserving the separators.
func escapePath(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
