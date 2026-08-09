package database

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestSharedProvider(t *testing.T) {
	provider := &SharedProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	if err != nil {
		t.Errorf("Provision error: %v", err)
	}
	if status == nil {
		t.Errorf("Expected status, got nil")
	}

	err = provider.Teardown(context.Background(), env)
	if err != nil {
		t.Errorf("Teardown error: %v", err)
	}
}

func TestSchemaProvider(t *testing.T) {
	provider := &SchemaProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	if err != nil {
		t.Errorf("Provision error: %v", err)
	}
	if status == nil {
		t.Errorf("Expected status, got nil")
	}
}

func TestSnapshotProvider(t *testing.T) {
	provider := &SnapshotProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	if err != nil {
		t.Errorf("Provision error: %v", err)
	}
	if status == nil {
		t.Errorf("Expected status, got nil")
	}
}

func TestFreshProvider(t *testing.T) {
	provider := &FreshProvider{}
	env := &v1alpha1.Environment{}

	status, err := provider.Provision(context.Background(), env)
	if err != nil {
		t.Errorf("Provision error: %v", err)
	}
	if status == nil {
		t.Errorf("Expected status, got nil")
	}
}
