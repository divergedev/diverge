package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/divergedev/diverge/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DatabaseProvider defines the interface for database provisioning
type DatabaseProvider interface {
	Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
	Teardown(ctx context.Context, env *v1alpha1.Environment) error
	Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error)
}

type DatabaseStatus struct {
	Ready            bool
	ConnectionSecret string
	Message          string
	SchemaName       string
}

// SQLExecutor defines the interface for executing SQL commands
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) error
	QueryRowContext(ctx context.Context, query string, args ...any) Row
	Close() error
}

type Row interface {
	Scan(dest ...any) error
}

type PgExecutor struct {
	db *sql.DB
}

func NewPgExecutor(db *sql.DB) *PgExecutor {
	return &PgExecutor{db: db}
}

func (p *PgExecutor) ExecContext(ctx context.Context, query string, args ...any) error {
	_, err := p.db.ExecContext(ctx, query, args...)
	return err
}

func (p *PgExecutor) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return p.db.QueryRowContext(ctx, query, args...)
}

func (p *PgExecutor) Close() error {
	return p.db.Close()
}

// SchemaName generates a sanitized PostgreSQL schema name for an environment.
func SchemaName(env *v1alpha1.Environment) (string, error) {
	if env == nil || env.Name == "" {
		return "", errors.New("environment name is empty")
	}

	raw := strings.ToLower("diverge_env_" + env.Name)
	raw = strings.NewReplacer(".", "_", "-", "_").Replace(raw)
	raw = regexp.MustCompile(`[^a-z0-9_]`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`_{2,}`).ReplaceAllString(raw, "_")
	raw = strings.Trim(raw, "_")

	hashInput := env.Namespace + "/" + env.Name
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]

	if len(raw) <= 63-9 {
		raw = raw + "_" + hash
	} else {
		raw = raw[:63-9] + "_" + hash
	}

	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(raw) {
		return "", fmt.Errorf("generated schema name %q is invalid", raw)
	}

	return raw, nil
}

// SharedProvider provides a shared database connection
type SharedProvider struct{}

func (p *SharedProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}
func (p *SharedProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *SharedProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{}, nil
}

// SchemaProvider creates a new logical schema within an existing database
type SchemaProvider struct {
	Executor SQLExecutor
	Client   client.Client    // K8s client for secret management
	BaseURL  string           // Base DSN without search_path, used to construct per-schema DATABASE_URL
	Runner   *MigrationRunner // optional: runs migrations after schema creation
}

func (p *SchemaProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	schemaName, err := SchemaName(env)
	if err != nil {
		return nil, err
	}

	if p.Executor != nil {
		// DDL commands cannot be parameterized in Postgres, so we use Sprintf.
		// The schemaName has already been sanitized and regex-validated against injection.
		createStmt := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)
		if err := p.Executor.ExecContext(ctx, createStmt); err != nil {
			return nil, fmt.Errorf("failed to create schema: %w", err)
		}
	}

	secretName := fmt.Sprintf("diverge-db-%s", schemaName)
	namespace := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		namespace = env.PreviewNamespace()
	}

	if p.Client != nil {
		// In a real implementation we would generate the DATABASE_URL with actual connection string
		// including the schema name, user, etc. We use a placeholder here for the Secret.
		var dbURL string
		if p.BaseURL != "" {
			// Parse the base URL and add search_path
			baseURL := p.BaseURL
			if strings.Contains(baseURL, "?") {
				dbURL = baseURL + "&search_path=" + schemaName
			} else {
				dbURL = baseURL + "?search_path=" + schemaName
			}
		} else {
			dbURL = fmt.Sprintf("postgres://user:pass@host:5432/dbname?search_path=%s", schemaName)
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
				Labels: map[string]string{
					"diverge.io/environment": env.Name,
					"diverge.io/managed-by":  "diverge",
				},
			},
			StringData: map[string]string{
				"DATABASE_URL": dbURL,
			},
		}

		var existing corev1.Secret
		err = p.Client.Get(ctx, client.ObjectKeyFromObject(secret), &existing)
		if apierrors.IsNotFound(err) {
			err = p.Client.Create(ctx, secret)
			if err != nil {
				return nil, fmt.Errorf("failed to create connection secret: %w", err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("failed to get connection secret: %w", err)
		} else {
			// Update if DATABASE_URL changed
			if string(existing.Data["DATABASE_URL"]) != dbURL {
				existing.StringData = map[string]string{"DATABASE_URL": dbURL}
				if err := p.Client.Update(ctx, &existing); err != nil {
					return nil, fmt.Errorf("failed to update connection secret: %w", err)
				}
			}
		}
	}

	// Run migrations if configured
	if p.Runner != nil && env.Spec.Database.MigrationJob != nil {
		completed, err := p.Runner.RunOrCheck(ctx, env, secretName)
		if err != nil {
			return &DatabaseStatus{
				Ready:            false,
				ConnectionSecret: secretName,
				Message:          fmt.Sprintf("migration failed: %v", err),
				SchemaName:       schemaName,
			}, err
		}
		if !completed {
			return &DatabaseStatus{
				Ready:            false,
				ConnectionSecret: secretName,
				Message:          "migration running",
				SchemaName:       schemaName,
			}, nil
		}
	}

	return &DatabaseStatus{
		Ready:            true,
		ConnectionSecret: secretName,
		Message:          schemaName,
		SchemaName:       schemaName,
	}, nil
}

