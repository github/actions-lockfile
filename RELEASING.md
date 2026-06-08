# Releasing `actions-lockfile`

`github.com/github/actions-lockfile` is the multi-language home for the
GitHub Actions lockfile format. The Go implementation lives in the
`github.com/github/actions-lockfile/go` module under `go/`, kept as a
standalone module so consumers can import it on its own.

## Tag conventions

The Go sub-module uses path-prefixed semver tags, per Go's multi-module
repository rules:

```
go/v0.1.0
go/v0.1.1
go/v1.0.0
```

If the CLI and parser need to ship together, tag `github/gh-actions-pin`
first, then tag this repository from the corresponding migrated commit.

## Format invariants

Shared lockfile invariants live outside language implementations:

- `schema/lockfile-v0.0.1.json` is the published schema.
- `go/pkg/lockfile/schema_gen.go` is generated from the root schema for Go
  consumers. Run `make generate` after schema changes; Go tests enforce that
  the generated value still matches the root schema.

## Local development

Run the module tests directly:

```sh
make generate
make test
```
