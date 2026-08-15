FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /activator-proxy ./cmd/activator-proxy/

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /activator-proxy /activator-proxy
USER 65532:65532
ENTRYPOINT ["/activator-proxy"]
