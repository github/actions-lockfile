.PHONY: generate test lint

generate:
	cd go/pkg/lockfile && go generate ./...

test:
	cd go && go test ./... -count=1

lint:
	cd go && go vet ./...
