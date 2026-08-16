package server

import (
	"encoding/json"

	"github.com/divergedev/diverge/api/v1alpha1"
	domain "github.com/divergedev/diverge/gen/domain/github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

// CRDEnvToDomain maps a CRD Environment to the protobuf domain type.
func CRDEnvToDomain(crd *v1alpha1.Environment) (*domain.Environment, error) {
	if crd == nil {
		return nil, nil
	}
	b, err := json.Marshal(crd)
	if err != nil {
		return nil, err
	}
	var dom domain.Environment
	if err := json.Unmarshal(b, &dom); err != nil {
		return nil, err
	}
	// Copy top level metadata
	dom.Name = crd.Name
	dom.Namespace = crd.Namespace
	dom.Labels = crd.Labels
	dom.Annotations = crd.Annotations
	return &dom, nil
}

// DomainEnvToCRD maps a protobuf domain Environment to the CRD type.
func DomainEnvToCRD(dom *domain.Environment) (*v1alpha1.Environment, error) {
	if dom == nil {
		return nil, nil
	}
	b, err := json.Marshal(dom)
	if err != nil {
		return nil, err
	}
	var crd v1alpha1.Environment
	if err := json.Unmarshal(b, &crd); err != nil {
		return nil, err
	}
	crd.Name = dom.Name
	crd.Namespace = dom.Namespace
	crd.Labels = dom.Labels
	crd.Annotations = dom.Annotations
	return &crd, nil
}

// CRDPgToDomain maps a CRD PreviewGroup to the protobuf domain type.
func CRDPgToDomain(crd *v1alpha1.PreviewGroup) (*domain.PreviewGroup, error) {
	if crd == nil {
		return nil, nil
	}
	b, err := json.Marshal(crd)
	if err != nil {
		return nil, err
	}
	var dom domain.PreviewGroup
	if err := json.Unmarshal(b, &dom); err != nil {
		return nil, err
	}
	dom.Name = crd.Name
	dom.Namespace = crd.Namespace
	dom.Labels = crd.Labels
	dom.Annotations = crd.Annotations
	return &dom, nil
}

// DomainPgToCRD maps a protobuf domain PreviewGroup to the CRD type.
func DomainPgToCRD(dom *domain.PreviewGroup) (*v1alpha1.PreviewGroup, error) {
	if dom == nil {
		return nil, nil
	}
	b, err := json.Marshal(dom)
	if err != nil {
		return nil, err
	}
	var crd v1alpha1.PreviewGroup
	if err := json.Unmarshal(b, &crd); err != nil {
		return nil, err
	}
	crd.Name = dom.Name
	crd.Namespace = dom.Namespace
	crd.Labels = dom.Labels
	crd.Annotations = dom.Annotations
	return &crd, nil
}
