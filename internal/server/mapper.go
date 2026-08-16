package server

import (
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func EnvironmentToProto(crd *v1alpha1.Environment) *pb.Environment {
	if crd == nil {
		return nil
	}
	out := &pb.Environment{
		Name:        crd.Name,
		Namespace:   crd.Namespace,
		Labels:      crd.Labels,
		Annotations: crd.Annotations,
		Spec:        mapEnvironmentSpecToProto(crd.Spec),
		Status:      mapEnvironmentStatusToProto(crd.Status),
	}
	if !crd.CreationTimestamp.IsZero() {
		out.CreatedAt = timestamppb.New(crd.CreationTimestamp.Time)
	}
	return out
}

func ProtoToEnvironment(msg *pb.Environment) *v1alpha1.Environment {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        msg.Name,
			Namespace:   msg.Namespace,
			Labels:      msg.Labels,
			Annotations: msg.Annotations,
		},
		Spec:   mapProtoToEnvironmentSpec(msg.Spec),
		Status: mapProtoToEnvironmentStatus(msg.Status),
	}
	if msg.CreatedAt != nil {
		out.CreationTimestamp = metav1.NewTime(msg.CreatedAt.AsTime())
	}
	return out
}

func mapEnvironmentSpecToProto(spec v1alpha1.EnvironmentSpec) *pb.EnvironmentSpec {
	return &pb.EnvironmentSpec{
		Source:        mapEnvironmentSourceToProto(spec.Source),
		Deploy:        mapEnvironmentDeployToProto(spec.Deploy),
		Routing:       mapEnvironmentRoutingToProto(spec.Routing),
		Database:      mapEnvironmentDatabaseToProto(&spec.Database),
		Lifecycle:     mapEnvironmentLifecycleToProto(spec.Lifecycle),
		Testing:       mapTestingSpecToProto(spec.Testing),
		ServiceConfig: mapServicePreviewConfigToProto(spec.ServiceConfig),
	}
}

func mapProtoToEnvironmentSpec(msg *pb.EnvironmentSpec) v1alpha1.EnvironmentSpec {
	if msg == nil {
		return v1alpha1.EnvironmentSpec{}
	}
	return v1alpha1.EnvironmentSpec{
		Source:        mapProtoToEnvironmentSource(msg.Source),
		Deploy:        mapProtoToEnvironmentDeploy(msg.Deploy),
		Routing:       mapProtoToEnvironmentRouting(msg.Routing),
		Database:      mapProtoToEnvironmentDatabase(msg.Database),
		Lifecycle:     mapProtoToEnvironmentLifecycle(msg.Lifecycle),
		Testing:       mapProtoToTestingSpec(msg.Testing),
		ServiceConfig: mapProtoToServicePreviewConfig(msg.ServiceConfig),
	}
}

func mapEnvironmentSourceToProto(src v1alpha1.EnvironmentSource) *pb.EnvironmentSource {
	return &pb.EnvironmentSource{
		Provider:  src.Provider,
		Project:   src.Project,
		Mr:        int32(src.MR),
		Branch:    src.Branch,
		CommitSha: src.CommitSHA,
	}
}

func mapProtoToEnvironmentSource(msg *pb.EnvironmentSource) v1alpha1.EnvironmentSource {
	if msg == nil {
		return v1alpha1.EnvironmentSource{}
	}
	return v1alpha1.EnvironmentSource{
		Provider:  msg.Provider,
		Project:   msg.Project,
		MR:        int(msg.Mr),
		Branch:    msg.Branch,
		CommitSHA: msg.CommitSha,
	}
}

func mapEnvironmentDeployToProto(deploy v1alpha1.EnvironmentDeploy) *pb.EnvironmentDeploy {
	return &pb.EnvironmentDeploy{
		Mode:            deploy.Mode,
		Namespace:       deploy.Namespace,
		NamespaceLabels: deploy.NamespaceLabels,
		ChangedServices: deploy.ChangedServices,
		BaselineRef:     deploy.BaselineRef,
		Manifests:       mapManifestSourceToProto(deploy.Manifests),
	}
}

