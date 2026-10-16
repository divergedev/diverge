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

const (
	AnnotationRedisAddr = "divergedev.com/redis-addr"
	AnnotationRedisDB   = "divergedev.com/redis-db"
	maxRESPBulkBytes    = 16 * 1024 * 1024
	maxRESPArrayLen     = 100_000
	maxRESPDepth        = 8
)

// RedisProvisioner provisions key-prefix isolation for Redis and Valkey preview environments
// and evicts preview-scoped keys via non-blocking SCAN + UNLINK upon environment teardown.
type RedisProvisioner struct {
	Addr     string // Optional Redis/Valkey host:port for active key eviction on Teardown
	Username string // Optional ACL username (Redis 6+ / Valkey)
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

func (r *RedisProvisioner) resolveAddr(env *v1alpha1.Environment) string {
	if env != nil && env.Annotations != nil {
		if addr := strings.TrimSpace(env.Annotations[AnnotationRedisAddr]); addr != "" {
			return addr
		}
	}
	if addr := strings.TrimSpace(r.Addr); addr != "" {
		return addr
	}
	return strings.TrimSpace(os.Getenv("DIVERGE_REDIS_ADDR"))
}

func (r *RedisProvisioner) resolveDB(env *v1alpha1.Environment) (int, error) {
	if env != nil && env.Annotations != nil {
		if raw := strings.TrimSpace(env.Annotations[AnnotationRedisDB]); raw != "" {
			db, err := strconv.Atoi(raw)
			if err != nil || db < 0 {
				return 0, fmt.Errorf("invalid %s annotation %q", AnnotationRedisDB, raw)
			}
			return db, nil
		}
	}
	if r.DB < 0 {
		return 0, fmt.Errorf("invalid redis DB %d: must be >= 0", r.DB)
	}
	return r.DB, nil
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
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return nil, fmt.Errorf("environment with non-empty name is required")
	}

	prefix := FormatRedisKeyPrefix(route.Target, env.Name)
	namespace := strings.TrimSuffix(prefix, ":")

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		envVars["DIVERGE_REDIS_PREFIX"] = prefix
		envVars["REDIS_KEY_PREFIX"] = prefix
		envVars["REDIS_NAMESPACE"] = namespace
		if addr := r.resolveAddr(env); addr != "" {
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
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return fmt.Errorf("environment with non-empty name is required")
	}

	addr := r.resolveAddr(env)
	if addr == "" {
		return nil
	}

	db, err := r.resolveDB(env)
	if err != nil {
		return err
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

	username := strings.TrimSpace(r.Username)
	if username == "" {
		username = strings.TrimSpace(os.Getenv("DIVERGE_REDIS_USERNAME"))
	}
	password := r.Password
	if password == "" {
		password = os.Getenv("DIVERGE_REDIS_PASSWORD")
	}
	if password != "" {
		authArgs := []string{"AUTH", password}
		if username != "" {
			authArgs = []string{"AUTH", username, password}
		}
		if _, err := sendRESPCommand(reader, writer, authArgs...); err != nil {
			return fmt.Errorf("redis AUTH failed: %w", err)
		}
	}

	if db > 0 {
		if _, err := sendRESPCommand(reader, writer, "SELECT", strconv.Itoa(db)); err != nil {
			return fmt.Errorf("redis SELECT %d failed: %w", db, err)
		}
	}

	prefix := FormatRedisKeyPrefix(route.Target, env.Name)
	pattern := escapeRedisGlob(prefix) + "*"
	cursor := "0"

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

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
			if err := evictRedisKeys(reader, writer, keys); err != nil {
				return err
			}
		}

		if nextCursor == "0" {
			break
		}
		cursor = nextCursor
	}

	return nil
}

func evictRedisKeys(reader *bufio.Reader, writer *bufio.Writer, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	unlinkArgs := append([]string{"UNLINK"}, keys...)
	_, err := sendRESPCommand(reader, writer, unlinkArgs...)
	if err == nil {
		return nil
	}

	lowerErr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lowerErr, "unknown command") || strings.Contains(lowerErr, "noperm"):
		delArgs := append([]string{"DEL"}, keys...)
		if _, delErr := sendRESPCommand(reader, writer, delArgs...); delErr != nil {
			if strings.Contains(strings.ToLower(delErr.Error()), "crossslot") {
				for _, k := range keys {
					if _, singleErr := sendRESPCommand(reader, writer, "DEL", k); singleErr != nil {
						return fmt.Errorf("redis DEL %s failed: %w", k, singleErr)
					}
				}
				return nil
			}
			return fmt.Errorf("redis DEL failed: %w", delErr)
		}
		return nil
	case strings.Contains(lowerErr, "crossslot"):
		for _, k := range keys {
			if _, singleErr := sendRESPCommand(reader, writer, "UNLINK", k); singleErr != nil {
				lowerSingle := strings.ToLower(singleErr.Error())
				if strings.Contains(lowerSingle, "unknown command") || strings.Contains(lowerSingle, "noperm") {
					if _, delErr := sendRESPCommand(reader, writer, "DEL", k); delErr != nil {
						return fmt.Errorf("redis DEL %s failed: %w", k, delErr)
					}
					continue
				}
				return fmt.Errorf("redis UNLINK %s failed: %w", k, singleErr)
			}
		}
		return nil
	default:
		return fmt.Errorf("redis UNLINK failed: %w", err)
	}
}

func escapeRedisGlob(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
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
	return readRESPValueDepth(r, 0)
}

func readRESPValueDepth(r *bufio.Reader, depth int) (any, error) {
	if depth > maxRESPDepth {
		return nil, fmt.Errorf("RESP nesting depth %d exceeds maximum %d", depth, maxRESPDepth)
	}

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
		if length == -1 {
			return "", nil
		}
		if length < -1 || length > maxRESPBulkBytes {
			return nil, fmt.Errorf("invalid RESP bulk string length %d", length)
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		if buf[length] != '\r' || buf[length+1] != '\n' {
			return nil, fmt.Errorf("missing CRLF terminator on RESP bulk string")
		}
		return string(buf[:length]), nil
	case '*':
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if count == -1 || count == 0 {
			return []any{}, nil
		}
		if count < -1 || count > maxRESPArrayLen {
			return nil, fmt.Errorf("invalid RESP array length %d", count)
		}
		items := make([]any, 0, min(count, 1024))
		for i := 0; i < count; i++ {
			item, err := readRESPValueDepth(r, depth+1)
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
