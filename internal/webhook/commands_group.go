package webhook

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/previewgroup"
)

func isValidGroupDBMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "shared", "schema", "snapshot", "fresh", "cnpg", "neon":
		return true
	default:
		return false
	}
}

func parseGroupCommandArgs(tokens []string) (groupName, dbMode string, ok bool) {
	if len(tokens) == 0 {
		return "", "", false
	}
	for i := 0; i < len(tokens); i++ {
		tok := strings.TrimSpace(tokens[i])
		if tok == "" {
			continue
		}
		if tok == "--db-mode" {
			if i+1 >= len(tokens) {
				return "", "", false
			}
			val := strings.ToLower(strings.TrimSpace(tokens[i+1]))
			if !isValidGroupDBMode(val) {
				return "", "", false
			}
			dbMode = val
			i++
			continue
		}
		if strings.HasPrefix(tok, "--db-mode=") {
			val := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(tok, "--db-mode=")))
			if !isValidGroupDBMode(val) {
				return "", "", false
			}
			dbMode = val
			continue
		}
		if strings.HasPrefix(tok, "-") {
			return "", "", false
		}
		if groupName != "" {
			return "", "", false
		}
		norm, err := previewgroup.NormalizeGroupName(tok)
		if err != nil {
			return "", "", false
		}
		groupName = norm
	}
	if groupName == "" {
		return "", "", false
	}
	return groupName, dbMode, true
}

