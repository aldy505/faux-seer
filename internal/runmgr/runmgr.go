// Package runmgr runs background work with process-lifetime cancellation.
//
// Runs are started by HTTP handlers that have already persisted the run row, so
// the manager only owns execution: it tracks in-flight tasks, cancels them on
// shutdown, and waits for them to finish.
package runmgr

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Task is one unit of background work.
//
// Implementations must observe ctx cancellation, persist a terminal status, and
// return. The context passed to Run is never a request context: it lives for the
// lifetime of the process so a run survives the HTTP response that started it.
type Task interface {
	Run(ctx context.Context) error
}

// TaskFunc adapts a function to the Task interface.
type TaskFunc func(context.Context) error

// Run implements Task.
func (f TaskFunc) Run(ctx context.Context) error { return f(ctx) }

// Manager owns in-flight tasks.
type Manager struct {
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	inflight map[string]context.CancelFunc
}

// New creates a manager whose tasks are cancelled when parent is cancelled.
func New(parent context.Context, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Manager{log: logger, ctx: ctx, cancel: cancel, inflight: map[string]context.CancelFunc{}}
}

// Start launches a task under a key that identifies the run, replacing any
// previous cancel handle for that key.
func (m *Manager) Start(key string, task Task) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	if previous, ok := m.inflight[key]; ok {
		previous()
	}
	m.inflight[key] = cancel
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() {
			m.mu.Lock()
			delete(m.inflight, key)
			m.mu.Unlock()
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				m.log.ErrorContext(ctx, "background run panicked", "run", key, "panic", recovered)
			}
		}()
		if err := task.Run(ctx); err != nil {
			m.log.ErrorContext(ctx, "background run failed", "run", key, "error", err)
		}
	}()
}

// Cancel stops one run.
func (m *Manager) Cancel(key string) {
	m.mu.Lock()
	cancel, ok := m.inflight[key]
	m.mu.Unlock()
	if ok {
		cancel()
	}
}

// CancelAll stops every run, including runs started later, by cancelling the
// process-lifetime context. Callers invoke this during shutdown.
func (m *Manager) CancelAll() { m.cancel() }

// Wait blocks until every in-flight run has returned.
func (m *Manager) Wait() { m.wg.Wait() }

// WaitContext blocks until every in-flight run has returned or ctx is done.
func (m *Manager) WaitContext(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for background runs: %w", ctx.Err())
	}
}

// Inflight reports the keys of the runs currently executing.
func (m *Manager) Inflight() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.inflight))
	for key := range m.inflight {
		keys = append(keys, key)
	}
	return keys
}
