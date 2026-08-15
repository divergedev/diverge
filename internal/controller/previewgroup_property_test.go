package controller

import (
	"regexp"
	"strings"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestChildEnvironmentName_DNSValid(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		groupName := hegel.Draw(ht, hegel.Text())
		serviceName := hegel.Draw(ht, hegel.Text())

		if groupName == "" || serviceName == "" {
			return // ignore empty inputs
		}
		name := childEnvironmentName(groupName, serviceName)

		if len(name) > 63 {
			ht.Errorf("name too long: %s", name)
		}
		if len(name) == 0 {
			ht.Errorf("name empty")
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			ht.Errorf("name has leading/trailing hyphen: %s", name)
		}
		match, _ := regexp.MatchString(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, name)
		if !match {
			ht.Errorf("name invalid DNS: %s", name)
		}
	})
}

func TestChildEnvironmentName_Deterministic(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		groupName := hegel.Draw(ht, hegel.Text())
		serviceName := hegel.Draw(ht, hegel.Text())

		name1 := childEnvironmentName(groupName, serviceName)
		name2 := childEnvironmentName(groupName, serviceName)
		if name1 != name2 {
			ht.Errorf("not deterministic")
		}
	})
}

func TestChildEnvironmentName_Unique(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		g1 := hegel.Draw(ht, hegel.Text())
		s1 := hegel.Draw(ht, hegel.Text())
		g2 := hegel.Draw(ht, hegel.Text())
		s2 := hegel.Draw(ht, hegel.Text())

		// skip identical inputs
		if g1 == g2 && s1 == s2 {
			return
		}
		name1 := childEnvironmentName(g1, s1)
		name2 := childEnvironmentName(g2, s2)
		if name1 == name2 {
			ht.Errorf("names not unique: %s", name1)
		}
	})
}

func TestDerivePreviewGroupPhase_AllRunningMeansRunning(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		seed := hegel.Draw(ht, hegel.Text())
		c := len(seed)%10 + 1 // 1 to 10
		services := make([]divergeiov1alpha1.PreviewGroupServiceStatus, c)
		for i := 0; i < c; i++ {
			services[i] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseRunning}
		}
		phase := derivePreviewGroupPhase(services)
		if phase != divergeiov1alpha1.PreviewGroupPhaseRunning {
			ht.Errorf("expected running")
		}
	})
}

func TestDerivePreviewGroupPhase_AnyFailedNotAllRunning(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		seed := hegel.Draw(ht, hegel.Text())
		seed2 := hegel.Draw(ht, hegel.Text())
		c := len(seed)%10 + 2 // 2 to 11
		fIdx := len(seed2) % c

		services := make([]divergeiov1alpha1.PreviewGroupServiceStatus, c)
		for i := 0; i < c; i++ {
			services[i] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseRunning}
		}
		services[fIdx] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseFailed}

		phase := derivePreviewGroupPhase(services)
		if phase != divergeiov1alpha1.PreviewGroupPhaseDegraded {
			ht.Errorf("expected degraded")
		}
	})
}

var dnsFirstChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
var dnsMidChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "-"}

func genDNSName(ht *hegel.T) string {
	length := hegel.Draw(ht, hegel.Integers(1, 63))
	first := hegel.Draw(ht, hegel.SampledFrom(dnsFirstChars))
	if length == 1 {
		return first
	}
	rest := ""
	for i := 0; i < length-2; i++ {
		rest += hegel.Draw(ht, hegel.SampledFrom(dnsMidChars))
	}
	return first + rest + hegel.Draw(ht, hegel.SampledFrom(dnsFirstChars))
}

func TestChildEnvLabelInvariant_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		pgName := genDNSName(ht)
		svcName := hegel.Draw(ht, hegel.Text())
		envName := hegel.Draw(ht, hegel.Text())

		pg := &divergeiov1alpha1.PreviewGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name: pgName,
			},
		}
		svc := divergeiov1alpha1.PreviewGroupServiceSpec{
			Name: svcName,
		}

		r := &PreviewGroupReconciler{}
		env := r.buildChildEnvironment(pg, svc, envName, "default")

		// Verify child env has the exact labels required by listChildEnvironments
		// labelPreviewGroup = "diverge.io/previewgroup"
		// labelManagedBy    = "diverge.io/managed-by" (with value "diverge-previewgroup")
		// See internal/controller/previewgroup_controller.go
		if env.Labels["diverge.io/previewgroup"] != pgName {
			ht.Fatalf("expected previewgroup label to be %q, got %q", pgName, env.Labels["diverge.io/previewgroup"])
		}
		if env.Labels["diverge.io/managed-by"] != "diverge-previewgroup" {
			ht.Fatalf("expected managed-by label to be %q, got %q", "diverge-previewgroup", env.Labels["diverge.io/managed-by"])
		}
	})
}
