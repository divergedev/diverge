package finops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/pkg/loadtest"
)

const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	DefaultBudgetAlertThresholdPercent = 80.0
	DefaultBudgetAlertCooldown         = time.Hour
	defaultWebhookTimeout              = 5 * time.Second
	defaultSecretWebhookKey            = "webhook-url"
)

// BudgetAlertInput describes the evaluated namespace quota and FinOps burn state.
type BudgetAlertInput struct {
	Namespace             string
	Environment           string
	HourlyCostUSD         float64
	MaxHourlyCostUSD      float64
	ActiveEnvironments    int
	MaxActiveEnvironments int
	CNPGClusters          int
	MaxCNPGClusters       int
	Policy                string
	Exceeded              bool
	Reason                string
	NamespaceAnnotations  map[string]string
}

// BudgetAlertPayload is the JSON body sent to Slack Incoming Webhooks and generic HTTP webhook receivers.
type BudgetAlertPayload struct {
	Text                  string    `json:"text"`
	Event                 string    `json:"event"`
	Severity              string    `json:"severity"`
	Namespace             string    `json:"namespace"`
	Environment           string    `json:"environment,omitempty"`
	HourlyCostUSD         float64   `json:"hourlyCostUsd"`
	MaxHourlyCostUSD      float64   `json:"maxHourlyCostUsd,omitempty"`
	ActiveEnvironments    int       `json:"activeEnvironments"`
	MaxActiveEnvironments int       `json:"maxActiveEnvironments,omitempty"`
	CNPGClusters          int       `json:"cnpgClusters,omitempty"`
	MaxCNPGClusters       int       `json:"maxCnpgClusters,omitempty"`
	UtilizationPercent    float64   `json:"utilizationPercent"`
	ThresholdPercent      float64   `json:"thresholdPercent"`
	Policy                string    `json:"policy,omitempty"`
	Reason                string    `json:"reason"`
	Timestamp             time.Time `json:"timestamp"`
}

type namespaceAlertState struct {
	lastSeverity string
	lastSentAt   time.Time
}

// BudgetAlertDispatcher evaluates FinOps quota utilization with hysteresis/cooldown deduplication
// and dispatches Slack-compatible JSON webhook notifications.
type BudgetAlertDispatcher struct {
	WebhookURL              string
	DefaultThresholdPercent float64
	Cooldown                time.Duration
	HTTPClient              *http.Client
	NowFunc                 func() time.Time

	mu     sync.Mutex
	states map[string]namespaceAlertState
}

// NewBudgetAlertDispatcher constructs a BudgetAlertDispatcher initialized from environment defaults.
func NewBudgetAlertDispatcher(webhookURL string, httpClient *http.Client) *BudgetAlertDispatcher {
	threshold := DefaultBudgetAlertThresholdPercent
	if raw := strings.TrimSuffix(strings.TrimSpace(os.Getenv(metrics.EnvBudgetAlertThreshold)), "%"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0 && parsed <= 100 {
			threshold = parsed
		}
	}

	cooldown := DefaultBudgetAlertCooldown
	if raw := strings.TrimSpace(os.Getenv(metrics.EnvBudgetAlertCooldown)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			cooldown = parsed
		}
	}

	if strings.TrimSpace(webhookURL) == "" {
		webhookURL = strings.TrimSpace(os.Getenv(metrics.EnvBudgetWebhookURL))
	}

	return &BudgetAlertDispatcher{
		WebhookURL:              strings.TrimSpace(webhookURL),
		DefaultThresholdPercent: threshold,
		Cooldown:                cooldown,
		HTTPClient:              newSafeOutboundClient(httpClient, defaultWebhookTimeout),
		states:                  make(map[string]namespaceAlertState),
	}
}

// ResolveThresholdPercent returns the effective alert threshold percentage for a namespace.
func (d *BudgetAlertDispatcher) ResolveThresholdPercent(annotations map[string]string) float64 {
	base := DefaultBudgetAlertThresholdPercent
	if d != nil && d.DefaultThresholdPercent > 0 && d.DefaultThresholdPercent <= 100 {
		base = d.DefaultThresholdPercent
	}
	if len(annotations) > 0 {
		if raw := strings.TrimSuffix(strings.TrimSpace(annotations[metrics.AnnotationBudgetAlertThreshold]), "%"); raw != "" {
			if parsed, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0 && parsed <= 100 {
				return parsed
			}
		}
	}
	return base
}

