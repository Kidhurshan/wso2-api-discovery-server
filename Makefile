.PHONY: build test test-unit test-integration lint clean docker run-local migrate-up

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)"

build:
	mkdir -p bin
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/ads ./cmd/ads

test: test-unit test-integration

test-unit:
	go test -v -short -race ./internal/...

test-integration:
	go test -v -tags=integration -timeout 5m ./test/integration/...

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

clean:
	rm -rf bin/

docker:
	docker build -t ghcr.io/Kidhurshan/wso2-api-discovery-server:$(VERSION) -f deploy/docker/Dockerfile .

run-local:
	./bin/ads --config config/config.toml.example

migrate-up:
	psql $$ADS_DB_URL -f schema/001_init.sql
	psql $$ADS_DB_URL -f schema/002_phase1_normalization_version.sql
	psql $$ADS_DB_URL -f schema/003_managed_apis.sql
	psql $$ADS_DB_URL -f schema/004_classifications.sql
	psql $$ADS_DB_URL -f schema/005_view.sql
