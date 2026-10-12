package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateFlags(t *testing.T) {
	tests := []struct {
		name           string
		activatorURL   string
		targetSelector string
		wantErr        bool
	}{
		{
			name:           "valid flags",
			activatorURL:   "http://activator.knative-serving.svc.cluster.local",
			targetSelector: "app=my-service",
			wantErr:        false,
		},
		{
			name:           "missing activator URL",
			activatorURL:   "",
			targetSelector: "app=my-service",
			wantErr:        true,
		},
		{
			name:           "missing target selector",
			activatorURL:   "http://activator.knative-serving.svc.cluster.local",
			targetSelector: "",
			wantErr:        true,
		},
		{
			name:           "both missing",
			activatorURL:   "",
			targetSelector: "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFlags(tt.activatorURL, tt.targetSelector)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