func mapProtoToEnvironmentDeploy(msg *pb.EnvironmentDeploy) v1alpha1.EnvironmentDeploy {
	if msg == nil {
		return v1alpha1.EnvironmentDeploy{}
	}
	return v1alpha1.EnvironmentDeploy{
		Mode:            msg.Mode,
		Namespace:       msg.Namespace,
		NamespaceLabels: msg.NamespaceLabels,
		ChangedServices: msg.ChangedServices,
		BaselineRef:     msg.BaselineRef,
		Manifests:       mapProtoToManifestSource(msg.Manifests),
	}
}

func mapEnvironmentRoutingToProto(routing v1alpha1.EnvironmentRouting) *pb.EnvironmentRouting {
	out := &pb.EnvironmentRouting{
		Mode:        routing.Mode,
		Provider:    routing.Provider,
		HeaderKey:   routing.HeaderKey,
		HeaderValue: routing.HeaderValue,
		ExternalUrl: routing.ExternalURL,
		DevIp:       routing.DevIP,
		BaseDomain:  routing.BaseDomain,
		Cookie:      mapCookieSpecToProto(routing.Cookie),
	}
	for _, ar := range routing.AsyncRoutes {
		out.AsyncRoutes = append(out.AsyncRoutes, mapAsyncRouteSpecToProto(ar))
	}
	return out
}

func mapProtoToEnvironmentRouting(msg *pb.EnvironmentRouting) v1alpha1.EnvironmentRouting {
	if msg == nil {
		return v1alpha1.EnvironmentRouting{}
	}
	out := v1alpha1.EnvironmentRouting{
		Mode:        msg.Mode,
		Provider:    msg.Provider,
		HeaderKey:   msg.HeaderKey,
		HeaderValue: msg.HeaderValue,
		ExternalURL: msg.ExternalUrl,
		DevIP:       msg.DevIp,
		BaseDomain:  msg.BaseDomain,
		Cookie:      mapProtoToCookieSpec(msg.Cookie),
	}
	for _, ar := range msg.AsyncRoutes {
		out.AsyncRoutes = append(out.AsyncRoutes, mapProtoToAsyncRouteSpec(ar))
	}
	return out
}

func mapEnvironmentDatabaseToProto(db *v1alpha1.EnvironmentDatabase) *pb.EnvironmentDatabase {
	if db == nil {
		return nil
	}
	return &pb.EnvironmentDatabase{
		Mode:          db.Mode,
		ConnectionRef: db.ConnectionRef,
		SeedSource:    db.SeedSource,
		MigrationJob:  mapMigrationJobSpecToProto(db.MigrationJob),
	}
}

func mapProtoToEnvironmentDatabase(msg *pb.EnvironmentDatabase) v1alpha1.EnvironmentDatabase {
	if msg == nil {
		return v1alpha1.EnvironmentDatabase{}
	}
	return v1alpha1.EnvironmentDatabase{
		Mode:          msg.Mode,
		ConnectionRef: msg.ConnectionRef,
		SeedSource:    msg.SeedSource,
		MigrationJob:  mapProtoToMigrationJobSpec(msg.MigrationJob),
	}
}

func mapEnvironmentDatabasePtrToProto(db *v1alpha1.EnvironmentDatabase) *pb.EnvironmentDatabase {
	return mapEnvironmentDatabaseToProto(db)
}
func mapProtoToEnvironmentDatabasePtr(msg *pb.EnvironmentDatabase) *v1alpha1.EnvironmentDatabase {
	if msg == nil {
		return nil
	}
	db := mapProtoToEnvironmentDatabase(msg)
	return &db
}

