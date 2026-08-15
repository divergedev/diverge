package registry_test

import (
	"sync"
	"testing"

	"github.com/divergedev/diverge/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyProvider struct {
	val string
}

func TestRegisterAndCreate(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	r.Register("foo", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) {
			return &dummyProvider{val: "bar"}, nil
		},
		Description: "foo provider",
	})

	p, err := r.Create("foo", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "bar", p.val)
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	r.Register("foo", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) {
			return &dummyProvider{}, nil
		},
	})

	assert.Panics(t, func() {
		r.Register("foo", registry.Provider[*dummyProvider]{
			Create: func(deps registry.Deps) (*dummyProvider, error) {
				return &dummyProvider{}, nil
			},
		})
	})
}

func TestNilCreatePanics(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	assert.Panics(t, func() {
		r.Register("foo", registry.Provider[*dummyProvider]{
			Create: nil,
		})
	})
}

func TestCreateUnknownProvider(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	r.Register("a", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})
	r.Register("b", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})

	p, err := r.Create("c", registry.Deps{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `test provider "c" not found`)
	assert.Contains(t, err.Error(), `[a b]`)
	assert.Nil(t, p)
}

func TestList(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	r.Register("c", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})
	r.Register("a", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})
	r.Register("b", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})

	assert.Equal(t, []string{"a", "b", "c"}, r.List())
}

func TestDescribe(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	r.Register("a", registry.Provider[*dummyProvider]{
		Create:      func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
		Description: "A provider",
	})
	r.Register("b", registry.Provider[*dummyProvider]{
		Create:      func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
		Description: "B provider",
	})

	desc := r.Describe()
	assert.Equal(t, map[string]string{
		"a": "A provider",
		"b": "B provider",
	}, desc)
}

func TestConcurrentAccess(t *testing.T) {
	r := registry.New[*dummyProvider]("test")

	// Pre-register some to test concurrent read/write and reads
	r.Register("initial", registry.Provider[*dummyProvider]{
		Create: func(deps registry.Deps) (*dummyProvider, error) { return nil, nil },
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// Register
			name := "p" + string(rune(i))
			r.Register(name, registry.Provider[*dummyProvider]{
				Create: func(deps registry.Deps) (*dummyProvider, error) {
					return &dummyProvider{val: name}, nil
				},
			})

			// Create
			_, _ = r.Create("initial", registry.Deps{})
			_, _ = r.Create(name, registry.Deps{})

			// Read
			_ = r.List()
			_ = r.Describe()
		}(i)
	}
	wg.Wait()
}
