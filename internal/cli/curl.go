package cli

import (
	"fmt"
	"io"
	"net/http"
	urlPkg "net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

type curlOptions struct {
	baseURL         string
	headerKey       string
	headerValue     string
	method          string
	data            string
	headers         []string
	compareBaseline bool
	timeout         time.Duration
}

type curlResponse struct {
	StatusCode int
	Status     string
	Duration   time.Duration
	Headers    http.Header
	Body       string
}

func newCurlCmd(app *App) *cobra.Command {
	opts := &curlOptions{}

	cmd := &cobra.Command{
		Use:   "curl <env-name> [path-or-url]",
		Short: "Send an HTTP request to a preview environment with automatic routing headers",
		Long: `Resolves a preview Environment (or PreviewGroup) and sends an HTTP request
with the environment's routing header (x-diverge-env) and W3C Baggage header
(baggage: diverge.env=<value>) automatically injected.

Use --compare-baseline to send the same request with and without preview routing
headers and compare status codes, latency, and response payloads side-by-side.`,
		Example: `	# Request /api/orders on preview environment pr-42
	diverge curl pr-42 /api/orders

	# Compare baseline vs preview response side-by-side
	diverge curl pr-42 /api/checkout --compare-baseline

	# POST JSON payload to a preview environment with an explicit base URL
	diverge curl pr-42 /api/items -X POST -d '{"sku":"abc"}' --base-url https://preview.example.com`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeEnvironmentNames(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			envName := args[0]
			pathOrURL := "/"
			if len(args) > 1 {
				pathOrURL = args[1]
			}

			baseURL, headerKey, headerValue, err := resolveCurlRouting(cmd, app, envName, pathOrURL, opts)
			if err != nil {
				return err
			}

			targetURL, err := buildCurlTargetURL(baseURL, pathOrURL)
			if err != nil {
				return err
			}

			method := strings.ToUpper(strings.TrimSpace(opts.method))
			if method == "" {
				if opts.data != "" {
					method = http.MethodPost
				} else {
					method = http.MethodGet
				}
			}

			httpClient := &http.Client{
				Timeout: opts.timeout,
			}

			out := cmd.OutOrStdout()

			if opts.compareBaseline {
				baseResp, err := executeCurlRequest(cmd, httpClient, method, targetURL, opts.data, opts.headers, "", "")
				if err != nil {
					return fmt.Errorf("baseline request failed: %w", err)
				}
				prevResp, err := executeCurlRequest(cmd, httpClient, method, targetURL, opts.data, opts.headers, headerKey, headerValue)
				if err != nil {
					return fmt.Errorf("preview request failed: %w", err)
				}

				printCurlComparison(out, envName, baseResp, prevResp)
				return nil
			}

			resp, err := executeCurlRequest(cmd, httpClient, method, targetURL, opts.data, opts.headers, headerKey, headerValue)
			if err != nil {
				return err
			}

			_, _ = io.WriteString(out, resp.Body)
			if !strings.HasSuffix(resp.Body, "\n") {
				_, _ = io.WriteString(out, "\n")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.baseURL, "base-url", "", "Override the base URL for the request")
	cmd.Flags().StringVar(&opts.headerKey, "header-key", "", "Override the routing header key (default: x-diverge-env)")
	cmd.Flags().StringVar(&opts.headerValue, "header-value", "", "Override the routing header value (default: environment name)")
	cmd.Flags().StringVarP(&opts.method, "method", "X", "GET", "HTTP request method")
	cmd.Flags().StringVarP(&opts.data, "data", "d", "", "HTTP request body data")
	cmd.Flags().StringArrayVarP(&opts.headers, "header", "H", nil, "Additional HTTP header in 'Key: Value' format")
	cmd.Flags().BoolVar(&opts.compareBaseline, "compare-baseline", false, "Fetch both baseline and preview responses and compare them")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 30*time.Second, "HTTP request timeout")

	return cmd
}

func resolveCurlRouting(cmd *cobra.Command, app *App, envName, pathOrURL string, opts *curlOptions) (string, string, string, error) {
	baseURL := strings.TrimSpace(opts.baseURL)
	headerKey := "x-diverge-env"
	headerValue := envName

	isFullURL := strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://")

	if app != nil {
		if app.Namespace == "" {
			_ = app.ResolveNamespace()
		}
		c, _, err := app.KubeClient()
		if err == nil && c != nil {
			ns := app.Namespace
			if ns == "" {
				ns = "default"
			}
			var env divergeiov1alpha1.Environment
			if getErr := c.Get(cmd.Context(), client.ObjectKey{Name: envName, Namespace: ns}, &env); getErr == nil {
				if baseURL == "" && env.Status.URL != "" {
					baseURL = env.Status.URL
				}
				if env.Spec.Routing.HeaderKey != "" {
					headerKey = env.Spec.Routing.HeaderKey
				}
				if env.Spec.Routing.HeaderValue != "" {
					headerValue = env.Spec.Routing.HeaderValue
				}
			} else {
				var pg divergeiov1alpha1.PreviewGroup
				pgErr := c.Get(cmd.Context(), client.ObjectKey{Name: envName, Namespace: ns}, &pg)
				if pgErr != nil {
					pgErr = c.Get(cmd.Context(), client.ObjectKey{Name: envName}, &pg)
				}
				if pgErr == nil {
					if baseURL == "" && pg.Spec.Routing.ExternalURL != "" {
						baseURL = pg.Spec.Routing.ExternalURL
					}
					if baseURL == "" {
						for _, svc := range pg.Status.Services {
							if svc.URL != "" {
								baseURL = svc.URL
								break
							}
						}
					}
					if pg.Spec.Routing.HeaderKey != "" {
						headerKey = pg.Spec.Routing.HeaderKey
					}
					if pg.Spec.Routing.HeaderValue != "" {
						headerValue = pg.Spec.Routing.HeaderValue
					}
				} else if baseURL == "" && !isFullURL {
					return "", "", "", fmt.Errorf("failed to get environment %s: %w", envName, getErr)
				}
			}
		} else if baseURL == "" && !isFullURL {
			return "", "", "", fmt.Errorf("failed to connect to cluster (and --base-url was not provided): %w", err)
		}
	}

	if opts.headerKey != "" {
		headerKey = opts.headerKey
	}
	if opts.headerValue != "" {
		headerValue = opts.headerValue
	}

	return baseURL, headerKey, headerValue, nil
}

func buildCurlTargetURL(baseURL, pathOrURL string) (string, error) {
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		parsed, err := urlPkg.Parse(pathOrURL)
		if err != nil || parsed.Hostname() == "" {
			return "", fmt.Errorf("invalid target URL %q", pathOrURL)
		}
		return parsed.String(), nil
	}

	if baseURL == "" {
		return "", fmt.Errorf("environment URL is not set; pass --base-url or a full URL")
	}

	baseParsed, err := urlPkg.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	if baseParsed.Scheme != "http" && baseParsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q (only http/https allowed)", baseParsed.Scheme)
	}
	if baseParsed.Hostname() == "" {
		return "", fmt.Errorf("base URL %q has no hostname", baseURL)
	}

	if pathOrURL == "" {
		pathOrURL = "/"
	}
	if !strings.HasPrefix(pathOrURL, "/") {
		pathOrURL = "/" + pathOrURL
	}

	relParsed, err := urlPkg.Parse(pathOrURL)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", pathOrURL, err)
	}

	resolved := baseParsed.ResolveReference(relParsed)
	return resolved.String(), nil
}

