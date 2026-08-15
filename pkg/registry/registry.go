package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deps holds universal dependencies passed to provider factories.
type Deps struct {
	Client client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger
}

// Provider describes a named provider factory.
type Provider[T any] struct {
	Create      func(deps Deps) (T, error)
	Description string
}

// Registry is a thread-safe, named-factory store for a single provider kind.
type Registry[T any] struct {
	mu        sync.RWMutex
	kind      string
	providers map[string]Provider[T]
}

func New[T any](kind string) *Registry[T] {
	return &Registry[T]{kind: kind, providers: make(map[string]Provider[T])}
}

func (r *Registry[T]) Register(name string, p Provider[T]) {
	if p.Create == nil {
		panic(fmt.Sprintf("%s provider %q: Create function must not be nil", r.kind, name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		panic(fmt.Sprintf("%s provider %q already registered", r.kind, name))
	}
	r.providers[name] = p
}

func (r *Registry[T]) Create(name string, deps Deps) (T, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		var zero T
		return zero, fmt.Errorf("%s provider %q not found; available: %v", r.kind, name, r.List())
	}
	return p.Create(deps)
}

func (r *Registry[T]) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry[T]) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

func (r *Registry[T]) Describe() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc := make(map[string]string, len(r.providers))
	for name, p := range r.providers {
		desc[name] = p.Description
	}
	return desc
}

// Plugin registry implemented
