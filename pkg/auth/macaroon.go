package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidSignature = errors.New("invalid token signature")
	ErrTokenExpired     = errors.New("token has expired")
	ErrCaveatFailed     = errors.New("caveat check failed")
	ErrInvalidCaveat    = ErrCaveatFailed
	ErrMalformedToken   = errors.New("malformed token")
)

// Macaroon implements Token using HMAC-SHA256 caveat chaining.
type Macaroon struct {
	TokenID   string   `json:"id"`
	Location  string   `json:"location"`
	CaveatSeq []Caveat `json:"caveats"`
	Signature string   `json:"signature"` // hex-encoded
}

// ID returns the Macaroon's task identifier.
func (m *Macaroon) ID() string {
	return m.TokenID
}

// Caveats returns the ordered caveat chain embedded in the Macaroon.
func (m *Macaroon) Caveats() []Caveat {
	return m.CaveatSeq
}

// Serialize encodes the Macaroon as JSON bytes.
func (m *Macaroon) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// MacaroonProvider implements Minter, Attenuator, and Verifier.
type MacaroonProvider struct {
	rootKey []byte
}

// MinMacaroonRootKeyLen is the minimum required length in bytes for a Macaroon HMAC root key.
const MinMacaroonRootKeyLen = 32

// NewMacaroonProvider initializes a provider with a 32-byte secret root key.
func NewMacaroonProvider(rootKey []byte) *MacaroonProvider {
	return &MacaroonProvider{rootKey: rootKey}
}

// computeHMAC calculates the HMAC-SHA256 digest of data under key.
func computeHMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// Mint creates a fresh root Macaroon.
func (p *MacaroonProvider) Mint(_ context.Context, claims Claims) (Token, error) {
	if p == nil || len(p.rootKey) < MinMacaroonRootKeyLen {
		return nil, fmt.Errorf("macaroon root key must be at least %d bytes", MinMacaroonRootKeyLen)
	}
	if claims.TaskID == "" {
		return nil, errors.New("claims must specify TaskID")
	}
	if claims.Namespace == "" {
		return nil, errors.New("claims must specify Namespace")
	}

	mac := &Macaroon{
		TokenID:  claims.TaskID,
		Location: "diverge-cluster",
	}

	sig := computeHMAC(p.rootKey, []byte(mac.TokenID))

	// Embed baseline caveats from claims (task_id at index 0, namespace at index 1)
	caveats := []Caveat{
		{Key: "task_id", Op: OpEqual, Value: claims.TaskID},
		{Key: "namespace", Op: OpEqual, Value: claims.Namespace},
	}
	if claims.RepoURL != "" {
		caveats = append(caveats, Caveat{
			Key:   "repo",
			Op:    OpEqual,
			Value: claims.RepoURL,
		})
	}
	if claims.Branch != "" {
		caveats = append(caveats, Caveat{
			Key:   "branch",
			Op:    OpEqual,
			Value: claims.Branch,
		})
	}
	if !claims.ExpiresAt.IsZero() {
		caveats = append(caveats, Caveat{
			Key:   "expires_at",
			Op:    OpBefore,
			Value: strconv.FormatInt(claims.ExpiresAt.Unix(), 10),
		})
	}
	if claims.MaxCostUSD > 0 {
		costVal := fmt.Sprintf("%.2f", claims.MaxCostUSD)
		if claims.MaxCostUSD < 0.01 {
			costVal = strconv.FormatFloat(claims.MaxCostUSD, 'f', -1, 64)
		}
		caveats = append(caveats, Caveat{
			Key:   "max_cost_usd",
			Op:    OpLessThan,
			Value: costVal,
		})
	}
	if claims.MaxTokens > 0 {
		caveats = append(caveats, Caveat{
			Key:   "max_tokens",
			Op:    OpLessThan,
			Value: strconv.FormatInt(claims.MaxTokens, 10),
		})
	}
	if len(claims.AllowedTools) > 0 {
		caveats = append(caveats, Caveat{
			Key:   "allowed_tools",
			Op:    OpGlob,
			Value: strings.Join(claims.AllowedTools, ","),
		})
	}
	if len(claims.AllowedModels) > 0 {
		caveats = append(caveats, Caveat{
			Key:   "allowed_models",
			Op:    OpIn,
			Value: strings.Join(claims.AllowedModels, ","),
		})
	}

	// Embed cryptographic nonce to ensure unique token signatures across distinct issuances
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	caveats = append(caveats, Caveat{
		Key:   "nonce",
		Op:    OpEqual,
		Value: hex.EncodeToString(nonceBytes),
	})

	for _, c := range caveats {
		sig = computeHMAC(sig, serializeCaveatCanonical(c))
		mac.CaveatSeq = append(mac.CaveatSeq, c)
	}

	mac.Signature = hex.EncodeToString(sig)
	return mac, nil
}

