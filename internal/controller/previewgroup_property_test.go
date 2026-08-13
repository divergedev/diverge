package controller

import (
	"regexp"
	"strings"
	"testing"
	"testing/quick"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestChildEnvironmentName_DNSValid(t *testing.T) {
	f := func(groupName, serviceName string) bool {
		if groupName == "" || serviceName == "" {
			return true // ignore empty inputs
		}
		name := childEnvironmentName(groupName, serviceName)

		if len(name) > 63 {
			return false
		}
		if len(name) == 0 {
			return false
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			return false
		}
		match, _ := regexp.MatchString(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, name)
		return match
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestChildEnvironmentName_Deterministic(t *testing.T) {
	f := func(groupName, serviceName string) bool {
		name1 := childEnvironmentName(groupName, serviceName)
		name2 := childEnvironmentName(groupName, serviceName)
		return name1 == name2
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestChildEnvironmentName_Unique(t *testing.T) {
	f := func(g1, s1, g2, s2 string) bool {
		// skip identical inputs
		if g1 == g2 && s1 == s2 {
			return true
		}
		name1 := childEnvironmentName(g1, s1)
		name2 := childEnvironmentName(g2, s2)
		return name1 != name2
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestDerivePreviewGroupPhase_AllRunningMeansRunning(t *testing.T) {
	f := func(count uint8) bool {
		c := int(count%10) + 1 // 1 to 10
		services := make([]divergeiov1alpha1.PreviewGroupServiceStatus, c)
		for i := 0; i < c; i++ {
			services[i] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseRunning}
		}
		phase := derivePreviewGroupPhase(services)
		return phase == divergeiov1alpha1.PreviewGroupPhaseRunning
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestDerivePreviewGroupPhase_AnyFailedNotAllRunning(t *testing.T) {
	f := func(count uint8, failedIdx uint8) bool {
		c := int(count%10) + 2 // 2 to 11
		fIdx := int(failedIdx) % c

		services := make([]divergeiov1alpha1.PreviewGroupServiceStatus, c)
		for i := 0; i < c; i++ {
			services[i] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseRunning}
		}
		services[fIdx] = divergeiov1alpha1.PreviewGroupServiceStatus{Phase: divergeiov1alpha1.PhaseFailed}

		phase := derivePreviewGroupPhase(services)
		return phase == divergeiov1alpha1.PreviewGroupPhaseDegraded
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