func mapEnvironmentLifecycleToProto(lc v1alpha1.EnvironmentLifecycle) *pb.EnvironmentLifecycle {
	out := &pb.EnvironmentLifecycle{
		CleanupOnMerge: lc.CleanupOnMerge,
	}
	if lc.TTL != nil {
		out.Ttl = durationpb.New(lc.TTL.Duration)
	}
	return out
}

func mapProtoToEnvironmentLifecycle(msg *pb.EnvironmentLifecycle) v1alpha1.EnvironmentLifecycle {
	if msg == nil {
		return v1alpha1.EnvironmentLifecycle{}
	}
	out := v1alpha1.EnvironmentLifecycle{
		CleanupOnMerge: msg.CleanupOnMerge,
	}
	if msg.Ttl != nil {
		out.TTL = &metav1.Duration{Duration: msg.Ttl.AsDuration()}
	}
	return out
}

func mapEnvironmentStatusToProto(status v1alpha1.EnvironmentStatus) *pb.EnvironmentStatus {
	out := &pb.EnvironmentStatus{
		Phase:              string(status.Phase),
		Url:                status.URL,
		Services:           status.Services,
		DatabaseStatus:     status.DatabaseStatus,
		ObservedGeneration: status.ObservedGeneration,
		CommitSha:          status.CommitSHA,
		CommentId:          int32(status.CommentID),
		CommitStatusUrl:    status.CommitStatusURL,
		TestStatus:         mapTestStatusToProto(status.TestStatus),
	}
	if status.CreatedAt != nil {
		out.CreatedAt = timestamppb.New(status.CreatedAt.Time)
	}
	if status.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(status.ExpiresAt.Time)
	}
	for _, c := range status.Conditions {
		out.Conditions = append(out.Conditions, mapConditionToProto(c))
	}
	return out
}

func mapProtoToEnvironmentStatus(msg *pb.EnvironmentStatus) v1alpha1.EnvironmentStatus {
	if msg == nil {
		return v1alpha1.EnvironmentStatus{}
	}
	out := v1alpha1.EnvironmentStatus{
		Phase:              v1alpha1.EnvironmentPhase(msg.Phase),
		URL:                msg.Url,
		Services:           msg.Services,
		DatabaseStatus:     msg.DatabaseStatus,
		ObservedGeneration: msg.ObservedGeneration,
		CommitSHA:          msg.CommitSha,
		CommentID:          int(msg.CommentId),
		CommitStatusURL:    msg.CommitStatusUrl,
		TestStatus:         mapProtoToTestStatus(msg.TestStatus),
	}
	if msg.CreatedAt != nil {
		t := metav1.NewTime(msg.CreatedAt.AsTime())
		out.CreatedAt = &t
	}
	if msg.ExpiresAt != nil {
		t := metav1.NewTime(msg.ExpiresAt.AsTime())
		out.ExpiresAt = &t
	}
	for _, c := range msg.Conditions {
		out.Conditions = append(out.Conditions, mapProtoToCondition(c))
	}
	return out
}

func mapConditionToProto(c metav1.Condition) *pb.Condition {
	return &pb.Condition{
		Type:               c.Type,
		Status:             string(c.Status),
		ObservedGeneration: c.ObservedGeneration,
		LastTransitionTime: timestamppb.New(c.LastTransitionTime.Time),
		Reason:             c.Reason,
		Message:            c.Message,
	}
}

func mapProtoToCondition(p *pb.Condition) metav1.Condition {
	if p == nil {
		return metav1.Condition{}
	}
	c := metav1.Condition{
		Type:               p.Type,
		Status:             metav1.ConditionStatus(p.Status),
		ObservedGeneration: p.ObservedGeneration,
		Reason:             p.Reason,
		Message:            p.Message,
	}
	if p.LastTransitionTime != nil {
		c.LastTransitionTime = metav1.NewTime(p.LastTransitionTime.AsTime())
	}
	return c
}

