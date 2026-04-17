.PHONY: build build-web test lint test-nat deploy

build-web:
	cd web && npm ci && npm run build

build: build-web
	go build -o bin/kube-nat ./cmd/kube-nat

test:
	go test ./... -race

lint:
	go vet ./...

test-nat:
	docker build -t kube-nat-test --target test .
	docker run --rm --cap-add NET_ADMIN kube-nat-test

deploy:
	helm upgrade --install kube-nat deploy/helm/ --create-namespace
