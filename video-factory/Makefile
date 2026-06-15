.PHONY: run build test migrate fmt

PG_BIN ?= /opt/homebrew/opt/postgresql@15/bin
PSQL   ?= $(PG_BIN)/psql -h 127.0.0.1 -U vf -d video_factory

run:
	go run ./cmd/user-svc

build:
	go build -o bin/user-svc ./cmd/user-svc

test:
	go test ./... -v

migrate:
	@for f in migrations/*.sql; do \
	  echo "==> applying $$f"; \
	  $(PSQL) -f $$f; \
	done

fmt:
	gofmt -w ./...