// TestingSpec
func mapTestingSpecToProto(t *v1alpha1.TestingSpec) *pb.TestingSpec {
	if t == nil {
		return nil
	}
	out := &pb.TestingSpec{
		Enabled:  t.Enabled,
		Trigger:  mapTestTriggerSpecToProto(t.Trigger),
		Required: t.Required,
	}
	if t.Timeout != nil {
		out.Timeout = durationpb.New(t.Timeout.Duration)
	}
	return out
}

func mapProtoToTestingSpec(msg *pb.TestingSpec) *v1alpha1.TestingSpec {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.TestingSpec{
		Enabled:  msg.Enabled,
		Trigger:  mapProtoToTestTriggerSpec(msg.Trigger),
		Required: msg.Required,
	}
	if msg.Timeout != nil {
		out.Timeout = &metav1.Duration{Duration: msg.Timeout.AsDuration()}
	}
	return out
}

func mapTestTriggerSpecToProto(t v1alpha1.TestTriggerSpec) *pb.TestTriggerSpec {
	return &pb.TestTriggerSpec{
		Type:       string(t.Type),
		Project:    t.Project,
		Ref:        t.Ref,
		EventType:  t.EventType,
		WebhookUrl: t.WebhookURL,
		SecretRef:  t.SecretRef,
	}
}
func mapProtoToTestTriggerSpec(msg *pb.TestTriggerSpec) v1alpha1.TestTriggerSpec {
	if msg == nil {
		return v1alpha1.TestTriggerSpec{}
	}
	return v1alpha1.TestTriggerSpec{
		Type:       v1alpha1.TestTriggerType(msg.Type),
		Project:    msg.Project,
		Ref:        msg.Ref,
		EventType:  msg.EventType,
		WebhookURL: msg.WebhookUrl,
		SecretRef:  msg.SecretRef,
	}
}

func mapTestStatusToProto(t *v1alpha1.TestStatus) *pb.TestStatus {
	if t == nil {
		return nil
	}
	out := &pb.TestStatus{
		State:   string(t.State),
		Summary: t.Summary,
		Url:     t.URL,
		RunId:   t.RunID,
	}
	if t.StartedAt != nil {
		out.StartedAt = timestamppb.New(t.StartedAt.Time)
	}
	if t.CompletedAt != nil {
		out.CompletedAt = timestamppb.New(t.CompletedAt.Time)
	}
	return out
}
func mapProtoToTestStatus(msg *pb.TestStatus) *v1alpha1.TestStatus {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.TestStatus{
		State:   v1alpha1.TestState(msg.State),
		Summary: msg.Summary,
		URL:     msg.Url,
		RunID:   msg.RunId,
	}
	if msg.StartedAt != nil {
		t := metav1.NewTime(msg.StartedAt.AsTime())
		out.StartedAt = &t
	}
	if msg.CompletedAt != nil {
		t := metav1.NewTime(msg.CompletedAt.AsTime())
		out.CompletedAt = &t
	}
	return out
}

func mapServicePreviewConfigToProto(c *v1alpha1.ServicePreviewConfig) *pb.ServicePreviewConfig {
	if c == nil {
		return nil
	}
	out := &pb.ServicePreviewConfig{
		ServiceName:     c.ServiceName,
		Namespace:       c.Namespace,
		Port:            c.Port,
		Image:           c.Image,
		ImagePullPolicy: c.ImagePullPolicy,
		DatabaseEnvKey:  c.DatabaseEnvKey,
		ParentRef:       c.ParentRef,
		HeaderKey:       c.HeaderKey,
		PathPrefix:      c.PathPrefix,
		Protocol:        c.Protocol,
		Endpoint:        c.Endpoint,
		Resources:       mapResourceOverrideToProto(c.Resources),
	}
	for _, e := range c.Env {
		out.Env = append(out.Env, mapEnvVarToProto(e))
	}
	return out
}

