package async

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	AnnotationStorageEndpoint = "divergedev.com/storage-endpoint"
	AnnotationStorageBucket   = "divergedev.com/storage-bucket"
	maxStorageResponseBytes   = 16 * 1024 * 1024
)

// StorageProvisioner provisions object key-prefix isolation for S3, GCS, and MinIO
// preview environments so preview uploads/mutations do not collide with baseline buckets.
type StorageProvisioner struct {
	Endpoint   string       // Optional S3/MinIO/GCS endpoint override
	Bucket     string       // Optional default bucket name
	Provider   string       // "s3", "gcs", or "minio" (default "s3")
	HTTPClient *http.Client // Optional HTTP client for Teardown API calls
	AccessKey  string       // Optional S3/MinIO access key ID
	SecretKey  string       // Optional S3/MinIO secret access key
	Region     string       // Optional S3 region (default "us-east-1")
	GCSToken   string       // Optional GCS Bearer token
}

// Name returns the provisioner name ("s3", "gcs", or "minio").
func (s *StorageProvisioner) Name() string {
	if p := strings.TrimSpace(s.Provider); p != "" {
		return p
	}
	return "s3"
}

func (s *StorageProvisioner) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *StorageProvisioner) resolveEndpoint(env *v1alpha1.Environment) string {
	if env != nil && env.Annotations != nil {
		if ep := strings.TrimSpace(env.Annotations[AnnotationStorageEndpoint]); ep != "" {
			return ep
		}
	}
	if ep := strings.TrimSpace(s.Endpoint); ep != "" {
		return ep
	}
	return strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_ENDPOINT"))
}

func (s *StorageProvisioner) resolveBucket(env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) string {
	if env != nil && env.Annotations != nil {
		if b := strings.TrimSpace(env.Annotations[AnnotationStorageBucket]); b != "" {
			return b
		}
	}
	if b := strings.TrimSpace(s.Bucket); b != "" {
		return b
	}
	if b := strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_BUCKET")); b != "" {
		return b
	}
	trimmedTarget := strings.Trim(strings.TrimSpace(route.Target), "/")
	if trimmedTarget != "" && trimmedTarget != "default" {
		return trimmedTarget
	}
	return ""
}

func (s *StorageProvisioner) resolveAccessKey() string {
	if ak := strings.TrimSpace(s.AccessKey); ak != "" {
		return ak
	}
	if ak := strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_ACCESS_KEY")); ak != "" {
		return ak
	}
	return strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID"))
}

func (s *StorageProvisioner) resolveSecretKey() string {
	if sk := strings.TrimSpace(s.SecretKey); sk != "" {
		return sk
	}
	if sk := strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_SECRET_KEY")); sk != "" {
		return sk
	}
	return strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
}

func (s *StorageProvisioner) resolveRegion() string {
	if reg := strings.TrimSpace(s.Region); reg != "" {
		return reg
	}
	if reg := strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_REGION")); reg != "" {
		return reg
	}
	if reg := strings.TrimSpace(os.Getenv("AWS_REGION")); reg != "" {
		return reg
	}
	return "us-east-1"
}

func (s *StorageProvisioner) resolveGCSToken() string {
	if tok := strings.TrimSpace(s.GCSToken); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv("DIVERGE_GCS_TOKEN"))
}

// FormatStorageKeyPrefix returns the deterministic object key prefix for an environment, route target, and optional namespace.
func FormatStorageKeyPrefix(target, envName string, namespace ...string) string {
	trimmedTarget := strings.Trim(strings.TrimSpace(target), "/")
	trimmedEnv := strings.Trim(strings.TrimSpace(envName), "/")
	var ns string
	if len(namespace) > 0 {
		ns = strings.Trim(strings.TrimSpace(namespace[0]), "/")
	}
	if ns != "" {
		if trimmedTarget == "" || trimmedTarget == "default" {
			return fmt.Sprintf("previews/%s/%s/", ns, trimmedEnv)
		}
		return fmt.Sprintf("previews/%s/%s/%s/", ns, trimmedEnv, trimmedTarget)
	}
	if trimmedTarget == "" || trimmedTarget == "default" {
		return fmt.Sprintf("previews/%s/", trimmedEnv)
	}
	return fmt.Sprintf("previews/%s/%s/", trimmedEnv, trimmedTarget)
}

