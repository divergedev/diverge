package async

import (
	"flag"
	"os"
	"strconv"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	redisAddr         string
	redisUsernameFlag string
	redisDB           int
)

func init() {
	flag.StringVar(&redisAddr, "redis-addr", "", "Redis/Valkey address (host:port) for async key-prefix eviction on teardown")
	flag.StringVar(&redisUsernameFlag, "redis-username", "", "Redis/Valkey ACL username for authentication")
	flag.IntVar(&redisDB, "redis-db", 0, "Redis/Valkey logical database number")

	register := func(providerName, desc string) {
		Providers.Register(providerName, registry.Provider[Provisioner]{
			Create: func(deps registry.Deps) (Provisioner, error) {
				addr := strings.TrimSpace(redisAddr)
				if addr == "" {
					addr = strings.TrimSpace(os.Getenv("DIVERGE_REDIS_ADDR"))
				}
				username := strings.TrimSpace(redisUsernameFlag)
				if username == "" {
					username = strings.TrimSpace(os.Getenv("DIVERGE_REDIS_USERNAME"))
				}
				db := redisDB
				if db == 0 {
					if envDB := strings.TrimSpace(os.Getenv("DIVERGE_REDIS_DB")); envDB != "" {
						if parsed, err := strconv.Atoi(envDB); err == nil && parsed >= 0 {
							db = parsed
						}
					}
				}
				return &RedisProvisioner{
					Addr:     addr,
					Username: username,
					Password: strings.TrimSpace(os.Getenv("DIVERGE_REDIS_PASSWORD")),
					DB:       db,
					Provider: providerName,
				}, nil
			},
			Description: desc,
		})
	}

	register("redis", "Redis key-prefix provisioner with SCAN+UNLINK teardown")
	register("valkey", "Valkey key-prefix provisioner with SCAN+UNLINK teardown")
}
