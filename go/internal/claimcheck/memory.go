// In-memory backend: tests only (unit tests and the workflow test suite's activity tests).
package claimcheck

import (
	"context"
	"sync"
)

// Memory is a map-backed Store.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
	reads   []string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{objects: map[string][]byte{}} }

func (m *Memory) Name() string               { return "memory" }
func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = append([]byte(nil), data...)
	return nil
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads = append(m.reads, key)
	data, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

// Reads returns every key read so far, in order (tests assert that only the needed pages are read).
func (m *Memory) Reads() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.reads...)
}
