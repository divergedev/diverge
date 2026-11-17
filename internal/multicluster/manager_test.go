package multicluster

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, v1alpha1.AddToScheme(s))
	return s
}

func validTestKubeconfig(serverURL, token string) []byte {
	return []byte(strings.Join([]string{
		"apiVersion: v1",
		"kind: Config",
		"clusters:",
		"- name: spoke",
		"  cluster:",
		fmt.Sprintf("    server: %s", serverURL),
		"    insecure-skip-tls-verify: true",
		"contexts:",
		"- name: spoke",
		"  context:",
		"    cluster: spoke",
		"    user: spoke-bot",
		"current-context: spoke",
		"users:",
		"- name: spoke-bot",
		"  user:",
		fmt.Sprintf("    token: %s", token),
		"",
	}, "\n"))
}

func TestManager_ResolveLocalAndRemoteCaching(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-west-kubeconfig",
			Namespace: "team-a",
		},
		Data: map[string][]byte{
			"kubeconfig": validTestKubeconfig("https://api.spoke-west.example.com:6443", "tok-v1"),
		},
	}

	hubClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	hubClientset := k8sfake.NewClientset()

	var factoryCalls atomic.Int32
	mgr := NewManager(hubClient, hubClientset, &rest.Config{Host: "https://hub.example.com"}, scheme)
	mgr.ClientFactory = func(cfg *rest.Config, s *runtime.Scheme) (client.Client, kubernetes.Interface, error) {
		factoryCalls.Add(1)
		spokeCR := fake.NewClientBuilder().WithScheme(s).Build()
		spokeK8s := k8sfake.NewClientset()
		return spokeCR, spokeK8s, nil
	}

	// 1. Local environment resolves to hub without invoking spoke factory.
	localEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "team-a"},
	}
	localTarget, err := mgr.Resolve(ctx, localEnv)
	require.NoError(t, err)
	assert.False(t, localTarget.IsRemote)
	assert.Equal(t, LocalClusterName, localTarget.Name)
	assert.Same(t, hubClient, localTarget.Client)
	assert.Equal(t, int32(0), factoryCalls.Load())

	// 2. Remote environment resolves spoke client and caches it across calls.
	remoteEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-2", Namespace: "team-a"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name: "us-west-burst",
					KubeconfigSecretRef: &v1alpha1.SecretRef{
						Name: "spoke-west-kubeconfig",
					},
				},
			},
		},
	}

	first, err := mgr.Resolve(ctx, remoteEnv)
	require.NoError(t, err)
	assert.True(t, first.IsRemote)
	assert.Equal(t, "us-west-burst", first.Name)
	assert.Equal(t, "https://api.spoke-west.example.com:6443", first.RESTConfig.Host)
	assert.Equal(t, "tok-v1", first.RESTConfig.BearerToken)
	assert.Equal(t, int32(1), factoryCalls.Load())

	second, err := mgr.Resolve(ctx, remoteEnv)
	require.NoError(t, err)
	assert.Same(t, first.Client, second.Client)
	assert.Equal(t, int32(1), factoryCalls.Load())

	// 3. Rotating the Secret invalidates the cached spoke client automatically.
	var updated corev1.Secret
	require.NoError(t, hubClient.Get(ctx, client.ObjectKeyFromObject(sec), &updated))
	updated.Data["kubeconfig"] = validTestKubeconfig("https://api.spoke-west.example.com:6443", "tok-v2")
	require.NoError(t, hubClient.Update(ctx, &updated))

	rotated, err := mgr.Resolve(ctx, remoteEnv)
	require.NoError(t, err)
	assert.Equal(t, "tok-v2", rotated.RESTConfig.BearerToken)
	assert.Equal(t, int32(2), factoryCalls.Load())
}

