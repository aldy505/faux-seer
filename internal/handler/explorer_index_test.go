package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aldy505/faux-seer/internal/assistedquery"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/explorer"
	"github.com/aldy505/faux-seer/internal/feedback"
	"github.com/aldy505/faux-seer/internal/generation"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/investigations"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/monitoring"
	"github.com/aldy505/faux-seer/internal/preferences"
	"github.com/aldy505/faux-seer/internal/replay"
	"github.com/aldy505/faux-seer/internal/runmgr"
	"github.com/aldy505/faux-seer/internal/runs"
	"github.com/aldy505/faux-seer/internal/severity"
	"github.com/aldy505/faux-seer/internal/similarity"
	"github.com/aldy505/faux-seer/internal/testutil"
	"github.com/aldy505/faux-seer/internal/vectorstore/sqlitevec"
)

// repositoryTestSource is the single file served by the GitHub stub.
const repositoryTestSource = "package checkout\n\n// ValidateCart rejects a cart whose total is negative.\nfunc ValidateCart(total int) error {\n\tif total < 0 {\n\t\treturn fmt.Errorf(\"negative total\")\n\t}\n\treturn nil\n}\n"

// newRepositoryBackedServer wires the real HTTP surface to a real code index
// backed by a stub GitHub API, so repository-backed behavior can be exercised
// without network access. It returns the server, the code index, and the
// captured model prompts.
func newRepositoryBackedServer(t *testing.T) (*Server, *codeindex.Service, func() string) {
	t.Helper()
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widget/git/trees/main":
			fmt.Fprint(w, `{"truncated":false,"tree":[{"path":"checkout.go","type":"blob","size":200}]}`)
		case "/repos/acme/widget/contents/checkout.go":
			fmt.Fprint(w, repositoryTestSource)
		case "/repos/acme/widget":
			fmt.Fprint(w, `{"id":1,"name":"widget","full_name":"acme/widget","default_branch":"main","permissions":{"push":true}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(github.Close)

	provider, err := git.NewGitHubProvider(github.URL, "test-token", nil)
	if err != nil {
		t.Fatalf("create github provider: %v", err)
	}
	ctx := context.Background()
	store, err := db.New(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	vectorStore, err := sqlitevec.New(ctx, store, 8)
	if err != nil {
		t.Fatalf("create sqlitevec store: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The prompt is captured under a mutex because replies are generated on a
	// background run goroutine.
	var (
		promptMu   sync.Mutex
		lastPrompt string
	)
	llmClient := &testutil.MockLLMClient{CompleteFunc: func(_ context.Context, req llm.CompletionRequest) (string, error) {
		promptMu.Lock()
		lastPrompt = req.UserPrompt
		promptMu.Unlock()
		return "ValidateCart rejects negative totals.", nil
	}}
	cfg := &config.Config{SharedSecrets: []string{"test-secret"}, LLMModel: []string{"test-model"}, EmbeddingModel: []string{"stub"}, SimilarityThreshold: 0.1, VectorDimensions: 8}
	manager := runmgr.New(ctx, logger)
	t.Cleanup(manager.CancelAll)
	runService := runs.New(store, llmClient, manager)
	codeIndex := codeindex.New(provider, embedding.NewStubClient(8), vectorStore, 1000, 200)
	server := New(Services{
		Config:         cfg,
		Logger:         logger,
		Store:          store,
		LLM:            llmClient,
		Git:            provider,
		CodeIndex:      codeIndex,
		Runs:           manager,
		Autofix:        autofix.New(store, llmClient, manager),
		Explorer:       explorer.New(store, llmClient, codeIndex, manager),
		Similarity:     similarity.New(cfg, embedding.NewStubClient(8), vectorStore),
		Severity:       severity.New(llmClient),
		IssueSummary:   issuesummary.New(llmClient),
		Feedback:       feedback.New(llmClient),
		Replay:         replay.New(store, llmClient),
		Generation:     generation.New(llmClient, provider, cfg),
		RunStore:       runService,
		Investigations: investigations.New(runService),
		AssistedQuery:  assistedquery.New(runService, codeIndex),
		Preferences:    preferences.New(store),
		Monitoring:     monitoring.New(cfg),
	})
	return server, codeIndex, func() string {
		promptMu.Lock()
		defer promptMu.Unlock()
		return lastPrompt
	}
}

// TestExplorerIndexThenChatGroundsReplyInIndexedCode drives the whole
// repository-backed path through the real HTTP surface: a GitHub API stub serves
// one source file, the index endpoint fetches and embeds it into the sqlite-vec
// store, and a code-related chat question must then carry that file into the
// model prompt.
func TestExplorerIndexThenChatGroundsReplyInIndexedCode(t *testing.T) {
	server, codeIndex, recordedPrompt := newRepositoryBackedServer(t)
	ctx := context.Background()

	indexResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/index/org-repo-knowledge",
		[]byte(`{"org_id":42,"repos":[{"provider":"github","owner":"acme","name":"widget","external_id":"1"}]}`))
	if indexResp.Code != http.StatusOK {
		t.Fatalf("index request returned %d: %s", indexResp.Code, indexResp.Body.String())
	}
	waitForCondition(t, "repository index", func() bool {
		indexed, err := codeIndex.HasOrgIndex(ctx, 42)
		return err == nil && indexed
	})

	chatResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat",
		[]byte(`{"organization_id":42,"query":"why does ValidateCart in checkout.go reject my order?","user_org_context":{"user_id":7}}`))
	if chatResp.Code != http.StatusOK {
		t.Fatalf("chat returned %d: %s", chatResp.Code, chatResp.Body.String())
	}
	var started struct {
		RunID                int64 `json:"run_id"`
		HasExplorerIndex     bool  `json:"has_explorer_index"`
		HasOrgProjectContext bool  `json:"has_org_project_context"`
	}
	if err := json.Unmarshal(chatResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if !started.HasExplorerIndex || !started.HasOrgProjectContext {
		t.Fatalf("expected the chat response to report the org index, got %#v", started)
	}
	waitForExplorerRun(t, server, 42, started.RunID)

	waitForCondition(t, "a prompt carrying indexed code", func() bool {
		return strings.Contains(recordedPrompt(), "Relevant repository code:")
	})
	if prompt := recordedPrompt(); !strings.Contains(prompt, "ValidateCart") {
		t.Fatalf("expected the indexed function in the prompt, got %q", prompt)
	}
}

// TestIndexSkipsRemovedRepositories checks that a repository Sentry removed
// through the project-preference endpoints is not indexed again.
func TestIndexSkipsRemovedRepositories(t *testing.T) {
	server, codeIndex, _ := newRepositoryBackedServer(t)
	ctx := context.Background()

	removeResp := issueRequest(server, http.MethodPost, "/v1/project-preference/remove-repository",
		[]byte(`{"organization_id":42,"repo_provider":"github","repo_external_id":"1"}`))
	if removeResp.Code != http.StatusOK {
		t.Fatalf("remove repository returned %d: %s", removeResp.Code, removeResp.Body.String())
	}

	indexResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/index/org-repo-knowledge",
		[]byte(`{"org_id":42,"repos":[{"provider":"github","owner":"acme","name":"widget","external_id":"1"}]}`))
	if indexResp.Code != http.StatusOK {
		t.Fatalf("index request returned %d: %s", indexResp.Code, indexResp.Body.String())
	}
	if indexed, err := codeIndex.HasOrgIndex(ctx, 42); err != nil {
		t.Fatalf("check org index: %v", err)
	} else if indexed {
		t.Fatal("expected a removed repository to stay out of the index")
	}
}

// waitForCondition polls until condition holds or the deadline passes.
func waitForCondition(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", what)
}
