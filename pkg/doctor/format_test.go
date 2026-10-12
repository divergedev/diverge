package doctor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormat_DoctorReport(t *testing.T) {
	report := &Report{
		EnvironmentName: "pr-100",
		Namespace:       "test-ns",
		Healthy:         false,
		Issues: []Issue{
			{
				Code:        CodeContainerExit,
				Severity:    SeverityCritical,
				Component:   "Container/api",
				Summary:     "Container crashed",
				Details:     "exit code 1",
				Remediation: "Check logs",
				DocsURL:     DocsBaseURL + "#div-109-container-exit",
			},
		},
	}

	var tableBuf bytes.Buffer
	err := FormatTable(&tableBuf, report)
	require.NoError(t, err)
	assert.Contains(t, tableBuf.String(), "DIVERGE SYSTEM & ENVIRONMENT DOCTOR")
	assert.Contains(t, tableBuf.String(), "🔴 CRIT [DIV-109]: Container/api")
	assert.Contains(t, tableBuf.String(), "Docs:        🔗 https://divergedev.com/guides/troubleshooting/#div-109-container-exit")

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, report)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), `"healthy": false`)
	assert.Contains(t, jsonBuf.String(), `"code": "DIV-109"`)
	assert.Contains(t, jsonBuf.String(), `"docs_url": "https://divergedev.com/guides/troubleshooting/#div-109-container-exit"`)
}

type errWriter struct{}

func (errWriter) Write(p []byte) (n int, err error) {
	return 0, assert.AnError
}

func TestFormatTable_WriterError(t *testing.T) {
	report := &Report{Healthy: true}
	err := FormatTable(errWriter{}, report)
	require.Error(t, err)

	unhealthyReport := &Report{
		Healthy: false,
		Issues: []Issue{
			{Severity: SeverityCritical, Component: "app", Summary: "err"},
		},
	}
	err = FormatTable(errWriter{}, unhealthyReport)
	require.Error(t, err)
}