func TestManager_SecurityHardeningRejections(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)

	tests := []struct {
		name       string
		env        *v1alpha1.Environment
		secret     *corev1.Secret
		wantReason string
	}{
		{
			name: "cross-namespace secret ref is forbidden",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "team-a"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						ClusterRef: &v1alpha1.ClusterTargetRef{
							Name: "prod",
							KubeconfigSecretRef: &v1alpha1.SecretRef{
								Namespace: "kube-system",
								Name:      "admin-kubeconfig",
							},
						},
					},
				},
			},
			wantReason: "CrossNamespaceRef",
		},
		{
			name: "missing kubeconfigSecretRef when remote name set",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "pr-1", Namespace: "team-a"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						ClusterRef: &v1alpha1.ClusterTargetRef{
							Name: "eu-central-1",
						},
					},
				},
			},
			wantReason: "MissingKubeconfigSecretRef",
		},
		{
			name: "exec credential plugin is rejected to prevent RCE",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-exec", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": []byte(strings.Join([]string{
						"apiVersion: v1",
						"kind: Config",
						"clusters:",
						"- name: c",
						"  cluster:",
						"    server: https://api.spoke.example.com:6443",
						"contexts:",
						"- name: c",
						"  context:",
						"    cluster: c",
						"    user: u",
						"current-context: c",
						"users:",
						"- name: u",
						"  user:",
						"    exec:",
						"      apiVersion: client.authentication.k8s.io/v1beta1",
						"      command: /bin/sh",
						"",
					}, "\n")),
				},
			},
			wantReason: "ForbiddenAuthPlugin",
		},
		{
			name: "tokenFile local path is rejected to prevent SA token exfiltration",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-tokenfile", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": []byte(strings.Join([]string{
						"apiVersion: v1",
						"kind: Config",
						"clusters:",
						"- name: c",
						"  cluster:",
						"    server: https://api.spoke.example.com:6443",
						"contexts:",
						"- name: c",
						"  context:",
						"    cluster: c",
						"    user: u",
						"current-context: c",
						"users:",
						"- name: u",
						"  user:",
						"    tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token",
						"",
					}, "\n")),
				},
			},
			wantReason: "ForbiddenLocalFileRef",
		},
		{
			name: "certificate-authority local file path is rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-cafile", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": []byte(strings.Join([]string{
						"apiVersion: v1",
						"kind: Config",
						"clusters:",
						"- name: c",
						"  cluster:",
						"    server: https://api.spoke.example.com:6443",
						"    certificate-authority: /etc/kubernetes/pki/ca.crt",
						"contexts:",
						"- name: c",
						"  context:",
						"    cluster: c",
						"    user: u",
						"current-context: c",
						"users:",
						"- name: u",
						"  user:",
						"    token: tok",
						"",
					}, "\n")),
				},
			},
			wantReason: "ForbiddenLocalFileRef",
		},
		{
			name: "proxy-url is rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-proxy", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": []byte(strings.Join([]string{
						"apiVersion: v1",
						"kind: Config",
						"clusters:",
						"- name: c",
						"  cluster:",
						"    server: https://api.spoke.example.com:6443",
						"    proxy-url: http://169.254.169.254:80",
						"contexts:",
						"- name: c",
						"  context:",
						"    cluster: c",
						"    user: u",
						"current-context: c",
						"users:",
						"- name: u",
						"  user:",
						"    token: tok",
						"",
					}, "\n")),
				},
			},
			wantReason: "ForbiddenProxyURL",
		},
		{
			name: "insecure http server URL is rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-http", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": validTestKubeconfig("http://api.spoke.example.com:6443", "tok"),
				},
			},
			wantReason: "InsecureAPIServerURL",
		},
		{
			name: "cloud metadata IP 169.254.169.254 is rejected",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-imds", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": validTestKubeconfig("https://169.254.169.254", "tok"),
				},
			},
			wantReason: "ForbiddenAPIServerHost",
		},
		{
			name: "loopback IP 127.0.0.1 is rejected by default",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-loopback", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": validTestKubeconfig("https://127.0.0.1:6443", "tok"),
				},
			},
			wantReason: "ForbiddenAPIServerHost",
		},
		{
			name: "in-cluster kubernetes.default.svc is rejected to block self-escalation",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-incluster", Namespace: "team-a"},
				Data: map[string][]byte{
					"kubeconfig": validTestKubeconfig("https://kubernetes.default.svc", "tok"),
				},
			},
			wantReason: "ForbiddenAPIServerHost",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			env := tc.env
			if tc.secret != nil {
				builder = builder.WithObjects(tc.secret)
				env = &v1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-sec", Namespace: tc.secret.Namespace},
					Spec: v1alpha1.EnvironmentSpec{
						Deploy: v1alpha1.EnvironmentDeploy{
							ClusterRef: &v1alpha1.ClusterTargetRef{
								Name: "spoke",
								KubeconfigSecretRef: &v1alpha1.SecretRef{
									Name: tc.secret.Name,
								},
							},
						},
					},
				}
			}
			mgr := NewManager(builder.Build(), k8sfake.NewClientset(), nil, scheme)
			_, err := mgr.Resolve(ctx, env)
			require.Error(t, err)
			ve, ok := IsValidationError(err)
			require.True(t, ok, "expected ValidationError, got %T (%v)", err, err)
			assert.Equal(t, tc.wantReason, ve.Reason)
		})
	}
}

