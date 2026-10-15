package async

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func TestRedisProvisioner_NameAndRegistry(t *testing.T) {
	r := &RedisProvisioner{}
	assert.Equal(t, "redis", r.Name())

	v := &RedisProvisioner{Provider: "valkey"}
	assert.Equal(t, "valkey", v.Name())

	assert.True(t, Providers.Has("redis"))
	assert.True(t, Providers.Has("valkey"))

	redisProv, err := Providers.Create("redis", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "redis", redisProv.Name())

	valkeyProv, err := Providers.Create("valkey", registry.Deps{})
	require.NoError(t, err)
	assert.Equal(t, "valkey", valkeyProv.Name())
}

func TestFormatRedisKeyPrefix(t *testing.T) {
	assert.Equal(t, "preview:pr-42:", FormatRedisKeyPrefix("", "pr-42"))
	assert.Equal(t, "preview:pr-42:", FormatRedisKeyPrefix("default", "pr-42"))
	assert.Equal(t, "preview:pr-42:cache:", FormatRedisKeyPrefix("cache", "pr-42"))
}

func TestRedisProvisioner_Provision_Defaults(t *testing.T) {
	p := &RedisProvisioner{Addr: "redis.default.svc:6379"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}

	// Default target
	resDefault, err := p.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "redis",
		Target:   "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "preview:pr-42:", resDefault.ResolvedTarget)
	assert.Equal(t, "preview:pr-42:", resDefault.EnvVars["DIVERGE_REDIS_PREFIX"])
	assert.Equal(t, "preview:pr-42:", resDefault.EnvVars["REDIS_KEY_PREFIX"])
	assert.Equal(t, "preview:pr-42", resDefault.EnvVars["REDIS_NAMESPACE"])
	assert.Equal(t, "redis.default.svc:6379", resDefault.EnvVars["REDIS_ADDR"])

	// Named target
	resNamed, err := p.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "valkey",
		Target:   "sessions",
	})
	require.NoError(t, err)
	assert.Equal(t, "preview:pr-42:sessions:", resNamed.ResolvedTarget)
	assert.Equal(t, "preview:pr-42:sessions:", resNamed.EnvVars["DIVERGE_REDIS_PREFIX"])
	assert.Equal(t, "preview:pr-42:sessions:", resNamed.EnvVars["REDIS_KEY_PREFIX"])
	assert.Equal(t, "preview:pr-42:sessions", resNamed.EnvVars["REDIS_NAMESPACE"])
	assert.Equal(t, "redis.default.svc:6379", resNamed.EnvVars["REDIS_ADDR"])
}

func TestRedisProvisioner_Provision_CustomEnvVarMapping(t *testing.T) {
	p := &RedisProvisioner{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-99"},
	}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "redis",
		Target:   "cache",
		EnvVarMapping: map[string]string{
			"CUSTOM_REDIS_PREFIX": "{{ .ResolvedTarget }}",
			"EMPTY_TMPL_PREFIX":   "",
			"COMPOSITE_PREFIX":    "ns:{{ .ResolvedTarget }}v1:",
		},
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "preview:pr-99:cache:", res.ResolvedTarget)
	assert.Equal(t, "preview:pr-99:cache:", res.EnvVars["CUSTOM_REDIS_PREFIX"])
	assert.Equal(t, "preview:pr-99:cache:", res.EnvVars["EMPTY_TMPL_PREFIX"])
	assert.Equal(t, "ns:preview:pr-99:cache:v1:", res.EnvVars["COMPOSITE_PREFIX"])
	_, hasDefault := res.EnvVars["DIVERGE_REDIS_PREFIX"]
	assert.False(t, hasDefault)
}

func TestRedisProvisioner_Teardown_EmptyAddrNoop(t *testing.T) {
	t.Setenv("DIVERGE_REDIS_ADDR", "")
	p := &RedisProvisioner{}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}
	err := p.Teardown(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "redis",
		Target:   "cache",
	})
	require.NoError(t, err)
}

type fakeRESPServer struct {
	listener     net.Listener
	mu           sync.Mutex
	commands     [][]string
	rejectUnlink bool
	expectPass   string
	unlinkedKeys []string
	deletedKeys  []string
}

func newFakeRESPServer(t *testing.T, rejectUnlink bool, expectPass string) *fakeRESPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &fakeRESPServer{
		listener:     ln,
		rejectUnlink: rejectUnlink,
		expectPass:   expectPass,
	}

	go srv.serve()
	t.Cleanup(func() {
		_ = ln.Close()
	})
	return srv
}

