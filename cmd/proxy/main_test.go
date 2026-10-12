package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateProxyFlags(t *testing.T) {
	tests := []struct {
		name          string
		previewDomain string
		upstream      string
		wantErr       bool
	}{
		{
			name:          "valid flags",
			previewDomain: "preview.example.com",
			upstream:      "https://app.staging.example.com",
			wantErr:       false,
		},
		{
			name:          "missing preview domain",
			previewDomain: "",
			upstream:      "https://app.staging.example.com",
			wantErr:       true,
		},
		{
			name:          "missing upstream",
			previewDomain: "preview.example.com",
			upstream:      "",
			wantErr:       true,
		},
		{
			name:          "both missing",
			previewDomain: "",
			upstream:      "",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProxyFlags(tt.previewDomain, tt.upstream)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