func TestManager_CacheEvictionAndHelpers(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)

	s1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "ns"},
		Data:       map[string][]byte{"value": validTestKubeconfig("https://spoke1.example.com:6443", "t1")},
	}
	s2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s2", Namespace: "ns"},
		Data:       map[string][]byte{"config": validTestKubeconfig("https://spoke2.example.com:6443", "t2")},
	}
	hubClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(s1, s2).Build()

	mgr := NewManager(hubClient, k8sfake.NewClientset(), nil, scheme)
	mgr.MaxCacheEntries = 1
	mgr.ClientFactory = func(cfg *rest.Config, s *runtime.Scheme) (client.Client, kubernetes.Interface, error) {
		return fake.NewClientBuilder().WithScheme(s).Build(), k8sfake.NewClientset(), nil
	}

	env1 := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ClusterRef: &v1alpha1.ClusterTargetRef{
					KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "s1"},
				},
			},
		},
	}
	env2 := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "e2", Namespace: "ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name:                "eu-spoke",
					KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "s2"},
				},
			},
		},
	}

	t1, err := mgr.Resolve(ctx, env1)
	require.NoError(t, err)
	assert.Equal(t, "s1", t1.Name)

	t2, err := mgr.Resolve(ctx, env2)
	require.NoError(t, err)
	assert.Equal(t, "eu-spoke", t2.Name)

	mgr.mu.RLock()
	assert.Len(t, mgr.cache, 1)
	mgr.mu.RUnlock()

	assert.Equal(t, LocalClusterName, ClusterName(nil))
	assert.Equal(t, "s1", ClusterName(env1))
	assert.False(t, HasConfirmedRemoteTarget(env1))
	env1.Status.TargetCluster = "pinned-west"
	assert.Equal(t, "pinned-west", ClusterName(env1))
	assert.True(t, HasConfirmedRemoteTarget(env1))
	env1.Status.TargetCluster = LocalClusterName
	assert.False(t, HasConfirmedRemoteTarget(env1))
}

func TestManager_DNSRebindingDialGuardAndStreamingClientset(t *testing.T) {
	ctx := context.Background()
	scheme := testScheme(t)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "rebind-sec", Namespace: "team-a"},
		Data: map[string][]byte{
			"kubeconfig": validTestKubeconfig("https://spoke-rebind.example.com:6443", "tok"),
		},
	}
	hubClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()

	var lookupCalls atomic.Int32
	mgr := NewManager(hubClient, k8sfake.NewClientset(), nil, scheme)
	mgr.LookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		call := lookupCalls.Add(1)
		if call == 1 {
			// Simulate DNS resolving to loopback (DNS rebinding to 127.0.0.1).
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		// Simulate DNS resolving to cloud metadata IMDS 169.254.169.254 on retry.
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-rebind", Namespace: "team-a"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name:                "rebind-spoke",
					KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "rebind-sec"},
				},
			},
		},
	}

	// Hostname passes initial parse, but both Clientset and StreamingClientset are constructed
	// with the connection-time safe dialer and distinct timeout profiles.
	target, err := mgr.Resolve(ctx, env)
	require.NoError(t, err)
	require.NotNil(t, target.RESTConfig.Dial)
	require.NotNil(t, target.Clientset)
	require.NotNil(t, target.StreamingClientset)
	assert.Equal(t, defaultRequestTimeout, target.RESTConfig.Timeout)

	// Connection #1: DNS resolves to 127.0.0.1 -> blocked before dialing!
	_, err = target.RESTConfig.Dial(ctx, "tcp", "spoke-rebind.example.com:6443")
	require.Error(t, err)
	ve1, ok1 := IsValidationError(err)
	require.True(t, ok1)
	assert.Equal(t, "ForbiddenAPIServerHost", ve1.Reason)

	// Connection #2: DNS rebinds to 169.254.169.254 -> re-evaluated on every connection and blocked!
	_, err = target.RESTConfig.Dial(ctx, "tcp", "spoke-rebind.example.com:6443")
	require.Error(t, err)
	ve2, ok2 := IsValidationError(err)
	require.True(t, ok2)
	assert.Equal(t, "ForbiddenAPIServerHost", ve2.Reason)
	assert.Equal(t, int32(2), lookupCalls.Load())

	// Context-pinned target returns directly without re-resolving Secrets.
	pinned := &Target{Name: "pinned", IsRemote: true, Client: hubClient}
	gotPinned, err := mgr.Resolve(WithTarget(ctx, pinned), env)
	require.NoError(t, err)
	assert.Same(t, pinned, gotPinned)
}
