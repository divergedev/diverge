package auth

import (
	"context"
	"time"
)

// CaveatOp defines operators for checking caveats.
type CaveatOp string

const (
	OpEqual    CaveatOp = "="
	OpIn       CaveatOp = "in"
	OpLessThan CaveatOp = "<"
	OpBefore   CaveatOp = "before"
	OpGlob     CaveatOp = "glob"
)

// Caveat represents a restriction appended to a token.
type Caveat struct {
	Key   string   `json:"key"`
	Op    CaveatOp `json:"op"`
	Value string   `json:"value"`
}

// Claims encapsulates the authorized attributes of an AgentTask token.
type Claims struct {
	TaskID         string    `json:"task_id"`
	RepoURL        string    `json:"repo_url"`
	Branch         string    `json:"branch"`
	AllowedTools   []string  `json:"allowed_tools,omitempty"`
	AllowedModels  []string  `json:"allowed_models,omitempty"`
	MaxCostUSD     float64   `json:"max_cost_usd,omitempty"`
	MaxTokens      int64     `json:"max_tokens,omitempty"`
	TokensConsumed int64     `json:"tokens_consumed,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// Token represents a cryptographic credential that can be serialized and attenuated.
type Token interface {
	ID() string
	Serialize() ([]byte, error)
	Caveats() []Caveat
}

// Minter creates root authorization tokens for AgentTasks.
type Minter interface {
	Mint(ctx context.Context, claims Claims) (Token, error)
}

// Attenuator appends restrictive caveats to an existing token without re-contacting the issuer.
type Attenuator interface {
	Attenuate(ctx context.Context, token Token, caveats ...Caveat) (Token, error)
}

// Verifier checks token validity, signature chain, and required caveats.
type Verifier interface {
	Verify(ctx context.Context, raw []byte, required Claims) error
}

// Provider combines token issuance, attenuation, and verification capabilities.
type Provider interface {
	Minter
	Attenuator
	Verifier
}

// TokenStore manages persistence, lookup, and revocation of capability tokens.
type TokenStore interface {
	SaveToken(ctx context.Context, taskID, namespace string, token []byte) error
	GetToken(ctx context.Context, taskID, namespace string) ([]byte, error)
	DeleteToken(ctx context.Context, taskID, namespace string) error
	RevokeToken(ctx context.Context, taskID, namespace string) error
	IsRevoked(ctx context.Context, taskID, namespace string) bool
}

// AuthEventType defines the category of security/capability events.
type AuthEventType string

const (
	EventMint      AuthEventType = "mint"
	EventAttenuate AuthEventType = "attenuate"
	EventVerify    AuthEventType = "verify"
	EventRevoke    AuthEventType = "revoke"
)

// AuthEvent records a security-sensitive capability event for auditing and compliance.
type AuthEvent struct {
	Type      AuthEventType `json:"type"`
	TaskID    string        `json:"task_id"`
	Namespace string        `json:"namespace"`
	Principal string        `json:"principal"`
	Success   bool          `json:"success"`
	Reason    string        `json:"reason,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// TokenAuditor is an interface for streaming authentication/authorization events to an audit sink.
type TokenAuditor interface {
	RecordEvent(ctx context.Context, event AuthEvent) error
}

// CaveatValidator evaluates caveats against token claims with strict fail-closed typing.
type CaveatValidator interface {
	Validate(ctx context.Context, caveat Caveat, claims Claims) error
}

// VCSCredentials encapsulates short-lived repository access credentials.
type VCSCredentials struct {
	Username string
	Token    string
	AuthType string // "bearer", "basic", "ssh"
}

// CredentialBroker supplies short-lived VCS credentials for git clone operations.
type CredentialBroker interface {
	BrokerCredentials(ctx context.Context, repoURL string) (*VCSCredentials, error)
}
