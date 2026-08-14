package cli

import (
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
	ErrTailscaleNotFound = errors.New("tailscale interface not found: make sure tailscale is running or pass --endpoint")
	ErrNoGitRepo         = errors.New("not in a git repository")
)

// EnvironmentDetector abstracts OS/network dependencies for testability.
type EnvironmentDetector interface {
	// DetectTailscaleIP returns the local Tailscale IPv4 address.
	// It scans net.Interfaces() for interfaces named 'tailscale0', 'utun*', or 'wg0'.
	DetectTailscaleIP() (string, error)

	// DetectGitBranch returns the current git branch name.
	DetectGitBranch() (string, error)

	// DetectServiceName returns the service name from .diverge.yaml or cwd.
	DetectServiceName() (string, error)

	// DetectUsername returns the current OS username.
	DetectUsername() string
}

type DefaultEnvironmentDetector struct{}

func (d *DefaultEnvironmentDetector) DetectTailscaleIP() (string, error) {
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
				if ip == nil || ip.IsLoopback() {
					continue
				}
				ip = ip.To4()
				if ip != nil {
					return ip.String(), nil
				}
			}
		}
	}
	return "", ErrTailscaleNotFound
}

func (d *DefaultEnvironmentDetector) DetectGitBranch() (string, error) {
	gitCtx, err := git.Detect()
	if err != nil || gitCtx == nil || gitCtx.Branch == "" {
		return "", ErrNoGitRepo
	}
	return gitCtx.Branch, nil
}

func (d *DefaultEnvironmentDetector) DetectServiceName() (string, error) {
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

func (d *DefaultEnvironmentDetector) DetectUsername() string {
	u, err := user.Current()
	if err == nil && u != nil && u.Username != "" {
		return strings.ToLower(u.Username)
	}
	return "dev"
}