func (s *fakeRESPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *fakeRESPServer) handleConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		val, err := readRESPValue(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		rawArr, ok := val.([]any)
		if !ok || len(rawArr) == 0 {
			return
		}
		cmd := make([]string, 0, len(rawArr))
		for _, item := range rawArr {
			str, _ := item.(string)
			cmd = append(cmd, str)
		}

		s.mu.Lock()
		s.commands = append(s.commands, cmd)
		s.mu.Unlock()

		switch strings.ToUpper(cmd[0]) {
		case "AUTH":
			if s.expectPass != "" && (len(cmd) < 2 || cmd[1] != s.expectPass) {
				_, _ = writer.WriteString("-ERR invalid password\r\n")
			} else {
				_, _ = writer.WriteString("+OK\r\n")
			}
		case "SELECT":
			_, _ = writer.WriteString("+OK\r\n")
		case "SCAN":
			cursor := "0"
			if len(cmd) > 1 {
				cursor = cmd[1]
			}
			if cursor == "0" {
				// Page 1 -> cursor "17" with 2 keys
				k1 := "preview:pr-42:cache:user:1"
				k2 := "preview:pr-42:cache:user:2"
				resp := fmt.Sprintf("*2\r\n$2\r\n17\r\n*2\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(k1), k1, len(k2), k2)
				_, _ = writer.WriteString(resp)
			} else {
				// Page 2 -> cursor "0" with 1 key
				k3 := "preview:pr-42:cache:session:9"
				resp := fmt.Sprintf("*2\r\n$1\r\n0\r\n*1\r\n$%d\r\n%s\r\n", len(k3), k3)
				_, _ = writer.WriteString(resp)
			}
		case "UNLINK":
			if s.rejectUnlink {
				_, _ = writer.WriteString("-ERR unknown command 'UNLINK'\r\n")
			} else {
				s.mu.Lock()
				s.unlinkedKeys = append(s.unlinkedKeys, cmd[1:]...)
				s.mu.Unlock()
				_, _ = writer.WriteString(":" + strconv.Itoa(len(cmd)-1) + "\r\n")
			}
		case "DEL":
			s.mu.Lock()
			s.deletedKeys = append(s.deletedKeys, cmd[1:]...)
			s.mu.Unlock()
			_, _ = writer.WriteString(":" + strconv.Itoa(len(cmd)-1) + "\r\n")
		default:
			_, _ = writer.WriteString("+OK\r\n")
		}
		_ = writer.Flush()
	}
}

func TestRedisProvisioner_Teardown_ScanAndUnlink(t *testing.T) {
	srv := newFakeRESPServer(t, false, "secret-pass")

	p := &RedisProvisioner{
		Addr:     srv.listener.Addr().String(),
		Password: "secret-pass",
		DB:       2,
		Provider: "redis",
	}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "redis",
		Target:   "cache",
	}

	err := p.Teardown(context.Background(), env, route)
	require.NoError(t, err)

	srv.mu.Lock()
	defer srv.mu.Unlock()

	require.Len(t, srv.commands, 6)
	assert.Equal(t, []string{"AUTH", "secret-pass"}, srv.commands[0])
	assert.Equal(t, []string{"SELECT", "2"}, srv.commands[1])
	assert.Equal(t, []string{"SCAN", "0", "MATCH", "preview:pr-42:cache:*", "COUNT", "500"}, srv.commands[2])
	assert.Equal(t, []string{"UNLINK", "preview:pr-42:cache:user:1", "preview:pr-42:cache:user:2"}, srv.commands[3])
	assert.Equal(t, []string{"SCAN", "17", "MATCH", "preview:pr-42:cache:*", "COUNT", "500"}, srv.commands[4])
	assert.Equal(t, []string{"UNLINK", "preview:pr-42:cache:session:9"}, srv.commands[5])

	assert.Equal(t, []string{
		"preview:pr-42:cache:user:1",
		"preview:pr-42:cache:user:2",
		"preview:pr-42:cache:session:9",
	}, srv.unlinkedKeys)
}

func TestRedisProvisioner_Teardown_FallbackToDel(t *testing.T) {
	srv := newFakeRESPServer(t, true, "")

	p := &RedisProvisioner{
		Addr:     srv.listener.Addr().String(),
		Provider: "valkey",
	}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "valkey",
		Target:   "cache",
	}

	err := p.Teardown(context.Background(), env, route)
	require.NoError(t, err)

	srv.mu.Lock()
	defer srv.mu.Unlock()

	assert.Empty(t, srv.unlinkedKeys)
	assert.Equal(t, []string{
		"preview:pr-42:cache:user:1",
		"preview:pr-42:cache:user:2",
		"preview:pr-42:cache:session:9",
	}, srv.deletedKeys)
}
