package config

import "sync/atomic"

// ModelSelector round-robins across a configured list of model names so that
// concurrent requests spread provider load instead of pinning one model.
type ModelSelector struct {
	models  []string
	counter uint64
}

// NewModelSelector creates a selector over the given model names.
func NewModelSelector(models []string) *ModelSelector {
	return &ModelSelector{models: models}
}

// Next returns the next model name, cycling through the configured list.
// It returns an empty string when no models are configured.
func (m *ModelSelector) Next() string {
	if len(m.models) == 0 {
		return ""
	}
	index := atomic.AddUint64(&m.counter, 1) - 1
	return m.models[index%uint64(len(m.models))]
}
