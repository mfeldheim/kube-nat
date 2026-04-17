.PHONY: build test lint test-nat

build:
	go build -o bin/kube-nat ./cmd/kube-nat

test:
	go test ./... -v -race

lint:
	go vet ./...

test-nat:
	docker build -t kube-nat-test --target test .
	docker run --rm --cap-add NET_ADMIN kube-nat-test
