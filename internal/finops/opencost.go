package finops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/pkg/loadtest"
)

const (
	defaultOpenCostTimeout  = 3 * time.Second
	maxOpenCostResponseSize = 4 * 1024 * 1024 // 4 MiB cap
	bytesPerGiB             = 1024.0 * 1024.0 * 1024.0
)

// EnvironmentAllocation holds live hourly compute spend and resource usage reported by OpenCost / Kubecost.
type EnvironmentAllocation struct {
	HourlyComputeUSD float64
	CPUCores         float64
	MemoryGiB        float64
}

// OpenCostClient queries an OpenCost or Kubecost Allocation API endpoint to enrich
// preview environment cost estimates with live cluster allocation data.
type OpenCostClient struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

// NewOpenCostClient constructs an SSRF-hardened OpenCostClient for baseURL.
// Returns nil when baseURL is empty.
func NewOpenCostClient(baseURL string, httpClient *http.Client) *OpenCostClient {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil
	}
	return &OpenCostClient{
		baseURL:    trimmed,
		httpClient: newSafeOutboundClient(httpClient, defaultOpenCostTimeout),
		timeout:    defaultOpenCostTimeout,
	}
}

// OpenCostClientFromEnv constructs an OpenCostClient from DIVERGE_OPENCOST_URL when set.
func OpenCostClientFromEnv() *OpenCostClient {
	return NewOpenCostClient(os.Getenv(metrics.EnvOpenCostURL), nil)
}

type openCostAllocationResponse struct {
	Code int                                  `json:"code"`
	Data []map[string]openCostAllocationEntry `json:"data"`
}

type openCostAllocationEntry struct {
	Name         string             `json:"name"`
	Minutes      float64            `json:"minutes"`
	CPUCoreHours float64            `json:"cpuCoreHours"`
	CPUCost      float64            `json:"cpuCost"`
	RAMByteHours float64            `json:"ramByteHours"`
	RAMCost      float64            `json:"ramCost"`
	PVCost       float64            `json:"pvCost"`
	NetworkCost  float64            `json:"networkCost"`
	TotalCost    float64            `json:"totalCost"`
	Properties   openCostEntryProps `json:"properties"`
}

type openCostEntryProps struct {
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

// FetchEnvironmentAllocations queries OpenCost's /allocation/compute API for a 1h window
// and returns hourly allocations keyed by "<namespace>/<environment>".
func (c *OpenCostClient) FetchEnvironmentAllocations(ctx context.Context, namespace string) (map[string]EnvironmentAllocation, error) {
	var namespaces []string
	if trimmed := strings.TrimSpace(namespace); trimmed != "" {
		namespaces = []string{trimmed}
	}
	return c.FetchAllocationsForNamespaces(ctx, namespaces)
}

// FetchAllocationsForNamespaces queries OpenCost's Allocation API for the given workload namespaces
// (or cluster-wide when namespaces is empty) and returns hourly allocations keyed by "<namespace>/<environment>".
func (c *OpenCostClient) FetchAllocationsForNamespaces(ctx context.Context, namespaces []string) (map[string]EnvironmentAllocation, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}

	parsedURL, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, errors.New("parsing OpenCost URL: malformed URL")
	}
	trimmedPath := strings.TrimRight(parsedURL.Path, "/")
	if !strings.HasSuffix(trimmedPath, "/allocation/compute") && !strings.HasSuffix(trimmedPath, "/allocation") {
		parsedURL.Path = trimmedPath + "/allocation/compute"
	} else {
		parsedURL.Path = trimmedPath
	}
	parsedURL.RawPath = ""

	var cleanNamespaces []string
	seenNS := make(map[string]struct{}, len(namespaces))
	for _, raw := range namespaces {
		ns := strings.TrimSpace(raw)
		if ns == "" {
			continue
		}
		if _, exists := seenNS[ns]; !exists {
			seenNS[ns] = struct{}{}
			cleanNamespaces = append(cleanNamespaces, ns)
		}
	}

	q := parsedURL.Query()
	if q.Get("window") == "" {
		q.Set("window", "1h")
	}
	if q.Get("aggregate") == "" {
		q.Set("aggregate", "namespace,label:divergedev.com/environment")
	}
	if len(cleanNamespaces) > 0 && q.Get("filter") == "" {
		quoted := make([]string, 0, len(cleanNamespaces))
		for _, ns := range cleanNamespaces {
			quoted = append(quoted, fmt.Sprintf("%q", ns))
		}
		q.Set("filter", "namespace:"+strings.Join(quoted, ","))
	}
	parsedURL.RawQuery = q.Encode()

	finalURL := parsedURL.String()
	if err := loadtest.ValidateTargetURL(finalURL); err != nil {
		return nil, fmt.Errorf("invalid OpenCost URL: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, finalURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building OpenCost request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying OpenCost allocation API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenCost allocation API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenCostResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading OpenCost response: %w", err)
	}
	if len(body) > maxOpenCostResponseSize {
		return nil, errors.New("OpenCost response exceeded 4 MiB limit")
	}

	defaultNS := ""
	if len(cleanNamespaces) == 1 {
		defaultNS = cleanNamespaces[0]
	}
	return parseOpenCostPayload(body, defaultNS)
}

