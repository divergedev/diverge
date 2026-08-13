package database_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/divergedev/diverge/pkg/database"
	"hegel.dev/go/hegel"
)

// validProviderName generates a plausible provider name.
func validProviderName(ht *hegel.T) string {
	names := []string{"neon", "atlas", "supabase", "planetscale", "turso", "crunchy", "schemahero"}
	base := names[hegel.Draw(ht, hegel.Integers(0, len(names)-1))]
	suffix := hegel.Draw(ht, hegel.Integers(0, 999))
	return fmt.Sprintf("%s-%d", base, suffix)
}

func TestProperty_RegisterN_ListReturnsAll(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		database.ResetRegistry()
		defer database.ResetRegistry()

		n := hegel.Draw(ht, hegel.Integers(0, 20))
		registered := make(map[string]bool)
		for range n {
			name := validProviderName(ht)
			if registered[name] {
				continue // skip duplicates
			}
			database.RegisterProvider(name, func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
				return nil, nil
			})
			registered[name] = true
		}

		listed := database.RegisteredProviders()
		if len(listed) != len(registered) {
			ht.Fatalf("registered %d, listed %d", len(registered), len(listed))
		}
		for _, name := range listed {
			if !registered[name] {
				ht.Fatalf("listed unknown provider %q", name)
			}
		}
	})
}

func TestProperty_ConcurrentGetNeverPanics(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		database.ResetRegistry()
		defer database.ResetRegistry()

		// Register some providers
		n := hegel.Draw(ht, hegel.Integers(1, 10))
		names := make([]string, n)
		for i := range n {
			name := fmt.Sprintf("provider-%d", i)
			names[i] = name
			database.RegisterProvider(name, func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
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
				idx := 0
				for range 100 {
					name := names[idx%len(names)]
					database.GetProvider(name)
					database.RegisteredProviders()
					idx++
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

		name := validProviderName(ht)
		database.RegisterProvider(name, func(cfg database.ProviderConfig) (database.DatabaseProvider, error) {
			return nil, nil
		})

		factory, ok := database.GetProvider(name)
		if !ok {
			ht.Fatalf("GetProvider(%q) returned false after registration", name)
		}
		if factory == nil {
			ht.Fatalf("GetProvider(%q) returned nil factory after registration", name)
		}

		// Case-different name should NOT be found
		upper := strings.ToUpper(name)
		if upper != name {
			_, ok := database.GetProvider(upper)
			if ok {
				ht.Fatalf("GetProvider(%q) found case-different name %q", upper, name)
			}
		}
	})
}
