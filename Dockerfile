# Stage 1: Build the React SPA
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --prefer-offline
COPY web/ ./
RUN npm run build

# Stage 2: Build the Go binary
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kube-nat ./cmd/kube-nat

# Stage 3: Minimal runtime image
FROM alpine:3.21 AS runtime
RUN apk add --no-cache iptables conntrack-tools
COPY --from=builder /kube-nat /usr/local/bin/kube-nat
ENTRYPOINT ["/usr/local/bin/kube-nat"]
CMD ["agent"]