func mapProtoToServicePreviewConfig(msg *pb.ServicePreviewConfig) *v1alpha1.ServicePreviewConfig {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.ServicePreviewConfig{
		ServiceName:     msg.ServiceName,
		Namespace:       msg.Namespace,
		Port:            msg.Port,
		Image:           msg.Image,
		ImagePullPolicy: msg.ImagePullPolicy,
		DatabaseEnvKey:  msg.DatabaseEnvKey,
		ParentRef:       msg.ParentRef,
		HeaderKey:       msg.HeaderKey,
		PathPrefix:      msg.PathPrefix,
		Protocol:        msg.Protocol,
		Endpoint:        msg.Endpoint,
		Resources:       mapProtoToResourceOverride(msg.Resources),
	}
	for _, e := range msg.Env {
		out.Env = append(out.Env, mapProtoToEnvVar(e))
	}
	return out
}

func mapEnvVarToProto(e v1alpha1.EnvVar) *pb.EnvVar {
	return &pb.EnvVar{Name: e.Name, Value: e.Value}
}
func mapProtoToEnvVar(msg *pb.EnvVar) v1alpha1.EnvVar {
	if msg == nil {
		return v1alpha1.EnvVar{}
	}
	return v1alpha1.EnvVar{Name: msg.Name, Value: msg.Value}
}

func mapResourceOverrideToProto(r *v1alpha1.ResourceOverride) *pb.ResourceOverride {
	if r == nil {
		return nil
	}
	return &pb.ResourceOverride{
		CpuRequest:    r.CPURequest,
		MemoryRequest: r.MemoryRequest,
		CpuLimit:      r.CPULimit,
		MemoryLimit:   r.MemoryLimit,
	}
}
func mapProtoToResourceOverride(msg *pb.ResourceOverride) *v1alpha1.ResourceOverride {
	if msg == nil {
		return nil
	}
	return &v1alpha1.ResourceOverride{
		CPURequest:    msg.CpuRequest,
		MemoryRequest: msg.MemoryRequest,
		CPULimit:      msg.CpuLimit,
		MemoryLimit:   msg.MemoryLimit,
	}
}

func mapManifestSourceToProto(m *v1alpha1.ManifestSource) *pb.ManifestSource {
	if m == nil {
		return nil
	}
	return &pb.ManifestSource{Type: m.Type, Url: m.URL}
}
func mapProtoToManifestSource(msg *pb.ManifestSource) *v1alpha1.ManifestSource {
	if msg == nil {
		return nil
	}
	return &v1alpha1.ManifestSource{Type: msg.Type, URL: msg.Url}
}

func mapMigrationJobSpecToProto(m *v1alpha1.MigrationJobSpec) *pb.MigrationJobSpec {
	if m == nil {
		return nil
	}
	out := &pb.MigrationJobSpec{
		Image:          m.Image,
		Args:           m.Args,
		TimeoutSeconds: m.TimeoutSeconds,
	}
	for _, e := range m.EnvFrom {
		out.EnvFrom = append(out.EnvFrom, mapSecretRefToProto(e))
	}
	return out
}
func mapProtoToMigrationJobSpec(msg *pb.MigrationJobSpec) *v1alpha1.MigrationJobSpec {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.MigrationJobSpec{
		Image:          msg.Image,
		Args:           msg.Args,
		TimeoutSeconds: msg.TimeoutSeconds,
	}
	for _, e := range msg.EnvFrom {
		out.EnvFrom = append(out.EnvFrom, mapProtoToSecretRef(e))
	}
	return out
}

func mapSecretRefToProto(s v1alpha1.SecretRef) *pb.SecretRef {
	return &pb.SecretRef{Namespace: s.Namespace, Name: s.Name, Key: s.Key}
}
func mapProtoToSecretRef(msg *pb.SecretRef) v1alpha1.SecretRef {
	if msg == nil {
		return v1alpha1.SecretRef{}
	}
	return v1alpha1.SecretRef{Namespace: msg.Namespace, Name: msg.Name, Key: msg.Key}
}

