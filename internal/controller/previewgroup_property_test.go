package controller

import (
	"regexp"
	"strings"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"hegel.dev/go/hegel"
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