func executeCurlRequest(cmd *cobra.Command, httpClient *http.Client, method, targetURL, bodyData string, extraHeaders []string, headerKey, headerValue string) (*curlResponse, error) {
	var bodyReader io.Reader
	if bodyData != "" {
		bodyReader = strings.NewReader(bodyData)
	}

	req, err := http.NewRequestWithContext(cmd.Context(), method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for _, rawHdr := range extraHeaders {
		parts := strings.SplitN(rawHdr, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header %q (expected 'Key: Value')", rawHdr)
		}
		req.Header.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	if headerKey != "" && headerValue != "" {
		req.Header.Set(headerKey, headerValue)
		baggageItem := "diverge.env=" + headerValue
		if existing := req.Header.Get("baggage"); existing != "" {
			req.Header.Set("baggage", existing+","+baggageItem)
		} else {
			req.Header.Set("baggage", baggageItem)
		}
	}

	start := time.Now()
	resp, err := httpClient.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return &curlResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Duration:   duration,
		Headers:    resp.Header.Clone(),
		Body:       string(respBytes),
	}, nil
}

func printCurlComparison(out io.Writer, envName string, baseResp, prevResp *curlResponse) {
	_, _ = fmt.Fprintf(out, "=== Baseline (HTTP %d, %s) ===\n", baseResp.StatusCode, baseResp.Duration.Round(time.Millisecond))
	_, _ = fmt.Fprintln(out, strings.TrimRight(baseResp.Body, "\n"))
	_, _ = fmt.Fprintf(out, "\n=== Preview %s (HTTP %d, %s) ===\n", envName, prevResp.StatusCode, prevResp.Duration.Round(time.Millisecond))
	_, _ = fmt.Fprintln(out, strings.TrimRight(prevResp.Body, "\n"))
	_, _ = fmt.Fprintln(out, "\n=== Comparison ===")
	if baseResp.StatusCode == prevResp.StatusCode {
		_, _ = fmt.Fprintf(out, "Status: IDENTICAL (%d)\n", prevResp.StatusCode)
	} else {
		_, _ = fmt.Fprintf(out, "Status: CHANGED (%d -> %d)\n", baseResp.StatusCode, prevResp.StatusCode)
	}
	if baseResp.Body == prevResp.Body {
		_, _ = fmt.Fprintln(out, "Body:   IDENTICAL")
	} else {
		_, _ = fmt.Fprintln(out, "Body:   CHANGED")
		printLineDiff(out, baseResp.Body, prevResp.Body)
	}
}

func printLineDiff(out io.Writer, baseBody, prevBody string) {
	baseLines := strings.Split(strings.TrimRight(baseBody, "\n"), "\n")
	prevLines := strings.Split(strings.TrimRight(prevBody, "\n"), "\n")

	for _, l := range baseLines {
		if !containsLine(prevLines, l) {
			_, _ = fmt.Fprintf(out, "- %s\n", l)
		}
	}
	for _, l := range prevLines {
		if !containsLine(baseLines, l) {
			_, _ = fmt.Fprintf(out, "+ %s\n", l)
		}
	}
}

func containsLine(lines []string, target string) bool {
	for _, l := range lines {
		if l == target {
			return true
		}
	}
	return false
}
