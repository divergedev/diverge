package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

type fakeEnvMCPService struct {
	divergev1alpha1connect.UnimplementedEnvironmentServiceHandler
	envs map[string]*divergev1alpha1.Environment
}

func (f *fakeEnvMCPService) GetEnvironment(_ context.Context, req *connect.Request[divergev1alpha1.GetEnvironmentRequest]) (*connect.Response[divergev1alpha1.GetEnvironmentResponse], error) {
	env, ok := f.envs[req.Msg.Namespace+"/"+req.Msg.Name]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&divergev1alpha1.GetEnvironmentResponse{Environment: env}), nil
}

type fakePgMCPService struct {
	divergev1alpha1connect.UnimplementedPreviewGroupServiceHandler
	groups map[string]*divergev1alpha1.PreviewGroup
}

func (f *fakePgMCPService) GetPreviewGroup(_ context.Context, req *connect.Request[divergev1alpha1.GetPreviewGroupRequest]) (*connect.Response[divergev1alpha1.GetPreviewGroupResponse], error) {
	pg, ok := f.groups[req.Msg.Name]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&divergev1alpha1.GetPreviewGroupResponse{PreviewGroup: pg}), nil
}

func (f *fakePgMCPService) CreatePreviewGroup(_ context.Context, req *connect.Request[divergev1alpha1.CreatePreviewGroupRequest]) (*connect.Response[divergev1alpha1.CreatePreviewGroupResponse], error) {
	pg := req.Msg.PreviewGroup
	pg.ResourceVersion = "1"
	f.groups[pg.Name] = pg
	return connect.NewResponse(&divergev1alpha1.CreatePreviewGroupResponse{PreviewGroup: pg}), nil
}

func (f *fakePgMCPService) UpdatePreviewGroup(_ context.Context, req *connect.Request[divergev1alpha1.UpdatePreviewGroupRequest]) (*connect.Response[divergev1alpha1.UpdatePreviewGroupResponse], error) {
	pg := req.Msg.PreviewGroup
	pg.ResourceVersion = "2"
	f.groups[pg.Name] = pg
	return connect.NewResponse(&divergev1alpha1.UpdatePreviewGroupResponse{PreviewGroup: pg}), nil
}

func (f *fakePgMCPService) DeletePreviewGroup(_ context.Context, req *connect.Request[divergev1alpha1.DeletePreviewGroupRequest]) (*connect.Response[divergev1alpha1.DeletePreviewGroupResponse], error) {
	delete(f.groups, req.Msg.Name)
	return connect.NewResponse(&divergev1alpha1.DeletePreviewGroupResponse{}), nil
}

func TestMCPPreviewGroupJoinAndLeaveTools(t *testing.T) {
	envSvc := &fakeEnvMCPService{
		envs: map[string]*divergev1alpha1.Environment{
			"default/pr-web-101": {
				Name:      "pr-web-101",
				Namespace: "default",
				Spec: &divergev1alpha1.EnvironmentSpec{
					Source: &divergev1alpha1.EnvironmentSource{
						Provider: "github",
						Project:  "acme/web",
						Mr:       101,
						Branch:   "feat/checkout",
					},
					ServiceConfig: &divergev1alpha1.ServicePreviewConfig{
						ServiceName: "web-app",
						Image:       "ghcr.io/acme/web:pr-101",
						Port:        3000,
					},
				},
			},
		},
	}
	pgSvc := &fakePgMCPService{groups: make(map[string]*divergev1alpha1.PreviewGroup)}

	mux := http.NewServeMux()
	envPath, envH := divergev1alpha1connect.NewEnvironmentServiceHandler(envSvc)
	pgPath, pgH := divergev1alpha1connect.NewPreviewGroupServiceHandler(pgSvc)
	mux.Handle(envPath, envH)
	mux.Handle(pgPath, pgH)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	envClient := divergev1alpha1connect.NewEnvironmentServiceClient(srv.Client(), srv.URL)
	pgClient := divergev1alpha1connect.NewPreviewGroupServiceClient(srv.Client(), srv.URL)

	registry := mcpruntime.NewToolRegistry()
	registerPreviewGroupComposeTools(registry, envClient, pgClient)

	joinHandler, ok := registry.Lookup("diverge_join_preview_group")
	require.True(t, ok)
	leaveHandler, ok := registry.Lookup("diverge_leave_preview_group")
	require.True(t, ok)

	// 1. Join Frontend via environment_names and Backend via explicit services in one call!
	res1, err := joinHandler(context.Background(), mcpruntime.ToolRequest{
		Arguments: json.RawMessage(`{
			"group_name": "feat/checkout",
			"environment_names": ["pr-web-101"],
			"services": [{"name": "payments-api", "image": "ghcr.io/acme/payments:pr-42", "project": "acme/payments", "mr": 42}]
		}`),
	})
	require.NoError(t, err)
	assert.False(t, res1.IsError, string(res1.Content))

	var joinOut struct {
		Created   bool   `json:"created"`
		GroupName string `json:"group_name"`
	}
	require.NoError(t, json.Unmarshal(res1.Content, &joinOut))
	assert.True(t, joinOut.Created)
	assert.Equal(t, "feat-checkout", joinOut.GroupName)
	require.Len(t, pgSvc.groups["feat-checkout"].Spec.Services, 2)

	// 2. Leave Backend by mr -> group remains with web-app
	res2, err := leaveHandler(context.Background(), mcpruntime.ToolRequest{
		Arguments: json.RawMessage(`{"group_name": "feat-checkout", "project": "acme/payments", "mr": 42}`),
	})
	require.NoError(t, err)
	assert.False(t, res2.IsError, string(res2.Content))
	var leaveOut struct {
		RemovedServices   []string `json:"removed_services"`
		RemainingServices []string `json:"remaining_services"`
		DeletedGroup      bool     `json:"deleted_group"`
	}
	require.NoError(t, json.Unmarshal(res2.Content, &leaveOut))
	assert.False(t, leaveOut.DeletedGroup)
	assert.Equal(t, []string{"payments-api"}, leaveOut.RemovedServices)
	assert.Equal(t, []string{"web-app"}, leaveOut.RemainingServices)

	// 3. Leave Frontend by environment_names -> last active service leaves, deletes group
	res3, err := leaveHandler(context.Background(), mcpruntime.ToolRequest{
		Arguments: json.RawMessage(`{"group_name": "feat-checkout", "environment_names": ["default/pr-web-101"]}`),
	})
	require.NoError(t, err)
	assert.False(t, res3.IsError, string(res3.Content))
	assert.Contains(t, string(res3.Content), `"deleted_group":true`)
	assert.Empty(t, pgSvc.groups)

	// 4. Verify allowDestructive gates diverge_leave_preview_group while keeping diverge_join_preview_group
	safeSrv := newMCPServer(envClient, pgClient, nil, false)
	safeTools := safeSrv.ListTools()
	assert.Contains(t, safeTools, "diverge_join_preview_group")
	assert.NotContains(t, safeTools, "diverge_leave_preview_group")

	destrSrv := newMCPServer(envClient, pgClient, nil, true)
	destrTools := destrSrv.ListTools()
	assert.Contains(t, destrTools, "diverge_join_preview_group")
	assert.Contains(t, destrTools, "diverge_leave_preview_group")
}
