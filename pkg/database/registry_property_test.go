package database_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/divergedev/diverge/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

// uniqueProviderName generates a guaranteed-unique provider name using a
// sequential index to avoid collisions in property-based tests.
func uniqueProviderName(i int) string {
	return fmt.Sprintf("provider-%d", i)
}

func TestProperty_RegisterN_ListReturnsAll(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		database.ResetRegistry()
		defer database.ResetRegistry()

		n := hegel.Draw(ht, hegel.Integers(0, 20))

		// Use sequential names to guarantee uniqueness
		for i := range n {
			database.RegisterProvider(uniqueProviderName(i), func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
				return nil, nil
			})
		}

		listed := database.RegisteredProviders()
		require.Len(ht, listed, n, "RegisteredProviders should return exactly N providers")
		for _, name := range listed {
			_, ok := database.GetProvider(name)
			assert.True(ht, ok, "listed provider %q should be retrievable", name)
		}
	})
}

func TestProperty_ConcurrentGetNeverPanics(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		database.ResetRegistry()
		defer database.ResetRegistry()

		n := hegel.Draw(ht, hegel.Integers(1, 10))
		names := make([]string, n)
		for i := range n {
			names[i] = uniqueProviderName(i)
			database.RegisterProvider(names[i], func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
				return nil, nil
			})
		}

		// Concurrent reads should never panic
		goroutines := hegel.Draw(ht, hegel.Integers(2, 20))
		var wg sync.WaitGroup
		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 100 {
					name := names[i%len(names)]
					database.GetProvider(name)
					database.RegisteredProviders()
				}
			}()
		}
		wg.Wait()
	})
}

func TestProperty_GetAfterRegisterAlwaysSucceeds(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		database.ResetRegistry()
		defer database.ResetRegistry()

		idx := hegel.Draw(ht, hegel.Integers(0, 9999))
		name := fmt.Sprintf("provider-%d", idx)
		database.RegisterProvider(name, func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
			return nil, nil
		})

		factory, ok := database.GetProvider(name)
		require.True(ht, ok, "GetProvider(%q) should succeed after registration", name)
		require.NotNil(ht, factory, "GetProvider(%q) factory should not be nil", name)

		// Case-different name should NOT be found
		upper := strings.ToUpper(name)
		if upper != name {
			_, found := database.GetProvider(upper)
			assert.False(ht, found, "GetProvider(%q) should not find case-different %q", upper, name)
		}
	})
}
