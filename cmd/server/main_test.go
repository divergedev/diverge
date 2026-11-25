package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveGCNamespaces(t *testing.T) {
	assert.Equal(t, []string{metav1.NamespaceAll}, resolveGCNamespaces(nil, "default", ""))
	assert.Equal(t, []string{"team-a", "default"}, resolveGCNamespaces([]string{"team-a"}, "default", ""))
	assert.Equal(t, []string{"team-a", "default"}, resolveGCNamespaces([]string{"team-a", "default"}, "default", ""))
	assert.Equal(t, []string{"watch-a", "watch-b", "dev"}, resolveGCNamespaces(nil, "dev", "watch-a, watch-b"))
	assert.Equal(t, []string{"team-a"}, resolveGCNamespaces([]string{"team-a"}, "", ""))
}
