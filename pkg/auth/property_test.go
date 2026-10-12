package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestMacaroon_MintSerializeVerifyRoundTrip_PBT(t *testing.T) {
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)
	ctx := context.Background()

	hegel.Test(t, func(ht *hegel.T) {
		taskID := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(80))
		namespace := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(63))
		repoURL := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(100))
		branch := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(60))
		costCents := hegel.Draw(ht, hegel.Integers(1, 1000000))
		maxCostUSD := float64(costCents) / 100.0
		maxTokens := int64(hegel.Draw(ht, hegel.Integers(1, 10000000)))
		model := hegel.Draw(ht, hegel.SampledFrom([]string{
			"gemini-2.5-pro",
			"gemini-2.5-flash",
			"claude-sonnet-4",
			"gpt-4o",
		}))

		claims := Claims{
			TaskID:        taskID,
			Namespace:     namespace,
			RepoURL:       repoURL,
			Branch:        branch,
			MaxCostUSD:    maxCostUSD,
			MaxTokens:     maxTokens,
			AllowedModels: []string{model},
			ExpiresAt:     time.Now().Add(1 * time.Hour),
		}

		tok, err := provider.Mint(ctx, claims)
		require.NoError(ht, err)

		raw, err := tok.Serialize()
		require.NoError(ht, err)

		mac, extracted, err := provider.VerifyAndExtract(ctx, raw)
		require.NoError(ht, err)
		require.NotNil(ht, mac)
		require.Equal(ht, taskID, extracted.TaskID)
		require.Equal(ht, namespace, extracted.Namespace)
		require.Equal(ht, repoURL, extracted.RepoURL)
		require.Equal(ht, branch, extracted.Branch)
		require.InDelta(ht, maxCostUSD, extracted.MaxCostUSD, 0.001)
		require.Equal(ht, maxTokens, extracted.MaxTokens)
		require.Equal(ht, []string{model}, extracted.AllowedModels)
	})
}

func TestMacaroon_AttenuationMonotonicity_PBT(t *testing.T) {
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)
	ctx := context.Background()

	hegel.Test(t, func(ht *hegel.T) {
		parentCents := hegel.Draw(ht, hegel.Integers(100, 100000))
		childCents := hegel.Draw(ht, hegel.Integers(1, parentCents))
		parentTokens := int64(hegel.Draw(ht, hegel.Integers(1000, 1000000)))
		childTokens := int64(hegel.Draw(ht, hegel.Integers(1, int(parentTokens))))

		parentCost := float64(parentCents) / 100.0
		childCost := float64(childCents) / 100.0

		claims := Claims{
			TaskID:        "task-pbt",
			Namespace:     "ns-alpha",
			MaxCostUSD:    parentCost,
			MaxTokens:     parentTokens,
			AllowedModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
			ExpiresAt:     time.Now().Add(1 * time.Hour),
		}

		rootTok, err := provider.Mint(ctx, claims)
		require.NoError(ht, err)

		// 1. Tighter attenuation succeeds and returns tighter bounds
		tighterTok, err := provider.Attenuate(ctx, rootTok,
			Caveat{Key: "max_cost_usd", Op: OpLessThan, Value: fmt.Sprintf("%.2f", childCost)},
			Caveat{Key: "max_tokens", Op: OpLessThan, Value: strconv.FormatInt(childTokens, 10)},
		)
		require.NoError(ht, err)

		tighterRaw, err := tighterTok.Serialize()
		require.NoError(ht, err)

		_, extracted, err := provider.VerifyAndExtract(ctx, tighterRaw)
		require.NoError(ht, err)
		require.InDelta(ht, childCost, extracted.MaxCostUSD, 0.001)
		require.Equal(ht, childTokens, extracted.MaxTokens)

		// 2. Attempting to widen max_cost_usd > parent cannot increase effective limit,
		// and Verify rejects cost > parent.
		widerCost := parentCost + 10.00
		widerTok, err := provider.Attenuate(ctx, rootTok,
			Caveat{Key: "max_cost_usd", Op: OpLessThan, Value: fmt.Sprintf("%.2f", widerCost)},
		)
		require.NoError(ht, err)
		widerRaw, err := widerTok.Serialize()
		require.NoError(ht, err)

		_, widerExtracted, err := provider.VerifyAndExtract(ctx, widerRaw)
		require.NoError(ht, err)
		require.InDelta(ht, parentCost, widerExtracted.MaxCostUSD, 0.001)

		err = provider.Verify(ctx, widerRaw, Claims{
			TaskID:        "task-pbt",
			Namespace:     "ns-alpha",
			AllowedModels: []string{"gemini-2.5-pro"},
			MaxCostUSD:    parentCost + 1.00,
		})
		require.True(ht, errors.Is(err, ErrCaveatFailed), "expected ErrCaveatFailed, got %v", err)

		// 3. Attenuating with a different namespace fails VerifyAndExtract with ErrCaveatFailed
		diffNS, err := provider.Attenuate(ctx, rootTok,
			Caveat{Key: "namespace", Op: OpEqual, Value: "ns-beta"},
		)
		require.NoError(ht, err)
		diffNSRaw, err := diffNS.Serialize()
		require.NoError(ht, err)

		_, _, err = provider.VerifyAndExtract(ctx, diffNSRaw)
		require.True(ht, errors.Is(err, ErrCaveatFailed), "expected ErrCaveatFailed for conflicting namespace, got %v", err)
	})
}

func TestMacaroon_TamperRejection_PBT(t *testing.T) {
	rootKey := []byte("0123456789abcdef0123456789abcdef")
	provider := NewMacaroonProvider(rootKey)
	ctx := context.Background()

	hegel.Test(t, func(ht *hegel.T) {
		taskID := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))
		tok, err := provider.Mint(ctx, Claims{
			TaskID:    taskID,
			Namespace: "default",
		})
		require.NoError(ht, err)

		mac := tok.(*Macaroon)
		require.NotEmpty(ht, mac.Signature)

		idx := hegel.Draw(ht, hegel.Integers(0, len(mac.Signature)-1))
		sigBytes := []byte(mac.Signature)
		if sigBytes[idx] == 'a' {
			sigBytes[idx] = 'b'
		} else {
			sigBytes[idx] = 'a'
		}
		mac.Signature = string(sigBytes)

		raw, err := mac.Serialize()
		require.NoError(ht, err)

		_, _, err = provider.VerifyAndExtract(ctx, raw)
		require.True(ht, errors.Is(err, ErrInvalidSignature), "expected ErrInvalidSignature, got %v", err)
	})
}

func TestStore_LabelAndSecretNameBounds_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		taskID := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(400))

		label1 := TaskLabelValue(taskID)
		label2 := TaskLabelValue(taskID)
		require.Equal(ht, label1, label2, "TaskLabelValue must be deterministic")
		require.LessOrEqual(ht, len(label1), 63, "TaskLabelValue must be <= 63 chars")
		errs := validation.IsValidLabelValue(label1)
		require.Empty(ht, errs, "TaskLabelValue(%q) = %q is not a valid K8s label value: %v", taskID, label1, errs)

		sec1 := SecretName(taskID)
		sec2 := SecretName(taskID)
		require.Equal(ht, sec1, sec2, "SecretName must be deterministic")
		require.LessOrEqual(ht, len(sec1), 253, "SecretName must be <= 253 chars")
	})
}
