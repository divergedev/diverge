package server

import (
	"encoding/json"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
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
	proto.Labels = crd.Labels
	proto.Annotations = crd.Annotations
	proto.ResourceVersion = crd.ResourceVersion
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
	crd.Name = proto.Name
	crd.Namespace = proto.Namespace
	crd.Labels = proto.Labels
	crd.Annotations = proto.Annotations
	crd.ResourceVersion = proto.ResourceVersion
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
	proto.Labels = crd.Labels
	proto.Annotations = crd.Annotations
	proto.ResourceVersion = crd.ResourceVersion
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
	crd.Name = proto.Name
	crd.Namespace = proto.Namespace
	crd.Labels = proto.Labels
	crd.Annotations = proto.Annotations
	crd.ResourceVersion = proto.ResourceVersion
	return &crd, nil
}
