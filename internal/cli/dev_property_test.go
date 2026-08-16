package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/divergedev/diverge/internal/git"
)

var nameChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "_", "-"}
var branchChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "_", "-", ".", "/"}

func genStr(ht *hegel.T, chars []string, maxLen int) string {
	length := hegel.Draw(ht, hegel.Integers(1, maxLen))
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	return res
}

func TestProperty_GroupNameAlwaysValidK8s(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		username := genStr(ht, nameChars, 20)
		service := genStr(ht, nameChars, 30)
		groupName := fmt.Sprintf("dev-%s-%s", strings.ToLower(username), service)
		groupName = strings.ReplaceAll(groupName, "_", "-")
		groupName = strings.ToLower(groupName)
		groupName = strings.TrimRight(groupName, "-")
		// Assert valid K8s name
		require.Regexp(ht, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, groupName)
		require.LessOrEqual(ht, len(groupName), 253)
	})
}

func TestProperty_HeaderValueAlwaysValidSlug(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		branch := genStr(ht, branchChars, 100)
		slug := git.SlugifyBranch(branch)
		require.Regexp(ht, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, slug)
		require.LessOrEqual(ht, len(slug), 63)
	})
}

func TestProperty_EndpointAlwaysHostPort(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ip1 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip2 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip3 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip4 := hegel.Draw(ht, hegel.Integers(0, 255))
		ipStr := fmt.Sprintf("%d.%d.%d.%d", ip1, ip2, ip3, ip4)

		port := hegel.Draw(ht, hegel.Integers(1, 65535))

		endpoint := fmt.Sprintf("%s:%d", ipStr, port)

		host, p, err := net.SplitHostPort(endpoint)
		require.NoError(ht, err)
		require.Equal(ht, ipStr, host)
		require.Equal(ht, fmt.Sprintf("%d", port), p)
	})
}

func TestProperty_AsyncEnvAlwaysWins(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		key := genStr(ht, nameChars, 20)
		baselineVal := genStr(ht, nameChars, 20)
		asyncVal := genStr(ht, nameChars, 20)
		if baselineVal == asyncVal {
			return
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "baseline", Namespace: "default", Labels: map[string]string{"app": "svc"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Env: []corev1.EnvVar{{Name: key, Value: baselineVal}}}}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		clientset := k8sfake.NewSimpleClientset(pod)

		var buf bytes.Buffer
		_, err := syncBaselineEnv(context.Background(), clientset, syncEnvOptions{
			Namespace: "default", ServiceName: "svc",
			Overrides: map[string]string{key: asyncVal},
		}, &buf)
		require.NoError(ht, err)
		// Verify async value is present
		output := buf.String()
		require.Contains(ht, output, fmt.Sprintf("%s=%s", key, asyncVal))
		// Verify baseline value doesn't appear as its own line (not substring)
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) == fmt.Sprintf("%s=%s", key, baselineVal) {
				ht.Errorf("baseline value should have been overridden: found %q", line)
			}
		}
	})
}