func parseOpenCostPayload(body []byte, defaultNamespace string) (map[string]EnvironmentAllocation, error) {
	var payload openCostAllocationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decoding OpenCost JSON: %w", err)
	}

	type stepAccumulator struct {
		sum   EnvironmentAllocation
		steps int
	}
	byKey := make(map[string]*stepAccumulator)

	for _, stepMap := range payload.Data {
		if len(stepMap) == 0 {
			continue
		}
		stepTotals := make(map[string]EnvironmentAllocation)
		for mapKey, entry := range stepMap {
			ns, envName, ok := resolveAllocationIdentity(mapKey, entry, defaultNamespace)
			if !ok {
				continue
			}
			alloc, valid := scaleEntryToHourly(entry)
			if !valid {
				continue
			}
			fullKey := ns + "/" + envName
			cur := stepTotals[fullKey]
			cur.HourlyComputeUSD += alloc.HourlyComputeUSD
			cur.CPUCores += alloc.CPUCores
			cur.MemoryGiB += alloc.MemoryGiB
			stepTotals[fullKey] = cur
		}
		for fullKey, stepAlloc := range stepTotals {
			acc, exists := byKey[fullKey]
			if !exists {
				acc = &stepAccumulator{}
				byKey[fullKey] = acc
			}
			acc.sum.HourlyComputeUSD += stepAlloc.HourlyComputeUSD
			acc.sum.CPUCores += stepAlloc.CPUCores
			acc.sum.MemoryGiB += stepAlloc.MemoryGiB
			acc.steps++
		}
	}

	out := make(map[string]EnvironmentAllocation, len(byKey))
	for k, acc := range byKey {
		if acc.steps <= 0 {
			continue
		}
		denom := float64(acc.steps)
		out[k] = EnvironmentAllocation{
			HourlyComputeUSD: acc.sum.HourlyComputeUSD / denom,
			CPUCores:         acc.sum.CPUCores / denom,
			MemoryGiB:        acc.sum.MemoryGiB / denom,
		}
	}
	return out, nil
}