func executeGroupCommand(ctx context.Context, c client.Client, ns string, cmd *SlashCommand, repoURL, branch string, prNumber int) (string, error) {
	groupName, err := previewgroup.NormalizeGroupName(cmd.GroupName)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
		}
		return "", err
	}

	project := projectFromRepoURL(repoURL)
	provider := resolveRequestProvider(ctx, repoURL)
	if provider == "" {
		provider = "github"
	}

	var svcSpecs []v1alpha1.PreviewGroupServiceSpec
	var members []previewgroup.MemberRecord
	var inheritedHeaderKey string
	var inheritedBaseURL string

	envs, err := findMatchingEnvironments(ctx, c, ns, repoURL, prNumber)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
		}
		return "", err
	}
	for i := range envs {
		env := &envs[i]
		if env.Labels != nil && env.Labels["divergedev.com/managed-by"] == "diverge-previewgroup" {
			continue
		}
		spec, member, convErr := previewgroup.ServiceFromEnvironment(env)
		if convErr != nil {
			continue
		}
		if member.Project == "" {
			member.Project = project
		}
		if member.MR == 0 && prNumber > 0 {
			member.MR = prNumber
		}
		if member.Branch == "" {
			member.Branch = branch
		}
		if member.Provider == "" {
			member.Provider = provider
		}
		svcSpecs = append(svcSpecs, spec)
		members = append(members, member)
		if inheritedHeaderKey == "" && strings.TrimSpace(env.Spec.Routing.HeaderKey) != "" {
			inheritedHeaderKey = strings.TrimSpace(env.Spec.Routing.HeaderKey)
		}
		if inheritedBaseURL == "" {
			if ext := stripPreviewQueryParam(env.Spec.Routing.ExternalURL); ext != "" && !strings.Contains(ext, "{") {
				inheritedBaseURL = ext
			} else if st := stripPreviewQueryParam(env.Status.URL); st != "" && !strings.EqualFold(env.Spec.Routing.Mode, "subdomain") {
				inheritedBaseURL = st
			}
		}
	}

	// Fallback: also support repos whose CI creates a per-MR PreviewGroup (e.g. GitHub Action / GitLab CI component)
	if len(svcSpecs) == 0 {
		pgs, pgErr := findMatchingPreviewGroups(ctx, c, repoURL, prNumber)
		if pgErr != nil {
			if apierrors.IsForbidden(pgErr) {
				return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
			}
			return "", pgErr
		}
		nowStr := time.Now().UTC().Format(time.RFC3339)
		for _, singlePG := range pgs {
			if singlePG.Name == groupName {
				continue
			}
			pgMembers := previewgroup.ParseMembers(&singlePG)
			isComposed := singlePG.Labels != nil && singlePG.Labels[previewgroup.LabelComposedGroup] == "true"
			memberBySvc := make(map[string]previewgroup.MemberRecord, len(pgMembers))
			for _, pm := range pgMembers {
				memberBySvc[pm.Service] = pm
			}
			contributedFromPG := false
			for _, svc := range singlePG.Spec.Services {
				if svc.Mode == v1alpha1.ServiceModeBaseline || strings.TrimSpace(svc.Image) == "" {
					continue
				}
				if len(pgMembers) > 0 || isComposed {
					pm, ok := memberBySvc[svc.Name]
					if !ok || pm.MR != prNumber {
						continue
					}
					if project != "" && pm.Project != "" && !strings.EqualFold(strings.TrimSpace(pm.Project), project) {
						continue
					}
					if provider != "" && pm.Provider != "" && pm.Provider != "local" && !strings.EqualFold(strings.TrimSpace(pm.Provider), provider) {
						continue
					}
				}
				copied := *svc.DeepCopy()
				if copied.Namespace == "" {
					copied.Namespace = ns
				}
				if copied.Database == nil && singlePG.Spec.Database != nil && !previewgroup.IsZeroDatabase(*singlePG.Spec.Database) {
					copied.Database = singlePG.Spec.Database.DeepCopy()
				}
				svcSpecs = append(svcSpecs, copied)
				members = append(members, previewgroup.MemberRecord{
					Service:   copied.Name,
					Namespace: copied.Namespace,
					Provider:  provider,
					Project:   project,
					MR:        prNumber,
					Branch:    branch,
					UpdatedAt: nowStr,
				})
				contributedFromPG = true
			}
			if contributedFromPG {
				if inheritedHeaderKey == "" && strings.TrimSpace(singlePG.Spec.Routing.HeaderKey) != "" {
					inheritedHeaderKey = strings.TrimSpace(singlePG.Spec.Routing.HeaderKey)
				}
				if inheritedBaseURL == "" {
					if ext := stripPreviewQueryParam(singlePG.Spec.Routing.ExternalURL); ext != "" && !strings.Contains(ext, "{") {
						inheritedBaseURL = ext
					}
				}
			}
		}
	}

	if len(svcSpecs) == 0 {
		return fmt.Sprintf(
			"⚠️ No active preview service with a built container image was found for this PR/MR yet.\n\n"+
				"Wait for CI/webhook preview provisioning to finish (or run `/diverge preview`), then comment `/diverge group %s` again.",
			groupName,
		), nil
	}

	baseURL := stripPreviewQueryParam(os.Getenv("DIVERGE_BASE_URL"))
	if baseURL == "" {
		baseURL = inheritedBaseURL
	}
	var externalURL string
	if baseURL != "" {
		externalURL = baseURL + "/?_diverge_env=" + groupName
	}

	headerKey := inheritedHeaderKey
	if headerKey == "" {
		headerKey = previewgroup.DefaultRoutingHeaderKey
	}

	var groupDB *v1alpha1.EnvironmentDatabase
	if cmd.DBMode != "" {
		groupDB = &v1alpha1.EnvironmentDatabase{
			Mode: cmd.DBMode,
		}
	}

	src := &v1alpha1.EnvironmentSource{
		Provider: provider,
		Project:  project,
		MR:       prNumber,
		Branch:   branch,
	}

	res, err := previewgroup.Join(ctx, c, previewgroup.JoinOptions{
		GroupName:     groupName,
		Namespace:     ns,
		Services:      svcSpecs,
		Members:       members,
		Source:        src,
		HeaderKey:     headerKey,
		HeaderValue:   groupName,
		ExternalURL:   externalURL,
		GroupDatabase: groupDB,
		Traceparent:   TraceparentFromContext(ctx),
	})
	if err != nil {
		if apierrors.IsForbidden(err) {
			return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
		}
		return "", err
	}

	pg := res.PreviewGroup
	effHeaderKey := pg.Spec.Routing.HeaderKey
	if effHeaderKey == "" {
		effHeaderKey = previewgroup.DefaultRoutingHeaderKey
	}
	effHeaderVal := pg.Spec.Routing.HeaderValue
	if effHeaderVal == "" {
		effHeaderVal = pg.Name
	}

	memberByService := make(map[string]previewgroup.MemberRecord, len(res.Members))
	for _, m := range res.Members {
		memberByService[m.Service] = m
	}

	var b strings.Builder
	titleVerb := "Joined"
	if res.Created {
		titleVerb = "Created & Joined"
	}
	fmt.Fprintf(&b, "### 🔗 %s Multi-MR PreviewGroup: `%s`\n\n", titleVerb, pg.Name)
	fmt.Fprintf(&b, "- **Routing Header**: `%s: %s`\n", effHeaderKey, effHeaderVal)
	if pg.Spec.Routing.ExternalURL != "" {
		fmt.Fprintf(&b, "- **Combined Preview Link**: [🔗 Open `%s`](%s)\n", pg.Name, pg.Spec.Routing.ExternalURL)
	}
	if pg.Spec.Database != nil && pg.Spec.Database.Mode != "" {
		fmt.Fprintf(&b, "- **Database Isolation**: Shared group database (`%s`)\n", pg.Spec.Database.Mode)
	} else {
		b.WriteString("- **Database Isolation**: Per-service (each MR keeps its own database config)\n")
	}
	b.WriteString("\n| Service | Namespace | Image / Mode | Source MR |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, svc := range pg.Spec.Services {
		svcNS := svc.Namespace
		if svcNS == "" {
			svcNS = ns
		}
		imgCell := fmt.Sprintf("`%s`", svc.Image)
		switch svc.Mode {
		case v1alpha1.ServiceModeBaseline:
			imgCell = "`baseline`"
		case v1alpha1.ServiceModeLocal:
			imgCell = fmt.Sprintf("`local (%s)`", svc.Endpoint)
		}
		srcCell := "—"
		if m, ok := memberByService[svc.Name]; ok {
			switch {
			case m.Project != "" && m.MR > 0:
				srcCell = fmt.Sprintf("`%s#%d`", m.Project, m.MR)
			case m.MR > 0:
				srcCell = fmt.Sprintf("`#%d`", m.MR)
			case m.Branch != "":
				srcCell = fmt.Sprintf("`%s`", m.Branch)
			}
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", svc.Name, svcNS, imgCell, srcCell)
	}
	fmt.Fprintf(&b, "\n💡 *New commits pushed to participating PRs/MRs update `%s` automatically. Merging/closing a PR detaches its service back to baseline (and deletes `%s` when the last MR closes), or comment `/diverge ungroup %s` to detach anytime.*", pg.Name, pg.Name, pg.Name)
	return b.String(), nil
}

