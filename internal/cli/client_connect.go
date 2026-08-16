package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConnectClient implements EnvironmentClient via ConnectRPC.
type ConnectClient struct {
	envClient divergev1alpha1connect.EnvironmentServiceClient
	pgClient  divergev1alpha1connect.PreviewGroupServiceClient
	token     string
}

// NewConnectClient creates a ConnectRPC-backed environment client.
func NewConnectClient(serverURL, token string) *ConnectClient {
	httpClient := &http.Client{}
	envClient := divergev1alpha1connect.NewEnvironmentServiceClient(
		httpClient,
		serverURL,
		connect.WithInterceptors(newAuthInterceptor(token)),
	)
	pgClient := divergev1alpha1connect.NewPreviewGroupServiceClient(
		httpClient,
		serverURL,
		connect.WithInterceptors(newAuthInterceptor(token)),
	)
	return &ConnectClient{
		envClient: envClient,
		pgClient:  pgClient,
		token:     token,
	}
}

// authInterceptor adds Bearer token to requests.
func newAuthInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if token != "" {
				req.Header().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, req)
		}
	}
}

func (c *ConnectClient) ListEnvironments(ctx context.Context, namespace string) ([]divergev1alpha1.Environment, error) {
	req := connect.NewRequest(&pb.ListEnvironmentsRequest{
		Namespace: namespace,
	})
	res, err := c.envClient.ListEnvironments(ctx, req)
	if err != nil {
		return nil, err
	}

	var envs []divergev1alpha1.Environment
	for _, pbEnv := range res.Msg.Environments {
		b, err := json.Marshal(pbEnv)
		if err != nil {
			return nil, err
		}
		var env divergev1alpha1.Environment
		if err := json.Unmarshal(b, &env); err != nil {
			return nil, err
		}
		env.Name = pbEnv.Name
		env.Namespace = pbEnv.Namespace
		if pbEnv.Labels != nil {
			env.Labels = pbEnv.Labels
		}
		if pbEnv.Annotations != nil {
			env.Annotations = pbEnv.Annotations
		}
		if pbEnv.CreatedAt != nil {
			env.CreationTimestamp = metav1.NewTime(pbEnv.CreatedAt.AsTime())
		}
		envs = append(envs, env)
	}
	return envs, nil
}

func (c *ConnectClient) GetEnvironment(ctx context.Context, namespace, name string) (*divergev1alpha1.Environment, error) {
	req := connect.NewRequest(&pb.GetEnvironmentRequest{
		Namespace: namespace,
		Name:      name,
	})
	res, err := c.envClient.GetEnvironment(ctx, req)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(res.Msg.Environment)
	if err != nil {
		return nil, err
	}
	var env divergev1alpha1.Environment
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	env.Name = res.Msg.Environment.Name
	env.Namespace = res.Msg.Environment.Namespace
	if res.Msg.Environment.Labels != nil {
		env.Labels = res.Msg.Environment.Labels
	}
	if res.Msg.Environment.Annotations != nil {
		env.Annotations = res.Msg.Environment.Annotations
	}
	if res.Msg.Environment.CreatedAt != nil {
		env.CreationTimestamp = metav1.NewTime(res.Msg.Environment.CreatedAt.AsTime())
	}
	return &env, nil
}

func (c *ConnectClient) DeleteEnvironment(ctx context.Context, namespace, name string) error {
	req := connect.NewRequest(&pb.DeleteEnvironmentRequest{
		Namespace: namespace,
		Name:      name,
	})
	_, err := c.envClient.DeleteEnvironment(ctx, req)
	return err
}

func (c *ConnectClient) StreamLogs(ctx context.Context, namespace, envName, service, container string, follow bool, tailLines int64, since string, timestamps bool, previous bool) (io.ReadCloser, error) {
	req := connect.NewRequest(&pb.StreamLogsRequest{
		Namespace:       namespace,
		EnvironmentName: envName,
		ServiceName:     service,
		Container:       container,
		Follow:          follow,
		TailLines:       tailLines,
		Timestamps:      timestamps,
		Previous:        previous,
	})
	stream, err := c.envClient.StreamLogs(ctx, req)
	if err != nil {
		return nil, err
	}

	r, w := io.Pipe()
	go func() {
		defer func() { _ = w.Close() }()
		for stream.Receive() {
			_, _ = w.Write([]byte(stream.Msg().Content))
		}
		_ = stream.Close()
	}()
	return r, nil
}

func (c *ConnectClient) ListPreviewGroups(ctx context.Context, namespace string) ([]divergev1alpha1.PreviewGroup, error) {
	req := connect.NewRequest(&pb.ListPreviewGroupsRequest{
		Namespace: namespace,
	})
	res, err := c.pgClient.ListPreviewGroups(ctx, req)
	if err != nil {
		return nil, err
	}

	var pgs []divergev1alpha1.PreviewGroup
	for _, pbPg := range res.Msg.PreviewGroups {
		b, err := json.Marshal(pbPg)
		if err != nil {
			return nil, err
		}
		var pg divergev1alpha1.PreviewGroup
		if err := json.Unmarshal(b, &pg); err != nil {
			return nil, err
		}
		pg.Name = pbPg.Name
		pg.Namespace = pbPg.Namespace
		if pbPg.Labels != nil {
			pg.Labels = pbPg.Labels
		}
		if pbPg.Annotations != nil {
			pg.Annotations = pbPg.Annotations
		}
		if pbPg.CreatedAt != nil {
			pg.CreationTimestamp = metav1.NewTime(pbPg.CreatedAt.AsTime())
		}
		pgs = append(pgs, pg)
	}
	return pgs, nil
}
