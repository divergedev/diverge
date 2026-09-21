package auth

// ModelTiers maps capability tier names to recommended underlying model identifiers.
var ModelTiers = map[string][]string{
	"smart": {
		"gemini-2.5-pro",
		"claude-3-7-sonnet",
	},
	"fast": {
		"gemini-2.5-flash",
		"gpt-4o-mini",
	},
	"lite": {
		"gemini-flash-lite",
	},
}

// ResolveModelTier looks up default model names for a capability tier.
func ResolveModelTier(tier string) []string {
	if models, ok := ModelTiers[tier]; ok {
		return models
	}
	return []string{tier}
}