func mapAsyncRouteSpecToProto(a v1alpha1.AsyncRouteSpec) *pb.AsyncRouteSpec {
	return &pb.AsyncRouteSpec{
		Protocol:      a.Protocol,
		Target:        a.Target,
		EnvVarMapping: a.EnvVarMapping,
	}
}
func mapProtoToAsyncRouteSpec(msg *pb.AsyncRouteSpec) v1alpha1.AsyncRouteSpec {
	if msg == nil {
		return v1alpha1.AsyncRouteSpec{}
	}
	return v1alpha1.AsyncRouteSpec{
		Protocol:      msg.Protocol,
		Target:        msg.Target,
		EnvVarMapping: msg.EnvVarMapping,
	}
}

func mapCookieSpecToProto(c *v1alpha1.CookieSpec) *pb.CookieSpec {
	if c == nil {
		return nil
	}
	return &pb.CookieSpec{
		Enabled:  c.Enabled,
		MaxAge:   int32(c.MaxAge),
		SameSite: c.SameSite,
		Secure:   c.Secure,
	}
}
func mapProtoToCookieSpec(msg *pb.CookieSpec) *v1alpha1.CookieSpec {
	if msg == nil {
		return nil
	}
	return &v1alpha1.CookieSpec{
		Enabled:  msg.Enabled,
		MaxAge:   int(msg.MaxAge),
		SameSite: msg.SameSite,
		Secure:   msg.Secure,
	}
}

// PreviewGroup
func PreviewGroupToProto(crd *v1alpha1.PreviewGroup) *pb.PreviewGroup {
	if crd == nil {
		return nil
	}
	out := &pb.PreviewGroup{
		Name:        crd.Name,
		Namespace:   crd.Namespace,
		Labels:      crd.Labels,
		Annotations: crd.Annotations,
		Spec:        mapPreviewGroupSpecToProto(crd.Spec),
		Status:      mapPreviewGroupStatusToProto(crd.Status),
	}
	if !crd.CreationTimestamp.IsZero() {
		out.CreatedAt = timestamppb.New(crd.CreationTimestamp.Time)
	}
	return out
}

func ProtoToPreviewGroup(msg *pb.PreviewGroup) *v1alpha1.PreviewGroup {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        msg.Name,
			Namespace:   msg.Namespace,
			Labels:      msg.Labels,
			Annotations: msg.Annotations,
		},
		Spec:   mapProtoToPreviewGroupSpec(msg.Spec),
		Status: mapProtoToPreviewGroupStatus(msg.Status),
	}
	if msg.CreatedAt != nil {
		out.CreationTimestamp = metav1.NewTime(msg.CreatedAt.AsTime())
	}
	return out
}

func mapPreviewGroupSpecToProto(spec v1alpha1.PreviewGroupSpec) *pb.PreviewGroupSpec {
	out := &pb.PreviewGroupSpec{
		Source:    mapEnvironmentSourceToProto(spec.Source),
		Routing:   mapPreviewGroupRoutingToProto(spec.Routing),
		Database:  mapEnvironmentDatabasePtrToProto(spec.Database),
		Lifecycle: mapPreviewGroupLifecycleToProto(spec.Lifecycle),
		Owner:     spec.Owner,
	}
	for _, s := range spec.Services {
		out.Services = append(out.Services, mapPreviewGroupServiceSpecToProto(s))
	}
	return out
}

func mapProtoToPreviewGroupSpec(msg *pb.PreviewGroupSpec) v1alpha1.PreviewGroupSpec {
	if msg == nil {
		return v1alpha1.PreviewGroupSpec{}
	}
	out := v1alpha1.PreviewGroupSpec{
		Source:    mapProtoToEnvironmentSource(msg.Source),
		Routing:   mapProtoToPreviewGroupRouting(msg.Routing),
		Database:  mapProtoToEnvironmentDatabasePtr(msg.Database),
		Lifecycle: mapProtoToPreviewGroupLifecycle(msg.Lifecycle),
		Owner:     msg.Owner,
	}
	for _, s := range msg.Services {
		out.Services = append(out.Services, mapProtoToPreviewGroupServiceSpec(s))
	}
	return out
}

