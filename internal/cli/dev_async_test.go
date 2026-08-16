package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func setupTestClient(t *testing.T, objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestWaitForAsyncRoutes(t *testing.T) {
	groupName := "dev-user-svc"
	serviceName := "svc"
	ns := "default"

	tests := []struct {
		name        string
		setup       func(t *testing.T) client.Client
		ctxTimeout  time.Duration
		expectedEnv map[string]string
		expectErr   bool
	}{
		{
			name: "no async routes -> no blocking",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout:  5 * time.Second,
			expectedEnv: nil,
			expectErr:   false,
		},
		{
			name: "condition true on first poll -> proceeds",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
						ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
							Env: []divergeiov1alpha1.EnvVar{
								{Name: "TEMPORAL_TASK_QUEUE", Value: "q1-preview"},
							},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionTrue},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout: 5 * time.Second,
			expectedEnv: map[string]string{
				"TEMPORAL_TASK_QUEUE": "q1-preview",
			},
			expectErr: false,
		},
		{
			name: "condition false then true -> polls and proceeds",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
						ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
							Env: []divergeiov1alpha1.EnvVar{
								{Name: "TEMPORAL_TASK_QUEUE", Value: "q1-preview"},
							},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionFalse},
						},
					},
				}
				c := setupTestClient(t, pg, env)
				go func() {
					time.Sleep(200 * time.Millisecond)
					var e divergeiov1alpha1.Environment
					_ = c.Get(context.Background(), client.ObjectKey{Name: "env-1", Namespace: ns}, &e)
					e.Status.Conditions[0].Status = metav1.ConditionTrue
					_ = c.Update(context.Background(), &e)
				}()
				return c
			},
			ctxTimeout: 5 * time.Second,
			expectedEnv: map[string]string{
				"TEMPORAL_TASK_QUEUE": "q1-preview",
			},
			expectErr: false,
		},
		{
			name: "context cancelled -> error",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionFalse},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout:  200 * time.Millisecond,
			expectedEnv: nil,
			expectErr:   true,
		},
		{
			name: "env var merge: async vars override baseline",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
						ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
							Env: []divergeiov1alpha1.EnvVar{
								{Name: "BASELINE_VAR", Value: "async_wins"},
							},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionTrue},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout: 5 * time.Second,
			expectedEnv: map[string]string{
				"BASELINE_VAR": "async_wins",
			},
			expectErr: false,
		},
		{
			name: "timeout test",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-1"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionFalse},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout:  500 * time.Millisecond,
			expectedEnv: nil,
			expectErr:   true,
		},
		{
			name: "environment not found",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-missing"},
						},
					},
				}
				return setupTestClient(t, pg)
			},
			ctxTimeout:  5 * time.Second,
			expectedEnv: nil,
			expectErr:   true,
		},
		{
			name: "multiple async routes",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
								{Protocol: "temporal", Target: "q1"},
								{Protocol: "temporal", Target: "q2"},
								{Protocol: "temporal", Target: "q3"},
							}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-multi"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-multi", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{
								{Protocol: "temporal", Target: "q1"},
								{Protocol: "temporal", Target: "q2"},
								{Protocol: "temporal", Target: "q3"},
							},
						},
						ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
							Env: []divergeiov1alpha1.EnvVar{
								{Name: "VAR_1", Value: "val1"},
								{Name: "VAR_2", Value: "val2"},
								{Name: "VAR_3", Value: "val3"},
							},
						},
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionTrue},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout: 5 * time.Second,
			expectedEnv: map[string]string{
				"VAR_1": "val1",
				"VAR_2": "val2",
				"VAR_3": "val3",
			},
			expectErr: false,
		},
		{
			name: "nil ServiceConfig with async routes",
			setup: func(t *testing.T) client.Client {
				pg := &divergeiov1alpha1.PreviewGroup{
					ObjectMeta: metav1.ObjectMeta{Name: groupName},
					Spec: divergeiov1alpha1.PreviewGroupSpec{
						Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
							{Name: serviceName, AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}}},
						},
					},
					Status: divergeiov1alpha1.PreviewGroupStatus{
						Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
							{Name: serviceName, EnvironmentName: "env-nil-config"},
						},
					},
				}
				env := &divergeiov1alpha1.Environment{
					ObjectMeta: metav1.ObjectMeta{Name: "env-nil-config", Namespace: ns},
					Spec: divergeiov1alpha1.EnvironmentSpec{
						Routing: divergeiov1alpha1.EnvironmentRouting{
							AsyncRoutes: []divergeiov1alpha1.AsyncRouteSpec{{Protocol: "temporal", Target: "q1"}},
						},
						ServiceConfig: nil,
					},
					Status: divergeiov1alpha1.EnvironmentStatus{
						Conditions: []metav1.Condition{
							{Type: "AsyncRoutingReady", Status: metav1.ConditionTrue},
						},
					},
				}
				return setupTestClient(t, pg, env)
			},
			ctxTimeout:  5 * time.Second,
			expectedEnv: map[string]string{},
			expectErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), tt.ctxTimeout)
			defer cancel()

			envVars, err := waitForAsyncRoutes(ctx, c, groupName, serviceName, ns)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedEnv, envVars)
			}
		})
	}
}
