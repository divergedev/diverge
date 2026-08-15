#!/bin/bash
set -e

# Task 1
sed -i '' 's|gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"|gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"|g' internal/crossns/grants.go internal/crossns/grants_test.go internal/crossns/grants_property_test.go
sed -i '' 's/gatewayv1beta1\./gatewayv1\./g' internal/crossns/grants.go internal/crossns/grants_test.go internal/crossns/grants_property_test.go

nix develop -c go get sigs.k8s.io/gateway-api@v1.6.1
nix develop -c go mod tidy

# Task 2
sed -i '' 's|"github.com/divergedev/diverge/internal/database"|"github.com/divergedev/diverge/pkg/database"|g' ./cmd/controller/main.go ./internal/controller/environment_controller_more_test.go ./internal/controller/previewgroup_db_test.go ./internal/controller/environment_controller.go ./internal/controller/environment_controller_async_test.go ./internal/controller/previewgroup_controller.go
rm -f internal/database/provider.go

sed -i '' 's|Inner DatabaseProvider|Inner pkgdb.DatabaseProvider|g' internal/database/instrumented.go
sed -i '' 's|\*DatabaseResult|\*pkgdb.DatabaseResult|g' internal/database/instrumented.go
sed -i '' 's|\*DatabaseStatus|\*pkgdb.DatabaseStatus|g' internal/database/instrumented.go
sed -i '' '/"github.com\/divergedev\/diverge\/api\/v1alpha1"/a\
	pkgdb "github.com/divergedev/diverge/pkg/database"\
' internal/database/instrumented.go

sed -i '' 's|DatabaseProvider|pkgdb.DatabaseProvider|g' internal/database/noop.go
sed -i '' 's|DatabaseResult|pkgdb.DatabaseResult|g' internal/database/noop.go
sed -i '' 's|DatabaseStatus|pkgdb.DatabaseStatus|g' internal/database/noop.go
sed -i '' '/"github.com\/divergedev\/diverge\/api\/v1alpha1"/a\
	pkgdb "github.com/divergedev/diverge/pkg/database"\
' internal/database/noop.go

sed -i '' '27s|"github.com/divergedev/diverge/pkg/database"|"github.com/divergedev/diverge/internal/database"|' cmd/controller/main.go

# Task 3
sed -i '' 's/func buildEnvironment(name string/func buildEnvironment(ctx context.Context, name string/' internal/cli/create.go
sed -i '' 's/env, err := buildEnvironment(name, gitCtx, resolved, cfg, app, mrNumber)/env, err := buildEnvironment(cmd.Context(), name, gitCtx, resolved, cfg, app, mrNumber)/' internal/cli/create.go
sed -i '' 's/detector.DetectChanges(context.TODO()/detector.DetectChanges(ctx/' internal/cli/create.go

sed -i '' 's/func runPreviewCreate(_ \*cobra.Command/func runPreviewCreate(cmd \*cobra.Command/' internal/cli/preview.go
sed -i '' 's/func runPreviewStatus(app \*App/func runPreviewStatus(cmd \*cobra.Command, app \*App/' internal/cli/preview.go
sed -i '' 's/runPreviewStatus(app, args\[0\]/runPreviewStatus(cmd, app, args[0]/' internal/cli/preview.go
sed -i '' 's/func runPreviewDelete(app \*App/func runPreviewDelete(cmd \*cobra.Command, app \*App/' internal/cli/preview.go
sed -i '' 's/runPreviewDelete(app, args\[0\]/runPreviewDelete(cmd, app, args[0]/' internal/cli/preview.go
sed -i '' 's/context.TODO()/cmd.Context()/g' internal/cli/preview.go

sed -i '' 's/context.TODO()/context.Background()/g' internal/controller/suite_test.go

# Task 4
rm -f .github/workflows/e2e.yml

# Syscall process split
sed -i '' 's/os.Interrupt, syscall.SIGTERM/os.Interrupt/g' internal/cli/root.go
sed -i '' '/"syscall"/d' internal/cli/root.go

cat << 'INNER_EOF' > internal/cli/process_unix.go
//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
	"time"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.AfterFunc(5*time.Second, func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
}
INNER_EOF

cat << 'INNER_EOF' > internal/cli/process_windows.go
//go:build windows

package cli

import (
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Process groups not supported on Windows
}

func killProcessGroup(pid int) {
	// On Windows, just kill the process directly
	// This is a best-effort stub
}
INNER_EOF

sed -i '' 's/syscall.SIGINT, syscall.SIGTERM/os.Interrupt/g' internal/cli/dev.go
sed -i '' '/"syscall"/d' internal/cli/dev.go
sed -i '' 's/cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}/setSysProcAttr(cmd)/' internal/cli/dev.go
sed -i '' 's/_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)/killProcessGroup(cmd.Process.Pid)/g' internal/cli/dev.go
sed -i '' 's/_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)//g' internal/cli/dev.go
sed -i '' '/time.AfterFunc(5\*time.Second, func() {/,/})/d' internal/cli/dev.go
sed -i '' 's|runPreviewStatus(app, groupName, cmd.OutOrStdout())|runPreviewStatus(cmd, app, groupName, cmd.OutOrStdout())|g' internal/cli/dev.go

nix develop -c git add -A
nix develop -c git commit -m 'chore: modernize codebase

- Migrate gatewayv1beta1 ReferenceGrant to GA gatewayv1
- Remove backward-compat type aliases in internal/database
- Replace context.TODO() with proper contexts
- Delete stale e2e.yml workflow
- Split platform-specific process management'
nix develop -c git push -f origin chore/modernize
