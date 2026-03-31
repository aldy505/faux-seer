package handler

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Server) autofixStore() interface {
	GetProjectPreference(context.Context, int64) (json.RawMessage, error)
	PutProjectPreference(context.Context, int64, int64, any) error
} {
	return s.store
}

func (s *Server) autofixStoreGet(ctx context.Context, projectID int64) (map[string]any, error) {
	payload, err := s.autofixStore().GetProjectPreference(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("decode stored preference: %w", err)
	}
	return out, nil
}

func (s *Server) autofixStorePut(ctx context.Context, projectID, organizationID int64, pref map[string]any) error {
	return s.autofixStore().PutProjectPreference(ctx, projectID, organizationID, pref)
}