// FormatNamespacedStorageKeyPrefix returns the namespace-scoped object key prefix for an environment and route target.
func FormatNamespacedStorageKeyPrefix(namespace, target, envName string) string {
	ns := strings.Trim(strings.TrimSpace(namespace), "/")
	if ns == "" {
		ns = "default"
	}
	return FormatStorageKeyPrefix(target, envName, ns)
}

func isStorageProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "s3", "gcs", "minio":
		return true
	default:
		return false
	}
}

func sanitizeStorageTargetEnvSuffix(target string) string {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	if trimmed == "" || strings.EqualFold(trimmed, "default") {
		return "DEFAULT"
	}
	upper := strings.ToUpper(trimmed)
	var b strings.Builder
	lastUnderscore := false
	for i := 0; i < len(upper); i++ {
		ch := upper[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			b.WriteByte(ch)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	res := strings.Trim(b.String(), "_")
	if res == "" {
		return "DEFAULT"
	}
	return res
}

// Provision computes the preview object storage key prefix and injects environment variables.
func (s *StorageProvisioner) Provision(_ context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return nil, fmt.Errorf("environment with non-empty name is required")
	}

	prefix := FormatNamespacedStorageKeyPrefix(env.Namespace, route.Target, env.Name)
	bucket := s.resolveBucket(env, route)
	endpoint := s.resolveEndpoint(env)

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		trimmedTarget := strings.Trim(strings.TrimSpace(route.Target), "/")
		suffix := sanitizeStorageTargetEnvSuffix(route.Target)

		defaultStorageRouteCount := 0
		sameSuffixCount := 0
		for _, r := range env.Spec.Routing.AsyncRoutes {
			if isStorageProtocol(r.Protocol) && len(r.EnvVarMapping) == 0 {
				defaultStorageRouteCount++
				if sanitizeStorageTargetEnvSuffix(r.Target) == suffix {
					sameSuffixCount++
				}
			}
		}
		if defaultStorageRouteCount > 1 && sameSuffixCount > 1 {
			return nil, fmt.Errorf("multiple storage async routes with target %q require explicit envVarMapping", route.Target)
		}

		isNamedTarget := trimmedTarget != "" && !strings.EqualFold(trimmedTarget, "default")
		if defaultStorageRouteCount <= 1 || !isNamedTarget {
			envVars["DIVERGE_STORAGE_PREFIX"] = prefix
			envVars["S3_KEY_PREFIX"] = prefix
			envVars["AWS_S3_PREFIX"] = prefix
			envVars["GCS_KEY_PREFIX"] = prefix
			if bucket != "" {
				envVars["DIVERGE_STORAGE_BUCKET"] = bucket
			}
			if endpoint != "" {
				envVars["DIVERGE_STORAGE_ENDPOINT"] = endpoint
			}
		}
		if isNamedTarget {
			envVars["DIVERGE_STORAGE_PREFIX_"+suffix] = prefix
			envVars["S3_KEY_PREFIX_"+suffix] = prefix
			envVars["AWS_S3_PREFIX_"+suffix] = prefix
			envVars["GCS_KEY_PREFIX_"+suffix] = prefix
			if bucket != "" {
				envVars["DIVERGE_STORAGE_BUCKET_"+suffix] = bucket
			}
			if endpoint != "" {
				envVars["DIVERGE_STORAGE_ENDPOINT_"+suffix] = endpoint
			}
		}
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = prefix
			} else {
				envVars[envVar] = strings.ReplaceAll(tmpl, "{{ .ResolvedTarget }}", prefix)
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: prefix,
		EnvVars:        envVars,
	}, nil
}

