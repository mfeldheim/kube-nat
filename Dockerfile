FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kube-nat ./cmd/kube-nat

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    iptables \
    conntrack \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /kube-nat /usr/local/bin/kube-nat
ENTRYPOINT ["/usr/local/bin/kube-nat"]
CMD ["agent"]
