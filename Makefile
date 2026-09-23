.PHONY: build test vet fmt check

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Run before committing: formatting, static checks, and tests.
check: vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt: files need formatting"; gofmt -l .; exit 1)
	@echo "check: ok"
