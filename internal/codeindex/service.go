// Package codeindex indexes repository source text so explorer runs can search
// it semantically.
package codeindex

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/vectorstore"
)

// maxCodeFileBytes bounds the size of a single indexed file. Larger files are
// generated artifacts or data, not code worth retrieving.
const maxCodeFileBytes = 1 << 20

// embedBatchSize bounds how many chunks are sent to the embedding provider in
// one request.
const embedBatchSize = 64

// binaryExtensions lists file types that never carry searchable source text.
var binaryExtensions = map[string]struct{}{
	".avif": {}, ".bmp": {}, ".class": {}, ".dll": {}, ".eot": {}, ".exe": {},
	".gif": {}, ".gz": {}, ".ico": {}, ".jar": {}, ".jpeg": {}, ".jpg": {},
	".lock": {}, ".mov": {}, ".mp3": {}, ".mp4": {}, ".o": {}, ".otf": {},
	".pdf": {}, ".png": {}, ".pyc": {}, ".so": {}, ".svgz": {}, ".tar": {},
	".ttf": {}, ".wav": {}, ".webm": {}, ".webp": {}, ".woff": {}, ".woff2": {},
	".zip": {},
}

// Service indexes repositories and answers semantic code queries.
type Service struct {
	provider     git.Provider
	embeddings   embedding.Client
	store        vectorstore.Store
	chunkSize    int
	chunkOverlap int
}

// New creates a code index service. A nil provider means no repository host is
// configured; indexing then reports git.ErrNotConfigured.
func New(provider git.Provider, embeddings embedding.Client, store vectorstore.Store, chunkSize, chunkOverlap int) *Service {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize / 5
	}
	return &Service{provider: provider, embeddings: embeddings, store: store, chunkSize: chunkSize, chunkOverlap: chunkOverlap}
}

// IndexRequest identifies the repository content to index.
type IndexRequest struct {
	OrganizationID int64
	Provider       string
	Owner          string
	Name           string
	Ref            string
}

// IndexStats reports what an indexing pass stored.
type IndexStats struct {
	Ref          string `json:"ref"`
	FilesIndexed int    `json:"files_indexed"`
	FilesSkipped int    `json:"files_skipped"`
	ChunksStored int    `json:"chunks_stored"`
}

// SearchOptions narrows a code search.
type SearchOptions struct {
	OrganizationID int64
	Provider       string
	Owner          string
	Name           string
	Ref            string
	K              int
}

// ErrNotConfigured reports that no repository host is configured.
var ErrNotConfigured = git.ErrNotConfigured

// IndexRepo fetches a repository tree, chunks its text files, embeds them, and
// replaces the repository's stored index.
//
// Indexing replaces every previously stored chunk for the repository because a
// stale chunk whose file shrank cannot be identified from the new tree alone.
func (s *Service) IndexRepo(ctx context.Context, request IndexRequest) (IndexStats, error) {
	if s.provider == nil {
		return IndexStats{}, fmt.Errorf("%w: no git provider is configured", ErrNotConfigured)
	}
	if request.Owner == "" || request.Name == "" {
		return IndexStats{}, fmt.Errorf("repository owner and name are required")
	}
	providerName := request.Provider
	if providerName == "" {
		providerName = s.provider.Name()
	}
	ref := request.Ref
	if ref == "" {
		defaultBranch, err := s.provider.GetDefaultBranch(ctx, request.Owner, request.Name)
		if err != nil {
			return IndexStats{}, err
		}
		ref = defaultBranch
	}
	tree, err := s.provider.ReadTree(ctx, request.Owner, request.Name, ref)
	if err != nil {
		return IndexStats{}, err
	}

	stats := IndexStats{Ref: ref}
	var chunks []vectorstore.CodeChunk
	for _, entry := range tree {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if !entry.IsFile() || !indexablePath(entry.Path) || entry.Size > maxCodeFileBytes {
			stats.FilesSkipped++
			continue
		}
		content, err := s.provider.ReadFile(ctx, request.Owner, request.Name, ref, entry.Path)
		if err != nil {
			if errors.Is(err, git.ErrNotFound) {
				stats.FilesSkipped++
				continue
			}
			return stats, err
		}
		if len(content) == 0 || len(content) > maxCodeFileBytes || !utf8.Valid(content) || bytesLookBinary(content) {
			stats.FilesSkipped++
			continue
		}
		fileChunks := chunkText(string(content), s.chunkSize, s.chunkOverlap)
		if len(fileChunks) == 0 {
			stats.FilesSkipped++
			continue
		}
		stats.FilesIndexed++
		for index, chunk := range fileChunks {
			chunks = append(chunks, vectorstore.CodeChunk{
				OrganizationID: request.OrganizationID,
				Provider:       providerName,
				Owner:          request.Owner,
				Name:           request.Name,
				Ref:            ref,
				Path:           entry.Path,
				ChunkIndex:     index,
				StartLine:      chunk.startLine,
				EndLine:        chunk.endLine,
				Text:           chunk.text,
			})
		}
	}
	if len(chunks) == 0 {
		return stats, nil
	}
	if err := s.embedChunks(ctx, chunks); err != nil {
		return stats, err
	}
	if _, err := s.store.DeleteCodeRepo(ctx, request.OrganizationID, providerName, request.Owner, request.Name); err != nil {
		return stats, err
	}
	if err := s.store.UpsertCodeChunks(ctx, chunks); err != nil {
		return stats, err
	}
	stats.ChunksStored = len(chunks)
	return stats, nil
}

