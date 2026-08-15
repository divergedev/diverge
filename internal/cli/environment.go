package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
)

var (
	// ErrTailscaleNotFound ...
	ErrTailscaleNotFound = errors.New("tailscale interface not found: make sure tailscale is running or pass --endpoint")
	// ErrNoGitRepo ...
	ErrNoGitRepo = errors.New("not in a git repository")
)

// EnvironmentDetector abstracts OS/network dependencies for testability.
type EnvironmentDetector interface {
	// DetectLocalIP returns the developer's routable IP (e.g., Tailscale).
	DetectLocalIP(ctx context.Context) (string, error)

	// DetectGitBranch returns the current git branch name.
	DetectGitBranch(ctx context.Context) (string, error)

	// DetectServiceName returns the service name from .diverge.yaml or cwd.
	DetectServiceName(ctx context.Context) (string, error)

	// DetectUsername returns the current OS username.
	DetectUsername(ctx context.Context) (string, error)
}

// DefaultEnvironmentDetector represents the configuration or state for this type.
type DefaultEnvironmentDetector struct{}

// DetectLocalIP performs its designated operation.
func (d *DefaultEnvironmentDetector) DetectLocalIP(ctx context.Context) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if iface.Name == "tailscale0" || strings.HasPrefix(iface.Name, "utun") || iface.Name == "wg0" {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
					continue
				}
				ip = ip.To4()
				if ip != nil {
					return ip.String(), nil
				}
			}
		}
	}

	// Fallback
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ip = ip.To4()
			if ip != nil {
				return ip.String(), nil
			}
		}
	}
	return "", ErrTailscaleNotFound
}

// DetectGitBranch performs its designated operation.
func (d *DefaultEnvironmentDetector) DetectGitBranch(ctx context.Context) (string, error) {
	gitCtx, err := git.Detect()
	if err != nil || gitCtx == nil || gitCtx.Branch == "" {
		return "", ErrNoGitRepo
	}
	return gitCtx.Branch, nil
}

// DetectServiceName performs its designated operation.
func (d *DefaultEnvironmentDetector) DetectServiceName(ctx context.Context) (string, error) {
	cfg, err := config.Load(".diverge.yaml")
	if err == nil && len(cfg.Services) > 0 {
		for k := range cfg.Services {
			return k, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Base(cwd), nil
}

// DetectUsername performs its designated operation.
func (d *DefaultEnvironmentDetector) DetectUsername(ctx context.Context) (string, error) {
	u, err := user.Current()
	if err == nil && u != nil && u.Username != "" {
		return strings.ToLower(u.Username), nil
	}
	return "dev", nil
}
