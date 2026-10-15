package async

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// RedisProvisioner provisions key-prefix isolation for Redis and Valkey preview environments
// and evicts preview-scoped keys via non-blocking SCAN + UNLINK upon environment teardown.
type RedisProvisioner struct {
	Addr     string // Optional Redis/Valkey host:port for active key eviction on Teardown
	Password string // Optional AUTH password
	DB       int    // Optional logical DB number
	Provider string // "redis" or "valkey" (default "redis")
}

// Name returns the provisioner name ("redis" or "valkey").
func (r *RedisProvisioner) Name() string {
	if strings.TrimSpace(r.Provider) != "" {
		return strings.TrimSpace(r.Provider)
	}
	return "redis"
}

// FormatRedisKeyPrefix returns the deterministic key prefix for an environment and route target.
func FormatRedisKeyPrefix(target, envName string) string {
	trimmedTarget := strings.TrimSpace(target)
	if trimmedTarget == "" || trimmedTarget == "default" {
		return fmt.Sprintf("preview:%s:", envName)
	}
	return fmt.Sprintf("preview:%s:%s:", envName, trimmedTarget)
}

// Provision computes the preview Redis/Valkey key prefix and injects environment variables.
func (r *RedisProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	prefix := FormatRedisKeyPrefix(route.Target, env.Name)
	namespace := strings.TrimSuffix(prefix, ":")

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		envVars["DIVERGE_REDIS_PREFIX"] = prefix
		envVars["REDIS_KEY_PREFIX"] = prefix
		envVars["REDIS_NAMESPACE"] = namespace
		if addr := strings.TrimSpace(r.Addr); addr != "" {
			envVars["REDIS_ADDR"] = addr
		}
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = prefix
			} else {
				envVars[envVar] = strings.ReplaceAll(tmpl, "{{ .ResolvedTarget }}", prefix)
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: prefix,
		EnvVars:        envVars,
	}, nil
}

// Teardown connects to Redis/Valkey (if configured) and evicts all keys matching the preview prefix
// using cursor-based SCAN and non-blocking UNLINK.
func (r *RedisProvisioner) Teardown(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) error {
	addr := strings.TrimSpace(r.Addr)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("DIVERGE_REDIS_ADDR"))
	}
	if addr == "" {
		return nil
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("redis dial %s failed: %w", addr, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	password := r.Password
	if password == "" {
		password = os.Getenv("DIVERGE_REDIS_PASSWORD")
	}
	if password != "" {
		if _, err := sendRESPCommand(reader, writer, "AUTH", password); err != nil {
			return fmt.Errorf("redis AUTH failed: %w", err)
		}
	}

	if r.DB > 0 {
		if _, err := sendRESPCommand(reader, writer, "SELECT", strconv.Itoa(r.DB)); err != nil {
			return fmt.Errorf("redis SELECT %d failed: %w", r.DB, err)
		}
	}

	prefix := FormatRedisKeyPrefix(route.Target, env.Name)
	pattern := prefix + "*"
	cursor := "0"

	for {
		val, err := sendRESPCommand(reader, writer, "SCAN", cursor, "MATCH", pattern, "COUNT", "500")
		if err != nil {
			return fmt.Errorf("redis SCAN failed: %w", err)
		}

		arr, ok := val.([]any)
		if !ok || len(arr) != 2 {
			return fmt.Errorf("unexpected SCAN response format: %T", val)
		}

		nextCursor, ok := arr[0].(string)
		if !ok {
			return fmt.Errorf("unexpected SCAN cursor type: %T", arr[0])
		}

		var keys []string
		if rawKeys, ok := arr[1].([]any); ok {
			for _, k := range rawKeys {
				if ks, ok := k.(string); ok && ks != "" {
					keys = append(keys, ks)
				}
			}
		}

		if len(keys) > 0 {
			unlinkArgs := append([]string{"UNLINK"}, keys...)
			if _, err := sendRESPCommand(reader, writer, unlinkArgs...); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unknown command") {
					delArgs := append([]string{"DEL"}, keys...)
					if _, delErr := sendRESPCommand(reader, writer, delArgs...); delErr != nil {
						return fmt.Errorf("redis DEL failed: %w", delErr)
					}
				} else {
					return fmt.Errorf("redis UNLINK failed: %w", err)
				}
			}
		}

		if nextCursor == "0" {
			break
		}
		cursor = nextCursor
	}

	return nil
}

func sendRESPCommand(r *bufio.Reader, w *bufio.Writer, args ...string) (any, error) {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return nil, err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return nil, err
		}
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}
	return readRESPValue(r)
}

func readRESPValue(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) == 0 {
		return nil, fmt.Errorf("empty RESP response line")
	}

	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, fmt.Errorf("%s", line[1:])
	case ':':
		n, err := strconv.ParseInt(line[1:], 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case '$':
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if length < 0 {
			return "", nil
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:length]), nil
	case '*':
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if count <= 0 {
			return []any{}, nil
		}
		items := make([]any, 0, count)
		for i := 0; i < count; i++ {
			item, err := readRESPValue(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported RESP prefix %q", line[0])
	}
}
