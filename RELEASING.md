# Releasing `actions-lockfile`

`github.com/github/actions-lockfile` is the multi-language home for the
GitHub Actions lockfile format. The Go implementation lives in the
`github.com/github/actions-lockfile/go` module under `go/`, kept as a
standalone module so consumers can import it on its own.

## Cutting a release

From a clean, current `main`, preview the next release:

```sh
RELEASE_DRY_RUN=1 script/release patch
```

Then cut it:

```sh
script/release patch
```

To preview a release candidate instead:

```sh
RELEASE_DRY_RUN=1 script/release patch --rc
```

Then cut it:

```sh
script/release patch --rc
```

The first candidate is `go/vX.Y.Z-rc.1`; repeating the same bump increments
`rc.N`. Run the bump without `--rc` to publish the stable `go/vX.Y.Z`.

Use `patch` for compatible fixes, `minor` for compatible additions, and
`major` for breaking changes.

The script validates the repository, checks the current branch and
`origin/main`, then pushes and verifies an annotated `go/vX.Y.Z[-rc.N]` tag.

The tag workflow verifies the tag, then creates the GitHub Release and warms
the Go module proxy.

A pushed release tag is immutable. If publishing fails, fix the publisher and
run the release script again for the next RC; do not move or reuse the failed tag.

## Tag conventions

The Go sub-module uses path-prefixed semver tags, per Go's multi-module
repository rules:

```
go/v0.1.0
go/v0.1.1-rc.1
go/v0.1.1
go/v1.0.0
```

A consumer resolves `go/vX.Y.Z` as module version `vX.Y.Z`:

```sh
go get github.com/github/actions-lockfile/go@v0.1.0
```

Major versions `>= 2` need a matching `/vN` suffix on the module path; the
release script refuses to mint such a tag until `go.mod` carries the suffix.

## Format invariants

Shared lockfile invariants live outside language implementations:

- `schema/lockfile-vX.Y.Z.json` files are the published schemas.
- `go/pkg/lockfile/schema_gen.go` is generated from the root schema for Go
  consumers. Run `make generate` after schema changes; Go tests enforce that
  the generated value still matches the root schema.

## Local development

Run the module tests directly:

```sh
make generate
make test
```