func (p *SchemaProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	schemaName, err := SchemaName(env)
	if err != nil {
		return err
	}

	// Clean up migration job first
	if p.Runner != nil {
		_ = p.Runner.Cleanup(ctx, env) // best-effort: don't fail teardown
	}

	if p.Executor != nil {
		dropStmt := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)
		if err := p.Executor.ExecContext(ctx, dropStmt); err != nil {
			return fmt.Errorf("failed to drop schema: %w", err)
		}
	}

	if p.Client != nil {
		secretName := fmt.Sprintf("diverge-db-%s", schemaName)
		namespace := env.Namespace
		if env.Spec.Deploy.Namespace == "create" {
			namespace = env.PreviewNamespace()
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
		}
		if err := p.Client.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete connection secret: %w", err)
		}
	}

	return nil
}

func (p *SchemaProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	schemaName, err := SchemaName(env)
	if err != nil {
		return nil, err
	}

	if p.Executor != nil {
		var name string
		err := p.Executor.QueryRowContext(ctx, "SELECT schema_name FROM information_schema.schemata WHERE schema_name = $1", schemaName).Scan(&name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &DatabaseStatus{Ready: false, Message: "schema not found"}, nil
			}
			return &DatabaseStatus{}, fmt.Errorf("checking schema status: %w", err)
		}
	}

	secretName := fmt.Sprintf("diverge-db-%s", schemaName)
	return &DatabaseStatus{
		Ready:            true,
		ConnectionSecret: secretName,
		Message:          schemaName,
		SchemaName:       schemaName,
	}, nil
}

// FreshProvider provisions a completely new database instance
type FreshProvider struct{}

func (p *FreshProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return nil, fmt.Errorf("database mode \"fresh\" is not yet supported; use \"schema\" or \"shared\"")
}
func (p *FreshProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return fmt.Errorf("database mode \"fresh\" is not yet supported")
}
func (p *FreshProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return nil, fmt.Errorf("database mode \"fresh\" is not yet supported")
}

// SnapshotProvider provisions a database from a snapshot
type SnapshotProvider struct{}

func (p *SnapshotProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return nil, fmt.Errorf("database mode \"snapshot\" is not yet supported; use \"schema\" or \"shared\"")
}
func (p *SnapshotProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	return fmt.Errorf("database mode \"snapshot\" is not yet supported")
}
func (p *SnapshotProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return nil, fmt.Errorf("database mode \"snapshot\" is not yet supported")
}

// NoopProvider is a dummy provider that does nothing
type NoopProvider struct{}

func (p *NoopProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{Ready: true, Message: "Noop provider"}, nil
}
func (p *NoopProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error { return nil }
func (p *NoopProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*DatabaseStatus, error) {
	return &DatabaseStatus{Ready: true, Message: "Noop provider"}, nil
}
