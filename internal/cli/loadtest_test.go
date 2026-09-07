package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadtestCmd_Execution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	app := &App{}
	root := NewRootCmd(app)

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{
		"loadtest",
		srv.URL,
		"--routing-key", "preview-pr-12",
		"--duration", "100ms",
		"--concurrency", "2",
	})

	err := root.Execute()
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, "DIVERGE LOAD TEST RESULTS")
	assert.Contains(t, out, "preview-pr-12")
	assert.Contains(t, out, "PASSED quality thresholds")
}

func TestLoadtestCmd_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := &App{}
	root := NewRootCmd(app)

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{
		"loadtest",
		srv.URL,
		"--preview", "my-preview",
		"--duration", "50ms",
		"--concurrency", "1",
		"--json",
	})

	err := root.Execute()
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, `"target_url":`)
	assert.Contains(t, out, `"routing_key": "my-preview"`)
	assert.Contains(t, out, `"passed": true`)
}

func TestLoadtestCmd_ThresholdFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := &App{}
	root := NewRootCmd(app)

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{
		"loadtest",
		srv.URL,
		"--duration", "50ms",
		"--max-p99", "1ms",
	})

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quality gate threshold failed")
}

func TestLoadtestCmd_BaselineCompare(t *testing.T) {
	srvBase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srvBase.Close()

	srvCand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srvCand.Close()

	app := &App{}
	root := NewRootCmd(app)

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{
		"loadtest",
		srvCand.URL,
		"--routing-key", "candidate-key",
		"--baseline",
		"--duration", "50ms",
		"--concurrency", "1",
		"--fail-on-latency-increase", "500.0",
	})

	err := root.Execute()
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "DIVERGE LOAD TEST RESULTS")
	assert.Contains(t, stdout.String(), "Baseline")
	assert.Contains(t, stdout.String(), "Candidate")
	assert.Contains(t, stdout.String(), "Delta")
}

func TestLoadtestCmd_InvalidArgs(t *testing.T) {
	app := &App{}
	root := NewRootCmd(app)

	// Missing URL argument
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"loadtest"})

	err := root.Execute()
	require.Error(t, err)
}
