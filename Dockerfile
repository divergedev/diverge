# Build stage
# Using golang:1.26
FROM golang:1.26 AS builder
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY gen/ gen/
COPY internal/ internal/
COPY pkg/ pkg/
COPY config/ config/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -a -o /out/diverge-controller cmd/controller/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -a -o /out/diverge-proxy cmd/proxy/main.go

# Runtime stage
# Using gcr.io/distroless/static:nonroot
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/diverge-controller /diverge-controller
COPY --from=builder /out/diverge-proxy /diverge-proxy
USER 65532:65532
ENTRYPOINT ["/diverge-controller"]
