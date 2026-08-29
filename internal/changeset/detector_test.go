package changeset

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestMatchServicesToFiles(t *testing.T) {
	servicePaths := map[string][]string{
		"api":      {"services/api"},
		"payments": {"services/payments"},
		"frontend": {"web/src", "web/public"},
		"infra":    {"deploy/"},
	}

	tests := []struct {
		name     string
		files    []string
		expected []string
	}{
		{
			name:     "single service match",
			files:    []string{"services/api/main.go", "services/api/handler.go"},
			expected: []string{"api"},
		},
		{
			name:     "multiple services match",
			files:    []string{"services/api/main.go", "services/payments/charge.go"},
			expected: []string{"api", "payments"},
		},
		{
			name:     "multi-path service match",
			files:    []string{"web/src/App.tsx"},
			expected: []string{"frontend"},
		},
		{
			name:     "no match",
			files:    []string{"README.md", "docs/guide.md"},
			expected: nil,
		},
		{
			name:     "empty files",
			files:    []string{},
			expected: nil,
		},
		{
			name:     "trailing slash path match",
			files:    []string{"deploy/helm/values.yaml"},
			expected: []string{"infra"},
		},
		{
			name:     "exact file match",
			files:    []string{"services/api"},
			expected: []string{"api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchServicesToFiles(tt.files, servicePaths)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchServicesToFiles_NoPaths(t *testing.T) {
	// Services with no paths configured match any changed file
	servicePaths := map[string][]string{
		"catch-all": {},
	}

	result := matchServicesToFiles([]string{"anything.go"}, servicePaths)
	assert.Equal(t, []string{"catch-all"}, result)
}

func TestMatchServicesToFiles_Dedup(t *testing.T) {
	servicePaths := map[string][]string{
		"api": {"services/api"},
	}

	// Multiple files in same service should only appear once
	result := matchServicesToFiles(
		[]string{"services/api/main.go", "services/api/handler.go", "services/api/test.go"},
		servicePaths,
	)
	assert.Equal(t, []string{"api"}, result)
}

func TestMatchesServicePaths(t *testing.T) {
	assert.True(t, matchesServicePaths("services/api/main.go", []string{"services/api"}))
	assert.True(t, matchesServicePaths("services/api", []string{"services/api"}))
	assert.False(t, matchesServicePaths("services/api-v2/main.go", []string{"services/api"}))
	assert.True(t, matchesServicePaths("anything.go", []string{}))
	assert.True(t, matchesServicePaths("anything.go", []string{"."}))
	assert.True(t, matchesServicePaths("Makefile", []string{"Makefile"}))
}

func TestDetectChangesFromFiles(t *testing.T) {
	servicePaths := map[string][]string{
		"api":      {"services/api"},
		"payments": {"services/payments"},
	}

	result := DetectChangesFromFiles(
		[]string{"services/api/main.go", "README.md"},
		servicePaths,
	)
	assert.Equal(t, []string{"api"}, result)
}

// --- Property-Based Tests ---

// Property: output is always a subset of the input service names.
func TestPBT_MatchServicesToFiles_SubsetOfServices(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		servicePaths := drawServicePaths(t)
		files := drawFiles(t)

		result := matchServicesToFiles(files, servicePaths)

		serviceNames := make(map[string]bool)
		for name := range servicePaths {
			serviceNames[name] = true
		}
		for _, svc := range result {
			assert.True(t, serviceNames[svc],
				"result %q must be a known service name", svc)
		}
	})
}

// Property: output never contains duplicates.
func TestPBT_MatchServicesToFiles_NoDuplicates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		servicePaths := drawServicePaths(t)
		files := drawFiles(t)

		result := matchServicesToFiles(files, servicePaths)

		seen := make(map[string]bool)
		for _, svc := range result {
			assert.False(t, seen[svc],
				"duplicate service %q in result", svc)
			seen[svc] = true
		}
	})
}

// Property: output is always sorted.
func TestPBT_MatchServicesToFiles_Sorted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		servicePaths := drawServicePaths(t)
		files := drawFiles(t)

		result := matchServicesToFiles(files, servicePaths)

		assert.True(t, sort.StringsAreSorted(result),
			"result must be sorted, got %v", result)
	})
}

// Property: adding more files never removes a service from the result (monotonicity).
func TestPBT_MatchServicesToFiles_Monotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		servicePaths := drawServicePaths(t)
		files1 := drawFiles(t)
		files2 := drawFiles(t)

		result1 := matchServicesToFiles(files1, servicePaths)
		combined := append(append([]string{}, files1...), files2...)
		result2 := matchServicesToFiles(combined, servicePaths)

		// Every service matched by files1 alone must also be in the combined result
		resultSet := make(map[string]bool)
		for _, svc := range result2 {
			resultSet[svc] = true
		}
		for _, svc := range result1 {
			assert.True(t, resultSet[svc],
				"service %q matched with fewer files must still match with more files", svc)
		}
	})
}

// Property: empty file list always returns nil/empty result.
func TestPBT_MatchServicesToFiles_EmptyFilesEmptyResult(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		servicePaths := drawServicePaths(t)

		result := matchServicesToFiles([]string{}, servicePaths)

		assert.Empty(t, result,
			"empty file list must produce empty result")
	})
}

// --- PBT Generators ---

func drawServicePaths(t *rapid.T) map[string][]string {
	numServices := rapid.IntRange(1, 5).Draw(t, "numServices")
	servicePaths := make(map[string][]string)
	for i := 0; i < numServices; i++ {
		name := rapid.StringMatching(`^[a-z]{2,10}$`).Draw(t, "serviceName")
		numPaths := rapid.IntRange(1, 3).Draw(t, "numPaths")
		var paths []string
		for j := 0; j < numPaths; j++ {
			path := rapid.StringMatching(`^[a-z]{2,8}(/[a-z]{2,8}){0,2}$`).Draw(t, "path")
			paths = append(paths, path)
		}
		servicePaths[name] = paths
	}
	return servicePaths
}

func drawFiles(t *rapid.T) []string {
	numFiles := rapid.IntRange(0, 10).Draw(t, "numFiles")
	var files []string
	for i := 0; i < numFiles; i++ {
		file := rapid.StringMatching(`^[a-z]{2,8}(/[a-z]{2,8}){0,3}\.[a-z]{1,4}$`).Draw(t, "file")
		files = append(files, file)
	}
	return files
}
