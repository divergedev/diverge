package async

import (
	"flag"
	"fmt"
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

func redisDBWasExplicitlySet() bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "redis-db" {
			set = true
		}
	})
	return set
}

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
				if !redisDBWasExplicitlySet() {
					if envDB := strings.TrimSpace(os.Getenv("DIVERGE_REDIS_DB")); envDB != "" {
						parsed, err := strconv.Atoi(envDB)
						if err != nil || parsed < 0 {
							return nil, fmt.Errorf("invalid DIVERGE_REDIS_DB %q", envDB)
						}
						db = parsed
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
