//go:build integration

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var _ = Describe("Environment Controller", func() {
	Context("When creating an Environment", func() {
		It("Should create successfully", func() {
			ctx := context.Background()
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env",
					Namespace: "default",
				},
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{
						Provider: "gitlab",
						Project:  "foo",
					},
				},
			}
			Expect(k8sClient.Create(ctx, env)).Should(Succeed())

			createdEnv := &v1alpha1.Environment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "test-env", Namespace: "default"}, createdEnv)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})
	})
})
