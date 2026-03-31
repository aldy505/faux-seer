// Package vectorstorefactory creates the configured vector store implementation.
package vectorstorefactory

import (
	"context"
	"fmt"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/vectorstore"
	"github.com/aldy505/faux-seer/internal/vectorstore/pgvector"
	"github.com/aldy505/faux-seer/internal/vectorstore/sqlitevec"
)

// New returns the configured vector store.
func New(ctx context.Context, cfg *config.Config, store *db.Store) (vectorstore.Store, error) {
	switch cfg.VectorStore {
	case "sqlitevec":
		return sqlitevec.New(ctx, store, cfg.VectorDimensions)
	case "pgvector":
		return pgvector.New(ctx, cfg.VectorStoreDSN, cfg.VectorDimensions)
	default:
		return nil, fmt.Errorf("unsupported vector store %q", cfg.VectorStore)
	}
}
