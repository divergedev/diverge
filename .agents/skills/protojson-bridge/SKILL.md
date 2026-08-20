---
name: protojson-bridge
description: |
  Pattern for mapping between Kubernetes CRD types and Protobuf types
  using a protojson bridge instead of a generated domain layer or
  hand-written field-by-field mappers. Activate when the user needs to
  convert between CRD structs and proto messages, mentions proto2type,
  domain layer mapping, CRD-to-Proto conversion, or asks about mapping
  Kubernetes custom resources to gRPC/ConnectRPC API types.
---

# Protojson Bridge Pattern

## When to Use

Use this pattern when you need to convert between Kubernetes CRD types (`api/v1alpha1/`) and Protobuf-generated types (`api/gen/`) in a server or API layer.

**Do NOT:**
- Generate an intermediate "domain" layer (e.g., via `proto2type`)
- Hand-write field-by-field struct mappers (500+ lines, breaks on every schema change)
- Use `encoding/json` for both sides (loses proto well-known types like Duration/Timestamp)

## The Pattern

### CRD → Proto

```go
import (
    "encoding/json"
    "google.golang.org/protobuf/encoding/protojson"
)

func CRDToProto(crd *v1alpha1.MyResource) (*pb.MyResource, error) {
    if crd == nil {
        return nil, nil
    }
    // Step 1: CRD → JSON (encoding/json handles K8s types correctly)
    b, err := json.Marshal(crd)
    if err != nil {
        return nil, fmt.Errorf("marshal CRD: %w", err)
    }

    // Step 2: JSON → Proto (protojson handles Duration, Timestamp, FieldMask)
    var proto pb.MyResource
    if err := protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(b, &proto); err != nil {
        return nil, fmt.Errorf("unmarshal proto: %w", err)
    }

    // Step 3: Copy ObjectMeta (not part of proto schema)
    proto.Name = crd.Name
    proto.Namespace = crd.Namespace
    proto.Labels = crd.Labels
    proto.Annotations = crd.Annotations
    proto.ResourceVersion = crd.ResourceVersion
    return &proto, nil
}
```

### Proto → CRD

```go
func ProtoToCRD(proto *pb.MyResource) (*v1alpha1.MyResource, error) {
    if proto == nil {
        return nil, nil
    }
    // Step 1: Proto → JSON (protojson outputs standard JSON)
    b, err := protojson.Marshal(proto)
    if err != nil {
        return nil, fmt.Errorf("marshal proto: %w", err)
    }

    // Step 2: JSON → CRD (encoding/json handles K8s types correctly)
    var crd v1alpha1.MyResource
    if err := json.Unmarshal(b, &crd); err != nil {
        return nil, fmt.Errorf("unmarshal CRD: %w", err)
    }

    // Step 3: Copy ObjectMeta
    crd.Name = proto.Name
    crd.Namespace = proto.Namespace
    crd.Labels = proto.Labels
    crd.Annotations = proto.Annotations
    crd.ResourceVersion = proto.ResourceVersion
    return &crd, nil
}
```

## Why This Works

1. **CRD types** use `encoding/json` tags (e.g., `json:"headerKey,omitempty"`)
2. **Proto types** use `protojson` which understands proto field naming conventions
3. Both produce **standard JSON** as the intermediate format
4. `protojson` automatically handles well-known types:
   - `metav1.Duration` ↔ `durationpb.Duration`
   - `metav1.Time` ↔ `timestamppb.Timestamp`
   - `fieldmaskpb.FieldMask`

## Key Options

```go
// When going JSON → Proto, discard CRD-only fields not in the proto schema
protojson.UnmarshalOptions{DiscardUnknown: true}

// When going Proto → JSON, use proto field names (snake_case) to match CRD tags
// NOTE: Only use UseProtoNames if your CRD json tags use snake_case.
// If CRD tags use camelCase (K8s convention), omit UseProtoNames.
protojson.MarshalOptions{UseProtoNames: true}  // or omit for camelCase
```

## ResourceVersion Handling

- **Read path (CRD → Proto):** Always copy `ResourceVersion` so clients can use optimistic concurrency
- **Update path (Proto → CRD):** Client must send back `ResourceVersion` they received; mapper must set it on `ObjectMeta.ResourceVersion`
- **Create path:** Do NOT set `ResourceVersion` on create (K8s assigns it)

## Testing

Always write roundtrip tests:

```go
func TestRoundTrip(t *testing.T) {
    original := &v1alpha1.MyResource{
        ObjectMeta: metav1.ObjectMeta{
            Name: "test", Namespace: "default",
            ResourceVersion: "12345",
            Labels: map[string]string{"app": "test"},
        },
        Spec: v1alpha1.MyResourceSpec{...},
    }

    proto, err := CRDToProto(original)
    require.NoError(t, err)

    roundtripped, err := ProtoToCRD(proto)
    require.NoError(t, err)

    assert.Equal(t, original.Name, roundtripped.Name)
    assert.Equal(t, original.Namespace, roundtripped.Namespace)
    assert.Equal(t, original.ResourceVersion, roundtripped.ResourceVersion)
    assert.Equal(t, original.Labels, roundtripped.Labels)
    // Verify spec fields...
}
```

## Anti-Patterns

| Anti-Pattern | Problem | Use Instead |
|-------------|---------|-------------|
| `proto2type` domain layer | 3,900+ lines of generated boilerplate | protojson bridge |
| Hand-written field mapper | 500+ lines, breaks on schema changes | protojson bridge |
| `json.Marshal` → `json.Unmarshal` (both sides) | Loses Duration/Timestamp semantics | Use `protojson` for the proto side |
| Forgetting `DiscardUnknown: true` | Fails on CRD fields not in proto | Always set it |
| Not copying ObjectMeta | Loses Name, Namespace, ResourceVersion | Always copy manually |

## Reference Implementation

See the Diverge project:
- Mapper: `internal/server/mapper.go`
- Tests: `internal/server/mapper_test.go`
- PR: https://github.com/divergedev/diverge/pull/175
