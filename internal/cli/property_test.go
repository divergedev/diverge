package cli

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateEnvNameAlwaysValid(t *testing.T) {
	for i := 0; i < 1000; i++ {
		mr := 0
		branch := ""
		if rand.Float32() < 0.5 {
			mr = rand.Intn(9999) + 1 // 1..9999, never 0
		} else {
			branch = fmt.Sprintf("branch-%d", rand.Intn(100000))
		}

		name := generateEnvName("preview", mr, branch)
		assert.NotEmpty(t, name)
		assert.True(t, len(name) <= 63, "name %s exceeds 63 characters", name)
		assert.NotEqual(t, byte('-'), name[len(name)-1], "name %s ends with -", name)
	}
}
