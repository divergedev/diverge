package testing

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHeaderKeyAndValue(t *testing.T) {
	t.Run("defaults when routing spec fields are empty", func(t *testing.T) {
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-101"},
		}
		assert.Equal(t, "x-diverge-env", headerKey(env))
		assert.Equal(t, "pr-101", headerValue(env))
	})

	t.Run("custom values from routing spec", func(t *testing.T) {
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-101"},
			Spec: v1alpha1.EnvironmentSpec{
				Routing: v1alpha1.EnvironmentRouting{
					HeaderKey:   "x-custom-preview",
					HeaderValue: "custom-val-101",
				},
			},
		}
		assert.Equal(t, "x-custom-preview", headerKey(env))
		assert.Equal(t, "custom-val-101", headerValue(env))
	})
}