// Teardown validates the environment and deletes all objects under the preview key prefix
// when a storage endpoint and bucket are configured.
func (s *StorageProvisioner) Teardown(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) error {
	if env == nil || strings.TrimSpace(env.Name) == "" {
		return fmt.Errorf("environment with non-empty name is required")
	}

	prefix := FormatNamespacedStorageKeyPrefix(env.Namespace, route.Target, env.Name)
	bucket := s.resolveBucket(env, route)
	endpoint := s.resolveEndpoint(env)
	gcsToken := s.resolveGCSToken()

	if endpoint == "" && strings.EqualFold(s.Name(), "gcs") && gcsToken != "" {
		endpoint = "https://storage.googleapis.com"
	}
	if bucket == "" || endpoint == "" {
		return nil
	}

	if strings.EqualFold(s.Name(), "gcs") {
		return s.teardownGCS(ctx, endpoint, bucket, prefix, gcsToken)
	}
	return s.teardownS3(ctx, endpoint, bucket, prefix)
}

func (s *StorageProvisioner) teardownGCS(ctx context.Context, endpoint, bucket, prefix, token string) error {
	baseEndpoint := strings.TrimRight(endpoint, "/")
	client := s.httpClient()
	pageToken := ""

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		listURL := fmt.Sprintf("%s/storage/v1/b/%s/o?prefix=%s", baseEndpoint, url.PathEscape(bucket), url.QueryEscape(prefix))
		if pageToken != "" {
			listURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return fmt.Errorf("gcs build list request failed: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("gcs list objects failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxStorageResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("gcs read list response failed: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("gcs list objects returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var listResp struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &listResp); err != nil {
			return fmt.Errorf("gcs decode list response failed: %w", err)
		}

		for _, item := range listResp.Items {
			if item.Name == "" {
				continue
			}
			delURL := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", baseEndpoint, url.PathEscape(bucket), url.PathEscape(item.Name))
			delReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
			if err != nil {
				return fmt.Errorf("gcs build delete request for %q failed: %w", item.Name, err)
			}
			if token != "" {
				delReq.Header.Set("Authorization", "Bearer "+token)
			}
			delResp, err := client.Do(delReq)
			if err != nil {
				return fmt.Errorf("gcs delete object %q failed: %w", item.Name, err)
			}
			delBody, _ := io.ReadAll(io.LimitReader(delResp.Body, maxStorageResponseBytes))
			_ = delResp.Body.Close()
			if delResp.StatusCode != http.StatusNotFound && (delResp.StatusCode < 200 || delResp.StatusCode >= 300) {
				return fmt.Errorf("gcs delete object %q returned status %d: %s", item.Name, delResp.StatusCode, strings.TrimSpace(string(delBody)))
			}
		}

		if strings.TrimSpace(listResp.NextPageToken) == "" {
			break
		}
		pageToken = strings.TrimSpace(listResp.NextPageToken)
	}

	return nil
}

type s3ListBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}

type s3ObjectIdentifier struct {
	Key string `xml:"Key"`
}

type s3DeleteRequest struct {
	XMLName xml.Name             `xml:"Delete"`
	Quiet   bool                 `xml:"Quiet"`
	Objects []s3ObjectIdentifier `xml:"Object"`
}

