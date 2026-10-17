package config

import (
	"bytes"
	"encoding/json"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// jsonSchemaNode represents a Draft-07 JSON Schema node with deterministic field ordering.
type jsonSchemaNode struct {
	Schema               string                     `json:"$schema,omitempty"`
	ID                   string                     `json:"$id,omitempty"`
	Title                string                     `json:"title,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Type                 string                     `json:"type,omitempty"`
	Enum                 []string                   `json:"enum,omitempty"`
	Default              any                        `json:"default,omitempty"`
	Pattern              string                     `json:"pattern,omitempty"`
	Properties           map[string]*jsonSchemaNode `json:"properties,omitempty"`
	AdditionalProperties any                        `json:"additionalProperties,omitempty"`
	Items                *jsonSchemaNode            `json:"items,omitempty"`
	Required             []string                   `json:"required,omitempty"`
}

type fieldConstraint struct {
	canonicalYAMLName string
	description       string
	enum              []string
	defaultVal        any
	pattern           string
}

var messageRequiredFields = map[string][]string{
	"diverge.v1alpha1.DivergeConfig":  {"version"},
	"diverge.v1alpha1.ConfigService":  {"paths", "image"},
	"diverge.v1alpha1.ConfigImage":    {"repository"},
	"diverge.v1alpha1.AsyncRouteSpec": {"protocol", "target"},
}

var messageDescriptions = map[string]string{
	"diverge.v1alpha1.ConfigService":             "Service discovery and container/Helm configuration.",
	"diverge.v1alpha1.ConfigImage":               "Container image template settings.",
	"diverge.v1alpha1.ConfigHelm":                "Optional Helm chart location.",
	"diverge.v1alpha1.ConfigEnvironmentSettings": "Default environment settings.",
	"diverge.v1alpha1.ConfigAtlasSettings":       "Atlas schema management and migration settings.",
	"diverge.v1alpha1.ConfigFeatureSettings":     "Feature flag overrides and remote configuration.",
	"diverge.v1alpha1.ConfigDevSettings":         "Local development conflict resolution settings (diverge dev).",
	"diverge.v1alpha1.ConfigNotifications":       "Notification settings for Git providers.",
	"diverge.v1alpha1.ConfigTopology":            "Service topology discovery configuration.",
	"diverge.v1alpha1.ConfigPrometheus":          "Prometheus-backed service dependency discovery settings.",
}

var fieldConstraints = map[string]fieldConstraint{
	"diverge.v1alpha1.DivergeConfig.version": {
		description: "Configuration schema version.",
		enum:        []string{"1"},
	},
	"diverge.v1alpha1.DivergeConfig.services": {
		description: "Service discovery mapping service names to source paths.",
	},
	"diverge.v1alpha1.DivergeConfig.defaults": {
		description: "Default environment settings.",
	},
	"diverge.v1alpha1.DivergeConfig.environments": {
		description: "Environment type overrides.",
	},
	"diverge.v1alpha1.DivergeConfig.label_overrides": {
		description: "MR label overrides mapping.",
	},
	"diverge.v1alpha1.DivergeConfig.notifications": {
		description: "Notification settings for Git providers.",
	},
	"diverge.v1alpha1.DivergeConfig.topology": {
		description: "Service dependency topology discovery configuration.",
	},
	"diverge.v1alpha1.ConfigService.paths": {
		description: "Glob patterns for change detection.",
	},
	"diverge.v1alpha1.ConfigService.image": {
		description: "Container image template settings.",
	},
	"diverge.v1alpha1.ConfigService.helm": {
		description: "Optional Helm chart location.",
	},
	"diverge.v1alpha1.ConfigService.async_routes": {
		canonicalYAMLName: "asyncRoutes",
		description:       "Async routing targets (Temporal task queues, Kafka topics, Redis/Valkey key prefixes, S3/GCS/MinIO object prefixes) for event-driven services.",
	},
	"diverge.v1alpha1.ConfigService.depends_on": {
		canonicalYAMLName: "dependsOn",
		description:       "Services that this service calls at runtime.",
	},
	"diverge.v1alpha1.ConfigService.entrypoint": {
		description: "Marks this service as an ingress gateway entrypoint for topology path resolution.",
	},
	"diverge.v1alpha1.AsyncRouteSpec.protocol": {
		description: "Async protocol type.",
		enum:        []string{"temporal", "kafka", "redis", "valkey", "s3", "gcs", "minio"},
	},
	"diverge.v1alpha1.AsyncRouteSpec.target": {
		description: "Baseline resource name (e.g., Temporal task queue, Kafka topic, or Redis/Valkey key prefix target).",
	},
	"diverge.v1alpha1.AsyncRouteSpec.env_var_mapping": {
		canonicalYAMLName: "envVarMapping",
		description:       "Maps provisioned targets to environment variable names.",
	},
	"diverge.v1alpha1.ConfigImage.tag_template": {
		defaultVal: "mr-{{.MR}}",
	},
	"diverge.v1alpha1.ConfigHelm.path": {
		description: "Relative path to chart in repo",
	},
	"diverge.v1alpha1.ConfigHelm.values_file": {
		description: "Optional override values",
	},
	"diverge.v1alpha1.ConfigDeploySettings.mode": {
		enum: []string{"delta", "full"},
	},
	"diverge.v1alpha1.ConfigDeploySettings.namespace": {
		enum: []string{"same", "create"},
	},
	"diverge.v1alpha1.ConfigRoutingSettings.mode": {
		enum: []string{"header", "namespace", "subdomain"},
	},
	"diverge.v1alpha1.ConfigBannerSettings.position": {
		enum: []string{"top", "bottom"},
	},
	"diverge.v1alpha1.ConfigBannerSettings.color": {
		pattern: "^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$",
	},
	"diverge.v1alpha1.ConfigDatabaseSettings.mode": {
		enum: []string{"shared", "schema", "snapshot", "fresh"},
	},
	"diverge.v1alpha1.ConfigDatabaseSettings.atlas": {
		description: "Atlas schema management and migration settings.",
	},
	"diverge.v1alpha1.ConfigAtlasSettings.mode": {
		enum: []string{"versioned", "declarative"},
	},
	"diverge.v1alpha1.ConfigAtlasSettings.engine": {
		enum: []string{"operator", "job"},
	},
	"diverge.v1alpha1.ConfigAtlasSettings.dir": {
		description: "Local path to migrations directory for versioned mode.",
	},
	"diverge.v1alpha1.ConfigAtlasSettings.schema": {
		description: "Local path to schema file (SQL or HCL) for declarative mode.",
	},
	"diverge.v1alpha1.ConfigAtlasSettings.extra_args": {
		description: "Additional command-line arguments to pass to the Atlas CLI.",
	},
	"diverge.v1alpha1.ConfigAtlasPolicySettings.destructive": {
		enum: []string{"error", "warn", "allow"},
	},
	"diverge.v1alpha1.ConfigFeatureSettings.provider": {
		enum: []string{"configmap", "flipt", "flagsmith", "unleash", "noop", "none"},
	},
	"diverge.v1alpha1.ConfigDevSettings.on_conflict": {
		enum: []string{"warn", "block", "allow"},
	},
	"diverge.v1alpha1.ConfigEnvironmentType.trigger": {
		enum: []string{"label", "auto", "manual", "branch", "schedule"},
	},
	"diverge.v1alpha1.ConfigNotifications.provider": {
		enum: []string{"gitlab", "github"},
	},
	"diverge.v1alpha1.ConfigPrometheus.token_env": {
		canonicalYAMLName: "tokenEnv",
	},
	"diverge.v1alpha1.ConfigPrometheus.token_file": {
		canonicalYAMLName: "tokenFile",
	},
	"diverge.v1alpha1.ConfigPrometheus.ca_bundle": {
		canonicalYAMLName: "caBundle",
	},
	"diverge.v1alpha1.ConfigPrometheus.insecure_tls": {
		canonicalYAMLName: "insecureTLS",
	},
	"diverge.v1alpha1.ConfigPrometheus.poll_interval": {
		canonicalYAMLName: "pollInterval",
	},
	"diverge.v1alpha1.ConfigPrometheus.cache_ttl": {
		canonicalYAMLName: "cacheTTL",
	},
	"diverge.v1alpha1.ConfigPrometheus.mesh_type": {
		canonicalYAMLName: "meshType",
	},
	"diverge.v1alpha1.ConfigPrometheus.lookback_window": {
		canonicalYAMLName: "lookbackWindow",
	},
}

// GenerateJSONSchema generates a deterministic Draft-07 JSON Schema for .diverge.yaml
// by reflecting over the diverge.v1alpha1.DivergeConfig protobuf descriptor.
func GenerateJSONSchema() ([]byte, error) {
	md := (&divergev1alpha1.DivergeConfig{}).ProtoReflect().Descriptor()
	root := schemaForMessage(md)
	root.Schema = "http://json-schema.org/draft-07/schema#"
	root.ID = "https://divergedev.com/schemas/diverge.schema.json"
	root.Title = "Diverge Config"
	root.Description = "Configuration for Diverge preview environments (generated from diverge.v1alpha1.DivergeConfig protobuf)"

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func schemaForMessage(md protoreflect.MessageDescriptor) *jsonSchemaNode {
	fullName := string(md.FullName())
	node := &jsonSchemaNode{
		Type:                 "object",
		Description:          messageDescriptions[fullName],
		Properties:           make(map[string]*jsonSchemaNode, md.Fields().Len()),
		Required:             messageRequiredFields[fullName],
		AdditionalProperties: false,
	}

	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fieldSchema := schemaForField(fd)
		constraint, hasConstraint := fieldConstraints[string(fd.FullName())]
		propName := string(fd.TextName())
		if hasConstraint && constraint.canonicalYAMLName != "" {
			propName = constraint.canonicalYAMLName
		}
		node.Properties[propName] = fieldSchema
	}

	return node
}

func schemaForField(fd protoreflect.FieldDescriptor) *jsonSchemaNode {
	constraint := fieldConstraints[string(fd.FullName())]

	if fd.IsMap() {
		valDesc := fd.MapValue()
		return &jsonSchemaNode{
			Type:                 "object",
			Description:          constraint.description,
			AdditionalProperties: schemaForSingularField(valDesc, fieldConstraint{}),
		}
	}

	if fd.IsList() {
		return &jsonSchemaNode{
			Type:        "array",
			Description: constraint.description,
			Items:       schemaForSingularField(fd, fieldConstraint{}),
		}
	}

	return schemaForSingularField(fd, constraint)
}

func schemaForSingularField(fd protoreflect.FieldDescriptor, constraint fieldConstraint) *jsonSchemaNode {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		msgNode := schemaForMessage(fd.Message())
		if constraint.description != "" {
			msgNode.Description = constraint.description
		}
		return msgNode
	case protoreflect.BoolKind:
		return &jsonSchemaNode{
			Type:        "boolean",
			Description: constraint.description,
			Default:     constraint.defaultVal,
		}
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return &jsonSchemaNode{
			Type:        "integer",
			Description: constraint.description,
			Default:     constraint.defaultVal,
		}
	default:
		return &jsonSchemaNode{
			Type:        "string",
			Description: constraint.description,
			Enum:        constraint.enum,
			Default:     constraint.defaultVal,
			Pattern:     constraint.pattern,
		}
	}
}
