package auth

import (
	"context"
	"sync"
)

// Storage persists auth session and PKCE state across requests.
type Storage interface {
	GetItem(ctx context.Context, key string) (string, error)
	SetItem(ctx context.Context, key, value string) error
	RemoveItem(ctx context.Context, key string) error
}

// MemoryStorage is a concurrency-safe in-memory Storage implementation.
type MemoryStorage struct {
	mu    sync.RWMutex
	items map[string]string
}

// NewMemoryStorage creates a new empty MemoryStorage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{items: make(map[string]string)}
}

func (m *MemoryStorage) GetItem(_ context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[key], nil
}

func (m *MemoryStorage) SetItem(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = make(map[string]string)
	}
	m.items[key] = value
	return nil
}

func (m *MemoryStorage) RemoveItem(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

// Lock serializes critical auth session mutations (such as token refresh).
type Lock interface {
	Acquire(ctx context.Context, name string, fn func(ctx context.Context) error) error
}

// MutexLock is a context-aware in-process Lock backed by per-key channel semaphores.
type MutexLock struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

// NewMutexLock creates a new MutexLock.
func NewMutexLock() *MutexLock {
	return &MutexLock{locks: make(map[string]chan struct{})}
}

func (l *MutexLock) Acquire(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]chan struct{})
	}
	ch, ok := l.locks[name]
	if !ok {
		ch = make(chan struct{}, 1)
		l.locks[name] = ch
	}
	l.mu.Unlock()

	select {
	case ch <- struct{}{}:
		defer func() { <-ch }()
		return fn(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}