type s3DeleteResult struct {
	XMLName xml.Name `xml:"DeleteResult"`
	Errors  []struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

func (s *StorageProvisioner) teardownS3(ctx context.Context, endpoint, bucket, prefix string) error {
	baseEndpoint := strings.TrimRight(endpoint, "/")
	client := s.httpClient()
	accessKey := s.resolveAccessKey()
	secretKey := s.resolveSecretKey()
	region := s.resolveRegion()
	continuationToken := ""

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		listURL := fmt.Sprintf("%s/%s?list-type=2&prefix=%s", baseEndpoint, url.PathEscape(bucket), url.QueryEscape(prefix))
		if continuationToken != "" {
			listURL += "&continuation-token=" + url.QueryEscape(continuationToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return fmt.Errorf("s3 build list request failed: %w", err)
		}
		if accessKey != "" && secretKey != "" {
			signS3Request(req, nil, accessKey, secretKey, region)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("s3 list objects failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxStorageResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("s3 read list response failed: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("s3 list objects returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var listResult s3ListBucketResult
		if err := xml.Unmarshal(body, &listResult); err != nil {
			return fmt.Errorf("s3 decode list response failed: %w", err)
		}

		var objs []s3ObjectIdentifier
		for _, item := range listResult.Contents {
			if item.Key != "" {
				objs = append(objs, s3ObjectIdentifier{Key: item.Key})
			}
		}

		if len(objs) > 0 {
			if err := s.deleteS3Batch(ctx, client, baseEndpoint, bucket, objs, accessKey, secretKey, region); err != nil {
				return err
			}
		}

		if !listResult.IsTruncated || strings.TrimSpace(listResult.NextContinuationToken) == "" {
			break
		}
		continuationToken = strings.TrimSpace(listResult.NextContinuationToken)
	}

	return nil
}

func (s *StorageProvisioner) deleteS3Batch(ctx context.Context, client *http.Client, baseEndpoint, bucket string, objs []s3ObjectIdentifier, accessKey, secretKey, region string) error {
	payload, err := xml.Marshal(s3DeleteRequest{
		Quiet:   true,
		Objects: objs,
	})
	if err != nil {
		return fmt.Errorf("s3 marshal batch delete failed: %w", err)
	}

	delURL := fmt.Sprintf("%s/%s?delete", baseEndpoint, url.PathEscape(bucket))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("s3 build batch delete request failed: %w", err)
	}
	md5Sum := md5.Sum(payload)
	req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(md5Sum[:]))
	req.Header.Set("Content-Type", "application/xml")
	if accessKey != "" && secretKey != "" {
		signS3Request(req, payload, accessKey, secretKey, region)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 batch delete failed: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxStorageResponseBytes))
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		for _, obj := range objs {
			if err := s.deleteSingleS3Object(ctx, client, baseEndpoint, bucket, obj.Key, accessKey, secretKey, region); err != nil {
				return err
			}
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("s3 batch delete returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) > 0 {
		var delResult s3DeleteResult
		if err := xml.Unmarshal(body, &delResult); err == nil && len(delResult.Errors) > 0 {
			first := delResult.Errors[0]
			return fmt.Errorf("s3 batch delete failed for %d object(s) (first key %q: %s - %s)", len(delResult.Errors), first.Key, first.Code, first.Message)
		}
	}
	return nil
}

func (s *StorageProvisioner) deleteSingleS3Object(ctx context.Context, client *http.Client, baseEndpoint, bucket, key, accessKey, secretKey, region string) error {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	delURL := fmt.Sprintf("%s/%s/%s", baseEndpoint, url.PathEscape(bucket), strings.Join(segments, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
	if err != nil {
		return fmt.Errorf("s3 build delete request for %q failed: %w", key, err)
	}
	if accessKey != "" && secretKey != "" {
		signS3Request(req, nil, accessKey, secretKey, region)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 delete object %q failed: %w", key, err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxStorageResponseBytes))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return fmt.Errorf("s3 delete object %q returned status %d: %s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func signS3Request(req *http.Request, payload []byte, accessKey, secretKey, region string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Host == "" && req.URL != nil {
		req.Host = req.URL.Host
	}

	headersToSign := map[string]string{
		"host":                 req.Host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if contentMD5 := strings.TrimSpace(req.Header.Get("Content-MD5")); contentMD5 != "" {
		headersToSign["content-md5"] = contentMD5
	}

	headerNames := make([]string, 0, len(headersToSign))
	for k := range headersToSign {
		headerNames = append(headerNames, k)
	}
	sort.Strings(headerNames)

	var canonicalHeaders strings.Builder
	for _, k := range headerNames {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headersToSign[k]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(headerNames, ";")

	canonicalURI := "/"
	if req.URL != nil && req.URL.EscapedPath() != "" {
		canonicalURI = req.URL.EscapedPath()
	}

	canonicalQuery := canonicalS3QueryString(req.URL)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature,
	))
}

func canonicalS3QueryString(u *url.URL) string {
	if u == nil || u.RawQuery == "" {
		return ""
	}
	vals := u.Query()
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		escapedK := strings.ReplaceAll(url.QueryEscape(k), "+", "%20")
		vList := vals[k]
		if len(vList) == 0 {
			pairs = append(pairs, escapedK+"=")
			continue
		}
		sort.Strings(vList)
		for _, v := range vList {
			escapedV := strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
			pairs = append(pairs, escapedK+"="+escapedV)
		}
	}
	return strings.Join(pairs, "&")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
