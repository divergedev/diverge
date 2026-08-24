# Proto generation stage for web TypeScript types
FROM bufbuild/buf:latest AS proto-gen
WORKDIR /workspace
COPY buf.yaml buf.gen.web.yaml ./
COPY api/ api/
RUN buf generate --template buf.gen.web.yaml

# Web dashboard build stage
FROM node:22-alpine AS web-builder
WORKDIR /web
COPY web/package*.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
COPY --from=proto-gen /workspace/web/src/api/gen ./src/api/gen
RUN npm run build

# Go build stage
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

# Copy compiled web dashboard assets for go:embed
COPY --from=web-builder /web/dist internal/server/dashboard/dist/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -a -o /out/diverge-controller cmd/controller/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -a -o /out/diverge-proxy cmd/proxy/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -a -o /out/diverge-server ./cmd/server

# Runtime stage
# Using gcr.io/distroless/static:nonroot
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/diverge-controller /diverge-controller
COPY --from=builder /out/diverge-proxy /diverge-proxy
COPY --from=builder /out/diverge-server /diverge-server
USER 65532:65532
ENTRYPOINT ["/diverge-controller"]