// Search embeds a query and returns the closest indexed code chunks.
func (s *Service) Search(ctx context.Context, query string, options SearchOptions) ([]vectorstore.CodeResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if s.embeddings == nil {
		return nil, fmt.Errorf("embedding client is not configured")
	}
	vectors, err := s.embeddings.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embedding provider returned no vector for the query")
	}
	k := options.K
	if k <= 0 {
		k = 6
	}
	return s.store.SearchCode(ctx, vectors[0], k, vectorstore.CodeFilters{
		OrganizationID: options.OrganizationID,
		Provider:       options.Provider,
		Owner:          options.Owner,
		Name:           options.Name,
		Ref:            options.Ref,
	})
}

// DeleteRepo removes one organization's stored chunks for a repository.
//
// The organization is part of the key because two organizations can index the
// same repository, and deleting one must not drop the other's index.
func (s *Service) DeleteRepo(ctx context.Context, organizationID int64, provider, owner, name string) error {
	if s.store == nil {
		return fmt.Errorf("vector store is not configured")
	}
	if organizationID == 0 {
		return fmt.Errorf("organization id is required to delete an index")
	}
	if provider == "" && s.provider != nil {
		provider = s.provider.Name()
	}
	_, err := s.store.DeleteCodeRepo(ctx, organizationID, provider, owner, name)
	return err
}

// HasOrgIndex reports whether any repository content is indexed for an org.
func (s *Service) HasOrgIndex(ctx context.Context, organizationID int64) (bool, error) {
	if s.store == nil || organizationID == 0 {
		return false, nil
	}
	return s.store.HasCodeChunks(ctx, organizationID)
}

// Indexed reports whether repository access is available at all.
func (s *Service) Indexed() bool { return s != nil && s.provider != nil }

func (s *Service) embedChunks(ctx context.Context, chunks []vectorstore.CodeChunk) error {
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := min(start+embedBatchSize, len(chunks))
		batch := chunks[start:end]
		texts := make([]string, 0, len(batch))
		for _, chunk := range batch {
			texts = append(texts, chunk.Text)
		}
		vectors, err := s.embeddings.EmbedTexts(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed code chunks: %w", err)
		}
		if len(vectors) != len(batch) {
			return fmt.Errorf("embedding provider returned %d vectors for %d chunks", len(vectors), len(batch))
		}
		for index := range batch {
			batch[index].Vector = vectors[index]
		}
	}
	return nil
}

// indexablePath reports whether a repository path holds searchable source text.
func indexablePath(filePath string) bool {
	base := path.Base(filePath)
	if base == "" || base == "." || base == ".." {
		return false
	}
	if _, skip := binaryExtensions[strings.ToLower(path.Ext(base))]; skip {
		return false
	}
	for _, segment := range strings.Split(filePath, "/") {
		switch segment {
		case ".git", "node_modules", "vendor", "dist", "build", ".venv", "venv", "__pycache__":
			return false
		}
	}
	return true
}

// bytesLookBinary reports whether content is binary, using a NUL byte in the
// first block as the signal.
func bytesLookBinary(content []byte) bool {
	limit := min(len(content), 512)
	for _, value := range content[:limit] {
		if value == 0 {
			return true
		}
	}
	return false
}

// textChunk is a slice of file text with its line span.
type textChunk struct {
	text      string
	startLine int
	endLine   int
}

// chunkText splits text into chunks of at most size runes, overlapping by
// overlap runes, and records the line span of each chunk.
func chunkText(text string, size, overlap int) []textChunk {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	step := size - overlap
	if step <= 0 {
		step = size
	}
	var chunks []textChunk
	for start := 0; start < len(runes); start += step {
		end := min(start+size, len(runes))
		segment := string(runes[start:end])
		if strings.TrimSpace(segment) == "" {
			if end == len(runes) {
				break
			}
			continue
		}
		chunks = append(chunks, textChunk{
			text:      segment,
			startLine: lineNumber(runes, start),
			endLine:   lineNumber(runes, end),
		})
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// lineNumber returns the one-based line containing the rune offset.
func lineNumber(runes []rune, offset int) int {
	line := 1
	for index := 0; index < offset && index < len(runes); index++ {
		if runes[index] == '\n' {
			line++
		}
	}
	return line
}
