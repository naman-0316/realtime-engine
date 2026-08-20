package game

import (
	"fmt"
	"sort"
	"sync"
)

// Registry maps game-type names (e.g. "tictactoe") to Factories, letting the
// service layer create Game instances by name without importing any
// concrete game package. Safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a Factory under name, overwriting any existing registration.
func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// New constructs a fresh Game instance for the given registered name.
func (r *Registry) New(name string) (Game, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("game: unknown game type %q", name)
	}
	return factory(), nil
}

// Names returns the sorted list of registered game-type names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
