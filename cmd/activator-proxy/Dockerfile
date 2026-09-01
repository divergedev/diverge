# golang:1.26
FROM golang@sha256:e30143be198ab04cf7ba25fba83ab3a692ca584c994aad0bf131fa0eb32dd8c1 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /activator-proxy ./cmd/activator-proxy/

# gcr.io/distroless/static:nonroot
FROM gcr.io/distroless/static@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7
COPY --from=builder /activator-proxy /activator-proxy
USER 65532:65532
ENTRYPOINT ["/activator-proxy"]
