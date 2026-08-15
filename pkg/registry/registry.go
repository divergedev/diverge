// Package registry provides a generic, thread-safe provider registry
// for Diverge extension points. Each provider kind (router, deployer,
// database, notifier) instantiates its own Registry[T] to manage
// named provider factories.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deps holds the universal dependencies passed to provider factories.
type Deps struct {
	// Client is the controller-runtime Kubernetes client.
	Client client.Client
	// Scheme is the runtime scheme with registered API types.
	Scheme *runtime.Scheme
	// Logger is a structured logger for the provider.
	Logger logr.Logger
}

// Provider describes a named provider factory.
type Provider[T any] struct {
	// Create constructs the provider from the given dependencies.
	Create func(deps Deps) (T, error)
	// Description is a one-line summary shown in --help output.
	Description string
}

// Registry is a thread-safe, named-factory store for a single provider kind.
type Registry[T any] struct {
	mu        sync.RWMutex
	kind      string
	providers map[string]Provider[T]
}

// New creates a new Registry for the given provider kind.
func New[T any](kind string) *Registry[T] {
	return &Registry[T]{
		kind:      kind,
		providers: make(map[string]Provider[T]),
	}
}

// Register adds a named provider factory to the registry.
// It panics if the name is already registered or if the factory is nil.
// This is typically called from init() functions.
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

// Create looks up a provider by name and invokes its factory.
// Returns a descriptive error listing available providers if not found.
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

// List returns the sorted names of all registered providers.
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

// Describe returns a map of provider names to their descriptions.
func (r *Registry[T]) Describe() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc := make(map[string]string, len(r.providers))
	for name, p := range r.providers {
		desc[name] = p.Description
	}
	return desc
}
