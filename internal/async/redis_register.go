package async

import (
	"flag"
	"os"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	redisAddr string
	redisDB   int
)

func init() {
	flag.StringVar(&redisAddr, "redis-addr", "", "Redis/Valkey address (host:port) for async key-prefix eviction on teardown")
	flag.IntVar(&redisDB, "redis-db", 0, "Redis/Valkey logical database number")

	register := func(providerName, desc string) {
		Providers.Register(providerName, registry.Provider[Provisioner]{
			Create: func(deps registry.Deps) (Provisioner, error) {
				addr := strings.TrimSpace(redisAddr)
				if addr == "" {
					addr = strings.TrimSpace(os.Getenv("DIVERGE_REDIS_ADDR"))
				}
				return &RedisProvisioner{
					Addr:     addr,
					Password: strings.TrimSpace(os.Getenv("DIVERGE_REDIS_PASSWORD")),
					DB:       redisDB,
					Provider: providerName,
				}, nil
			},
			Description: desc,
		})
	}

	register("redis", "Redis key-prefix provisioner with SCAN+UNLINK teardown")
	register("valkey", "Valkey key-prefix provisioner with SCAN+UNLINK teardown")
}
