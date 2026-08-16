package notifier_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ContractAssertion struct {
	Method      string
	PathPattern *regexp.Regexp
	Headers     map[string]string
	BodyFields  []string
}

func AssertHTTPContract(t *testing.T, req *http.Request, contract ContractAssertion) {
	t.Helper()
	assert.Equal(t, contract.Method, req.Method)
	assert.Regexp(t, contract.PathPattern, req.URL.Path)
	for k, v := range contract.Headers {
		assert.Equal(t, v, req.Header.Get(k), "header %s", k)
	}

	if len(contract.BodyFields) > 0 && req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		// restore body for subsequent reads if needed
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var bodyMap map[string]interface{}
		err = json.Unmarshal(bodyBytes, &bodyMap)
		require.NoError(t, err)
		for _, field := range contract.BodyFields {
			_, ok := bodyMap[field]
			assert.True(t, ok, "body should contain field %s", field)
		}
	}
}

func TestContract_GitHubNotifier(t *testing.T) {
	var lastReq *http.Request
	var lastBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = r.Clone(context.Background())
		lastBody, _ = io.ReadAll(r.Body)
		lastReq.Body = io.NopCloser(bytes.NewReader(lastBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 12345}`))
	}))
	defer ts.Close()

	n := notifier.NewGitHubNotifier(ts.URL, "fake-token")

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	postContract := ContractAssertion{
		Method:      http.MethodPost,
		PathPattern: regexp.MustCompile(`^/repos/owner/repo/issues/42/comments$`),
		Headers: map[string]string{
			"Accept":        "application/vnd.github.v3+json",
			"Authorization": "Bearer fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, postContract)

	assert.Equal(t, 12345, env.Status.CommentID)

	patchContract := ContractAssertion{
		Method:      http.MethodPatch,
		PathPattern: regexp.MustCompile(`^/repos/owner/repo/issues/comments/12345$`),
		Headers: map[string]string{
			"Accept":        "application/vnd.github.v3+json",
			"Authorization": "Bearer fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err = n.PostEnvironmentReady(context.Background(), env)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, patchContract)
}

func TestContract_GitLabNotifier(t *testing.T) {
	var lastReq *http.Request
	var lastBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = r.Clone(context.Background())
		lastBody, _ = io.ReadAll(r.Body)
		lastReq.Body = io.NopCloser(bytes.NewReader(lastBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 54321}`))
	}))
	defer ts.Close()

	n := notifier.NewGitLabNotifier(ts.URL, "fake-token")

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-env"},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "gitlab",
				Project:  "group/project",
				MR:       99,
			},
		},
	}

	postContract := ContractAssertion{
		Method:      http.MethodPost,
		PathPattern: regexp.MustCompile(`^/api/v4/projects/group/project/merge_requests/99/notes$`),
		Headers: map[string]string{
			"PRIVATE-TOKEN": "fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err := n.PostEnvironmentCreated(context.Background(), env)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, postContract)

	assert.Equal(t, 54321, env.Status.CommentID)

	putContract := ContractAssertion{
		Method:      http.MethodPut,
		PathPattern: regexp.MustCompile(`^/api/v4/projects/group/project/merge_requests/99/notes/54321$`),
		Headers: map[string]string{
			"PRIVATE-TOKEN": "fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err = n.PostEnvironmentReady(context.Background(), env)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, putContract)
}

func TestContract_GitHubPreviewGroupNotifier(t *testing.T) {
	var lastReq *http.Request
	var lastBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = r.Clone(context.Background())
		lastBody, _ = io.ReadAll(r.Body)
		lastReq.Body = io.NopCloser(bytes.NewReader(lastBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 11111}`))
	}))
	defer ts.Close()

	n := notifier.NewGitHubPreviewGroupNotifier(ts.URL, "fake-token")

	pg := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pg"},
		Spec: v1alpha1.PreviewGroupSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "owner/repo",
				MR:       42,
			},
		},
	}

	postContract := ContractAssertion{
		Method:      http.MethodPost,
		PathPattern: regexp.MustCompile(`^/repos/owner/repo/issues/42/comments$`),
		Headers: map[string]string{
			"Accept":        "application/vnd.github.v3+json",
			"Authorization": "Bearer fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err := n.PostGroupCreated(context.Background(), pg)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, postContract)

	assert.Equal(t, int64(11111), pg.Status.CommentID)

	patchContract := ContractAssertion{
		Method:      http.MethodPatch,
		PathPattern: regexp.MustCompile(`^/repos/owner/repo/issues/comments/11111$`),
		Headers: map[string]string{
			"Accept":        "application/vnd.github.v3+json",
			"Authorization": "Bearer fake-token",
			"Content-Type":  "application/json",
		},
		BodyFields: []string{"body"},
	}

	err = n.PostGroupReady(context.Background(), pg)
	require.NoError(t, err)
	AssertHTTPContract(t, lastReq, patchContract)
}
