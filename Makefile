.PHONY: build test race run docker-up docker-down load-test-smoke load-test-medium load-test-large fmt vet

build:
	go build ./...

test:
	go test ./...

# The race detector needs cgo + a real C toolchain. Run it in Docker if your
# host doesn't have one set up (e.g. Windows without mingw-w64):
#   docker run --rm -v "$$(pwd):/src" -w /src golang:1.24 go test -race ./...
race:
	go test -race -count=3 ./...

fmt:
	gofmt -l .

vet:
	go vet ./...

run:
	go run ./cmd/server

docker-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --build

docker-down:
	docker compose -f deploy/docker/docker-compose.yml down

# Requires the stack to be up (make docker-up) and a k6 install, or use the
# grafana/k6 Docker image directly — see test/loadtest/README.md.
load-test-smoke:
	BASE_URL=http://localhost:8080 k6 run test/loadtest/k6/ws_load_test.js

load-test-medium:
	BASE_URL=http://localhost:8080 PROFILE=medium k6 run --env PROFILE=medium test/loadtest/k6/ws_load_test.js

load-test-large:
	BASE_URL=http://localhost:8080 PROFILE=large k6 run --env PROFILE=large test/loadtest/k6/ws_load_test.js