func resolveAllocationIdentity(mapKey string, entry openCostAllocationEntry, defaultNamespace string) (string, string, bool) {
	isIdleOrUnallocated := func(s string) bool {
		trimmed := strings.TrimSpace(s)
		return trimmed == "" || strings.HasPrefix(trimmed, "__")
	}

	ns := strings.TrimSpace(entry.Properties.Namespace)
	var envName string
	if len(entry.Properties.Labels) > 0 {
		for _, labelKey := range []string{"divergedev.com/environment", "divergedev_com_environment"} {
			if v := strings.TrimSpace(entry.Properties.Labels[labelKey]); !isIdleOrUnallocated(v) {
				envName = v
				break
			}
		}
	}

	candidate := strings.TrimSpace(mapKey)
	if candidate == "" {
		candidate = strings.TrimSpace(entry.Name)
	}
	if parts := strings.Split(candidate, "/"); len(parts) >= 2 {
		if ns == "" && !isIdleOrUnallocated(parts[0]) {
			ns = strings.TrimSpace(parts[0])
		}
		if envName == "" && !isIdleOrUnallocated(parts[1]) {
			envName = strings.TrimSpace(parts[1])
		}
	} else if envName == "" && !isIdleOrUnallocated(candidate) {
		envName = candidate
	}

	if ns == "" {
		ns = defaultNamespace
	}
	if ns == "" {
		ns = "default"
	}
	if isIdleOrUnallocated(ns) || isIdleOrUnallocated(envName) {
		return "", "", false
	}
	return ns, envName, true
}

func scaleEntryToHourly(entry openCostAllocationEntry) (EnvironmentAllocation, bool) {
	minutes := entry.Minutes
	if !isFinitePositive(minutes) {
		minutes = 60.0
	}
	scale := 60.0 / minutes
	if !isFiniteNonNegative(scale) || scale <= 0 {
		return EnvironmentAllocation{}, false
	}

	cost := entry.TotalCost
	if !isFiniteNonNegative(cost) || cost == 0 {
		sumCost := zeroIfInvalid(entry.CPUCost) + zeroIfInvalid(entry.RAMCost) + zeroIfInvalid(entry.PVCost) + zeroIfInvalid(entry.NetworkCost)
		if sumCost > 0 {
			cost = sumCost
		} else if !isFiniteNonNegative(cost) {
			return EnvironmentAllocation{}, false
		}
	}

	hourlyCost := cost * scale
	cpuCores := zeroIfInvalid(entry.CPUCoreHours) * scale
	memGiB := (zeroIfInvalid(entry.RAMByteHours) / bytesPerGiB) * scale

	if !isFiniteNonNegative(hourlyCost) || !isFiniteNonNegative(cpuCores) || !isFiniteNonNegative(memGiB) {
		return EnvironmentAllocation{}, false
	}
	return EnvironmentAllocation{
		HourlyComputeUSD: hourlyCost,
		CPUCores:         cpuCores,
		MemoryGiB:        memGiB,
	}, true
}

func isFiniteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func zeroIfInvalid(v float64) float64 {
	if !isFiniteNonNegative(v) {
		return 0
	}
	return v
}

// newSafeOutboundClient wraps baseClient (or constructs a fresh http.Client) with dial-time
// SSRF IP validation (blocking cloud metadata and link-local addresses) and redirect blocking.
func newSafeOutboundClient(baseClient *http.Client, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	safeDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", addr, err)
		}
		if loadtest.IsProhibitedHost(host) {
			return nil, fmt.Errorf("connection to prohibited host %s blocked", host)
		}

		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else {
			resolved, lookupErr := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if lookupErr != nil {
				return nil, fmt.Errorf("resolving host %q: %w", host, lookupErr)
			}
			ips = resolved
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no IP addresses resolved for host %s", host)
		}
		for _, ip := range ips {
			if loadtest.IsProhibitedIP(ip) {
				return nil, fmt.Errorf("connection to prohibited IP %s blocked", ip.String())
			}
		}

		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}

	var out http.Client
	if baseClient != nil {
		out = *baseClient
	}
	if out.Timeout <= 0 {
		out.Timeout = timeout
	}
	out.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	switch tr := out.Transport.(type) {
	case nil:
		cloned := http.DefaultTransport.(*http.Transport).Clone()
		cloned.DialContext = safeDial
		out.Transport = cloned
	case *http.Transport:
		cloned := tr.Clone()
		cloned.DialContext = safeDial
		out.Transport = cloned
	}
	return &out
}
