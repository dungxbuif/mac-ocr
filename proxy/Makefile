.PHONY: build build-admin run admin local-dev mock-native test vet fmt tidy up down logs clean s3 s3-stop s3-test

build:
	go build -buildvcs=false ./...

build-admin:
	go build -buildvcs=false -o bin/admin ./cmd/admin

run:
	go run ./cmd/proxy

admin:
	go run ./cmd/admin $(ARGS)

mock-native:
	cd ../local-dev && npm run native

local-dev:
	cd ../local-dev && npm start

test:
	go test ./...

test-integration:
	TEST_S3=1 go test ./internal/repository/s3/tests/ -run TestRoundTrip -v

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

up:
	@echo "infra is managed externally (see ~/dev-env + make s3)"

down:
	@echo "infra is managed externally (see ~/dev-env + make s3)"

logs:
	@echo "infra is managed externally (see ~/dev-env + make s3)"

clean:
	@echo "infra is managed externally (see ~/dev-env + make s3)"

S3_DIR := ../local-dev/s3

s3:
	cd $(S3_DIR) && npm install --silent && npm start

s3-stop:
	-pkill -f "local-dev/s3/serve.js" || true

s3-test:
	$(MAKE) up
	$(MAKE) s3-stop
	cd $(S3_DIR) && (NODE_OPTIONS=--openssl-legacy-provider node serve.js > /tmp/s3rver.log 2>&1 &) && \
	  sleep 2 && cd ../../proxy && $(MAKE) test-integration
