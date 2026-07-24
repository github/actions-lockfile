.PHONY: generate test test-go test-ruby lint lint-go lint-ruby

generate:
	cd go/pkg/lockfile && go generate ./...

test: test-go test-ruby

test-go:
	cd go && go test ./... -count=1

test-ruby:
	cd ruby && bundle exec rake test

lint: lint-go lint-ruby

lint-go:
	cd go && go vet ./...

lint-ruby:
	cd ruby && bundle exec rake syntax
