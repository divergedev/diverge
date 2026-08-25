package otel

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	coreclient "k8s.io/client-go/kubernetes/fake"
)

func TestIsOperatorInstalled_Found_v1alpha2(t *testing.T) {
	clientset := coreclient.NewSimpleClientset()
	fakeDisc := clientset.Discovery().(*fakediscovery.FakeDiscovery)
	fakeDisc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "opentelemetry.io/v1alpha2",
			APIResources: []metav1.APIResource{
				{Kind: "Instrumentation"},
			},
		},
	}

	found, version, err := IsOperatorInstalled(context.Background(), fakeDisc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || version != "opentelemetry.io/v1alpha2" {
		t.Fatalf("expected true, opentelemetry.io/v1alpha2; got %v, %v", found, version)
	}
}

func TestIsOperatorInstalled_Found_v1alpha1_Fallback(t *testing.T) {
	clientset := coreclient.NewSimpleClientset()
	fakeDisc := clientset.Discovery().(*fakediscovery.FakeDiscovery)
	fakeDisc.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "opentelemetry.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Kind: "Instrumentation"},
			},
		},
	}

	found, version, err := IsOperatorInstalled(context.Background(), fakeDisc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || version != "opentelemetry.io/v1alpha1" {
		t.Fatalf("expected true, opentelemetry.io/v1alpha1; got %v, %v", found, version)
	}
}

func TestIsOperatorInstalled_NotFound(t *testing.T) {
	clientset := coreclient.NewSimpleClientset()
	fakeDisc := clientset.Discovery().(*fakediscovery.FakeDiscovery)
	fakeDisc.Resources = []*metav1.APIResourceList{}

	found, _, err := IsOperatorInstalled(context.Background(), fakeDisc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected false, got true")
	}
}
