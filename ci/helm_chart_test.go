package ci_test

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func renderHelmChart(t *testing.T, setFlags ...string) (string, error) {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	require.NoError(t, err, "helm binary must be available in PATH")

	args := []string{"template", "diverge", filepath.Join("..", "charts", "diverge")}
	for _, s := range setFlags {
		args = append(args, "--set", s)
	}
	cmd := exec.Command(helmBin, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return errBuf.String(), err
	}
	return out.String(), nil
}

func parseRenderedDocs(t *testing.T, rendered string) []map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewBufferString(rendered))
	var docs []map[string]any
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	return docs
}

func findResource(docs []map[string]any, kind, name string) map[string]any {
	for _, doc := range docs {
		k, _ := doc["kind"].(string)
		if k != kind {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		n, _ := meta["name"].(string)
		if n == name {
			return doc
		}
	}
	return nil
}

func findNamespacedResources(docs []map[string]any, kind, name string) []map[string]any {
	var matched []map[string]any
	for _, doc := range docs {
		k, _ := doc["kind"].(string)
		if k != kind {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		n, _ := meta["name"].(string)
		if n == name {
			matched = append(matched, doc)
		}
	}
	return matched
}

func TestHelmChart_LintAndServerTemplates(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	require.NoError(t, err, "helm binary must be available in PATH")

	lintCmd := exec.Command(helmBin, "lint", filepath.Join("..", "charts", "diverge"))
	lintOut, err := lintCmd.CombinedOutput()
	require.NoError(t, err, "helm lint failed:\n%s", string(lintOut))

	t.Run("default server configuration", func(t *testing.T) {
		rendered, err := renderHelmChart(t, "server.enabled=true")
		require.NoError(t, err, rendered)
		docs := parseRenderedDocs(t, rendered)

		svc := findResource(docs, "Service", "diverge-server")
		require.NotNil(t, svc)
		spec := svc["spec"].(map[string]any)
		ports := spec["ports"].([]any)
		for _, pRaw := range ports {
			p := pRaw.(map[string]any)
			switch p["name"] {
			case "grpc":
				assert.Equal(t, "kubernetes.io/h2c", p["appProtocol"])
			case "http-compat", "metrics":
				_, hasAppProto := p["appProtocol"]
				assert.False(t, hasAppProto, "appProtocol should only be set on grpc port")
			}
		}

		// Tunnel proxy Service is disabled by default.
		assert.Nil(t, findResource(docs, "Service", "diverge-tunnel-proxy"))

		dep := findResource(docs, "Deployment", "diverge-server")
		require.NotNil(t, dep)
		depSpec := dep["spec"].(map[string]any)
		tmplSpec := depSpec["template"].(map[string]any)["spec"].(map[string]any)
		container := tmplSpec["containers"].([]any)[0].(map[string]any)

		readiness := container["readinessProbe"].(map[string]any)["httpGet"].(map[string]any)
		assert.Equal(t, "/readyz", readiness["path"])
		liveness := container["livenessProbe"].(map[string]any)["httpGet"].(map[string]any)
		assert.Equal(t, "/healthz", liveness["path"])

		resources := container["resources"].(map[string]any)
		limits := resources["limits"].(map[string]any)
		assert.Equal(t, "512Mi", limits["memory"])

		envs := container["env"].([]any)
		var gotGoMemLimit string
		for _, eRaw := range envs {
			e := eRaw.(map[string]any)
			if e["name"] == "GOMEMLIMIT" {
				gotGoMemLimit, _ = e["value"].(string)
			}
		}
		assert.Equal(t, "430MiB", gotGoMemLimit)

		args := container["args"].([]any)
		for _, a := range args {
			assert.NotContains(t, a.(string), "--tunnel-namespaces=", "clusterWidePodAccess=true must not restrict --tunnel-namespaces")
		}
		assert.Contains(t, args, "--tunnel-create-services=true")
	})

	t.Run("tunnelProxy Service uses distinct component label to avoid ServiceMonitor collision", func(t *testing.T) {
		rendered, err := renderHelmChart(t,
			"server.enabled=true",
			"server.tunnelProxy.service.enabled=true",
			"server.metrics.serviceMonitor.enabled=true",
		)
		require.NoError(t, err, rendered)
		docs := parseRenderedDocs(t, rendered)

		proxySvc := findResource(docs, "Service", "diverge-tunnel-proxy")
		require.NotNil(t, proxySvc)
		proxyLabels := proxySvc["metadata"].(map[string]any)["labels"].(map[string]any)
		assert.Equal(t, "tunnel-proxy", proxyLabels["app.kubernetes.io/component"])

		proxySpec := proxySvc["spec"].(map[string]any)
		proxySelector := proxySpec["selector"].(map[string]any)
		assert.Equal(t, "server", proxySelector["app.kubernetes.io/component"])

		port := proxySpec["ports"].([]any)[0].(map[string]any)
		assert.Equal(t, "http-tunnel", port["targetPort"])
		assert.Equal(t, "http", port["appProtocol"])

		sm := findResource(docs, "ServiceMonitor", "diverge-server")
		require.NotNil(t, sm)
		smMatch := sm["spec"].(map[string]any)["selector"].(map[string]any)["matchLabels"].(map[string]any)
		assert.Equal(t, "server", smMatch["app.kubernetes.io/component"])
		assert.NotEqual(t, proxyLabels["app.kubernetes.io/component"], smMatch["app.kubernetes.io/component"])
	})

	t.Run("namespace-scoped server flags and proxy auth token secret", func(t *testing.T) {
		rendered, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.clusterWidePodAccess=false",
			"server.rbac.targetNamespaces={dev,staging}",
			"server.tunnelProxy.createPerTunnelServices=false",
			"server.tunnelProxy.defaultNamespace=dev",
			"server.tunnelProxy.sharedPreviewOwners={ci-bot,bot2}",
			"server.tunnelProxy.authTokenSecretRef.name=proxy-tok",
		)
		require.NoError(t, err, rendered)
		docs := parseRenderedDocs(t, rendered)

		dep := findResource(docs, "Deployment", "diverge-server")
		require.NotNil(t, dep)
		container := dep["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		args := container["args"].([]any)
		assert.Contains(t, args, "--tunnel-namespaces=dev,staging")
		assert.Contains(t, args, "--tunnel-create-services=false")
		assert.Contains(t, args, "--default-tunnel-namespace=dev")
		assert.Contains(t, args, "--shared-preview-owners=ci-bot,bot2")

		envs := container["env"].([]any)
		var tokenSecret map[string]any
		for _, eRaw := range envs {
			e := eRaw.(map[string]any)
			if e["name"] == "DIVERGE_TUNNEL_PROXY_TOKEN" {
				tokenSecret = e["valueFrom"].(map[string]any)["secretKeyRef"].(map[string]any)
			}
		}
		require.NotNil(t, tokenSecret)
		assert.Equal(t, "proxy-tok", tokenSecret["name"])
		assert.Equal(t, "token", tokenSecret["key"])
	})

	t.Run("oidcTunnelGroups RBAC honors prefix and scope", func(t *testing.T) {
		outMissingScope, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.oidcTunnelGroups={developers}",
		)
		require.Error(t, err)
		assert.Contains(t, outMissingScope, "server.rbac.oidcTunnelGroups")

		renderedDefault, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.oidcTunnelClusterWide=true",
			"server.rbac.oidcTunnelGroups={developers}",
		)
		require.NoError(t, err, renderedDefault)
		docsDefault := parseRenderedDocs(t, renderedDefault)
		crb := findResource(docsDefault, "ClusterRoleBinding", "diverge-oidc-tunnel-users")
		require.NotNil(t, crb)
		subj := crb["subjects"].([]any)[0].(map[string]any)
		assert.Equal(t, "oidc:developers", subj["name"])

		renderedCustom, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.oidcTunnelClusterWide=true",
			"server.auth.oidc.groupsPrefix=corp:",
			"server.rbac.oidcTunnelGroups={developers}",
		)
		require.NoError(t, err, renderedCustom)
		crbCustom := findResource(parseRenderedDocs(t, renderedCustom), "ClusterRoleBinding", "diverge-oidc-tunnel-users")
		require.NotNil(t, crbCustom)
		assert.Equal(t, "corp:developers", crbCustom["subjects"].([]any)[0].(map[string]any)["name"])

		renderedExplicitNS, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.clusterWidePodAccess=true",
			"server.rbac.oidcTunnelNamespaces={team-a}",
			"server.rbac.oidcTunnelGroups={developers}",
		)
		require.NoError(t, err, renderedExplicitNS)
		docsExplicitNS := parseRenderedDocs(t, renderedExplicitNS)
		assert.Nil(t, findResource(docsExplicitNS, "ClusterRoleBinding", "diverge-oidc-tunnel-users"))
		explicitRoles := findNamespacedResources(docsExplicitNS, "Role", "diverge-oidc-tunnel-users")
		explicitRBs := findNamespacedResources(docsExplicitNS, "RoleBinding", "diverge-oidc-tunnel-users")
		require.Len(t, explicitRoles, 1)
		require.Len(t, explicitRBs, 1)
		assert.Equal(t, "team-a", explicitRoles[0]["metadata"].(map[string]any)["namespace"])
		assert.Equal(t, "oidc:developers", explicitRBs[0]["subjects"].([]any)[0].(map[string]any)["name"])

		renderedScoped, err := renderHelmChart(t,
			"server.enabled=true",
			"server.rbac.clusterWidePodAccess=false",
			"server.rbac.targetNamespaces={dev,staging}",
			"server.auth.oidc.groupsPrefix=-",
			"server.rbac.oidcTunnelGroups={developers}",
		)
		require.NoError(t, err, renderedScoped)
		docsScoped := parseRenderedDocs(t, renderedScoped)
		roles := findNamespacedResources(docsScoped, "Role", "diverge-oidc-tunnel-users")
		roleBindings := findNamespacedResources(docsScoped, "RoleBinding", "diverge-oidc-tunnel-users")
		require.Len(t, roles, 2)
		require.Len(t, roleBindings, 2)
		for _, rb := range roleBindings {
			assert.Equal(t, "developers", rb["subjects"].([]any)[0].(map[string]any)["name"])
		}
	})

	t.Run("proxy service addr, dynamic GOMEMLIMIT, and secret key defaults", func(t *testing.T) {
		findEnv := func(container map[string]any, name string) map[string]any {
			for _, eRaw := range container["env"].([]any) {
				e := eRaw.(map[string]any)
				if e["name"] == name {
					return e
				}
			}
			return nil
		}

		renderedDefault, err := renderHelmChart(t,
			"server.enabled=true",
			"server.tunnelProxy.service.enabled=true",
		)
		require.NoError(t, err, renderedDefault)
		depDefault := findResource(parseRenderedDocs(t, renderedDefault), "Deployment", "diverge-server")
		require.NotNil(t, depDefault)
		cDefault := depDefault["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		assert.Contains(t, cDefault["args"].([]any), "--tunnel-proxy-service-addr=diverge-tunnel-proxy.default.svc.cluster.local:8081")
		gomemDefault := findEnv(cDefault, "GOMEMLIMIT")
		require.NotNil(t, gomemDefault)
		assert.Equal(t, "430MiB", gomemDefault["value"])

		rendered1Gi, err := renderHelmChart(t,
			"server.enabled=true",
			"server.tunnelProxy.proxyServiceAddr=custom.internal:9000",
			"server.resources.limits.memory=1Gi",
			"server.auth.oidc.enabled=true",
			"server.auth.session.secretRef.name=my-secret",
		)
		require.NoError(t, err, rendered1Gi)
		dep1Gi := findResource(parseRenderedDocs(t, rendered1Gi), "Deployment", "diverge-server")
		require.NotNil(t, dep1Gi)
		c1Gi := dep1Gi["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		assert.Contains(t, c1Gi["args"].([]any), "--tunnel-proxy-service-addr=custom.internal:9000")
		gomem1Gi := findEnv(c1Gi, "GOMEMLIMIT")
		require.NotNil(t, gomem1Gi)
		assert.Equal(t, "860MiB", gomem1Gi["value"])
		sessEnv := findEnv(c1Gi, "DIVERGE_SESSION_SECRET")
		require.NotNil(t, sessEnv)
		sessRef := sessEnv["valueFrom"].(map[string]any)["secretKeyRef"].(map[string]any)
		assert.Equal(t, "my-secret", sessRef["name"])
		assert.Equal(t, "session-secret", sessRef["key"])

		renderedExplicit, err := renderHelmChart(t,
			"server.enabled=true",
			"server.goMemLimit=700MiB",
		)
		require.NoError(t, err, renderedExplicit)
		depExplicit := findResource(parseRenderedDocs(t, renderedExplicit), "Deployment", "diverge-server")
		require.NotNil(t, depExplicit)
		cExplicit := depExplicit["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		gomemExplicit := findEnv(cExplicit, "GOMEMLIMIT")
		require.NotNil(t, gomemExplicit)
		assert.Equal(t, "700MiB", gomemExplicit["value"])
	})

	t.Run("fail guards enforce single replica invariants", func(t *testing.T) {
		out1, err := renderHelmChart(t,
			"server.enabled=true",
			"server.tunnelProxy.service.enabled=true",
			"server.replicaCount=2",
		)
		require.Error(t, err)
		assert.Contains(t, out1, "server.replicaCount <= 1")

		out2, err := renderHelmChart(t,
			"server.enabled=true",
			"server.auth.oidc.enabled=true",
			"server.replicaCount=2",
		)
		require.Error(t, err)
		assert.Contains(t, out2, "server.auth.session.secretRef.name")

		// Satisfying session.secretRef.name passes OIDC HA guard.
		renderedOK, err := renderHelmChart(t,
			"server.enabled=true",
			"server.auth.oidc.enabled=true",
			"server.replicaCount=2",
			"server.auth.session.secretRef.name=shared-session",
		)
		require.NoError(t, err, renderedOK)
	})

	t.Run("oidc cliClientId flag emitted only when set", func(t *testing.T) {
		renderedWithout, err := renderHelmChart(t,
			"server.enabled=true",
			"server.auth.oidc.enabled=true",
		)
		require.NoError(t, err, renderedWithout)
		depWithout := findResource(parseRenderedDocs(t, renderedWithout), "Deployment", "diverge-server")
		argsWithout := depWithout["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["args"].([]any)
		for _, a := range argsWithout {
			assert.NotContains(t, a.(string), "--oidc-cli-client-id=")
		}

		renderedWith, err := renderHelmChart(t,
			"server.enabled=true",
			"server.auth.oidc.enabled=true",
			"server.auth.oidc.cliClientId=diverge-cli",
		)
		require.NoError(t, err, renderedWith)
		depWith := findResource(parseRenderedDocs(t, renderedWith), "Deployment", "diverge-server")
		argsWith := depWith["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["args"].([]any)
		for _, a := range argsWith {
			_, isStr := a.(string)
			require.True(t, isStr, "every container arg must parse as YAML string, got %T (%v)", a, a)
		}
		assert.Contains(t, argsWith, "--oidc-cli-client-id=diverge-cli")
		assert.Contains(t, argsWith, "--oidc-username-prefix=oidc:")
		assert.Contains(t, argsWith, "--oidc-groups-prefix=oidc:")

		// Trailing backslash prefixes must render valid YAML and preserve the literal backslash.
		renderedBackslash, err := renderHelmChart(t,
			"server.enabled=true",
			"server.auth.oidc.enabled=true",
			`server.auth.oidc.usernamePrefix=DOMAIN\\`,
			`server.auth.oidc.groupsPrefix=CORP\\`,
		)
		require.NoError(t, err, renderedBackslash)
		depBackslash := findResource(parseRenderedDocs(t, renderedBackslash), "Deployment", "diverge-server")
		require.NotNil(t, depBackslash)
		argsBackslash := depBackslash["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["args"].([]any)
		assert.Contains(t, argsBackslash, `--oidc-username-prefix=DOMAIN\`)
		assert.Contains(t, argsBackslash, `--oidc-groups-prefix=CORP\`)
	})
}