// ComputeUtilization computes maximum quota utilization percentage and human-readable warning reason.
func ComputeUtilization(in BudgetAlertInput, thresholdPct float64) (float64, string) {
	var maxUtil float64
	var reasons []string

	if in.MaxHourlyCostUSD > 0 {
		pct := (in.HourlyCostUSD / in.MaxHourlyCostUSD) * 100.0
		if pct > maxUtil {
			maxUtil = pct
		}
		if pct >= thresholdPct {
			reasons = append(reasons, fmt.Sprintf("Hourly spend at %.0f%% ($%.3f / $%.3f/hr)", pct, in.HourlyCostUSD, in.MaxHourlyCostUSD))
		}
	}
	if in.MaxActiveEnvironments > 0 {
		pct := (float64(in.ActiveEnvironments) / float64(in.MaxActiveEnvironments)) * 100.0
		if pct > maxUtil {
			maxUtil = pct
		}
		if pct >= thresholdPct {
			reasons = append(reasons, fmt.Sprintf("Active environments at %d/%d (%.0f%% of quota)", in.ActiveEnvironments, in.MaxActiveEnvironments, pct))
		}
	}
	if in.MaxCNPGClusters > 0 {
		pct := (float64(in.CNPGClusters) / float64(in.MaxCNPGClusters)) * 100.0
		if pct > maxUtil {
			maxUtil = pct
		}
		if pct >= thresholdPct {
			reasons = append(reasons, fmt.Sprintf("CNPG clusters at %d/%d (%.0f%% of quota)", in.CNPGClusters, in.MaxCNPGClusters, pct))
		}
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = strings.Join(reasons, "; ")
	}
	return maxUtil, reason
}

// EvaluateAndNotify checks whether the namespace crossed the warning or critical FinOps threshold,
// applies per-namespace hysteresis and cooldown deduplication, and POSTs a webhook alert if configured.
// Returns (sent, severity, reason, err).
func (d *BudgetAlertDispatcher) EvaluateAndNotify(ctx context.Context, reader client.Reader, in BudgetAlertInput) (bool, string, string, error) {
	if d == nil {
		return false, "", "", nil
	}
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		ns = "default"
	}

	thresholdPct := d.ResolveThresholdPercent(in.NamespaceAnnotations)
	utilPct, reason := ComputeUtilization(in, thresholdPct)
	if in.Exceeded && utilPct < 100.0 {
		utilPct = 100.0
	}

	now := time.Now().UTC()
	if d.NowFunc != nil {
		now = d.NowFunc().UTC()
	}

	d.mu.Lock()
	if d.states == nil {
		d.states = make(map[string]namespaceAlertState)
	}
	if !in.Exceeded && utilPct < thresholdPct {
		// Hysteresis reset: utilization dropped back under warning threshold.
		delete(d.states, ns)
		d.mu.Unlock()
		return false, "", "", nil
	}

	severity := SeverityWarning
	if in.Exceeded || utilPct >= 100.0-1e-9 {
		severity = SeverityCritical
	}

	prev, hasPrev := d.states[ns]
	shouldFire := false
	switch {
	case !hasPrev:
		shouldFire = true
	case severity == SeverityCritical && prev.lastSeverity != SeverityCritical:
		// Escalating from warning -> critical always fires immediately without waiting for cooldown.
		shouldFire = true
	case d.Cooldown == 0 || !now.Before(prev.lastSentAt.Add(d.Cooldown)):
		shouldFire = true
	}
	if !shouldFire {
		d.mu.Unlock()
		return false, severity, reason, nil
	}
	d.mu.Unlock()

	targetURL, err := d.resolveWebhookURL(ctx, reader, ns, in.NamespaceAnnotations)
	if err != nil {
		metrics.FinOpsBudgetAlertsTotal.WithLabelValues(ns, severity, "error").Inc()
		return false, severity, reason, err
	}
	if targetURL == "" {
		return false, severity, reason, nil
	}

	if reason == "" {
		reason = fmt.Sprintf("Namespace %s FinOps quota utilization at %.0f%%", ns, utilPct)
	}

	icon := ":warning:"
	title := "Diverge FinOps Budget Warning"
	if severity == SeverityCritical {
		icon = ":rotating_light:"
		title = "Diverge FinOps Quota Exceeded"
	}
	text := fmt.Sprintf("%s *%s* (`%s`): %s", icon, title, ns, reason)
	if in.Environment != "" {
		text += fmt.Sprintf(" [env: `%s`]", in.Environment)
	}

	payload := BudgetAlertPayload{
		Text:                  text,
		Event:                 "finops.budget_alert",
		Severity:              severity,
		Namespace:             ns,
		Environment:           in.Environment,
		HourlyCostUSD:         in.HourlyCostUSD,
		MaxHourlyCostUSD:      in.MaxHourlyCostUSD,
		ActiveEnvironments:    in.ActiveEnvironments,
		MaxActiveEnvironments: in.MaxActiveEnvironments,
		CNPGClusters:          in.CNPGClusters,
		MaxCNPGClusters:       in.MaxCNPGClusters,
		UtilizationPercent:    utilPct,
		ThresholdPercent:      thresholdPct,
		Policy:                in.Policy,
		Reason:                reason,
		Timestamp:             now,
	}

	if postErr := d.postWebhook(ctx, targetURL, payload); postErr != nil {
		metrics.FinOpsBudgetAlertsTotal.WithLabelValues(ns, severity, "error").Inc()
		return false, severity, reason, postErr
	}

	d.mu.Lock()
	d.states[ns] = namespaceAlertState{
		lastSeverity: severity,
		lastSentAt:   now,
	}
	d.mu.Unlock()

	metrics.FinOpsBudgetAlertsTotal.WithLabelValues(ns, severity, "sent").Inc()
	return true, severity, reason, nil
}

