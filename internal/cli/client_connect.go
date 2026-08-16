package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ConnectClient implements EnvironmentClient via ConnectRPC.
type ConnectClient struct {
	envClient       divergev1alpha1connect.EnvironmentServiceClient
	streamEnvClient divergev1alpha1connect.EnvironmentServiceClient
	pgClient        divergev1alpha1connect.PreviewGroupServiceClient
	token           string
}

// NewConnectClient creates a ConnectRPC-backed environment client.
func NewConnectClient(serverURL, token string) *ConnectClient {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   10,
		},
	}
	streamClient := &http.Client{
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   10,
		},
	}
	envClient := divergev1alpha1connect.NewEnvironmentServiceClient(
		httpClient,
		serverURL,
		connect.WithInterceptors(newAuthInterceptor(token)),
	)
	streamEnvClient := divergev1alpha1connect.NewEnvironmentServiceClient(
		streamClient,
		serverURL,
		connect.WithInterceptors(newAuthInterceptor(token)),
	)
	pgClient := divergev1alpha1connect.NewPreviewGroupServiceClient(
		httpClient,
		serverURL,
		connect.WithInterceptors(newAuthInterceptor(token)),
	)
	return &ConnectClient{
		envClient:       envClient,
		streamEnvClient: streamEnvClient,
		pgClient:        pgClient,
		token:           token,
	}
}

func connectToK8sError(err error) error {
	if err == nil {
		return nil
	}
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeNotFound:
			return apierrors.NewNotFound(schema.GroupResource{Group: "diverge.io", Resource: "environments"}, "")
		case connect.CodeAlreadyExists:
			return apierrors.NewAlreadyExists(schema.GroupResource{}, "")
		case connect.CodePermissionDenied:
			return apierrors.NewForbidden(schema.GroupResource{}, "", errors.New("permission denied"))
		}
	}
	return err
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
		return nil, connectToK8sError(err)
	}

	var envs []divergev1alpha1.Environment
	for _, pbEnv := range res.Msg.Environments {
		b, err := json.Marshal(pbEnv)
		if err != nil {
			return nil, connectToK8sError(err)
		}
		var env divergev1alpha1.Environment
		if err := json.Unmarshal(b, &env); err != nil {
			return nil, connectToK8sError(err)
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
		return nil, connectToK8sError(err)
	}

	b, err := json.Marshal(res.Msg.Environment)
	if err != nil {
		return nil, connectToK8sError(err)
	}
	var env divergev1alpha1.Environment
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, connectToK8sError(err)
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

func (c *ConnectClient) CreateEnvironment(ctx context.Context, env *divergev1alpha1.Environment) (*divergev1alpha1.Environment, error) {
	b, err := json.Marshal(env)
	if err != nil {
		return nil, connectToK8sError(err)
	}
	var pbEnv pb.Environment
	if err := json.Unmarshal(b, &pbEnv); err != nil {
		return nil, connectToK8sError(err)
	}
	pbEnv.Name = env.Name
	pbEnv.Namespace = env.Namespace
	pbEnv.Labels = env.Labels
	pbEnv.Annotations = env.Annotations

	req := connect.NewRequest(&pb.CreateEnvironmentRequest{
		Environment: &pbEnv,
	})
	res, err := c.envClient.CreateEnvironment(ctx, req)
	if err != nil {
		return nil, connectToK8sError(err)
	}

	bRes, err := json.Marshal(res.Msg.Environment)
	if err != nil {
		return nil, connectToK8sError(err)
	}
	var resEnv divergev1alpha1.Environment
	if err := json.Unmarshal(bRes, &resEnv); err != nil {
		return nil, connectToK8sError(err)
	}
	return &resEnv, nil
}

func (c *ConnectClient) DeleteEnvironment(ctx context.Context, namespace, name string) error {
	req := connect.NewRequest(&pb.DeleteEnvironmentRequest{
		Namespace: namespace,
		Name:      name,
	})
	_, err := c.envClient.DeleteEnvironment(ctx, req)
	return connectToK8sError(err)
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
	stream, err := c.streamEnvClient.StreamLogs(ctx, req)
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
		return nil, connectToK8sError(err)
	}

	var pgs []divergev1alpha1.PreviewGroup
	for _, pbPg := range res.Msg.PreviewGroups {
		b, err := json.Marshal(pbPg)
		if err != nil {
			return nil, connectToK8sError(err)
		}
		var pg divergev1alpha1.PreviewGroup
		if err := json.Unmarshal(b, &pg); err != nil {
			return nil, connectToK8sError(err)
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
