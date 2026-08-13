package diverge

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestRootCmd(t *testing.T) {
	cmd := NewRootCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "diverge", cmd.Use)
}

func TestNewRootCmdReturnsFreshInstance(t *testing.T) {
	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd()
	assert.NotSame(t, cmd1, cmd2, "NewRootCmd should return fresh instances")
}

func TestRootCmdAllowsAddingCommands(t *testing.T) {
	// Simulate what diverge-enterprise does
	app := NewApp("dev", "none", "unknown")
	root := app.NewRootCmd()
	before := len(root.Commands())

	root.AddCommand(&cobra.Command{
		Use:   "test-enterprise-cmd",
		Short: "test",
	})

	assert.Equal(t, before+1, len(root.Commands()))
}
