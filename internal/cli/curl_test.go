package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestCurlCmd_SinglePreviewRequest(t *testing.T) {
	var receivedHeader, receivedBaggage, receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("x-diverge-env")
		receivedBaggage = r.Header.Get("baggage")
		receivedPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"preview-pr-42"}`))
	}))
	t.Cleanup(srv.Close)

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-42",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
			URL:   srv.URL,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newCurlCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"pr-42", "/api/v1/status?verbose=1"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "pr-42", receivedHeader)
	assert.Contains(t, receivedBaggage, "diverge.env=pr-42")
	assert.Equal(t, "/api/v1/status?verbose=1", receivedPath)
	assert.Contains(t, out.String(), `{"version":"preview-pr-42"}`)
}

func TestCurlCmd_CompareBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-diverge-env") == "pr-42" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("checkout_flow: v2\nstatus: preview\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("checkout_flow: v1\nstatus: baseline\n"))
	}))
	t.Cleanup(srv.Close)

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-42",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
			URL:   srv.URL,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newCurlCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"pr-42", "/api/checkout", "--compare-baseline"})

	require.NoError(t, cmd.Execute())
	output := out.String()
	assert.Contains(t, output, "=== Baseline (HTTP 200,")
	assert.Contains(t, output, "checkout_flow: v1")
	assert.Contains(t, output, "=== Preview pr-42 (HTTP 200,")
	assert.Contains(t, output, "checkout_flow: v2")
	assert.Contains(t, output, "Status: IDENTICAL (200)")
	assert.Contains(t, output, "Body:   CHANGED")
	assert.Contains(t, output, "- checkout_flow: v1")
	assert.Contains(t, output, "+ checkout_flow: v2")
}

func TestCurlCmd_CustomMethodDataAndHeadersWithoutCluster(t *testing.T) {
	var gotMethod, gotCustomHdr, gotBody, gotEnvHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCustomHdr = r.Header.Get("X-Custom")
		gotEnvHdr = r.Header.Get("x-diverge-env")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	t.Cleanup(srv.Close)

	app := &App{Kubeconfig: "/nonexistent/kubeconfig"}
	cmd := newCurlCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"pr-99",
		"/items",
		"--base-url", srv.URL,
		"-X", "POST",
		"-d", `{"name":"widget"}`,
		"-H", "X-Custom: abc",
	})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "abc", gotCustomHdr)
	assert.Equal(t, "pr-99", gotEnvHdr)
	assert.Equal(t, `{"name":"widget"}`, gotBody)
	assert.Contains(t, out.String(), "created")
}