func mapPreviewGroupRoutingToProto(r v1alpha1.PreviewGroupRouting) *pb.PreviewGroupRouting {
	return &pb.PreviewGroupRouting{
		Mode:        r.Mode,
		HeaderKey:   r.HeaderKey,
		HeaderValue: r.HeaderValue,
		ExternalUrl: r.ExternalURL,
		BaseDomain:  r.BaseDomain,
	}
}

func mapProtoToPreviewGroupRouting(msg *pb.PreviewGroupRouting) v1alpha1.PreviewGroupRouting {
	if msg == nil {
		return v1alpha1.PreviewGroupRouting{}
	}
	return v1alpha1.PreviewGroupRouting{
		Mode:        msg.Mode,
		HeaderKey:   msg.HeaderKey,
		HeaderValue: msg.HeaderValue,
		ExternalURL: msg.ExternalUrl,
		BaseDomain:  msg.BaseDomain,
	}
}

func mapPreviewGroupLifecycleToProto(l *v1alpha1.PreviewGroupLifecycle) *pb.PreviewGroupLifecycle {
	if l == nil {
		return nil
	}
	out := &pb.PreviewGroupLifecycle{
		CleanupOnMerge: l.CleanupOnMerge,
	}
	if l.TTL != nil {
		out.Ttl = durationpb.New(l.TTL.Duration)
	}
	return out
}

func mapProtoToPreviewGroupLifecycle(msg *pb.PreviewGroupLifecycle) *v1alpha1.PreviewGroupLifecycle {
	if msg == nil {
		return nil
	}
	out := &v1alpha1.PreviewGroupLifecycle{
		CleanupOnMerge: msg.CleanupOnMerge,
	}
	if msg.Ttl != nil {
		out.TTL = &metav1.Duration{Duration: msg.Ttl.AsDuration()}
	}
	return out
}

func mapPreviewGroupServiceSpecToProto(s v1alpha1.PreviewGroupServiceSpec) *pb.PreviewGroupServiceSpec {
	out := &pb.PreviewGroupServiceSpec{
		Name:            s.Name,
		Image:           s.Image,
		Mode:            string(s.Mode),
		Endpoint:        s.Endpoint,
		Namespace:       s.Namespace,
		Port:            s.Port,
		ParentRef:       s.ParentRef,
		PathPrefix:      s.PathPrefix,
		Protocol:        string(s.Protocol),
		ImagePullPolicy: s.ImagePullPolicy,
		Resources:       mapResourceOverrideToProto(s.Resources),
		Database:        mapEnvironmentDatabasePtrToProto(s.Database),
	}
	for _, ar := range s.AsyncRoutes {
		out.AsyncRoutes = append(out.AsyncRoutes, mapAsyncRouteSpecToProto(ar))
	}
	for _, env := range s.Env {
		out.Env = append(out.Env, mapEnvVarToProto(env))
	}
	return out
}

func mapProtoToPreviewGroupServiceSpec(msg *pb.PreviewGroupServiceSpec) v1alpha1.PreviewGroupServiceSpec {
	if msg == nil {
		return v1alpha1.PreviewGroupServiceSpec{}
	}
	out := v1alpha1.PreviewGroupServiceSpec{
		Name:            msg.Name,
		Image:           msg.Image,
		Mode:            v1alpha1.ServiceMode(msg.Mode),
		Endpoint:        msg.Endpoint,
		Namespace:       msg.Namespace,
		Port:            msg.Port,
		ParentRef:       msg.ParentRef,
		PathPrefix:      msg.PathPrefix,
		Protocol:        v1alpha1.ServiceProtocol(msg.Protocol),
		ImagePullPolicy: msg.ImagePullPolicy,
		Resources:       mapProtoToResourceOverride(msg.Resources),
		Database:        mapProtoToEnvironmentDatabasePtr(msg.Database),
	}
	for _, ar := range msg.AsyncRoutes {
		out.AsyncRoutes = append(out.AsyncRoutes, mapProtoToAsyncRouteSpec(ar))
	}
	for _, env := range msg.Env {
		out.Env = append(out.Env, mapProtoToEnvVar(env))
	}
	return out
}

