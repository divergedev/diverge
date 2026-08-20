package server

import (
	"encoding/json"
	"fmt"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// CRDEnvToProto maps a CRD Environment to the protobuf type.
func CRDEnvToProto(crd *v1alpha1.Environment) (*pb.Environment, error) {
	if crd == nil {
		return nil, nil
	}
	b, err := json.Marshal(crd)
	if err != nil {
		return nil, err
	}
	var proto pb.Environment
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, &proto); err != nil {
		return nil, err
	}
	proto.Name = crd.Name
	proto.Namespace = crd.Namespace
	if crd.Labels != nil {
		proto.Labels = make(map[string]string, len(crd.Labels))
		for k, v := range crd.Labels {
			proto.Labels[k] = v
		}
	}
	if crd.Annotations != nil {
		proto.Annotations = make(map[string]string, len(crd.Annotations))
		for k, v := range crd.Annotations {
			proto.Annotations[k] = v
		}
	}
	proto.ResourceVersion = crd.ResourceVersion
	if !crd.CreationTimestamp.IsZero() {
		proto.CreatedAt = timestamppb.New(crd.CreationTimestamp.Time)
	}
	return &proto, nil
}

// ProtoEnvToCRD maps a protobuf Environment to the CRD type.
func ProtoEnvToCRD(proto *pb.Environment) (*v1alpha1.Environment, error) {
	if proto == nil {
		return nil, nil
	}
	b, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(proto)
	if err != nil {
		return nil, err
	}
	var crd v1alpha1.Environment
	if err := json.Unmarshal(b, &crd); err != nil {
		return nil, err
	}

	for k, v := range proto.Labels {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label key %q: %s", k, errs[0])
		}
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label value for key %q: %s", k, errs[0])
		}
	}
	for k := range proto.Annotations {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, fmt.Errorf("invalid annotation key %q: %s", k, errs[0])
		}
	}

	crd.Name = proto.Name
	crd.Namespace = proto.Namespace
	if proto.Labels != nil {
		crd.Labels = make(map[string]string, len(proto.Labels))
		for k, v := range proto.Labels {
			crd.Labels[k] = v
		}
	}
	if proto.Annotations != nil {
		crd.Annotations = make(map[string]string, len(proto.Annotations))
		for k, v := range proto.Annotations {
			crd.Annotations[k] = v
		}
	}
	crd.ResourceVersion = proto.ResourceVersion
	if proto.CreatedAt != nil {
		crd.CreationTimestamp = metav1.NewTime(proto.CreatedAt.AsTime())
	}
	return &crd, nil
}

// CRDPgToProto maps a CRD PreviewGroup to the protobuf type.
func CRDPgToProto(crd *v1alpha1.PreviewGroup) (*pb.PreviewGroup, error) {
	if crd == nil {
		return nil, nil
	}
	b, err := json.Marshal(crd)
	if err != nil {
		return nil, err
	}
	var proto pb.PreviewGroup
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, &proto); err != nil {
		return nil, err
	}
	proto.Name = crd.Name
	proto.Namespace = crd.Namespace
	if crd.Labels != nil {
		proto.Labels = make(map[string]string, len(crd.Labels))
		for k, v := range crd.Labels {
			proto.Labels[k] = v
		}
	}
	if crd.Annotations != nil {
		proto.Annotations = make(map[string]string, len(crd.Annotations))
		for k, v := range crd.Annotations {
			proto.Annotations[k] = v
		}
	}
	proto.ResourceVersion = crd.ResourceVersion
	if !crd.CreationTimestamp.IsZero() {
		proto.CreatedAt = timestamppb.New(crd.CreationTimestamp.Time)
	}
	return &proto, nil
}

// ProtoPgToCRD maps a protobuf PreviewGroup to the CRD type.
func ProtoPgToCRD(proto *pb.PreviewGroup) (*v1alpha1.PreviewGroup, error) {
	if proto == nil {
		return nil, nil
	}
	b, err := (protojson.MarshalOptions{UseProtoNames: false}).Marshal(proto)
	if err != nil {
		return nil, err
	}
	var crd v1alpha1.PreviewGroup
	if err := json.Unmarshal(b, &crd); err != nil {
		return nil, err
	}

	for k, v := range proto.Labels {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label key %q: %s", k, errs[0])
		}
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			return nil, fmt.Errorf("invalid label value for key %q: %s", k, errs[0])
		}
	}
	for k := range proto.Annotations {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return nil, fmt.Errorf("invalid annotation key %q: %s", k, errs[0])
		}
	}

	crd.Name = proto.Name
	crd.Namespace = proto.Namespace
	if proto.Labels != nil {
		crd.Labels = make(map[string]string, len(proto.Labels))
		for k, v := range proto.Labels {
			crd.Labels[k] = v
		}
	}
	if proto.Annotations != nil {
		crd.Annotations = make(map[string]string, len(proto.Annotations))
		for k, v := range proto.Annotations {
			crd.Annotations[k] = v
		}
	}
	crd.ResourceVersion = proto.ResourceVersion
	if proto.CreatedAt != nil {
		crd.CreationTimestamp = metav1.NewTime(proto.CreatedAt.AsTime())
	}
	return &crd, nil
}
