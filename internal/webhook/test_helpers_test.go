package webhook

import (
	"fmt"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

var testScheme *runtime.Scheme

func TestMain(m *testing.M) {
	testScheme = runtime.NewScheme()
	if err := divergeiov1alpha1.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to register scheme: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func newTestClient(objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build()
}
