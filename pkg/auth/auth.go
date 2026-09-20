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