// serializeCaveatCanonical returns the canonical key:op:value byte representation of a caveat.
func serializeCaveatCanonical(c Caveat) []byte {
	return []byte(fmt.Sprintf("%s:%s:%s", c.Key, c.Op, c.Value))
}

// Attenuate appends additional caveats to an existing token.
func (p *MacaroonProvider) Attenuate(_ context.Context, token Token, caveats ...Caveat) (Token, error) {
	mac, ok := token.(*Macaroon)
	if !ok {
		return nil, fmt.Errorf("expected *Macaroon, got %T", token)
	}

	sig, err := hex.DecodeString(mac.Signature)
	if err != nil {
		return nil, ErrMalformedToken
	}

	newMac := &Macaroon{
		TokenID:   mac.TokenID,
		Location:  mac.Location,
		CaveatSeq: make([]Caveat, len(mac.CaveatSeq), len(mac.CaveatSeq)+len(caveats)),
	}
	copy(newMac.CaveatSeq, mac.CaveatSeq)

	for _, c := range caveats {
		sig = computeHMAC(sig, serializeCaveatCanonical(c))
		newMac.CaveatSeq = append(newMac.CaveatSeq, c)
	}

	newMac.Signature = hex.EncodeToString(sig)
	return newMac, nil
}

// Verify validates that the signature chain is intact and all caveats hold true.
func (p *MacaroonProvider) Verify(_ context.Context, raw []byte, required Claims) error {
	if p == nil || len(p.rootKey) < MinMacaroonRootKeyLen {
		return ErrInvalidSignature
	}
	var mac Macaroon
	if err := json.Unmarshal(raw, &mac); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}

	// Replay signature chain from root key
	sig := computeHMAC(p.rootKey, []byte(mac.TokenID))
	hasTaskID := false
	hasNamespace := false

	for _, c := range mac.CaveatSeq {
		sig = computeHMAC(sig, serializeCaveatCanonical(c))

		switch c.Key {
		case "task_id":
			if c.Value != "" {
				hasTaskID = true
			}
		case "namespace":
			if c.Value != "" {
				hasNamespace = true
			}
		}

		// Evaluate caveat
		if err := evaluateCaveat(c, required); err != nil {
			return err
		}
	}

	expectedSig := hex.EncodeToString(sig)
	if !hmac.Equal([]byte(expectedSig), []byte(mac.Signature)) {
		return ErrInvalidSignature
	}
	if !hasTaskID {
		return fmt.Errorf("%w: missing task_id caveat", ErrInvalidCaveat)
	}
	if !hasNamespace {
		return fmt.Errorf("%w: missing namespace caveat", ErrInvalidCaveat)
	}

	return nil
}

