.PHONY: generate test lint

generate:
	cd go/pkg/lockfile && go generate ./...

test:
	cd go && go test ./... -count=1
	script/release_test

lint:
	cd go && go vet ./...