func mapPreviewGroupStatusToProto(s v1alpha1.PreviewGroupStatus) *pb.PreviewGroupStatus {
	out := &pb.PreviewGroupStatus{
		Phase:              string(s.Phase),
		ServiceCount:       s.ServiceCount,
		ObservedGeneration: s.ObservedGeneration,
		CommentId:          s.CommentID,
	}
	if s.CreatedAt != nil {
		out.CreatedAt = timestamppb.New(s.CreatedAt.Time)
	}
	if s.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(s.ExpiresAt.Time)
	}
	if s.LeaseRenewedAt != nil {
		out.LeaseRenewedAt = timestamppb.New(s.LeaseRenewedAt.Time)
	}
	for _, svc := range s.Services {
		out.Services = append(out.Services, mapPreviewGroupServiceStatusToProto(svc))
	}
	for _, c := range s.Conditions {
		out.Conditions = append(out.Conditions, mapConditionToProto(c))
	}
	return out
}

func mapProtoToPreviewGroupStatus(msg *pb.PreviewGroupStatus) v1alpha1.PreviewGroupStatus {
	if msg == nil {
		return v1alpha1.PreviewGroupStatus{}
	}
	out := v1alpha1.PreviewGroupStatus{
		Phase:              v1alpha1.PreviewGroupPhase(msg.Phase),
		ServiceCount:       msg.ServiceCount,
		ObservedGeneration: msg.ObservedGeneration,
		CommentID:          msg.CommentId,
	}
	if msg.CreatedAt != nil {
		t := metav1.NewTime(msg.CreatedAt.AsTime())
		out.CreatedAt = &t
	}
	if msg.ExpiresAt != nil {
		t := metav1.NewTime(msg.ExpiresAt.AsTime())
		out.ExpiresAt = &t
	}
	if msg.LeaseRenewedAt != nil {
		t := metav1.NewTime(msg.LeaseRenewedAt.AsTime())
		out.LeaseRenewedAt = &t
	}
	for _, svc := range msg.Services {
		out.Services = append(out.Services, mapProtoToPreviewGroupServiceStatus(svc))
	}
	for _, c := range msg.Conditions {
		out.Conditions = append(out.Conditions, mapProtoToCondition(c))
	}
	return out
}

func mapPreviewGroupServiceStatusToProto(s v1alpha1.PreviewGroupServiceStatus) *pb.PreviewGroupServiceStatus {
	return &pb.PreviewGroupServiceStatus{
		Name:            s.Name,
		EnvironmentName: s.EnvironmentName,
		Namespace:       s.Namespace,
		Phase:           string(s.Phase),
		Url:             s.URL,
		Message:         s.Message,
		Reason:          s.Reason,
		LastLogSnippet:  s.LastLogSnippet,
	}
}

func mapProtoToPreviewGroupServiceStatus(msg *pb.PreviewGroupServiceStatus) v1alpha1.PreviewGroupServiceStatus {
	if msg == nil {
		return v1alpha1.PreviewGroupServiceStatus{}
	}
	return v1alpha1.PreviewGroupServiceStatus{
		Name:            msg.Name,
		EnvironmentName: msg.EnvironmentName,
		Namespace:       msg.Namespace,
		Phase:           v1alpha1.EnvironmentPhase(msg.Phase),
		URL:             msg.Url,
		Message:         msg.Message,
		Reason:          msg.Reason,
		LastLogSnippet:  msg.LastLogSnippet,
	}
}