// VerifyAndExtract validates the cryptographic HMAC chain, expiration, and caveat syntax of a
// serialized Macaroon and extracts the effective Claims embedded in its caveat sequence.
func (p *MacaroonProvider) VerifyAndExtract(_ context.Context, raw []byte) (*Macaroon, Claims, error) {
	if p == nil || len(p.rootKey) < MinMacaroonRootKeyLen {
		return nil, Claims{}, ErrInvalidSignature
	}
	var mac Macaroon
	if err := json.Unmarshal(raw, &mac); err != nil {
		return nil, Claims{}, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	if mac.TokenID == "" {
		return nil, Claims{}, ErrMalformedToken
	}

	sig := computeHMAC(p.rootKey, []byte(mac.TokenID))
	claims := Claims{TaskID: mac.TokenID}
	hasTaskID := false
	hasNamespace := false
	hasAllowedTools := false
	hasAllowedModels := false
	hasMaxCostUSD := false
	hasMaxTokens := false

	for i, c := range mac.CaveatSeq {
		sig = computeHMAC(sig, serializeCaveatCanonical(c))

		switch c.Key {
		case "task_id":
			if c.Op != OpEqual || c.Value == "" || c.Value != mac.TokenID || (!hasTaskID && i != 0) {
				return nil, Claims{}, fmt.Errorf("%w: invalid task_id caveat", ErrCaveatFailed)
			}
			hasTaskID = true
			claims.TaskID = c.Value
		case "namespace":
			// The namespace is fixed at mint time (directly after task_id); attenuation cannot introduce one.
			if c.Op != OpEqual || c.Value == "" || (!hasNamespace && i != 1) {
				return nil, Claims{}, fmt.Errorf("%w: invalid namespace caveat", ErrCaveatFailed)
			}
			if claims.Namespace != "" && claims.Namespace != c.Value {
				return nil, Claims{}, fmt.Errorf("%w: conflicting namespace caveat", ErrCaveatFailed)
			}
			hasNamespace = true
			claims.Namespace = c.Value
		case "repo":
			if c.Op != OpEqual || c.Value == "" {
				return nil, Claims{}, fmt.Errorf("%w: invalid repo caveat", ErrCaveatFailed)
			}
			if claims.RepoURL != "" && claims.RepoURL != c.Value {
				return nil, Claims{}, fmt.Errorf("%w: conflicting repo caveat", ErrCaveatFailed)
			}
			claims.RepoURL = c.Value
		case "branch":
			if c.Op != OpEqual || c.Value == "" {
				return nil, Claims{}, fmt.Errorf("%w: invalid branch caveat", ErrCaveatFailed)
			}
			if claims.Branch != "" && claims.Branch != c.Value {
				return nil, Claims{}, fmt.Errorf("%w: conflicting branch caveat", ErrCaveatFailed)
			}
			claims.Branch = c.Value
		case "expires_at":
			if c.Op != OpBefore {
				return nil, Claims{}, fmt.Errorf("%w: invalid operator %q for expires_at caveat", ErrCaveatFailed, c.Op)
			}
			unixSec, err := strconv.ParseInt(c.Value, 10, 64)
			if err != nil {
				return nil, Claims{}, ErrMalformedToken
			}
			if time.Now().Unix() > unixSec {
				return nil, Claims{}, ErrTokenExpired
			}
			exp := time.Unix(unixSec, 0)
			if claims.ExpiresAt.IsZero() || exp.Before(claims.ExpiresAt) {
				claims.ExpiresAt = exp
			}
		case "allowed_tools":
			if c.Op != OpGlob || c.Value == "" {
				return nil, Claims{}, fmt.Errorf("%w: invalid allowed_tools caveat", ErrCaveatFailed)
			}
			newPatterns := strings.Split(c.Value, ",")
			for _, np := range newPatterns {
				if np == "" {
					return nil, Claims{}, fmt.Errorf("%w: empty pattern in allowed_tools caveat", ErrCaveatFailed)
				}
			}
			if !hasAllowedTools {
				claims.AllowedTools = newPatterns
				hasAllowedTools = true
			} else {
				// Attenuated allowed_tools caveats can only narrow existing patterns
				var narrowed []string
				for _, np := range newPatterns {
					for _, existing := range claims.AllowedTools {
						if ok, _ := filepath.Match(existing, np); ok || np == existing {
							narrowed = append(narrowed, np)
							break
						}
					}
				}
				if len(narrowed) == 0 {
					return nil, Claims{}, fmt.Errorf("%w: attenuated allowed_tools %q is disjoint from prior allowed_tools", ErrCaveatFailed, c.Value)
				}
				claims.AllowedTools = narrowed
			}
		case "allowed_models":
			if c.Op != OpIn || c.Value == "" {
				return nil, Claims{}, fmt.Errorf("%w: invalid allowed_models caveat", ErrCaveatFailed)
			}
			newModels := strings.Split(c.Value, ",")
			for _, m := range newModels {
				if m == "" {
					return nil, Claims{}, fmt.Errorf("%w: empty model in allowed_models caveat", ErrCaveatFailed)
				}
			}
			if !hasAllowedModels {
				claims.AllowedModels = newModels
				hasAllowedModels = true
			} else {
				var narrowed []string
				for _, m := range newModels {
					for _, existing := range claims.AllowedModels {
						if MatchesAllowedModel(existing, m) {
							narrowed = append(narrowed, m)
							break
						}
					}
				}
				if len(narrowed) == 0 {
					return nil, Claims{}, fmt.Errorf("%w: attenuated allowed_models %q is disjoint from prior allowed_models", ErrCaveatFailed, c.Value)
				}
				claims.AllowedModels = narrowed
			}
		case "max_cost_usd":
			if c.Op != OpLessThan {
				return nil, Claims{}, fmt.Errorf("%w: invalid operator %q for max_cost_usd caveat", ErrCaveatFailed, c.Op)
			}
			maxLimit, err := strconv.ParseFloat(c.Value, 64)
			if err != nil {
				return nil, Claims{}, ErrMalformedToken
			}
			if math.IsNaN(maxLimit) || math.IsInf(maxLimit, 0) || maxLimit <= 0 {
				return nil, Claims{}, fmt.Errorf("%w: invalid non-positive max_cost_usd caveat %q", ErrCaveatFailed, c.Value)
			}
			if !hasMaxCostUSD || maxLimit < claims.MaxCostUSD {
				claims.MaxCostUSD = maxLimit
				hasMaxCostUSD = true
			}
		case "max_tokens":
			if c.Op != OpLessThan {
				return nil, Claims{}, fmt.Errorf("%w: invalid operator %q for max_tokens caveat", ErrCaveatFailed, c.Op)
			}
			maxTokens, err := strconv.ParseInt(c.Value, 10, 64)
			if err != nil {
				return nil, Claims{}, ErrMalformedToken
			}
			if maxTokens <= 0 {
				return nil, Claims{}, fmt.Errorf("%w: invalid non-positive max_tokens caveat %q", ErrCaveatFailed, c.Value)
			}
			if !hasMaxTokens || maxTokens < claims.MaxTokens {
				claims.MaxTokens = maxTokens
				hasMaxTokens = true
			}
		case "nonce":
			if c.Op != OpEqual || c.Value == "" {
				return nil, Claims{}, fmt.Errorf("%w: invalid nonce caveat", ErrCaveatFailed)
			}
		default:
			return nil, Claims{}, fmt.Errorf("%w: unrecognized caveat key %q", ErrCaveatFailed, c.Key)
		}
	}

	expectedSig := hex.EncodeToString(sig)
	if !hmac.Equal([]byte(expectedSig), []byte(mac.Signature)) {
		return nil, Claims{}, ErrInvalidSignature
	}
	if !hasTaskID {
		return nil, Claims{}, fmt.Errorf("%w: missing task_id caveat", ErrCaveatFailed)
	}
	if !hasNamespace {
		return nil, Claims{}, fmt.Errorf("%w: missing namespace caveat", ErrCaveatFailed)
	}

	return &mac, claims, nil
}

var capabilityTierModelPatterns = map[string][]string{
	"fast": {
		"*flash*", "*haiku*", "*mini*",
		"gpt-4o*", "claude-3-5-sonnet*", "claude-3-7-sonnet*", "claude-sonnet*", "gemini-2.5-*",
	},
	"smart": {
		"claude-*", "gpt-4*", "gpt-5*", "o1*", "o3*", "o4*", "gemini-*",
	},
	"lite": {
		"*lite*", "*flash*", "*haiku*", "*mini*", "*nano*",
	},
}

// MatchesAllowedModel reports whether model is permitted by pattern (exact match, capability tier alias,
// trailing '*' prefix match, or glob).
func MatchesAllowedModel(pattern, model string) bool {
	pattern = strings.TrimSpace(pattern)
	model = strings.TrimSpace(model)
	if pattern == "" || model == "" {
		return false
	}
	if pattern == "*" || pattern == model {
		return true
	}
	if tierPatterns, ok := capabilityTierModelPatterns[strings.ToLower(pattern)]; ok {
		lowerModel := strings.ToLower(model)
		for _, tp := range tierPatterns {
			if MatchesAllowedModel(tp, lowerModel) {
				return true
			}
		}
	}
	if strings.HasSuffix(pattern, "*") && !strings.HasPrefix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 2 {
		sub := strings.Trim(pattern, "*")
		if strings.Contains(model, sub) {
			return true
		}
	}
	if ok, err := filepath.Match(pattern, model); err == nil && ok {
		return true
	}
	return false
}

// evaluateCaveat verifies that a single Caveat holds against the provided request Claims.
func evaluateCaveat(c Caveat, req Claims) error {
	switch c.Key {
	case "task_id":
		if c.Op != OpEqual {
			return fmt.Errorf("%w: invalid operator %q for task_id caveat", ErrCaveatFailed, c.Op)
		}
		if req.TaskID == "" || req.TaskID != c.Value {
			return fmt.Errorf("%w: task mismatch (%s != %s)", ErrCaveatFailed, req.TaskID, c.Value)
		}
	case "namespace":
		if c.Op != OpEqual {
			return fmt.Errorf("%w: invalid operator %q for namespace caveat", ErrCaveatFailed, c.Op)
		}
		if req.Namespace != "" && req.Namespace != c.Value {
			return fmt.Errorf("%w: namespace mismatch (%s != %s)", ErrCaveatFailed, req.Namespace, c.Value)
		}
	case "repo":
		if c.Op != OpEqual {
			return fmt.Errorf("%w: invalid operator %q for repo caveat", ErrCaveatFailed, c.Op)
		}
		if req.RepoURL == "" || req.RepoURL != c.Value {
			return fmt.Errorf("%w: repo mismatch (%s != %s)", ErrCaveatFailed, req.RepoURL, c.Value)
		}
	case "branch":
		if c.Op != OpEqual {
			return fmt.Errorf("%w: invalid operator %q for branch caveat", ErrCaveatFailed, c.Op)
		}
		if req.Branch == "" || req.Branch != c.Value {
			return fmt.Errorf("%w: branch mismatch (%s != %s)", ErrCaveatFailed, req.Branch, c.Value)
		}
	case "expires_at":
		if c.Op != OpBefore {
			return fmt.Errorf("%w: invalid operator %q for expires_at caveat", ErrCaveatFailed, c.Op)
		}
		unixSec, err := strconv.ParseInt(c.Value, 10, 64)
		if err != nil {
			return ErrMalformedToken
		}
		if time.Now().Unix() > unixSec {
			return ErrTokenExpired
		}
	case "allowed_tools":
		if c.Op != OpGlob {
			return fmt.Errorf("%w: invalid operator %q for allowed_tools caveat", ErrCaveatFailed, c.Op)
		}
		if len(req.AllowedTools) == 0 {
			return fmt.Errorf("%w: token restricts allowed tools to %q but no tool was specified in request", ErrCaveatFailed, c.Value)
		}
		patterns := strings.Split(c.Value, ",")
		for _, tool := range req.AllowedTools {
			matched := false
			for _, pat := range patterns {
				if ok, _ := filepath.Match(pat, tool); ok {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("%w: tool %q not allowed by pattern %q", ErrCaveatFailed, tool, c.Value)
			}
		}
	case "allowed_models":
		if c.Op != OpIn {
			return fmt.Errorf("%w: invalid operator %q for allowed_models caveat", ErrCaveatFailed, c.Op)
		}
		if len(req.AllowedModels) == 0 {
			return fmt.Errorf("%w: token restricts allowed models to %q but no model was specified in request", ErrCaveatFailed, c.Value)
		}
		allowedList := strings.Split(c.Value, ",")
		for _, model := range req.AllowedModels {
			matched := false
			for _, allowedPat := range allowedList {
				if MatchesAllowedModel(allowedPat, model) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("%w: model %q not in allowed models %v", ErrCaveatFailed, model, allowedList)
			}
		}
	case "max_cost_usd":
		if c.Op != OpLessThan {
			return fmt.Errorf("%w: invalid operator %q for max_cost_usd caveat", ErrCaveatFailed, c.Op)
		}
		maxLimit, err := strconv.ParseFloat(c.Value, 64)
		if err != nil {
			return ErrMalformedToken
		}
		if math.IsNaN(maxLimit) || math.IsInf(maxLimit, 0) || maxLimit <= 0 {
			return fmt.Errorf("%w: invalid non-positive max_cost_usd caveat %q", ErrCaveatFailed, c.Value)
		}
		if req.MaxCostUSD > maxLimit {
			return fmt.Errorf("%w: cost %.2f exceeds max limit %.2f", ErrCaveatFailed, req.MaxCostUSD, maxLimit)
		}
	case "max_tokens":
		if c.Op != OpLessThan {
			return fmt.Errorf("%w: invalid operator %q for max_tokens caveat", ErrCaveatFailed, c.Op)
		}
		maxTokens, err := strconv.ParseInt(c.Value, 10, 64)
		if err != nil {
			return ErrMalformedToken
		}
		if maxTokens <= 0 {
			return fmt.Errorf("%w: invalid non-positive max_tokens caveat %q", ErrCaveatFailed, c.Value)
		}
		if req.TokensConsumed > maxTokens {
			return fmt.Errorf("%w: tokens consumed %d exceeds max limit %d", ErrCaveatFailed, req.TokensConsumed, maxTokens)
		}
	case "nonce":
		if c.Op != OpEqual {
			return fmt.Errorf("%w: invalid operator %q for nonce caveat", ErrCaveatFailed, c.Op)
		}
		if c.Value == "" {
			return fmt.Errorf("%w: empty nonce caveat value", ErrCaveatFailed)
		}
	default:
		return fmt.Errorf("%w: unrecognized caveat key %q", ErrCaveatFailed, c.Key)
	}
	return nil
}
