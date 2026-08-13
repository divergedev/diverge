package deployer

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	return scheme
}

func validEndpoint(ht *hegel.T) string {
	o1 := hegel.Draw(ht, hegel.Integers(1, 254))
	o2 := hegel.Draw(ht, hegel.Integers(1, 254))
	o3 := hegel.Draw(ht, hegel.Integers(1, 254))
	o4 := hegel.Draw(ht, hegel.Integers(1, 254))
	port := hegel.Draw(ht, hegel.Integers(1, 65535))
	return fmt.Sprintf("%d.%d.%d.%d:%d", o1, o2, o3, o4, port)
}

func TestLocalDeployer_Property_Deploy_Teardown_Roundtrip(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		endpoint := validEndpoint(ht)
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: &v1alpha1.ServicePreviewConfig{Endpoint: endpoint},
			},
		}

		d := &LocalDeployer{
			Client: fake.NewClientBuilder().WithScheme(buildScheme()).Build(),
		}

		ctx := context.Background()

		if err := d.Deploy(ctx, env); err != nil {
			ht.Fatalf("Deploy failed: %v", err)
		}

		if err := d.Teardown(ctx, env); err != nil {
			ht.Fatalf("Teardown failed: %v", err)
		}

		statuses, err := d.Status(ctx, env)
		if err != nil {
			ht.Fatalf("Status failed: %v", err)
		}
		if len(statuses) != 1 {
			ht.Fatalf("Expected 1 status, got %d", len(statuses))
		}
		if statuses[0].Health != "Missing" {
			ht.Fatalf("Expected Health to be Missing, got %s", statuses[0].Health)
		}
	})
}

func TestLocalDeployer_Property_Deploy_Status_Healthy(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		endpoint := validEndpoint(ht)
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: &v1alpha1.ServicePreviewConfig{Endpoint: endpoint},
			},
		}

		d := &LocalDeployer{
			Client: fake.NewClientBuilder().WithScheme(buildScheme()).Build(),
		}
		ctx := context.Background()

		if err := d.Deploy(ctx, env); err != nil {
			ht.Fatalf("Deploy failed: %v", err)
		}

		statuses, err := d.Status(ctx, env)
		if err != nil {
			ht.Fatalf("Status failed: %v", err)
		}
		if len(statuses) != 1 {
			ht.Fatalf("Expected 1 status, got %d", len(statuses))
		}
		if statuses[0].Health != "Healthy" {
			ht.Fatalf("Expected Health to be Healthy, got %s", statuses[0].Health)
		}
	})
}

func TestLocalDeployer_Property_Teardown_Idempotent(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		endpoint := validEndpoint(ht)
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: &v1alpha1.ServicePreviewConfig{Endpoint: endpoint},
			},
		}

		d := &LocalDeployer{
			Client: fake.NewClientBuilder().WithScheme(buildScheme()).Build(),
		}
		ctx := context.Background()

		// First Teardown (missing)
		if err := d.Teardown(ctx, env); err != nil {
			ht.Fatalf("Teardown on missing failed: %v", err)
		}

		// Deploy
		if err := d.Deploy(ctx, env); err != nil {
			ht.Fatalf("Deploy failed: %v", err)
		}

		// Second Teardown (existing)
		if err := d.Teardown(ctx, env); err != nil {
			ht.Fatalf("Teardown on existing failed: %v", err)
		}

		// Third Teardown (already torn down)
		if err := d.Teardown(ctx, env); err != nil {
			ht.Fatalf("Idempotent Teardown failed: %v", err)
		}
	})
}

func TestLocalDeployer_Property_Deploy_InvalidEndpoint(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		// Generate strings that are definitely not valid host:port combos.
		invalidEpGen := hegel.Filter(hegel.Text(), func(s string) bool {
			_, _, err := net.SplitHostPort(s)
			return err != nil
		})
		endpoint := hegel.Draw(ht, invalidEpGen)

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: &v1alpha1.ServicePreviewConfig{Endpoint: endpoint},
			},
		}

		d := &LocalDeployer{
			Client: fake.NewClientBuilder().WithScheme(buildScheme()).Build(),
		}
		ctx := context.Background()

		err := d.Deploy(ctx, env)
		if err == nil {
			ht.Fatalf("Deploy expected to fail with invalid endpoint %q, but succeeded", endpoint)
		}
	})
}

func TestLocalDeployer_Property_Deploy_NilServiceConfig(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: nil,
			},
		}

		d := &LocalDeployer{
			Client: fake.NewClientBuilder().WithScheme(buildScheme()).Build(),
		}
		ctx := context.Background()

		err := d.Deploy(ctx, env)
		if err == nil {
			ht.Fatalf("Deploy expected to fail with nil ServiceConfig, but succeeded")
		}
	})
}
