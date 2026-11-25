# Diverge Go Protobuf & ConnectRPC Module

`github.com/divergedev/diverge/api/gen` is a standalone Go module containing generated Protobuf types and ConnectRPC client/server stubs for the Diverge APIs (`diverge.v1alpha1`).

Splitting the generated contracts into an independent Go submodule keeps external consumers free of Kubernetes (`k8s.io/*`), `controller-runtime`, Knative, database, and CLI dependencies from the root `github.com/divergedev/diverge` module.

## Installation

```bash
go get github.com/divergedev/diverge/api/gen@v0.30.0
```

## Example Usage (Binary Protobuf over ConnectRPC)

`divergev1alpha1connect` clients speak binary Protobuf (`application/proto`) over the Connect protocol by default:

```go
package main

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

func resolve(ctx context.Context, baseURL string) (*divergev1alpha1.ResolvePreviewResponse, error) {
	client := divergev1alpha1connect.NewPreviewResolverServiceClient(http.DefaultClient, baseURL)
	resp, err := client.ResolvePreview(ctx, connect.NewRequest(&divergev1alpha1.ResolvePreviewRequest{
		Namespace: "default",
		PreviewId: "pr-42",
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
```
