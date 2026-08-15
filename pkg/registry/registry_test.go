package registry_test

import (
	"sync"
	"testing"

	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
)

type DummyProvider struct {
	Name string
}

func TestRegistry(t *testing.T) {
	r := registry.New[DummyProvider]("dummy")

	// Test Register and Has
	assert.False(t, r.Has("test1"))
	r.Register("test1", registry.Provider[DummyProvider]{
		Create: func(deps registry.Deps) (DummyProvider, error) {
			return DummyProvider{Name: "test1"}, nil
		},
		Description: "Test 1 Provider",
	})
	assert.True(t, r.Has("test1"))

	// Test Create
	p, err := r.Create("test1", registry.Deps{})
	assert.NoError(t, err)
	assert.Equal(t, "test1", p.Name)

	// Test unknown provider error
	_, err = r.Create("unknown", registry.Deps{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dummy provider \"unknown\" not found")

	// Test List
	r.Register("test2", registry.Provider[DummyProvider]{
		Create: func(deps registry.Deps) (DummyProvider, error) {
			return DummyProvider{Name: "test2"}, nil
		},
		Description: "Test 2 Provider",
	})
	list := r.List()
	assert.Equal(t, []string{"test1", "test2"}, list)

	// Test Describe
	desc := r.Describe()
	assert.Equal(t, map[string]string{
		"test1": "Test 1 Provider",
		"test2": "Test 2 Provider",
	}, desc)

	// Test duplicate panics
	assert.PanicsWithValue(t, "dummy provider \"test1\" already registered", func() {
		r.Register("test1", registry.Provider[DummyProvider]{
			Create: func(deps registry.Deps) (DummyProvider, error) {
				return DummyProvider{}, nil
			},
		})
	})

	// Test nil Create panics
	assert.PanicsWithValue(t, "dummy provider \"nil-create\": Create function must not be nil", func() {
		r.Register("nil-create", registry.Provider[DummyProvider]{
			Create: nil,
		})
	})

	// Test concurrent access
	r2 := registry.New[DummyProvider]("concurrent")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "p" + string(rune(i))
			r2.Register(name, registry.Provider[DummyProvider]{
				Create: func(deps registry.Deps) (DummyProvider, error) {
					return DummyProvider{}, nil
				},
			})
			_ = r2.Has(name)
			_ = r2.List()
			_ = r2.Describe()
			_, _ = r2.Create(name, registry.Deps{})
		}(i)
	}
	wg.Wait()
}
