//go:build !no_schema

package schemaprovider

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"

	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func init() {
	pkgdb.Providers.Register("schema", registry.Provider[pkgdb.DatabaseProvider]{
		Create: func(deps registry.Deps) (pkgdb.DatabaseProvider, error) {
			dbHost := os.Getenv("DIVERGE_DB_HOST")
			dbPort := os.Getenv("DIVERGE_DB_PORT")
			dbUser := os.Getenv("DIVERGE_DB_USER")
			dbPassword := os.Getenv("DIVERGE_DB_PASSWORD")
			dbName := os.Getenv("DIVERGE_DB_NAME")
			dbSSLMode := os.Getenv("DIVERGE_DB_SSLMODE")
			if dbSSLMode == "" {
				dbSSLMode = "disable"
			}
			if dbPort == "" {
				dbPort = "5432"
			}

			if dbHost == "" {
				return nil, fmt.Errorf("DIVERGE_DB_HOST is required for --database-provider=schema")
			}

			dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
				dbUser, url.QueryEscape(dbPassword), dbHost, dbPort, dbName, dbSSLMode)

			db, err := sql.Open("pgx", dsn)
			if err != nil {
				return nil, fmt.Errorf("failed to open database connection: %w", err)
			}
			// Verify connectivity
			pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pingCancel()
			if err := db.PingContext(pingCtx); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("failed to ping database (host=%s, port=%s): %w", dbHost, dbPort, err)
			}
			deps.Logger.Info("Connected to database", "host", dbHost, "port", dbPort, "database", dbName)
			_ = db.Close()

			return &SchemaDatabaseProvider{
				AdminDSN: dsn,
			}, nil
		},
		Description: "Schema-per-environment database provisioning",
	})
}