func executeUngroupCommand(ctx context.Context, c client.Client, ns, groupName, repoURL string, prNumber int) (string, error) {
	project := projectFromRepoURL(repoURL)
	provider := resolveRequestProvider(ctx, repoURL)

	var envName, envNS string
	envs, err := findMatchingEnvironments(ctx, c, ns, repoURL, prNumber)
	if err != nil {
		if apierrors.IsForbidden(err) {
			return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
		}
		return "", err
	}
	for i := range envs {
		if envs[i].Labels != nil && envs[i].Labels["divergedev.com/managed-by"] == "diverge-previewgroup" {
			continue
		}
		envName = envs[i].Name
		envNS = envs[i].Namespace
		break
	}

	if strings.TrimSpace(groupName) != "" {
		res, leaveErr := previewgroup.Leave(ctx, c, previewgroup.LeaveOptions{
			GroupName:            groupName,
			Namespace:            ns,
			Provider:             provider,
			Project:              project,
			MR:                   prNumber,
			EnvironmentName:      envName,
			EnvironmentNamespace: envNS,
		})
		if leaveErr != nil {
			if apierrors.IsForbidden(leaveErr) {
				return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
			}
			return "", leaveErr
		}
		if res == nil || res.NotFound {
			return fmt.Sprintf("ℹ️ PreviewGroup `%s` was not found (already cleaned up).", groupName), nil
		}
		if res.DeletedGroup {
			return fmt.Sprintf("🗑️ **Detached `%s` from `%s`** — no active preview services remained, so PreviewGroup `%s` was automatically deleted.", strings.Join(res.RemovedServices, ", "), res.GroupName, res.GroupName), nil
		}
		if len(res.RemovedServices) == 0 {
			return fmt.Sprintf("ℹ️ This PR/MR had no active services registered in PreviewGroup `%s`.", res.GroupName), nil
		}
		return fmt.Sprintf("🔌 **Detached `%s` from PreviewGroup `%s`** (traffic for `%s` now falls back to baseline; remaining active services: `%s`).",
			strings.Join(res.RemovedServices, ", "), res.GroupName, strings.Join(res.RemovedServices, ", "), strings.Join(res.RemainingServices, ", ")), nil
	}

	envRef := envName
	if envNS != "" && envName != "" {
		envRef = envNS + "/" + envName
	}
	results, err := previewgroup.DetachMemberByPR(ctx, c, provider, project, prNumber, envRef)
	formatDetachLines := func(items []previewgroup.LeaveResult) []string {
		outLines := make([]string, 0, len(items))
		for _, r := range items {
			if r.DeletedGroup {
				outLines = append(outLines, fmt.Sprintf("- **`%s`**: detached `%s` and deleted group (last active MR)", r.GroupName, strings.Join(r.RemovedServices, ", ")))
			} else {
				outLines = append(outLines, fmt.Sprintf("- **`%s`**: detached `%s` (remaining active: `%s`)", r.GroupName, strings.Join(r.RemovedServices, ", "), strings.Join(r.RemainingServices, ", ")))
			}
		}
		return outLines
	}
	if err != nil {
		if apierrors.IsForbidden(err) {
			if len(results) > 0 {
				return "🔌 **Partially detached from shared PreviewGroup(s)**:\n\n" +
					strings.Join(formatDetachLines(results), "\n") +
					"\n\n⚠️ Command did not finish: remaining shared `PreviewGroup`s could not be updated because cluster-scoped controller RBAC (`rbac.namespaceScoped=false`) is required.", nil
			}
			return "⚠️ Shared `PreviewGroup`s require cluster-scoped controller RBAC (`rbac.namespaceScoped=false`).", nil
		}
		if len(results) > 0 {
			return "🔌 **Partially detached from shared PreviewGroup(s)**:\n\n" +
				strings.Join(formatDetachLines(results), "\n") +
				fmt.Sprintf("\n\n⚠️ Command did not finish: %v", err), err
		}
		return "", err
	}
	if len(results) == 0 {
		return "ℹ️ This PR/MR is not currently attached to any shared PreviewGroup.", nil
	}

	return "🔌 **Detached from shared PreviewGroup(s)**:\n\n" + strings.Join(formatDetachLines(results), "\n"), nil
}