func (d *BudgetAlertDispatcher) resolveWebhookURL(ctx context.Context, reader client.Reader, namespace string, annotations map[string]string) (string, error) {
	if len(annotations) > 0 {
		if secretRef := strings.TrimSpace(annotations[metrics.AnnotationBudgetAlertSecret]); secretRef != "" {
			secretName := secretRef
			secretKey := defaultSecretWebhookKey
			if namePart, keyPart, hasSlash := strings.Cut(secretRef, "/"); hasSlash {
				secretName = strings.TrimSpace(namePart)
				if trimmedKey := strings.TrimSpace(keyPart); trimmedKey != "" {
					secretKey = trimmedKey
				}
			}
			if secretName == "" {
				return "", fmt.Errorf("invalid budget alert secret annotation %q in namespace %s: secret name is empty", secretRef, namespace)
			}
			if reader == nil {
				return "", fmt.Errorf("resolving budget alert secret %s/%s: Kubernetes reader is not configured", namespace, secretName)
			}
			secCtx, cancel := context.WithTimeout(ctx, defaultWebhookTimeout)
			var sec corev1.Secret
			err := reader.Get(secCtx, types.NamespacedName{Namespace: namespace, Name: secretName}, &sec)
			cancel()
			if err != nil {
				return "", fmt.Errorf("resolving budget alert secret %s/%s: %w", namespace, secretName, err)
			}
			val := strings.TrimSpace(string(sec.Data[secretKey]))
			if val == "" && len(sec.StringData) > 0 {
				val = strings.TrimSpace(sec.StringData[secretKey])
			}
			if val == "" {
				return "", fmt.Errorf("budget alert secret %s/%s key %q is missing or empty", namespace, secretName, secretKey)
			}
			return val, nil
		}
	}

	if trimmed := strings.TrimSpace(d.WebhookURL); trimmed != "" {
		return trimmed, nil
	}
	return strings.TrimSpace(os.Getenv(metrics.EnvBudgetWebhookURL)), nil
}

func sanitizeWebhookValidationError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "prohibited"):
		return errors.New("invalid budget webhook URL: destination is prohibited")
	case strings.Contains(msg, "allowed hosts"):
		return errors.New("invalid budget webhook URL: host is not in the allowed hosts list")
	case strings.Contains(msg, "valid http or https"):
		return errors.New("invalid budget webhook URL: scheme must be http or https")
	case strings.Contains(msg, "host cannot be empty"):
		return errors.New("invalid budget webhook URL: host cannot be empty")
	default:
		// Never wrap or echo raw URL parse errors, which may contain secret tokens in the webhook path.
		return errors.New("invalid budget webhook URL")
	}
}

func (d *BudgetAlertDispatcher) postWebhook(ctx context.Context, targetURL string, payload BudgetAlertPayload) error {
	if err := loadtest.ValidateTargetURL(targetURL); err != nil {
		return sanitizeWebhookValidationError(err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling budget alert payload: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, defaultWebhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("constructing budget alert HTTP request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "diverge-finops-alerter/1.0")

	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = newSafeOutboundClient(nil, defaultWebhookTimeout)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// Avoid echoing raw webhook URLs (which may contain Slack/Teams secret tokens in path) in error messages.
		return errors.New("delivering budget alert webhook failed")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("budget alert webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
