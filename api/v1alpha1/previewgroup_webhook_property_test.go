package v1alpha1

import (
	"testing"
	"testing/quick"
)

func TestValidatePreviewGroup_ValidLocalAlwaysHasEndpoint(t *testing.T) {
	f := func(endpoint string) bool {
		// Valid local means endpoint is not empty
		pg := &PreviewGroup{
			Spec: PreviewGroupSpec{
				Services: []PreviewGroupServiceSpec{
					{
						Name:     "test",
						Mode:     ServiceModeLocal,
						Endpoint: endpoint,
					},
				},
			},
		}

		errs := validatePreviewGroup(pg)
		if endpoint == "" {
			return len(errs) > 0
		}

		// If endpoint is not empty, there should be no error about endpoint missing
		for _, e := range errs {
			if e.Field == "spec.services[0].endpoint" && e.Type == "FieldValueRequired" {
				return false // Should not have required error if it's set
			}
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

func TestValidatePreviewGroup_ValidImageAlwaysHasImage(t *testing.T) {
	f := func(image string) bool {
		pg := &PreviewGroup{
			Spec: PreviewGroupSpec{
				Services: []PreviewGroupServiceSpec{
					{
						Name:  "test",
						Mode:  ServiceModeImage, // Image mode
						Image: image,
					},
				},
			},
		}

		errs := validatePreviewGroup(pg)
		if image == "" {
			return len(errs) > 0
		}

		for _, e := range errs {
			if e.Field == "spec.services[0].image" && e.Type == "FieldValueRequired" {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
