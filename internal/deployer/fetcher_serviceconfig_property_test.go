package deployer

import (
	"context"
	"fmt"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceConfigFetcher_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		fetcher := &ServiceConfigFetcher{}

		envName := genString(ht, []string{"a", "b", "0", "1", "-"}, 1, 20)
		svcName := genDNS1123(ht)
		port := int32(hegel.Draw(ht, hegel.Integers(1, 65535)))
		image := genString(ht, []string{"a", "A", "0", "-", ".", "/", ":"}, 1, 20)

		cfg := &v1alpha1.ServicePreviewConfig{
			ServiceName: svcName,
			Port:        port,
			Image:       image,
		}

		numEnvs := hegel.Draw(ht, hegel.Integers(0, 5))
		seenEnv := make(map[string]bool)
		for len(cfg.Env) < numEnvs {
			first := hegel.Draw(ht, hegel.SampledFrom([]string{"A", "_"}))
			rest := genString(ht, []string{"A", "B", "0", "_"}, 0, 10)
			name := first + rest

			if !seenEnv[name] && name != "APP_VERSION" {
				seenEnv[name] = true
				cfg.Env = append(cfg.Env, v1alpha1.EnvVar{
					Name:  name,
					Value: hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20)),
				})
			}
		}

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: envName,
			},
			Spec: v1alpha1.EnvironmentSpec{
				ServiceConfig: cfg,
			},
		}

		objs, err := fetcher.Fetch(context.Background(), env)
		if err != nil {
			ht.Fatalf("Fetch failed: %v", err)
		}

		// Always produces exactly 2 objects (Deployment + Service)
		if len(objs) != 2 {
			ht.Fatalf("expected 2 objects, got %d", len(objs))
		}

		deploy := objs[0]
		svc := objs[1]

		// Generated objects have correct GVK
		if deploy.GetAPIVersion() != "apps/v1" || deploy.GetKind() != "Deployment" {
			ht.Errorf("expected apps/v1 Deployment, got %s %s", deploy.GetAPIVersion(), deploy.GetKind())
		}
		if svc.GetAPIVersion() != "v1" || svc.GetKind() != "Service" {
			ht.Errorf("expected v1 Service, got %s %s", svc.GetAPIVersion(), svc.GetKind())
		}

		// All objects have diverge labels
		expectedLabels := map[string]string{
			"diverge.io/environment": envName,
			"diverge.io/managed-by":  "diverge",
			"diverge.io/role":        "preview",
		}
		for objIdx, obj := range objs {
			labels := obj.GetLabels()
			for k, v := range expectedLabels {
				if labels[k] != v {
					ht.Errorf("object %d missing label %s=%s", objIdx, k, v)
				}
			}
		}

		// Names are deterministic
		expectedName := fmt.Sprintf("%s-%s", envName, svcName)
		if deploy.GetName() != expectedName {
			ht.Errorf("expected deploy name %s, got %s", expectedName, deploy.GetName())
		}
		if svc.GetName() != expectedName {
			ht.Errorf("expected svc name %s, got %s", expectedName, svc.GetName())
		}

		// Port propagation
		deploySpec := deploy.Object["spec"].(map[string]interface{})
		template := deploySpec["template"].(map[string]interface{})
		podSpec := template["spec"].(map[string]interface{})
		containers := podSpec["containers"].([]interface{})
		container := containers[0].(map[string]interface{})
		ports := container["ports"].([]interface{})
		containerPort := ports[0].(map[string]interface{})["containerPort"].(int64)
		if containerPort != int64(port) {
			ht.Errorf("expected container port %d, got %d", port, containerPort)
		}

		svcSpec := svc.Object["spec"].(map[string]interface{})
		svcPorts := svcSpec["ports"].([]interface{})
		svcPortMap := svcPorts[0].(map[string]interface{})
		if svcPortMap["port"].(int64) != int64(port) {
			ht.Errorf("expected service port %d, got %d", port, svcPortMap["port"])
		}
		if svcPortMap["targetPort"].(int64) != int64(port) {
			ht.Errorf("expected service targetPort %d, got %d", port, svcPortMap["targetPort"])
		}

		// Env vars are injected
		containerEnvs, ok := container["env"].([]interface{})
		if !ok {
			ht.Fatalf("expected env to be []interface{}")
		}
		if len(containerEnvs) != len(cfg.Env)+1 {
			ht.Fatalf("expected %d env vars, got %d", len(cfg.Env)+1, len(containerEnvs))
		}

		// APP_VERSION is first
		appVersionMap := containerEnvs[0].(map[string]interface{})
		if appVersionMap["name"].(string) != "APP_VERSION" || appVersionMap["value"].(string) != envName {
			ht.Errorf("expected first env var to be APP_VERSION=%s", envName)
		}

		for i, e := range cfg.Env {
			m := containerEnvs[i+1].(map[string]interface{})
			if m["name"].(string) != e.Name || m["value"].(string) != e.Value {
				ht.Errorf("expected env[%d] %s=%s, got %s=%s", i, e.Name, e.Value, m["name"], m["value"])
			}
		}
	})
}
