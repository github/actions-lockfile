# Releasing `actions-lockfile`

`github.com/github/actions-lockfile` is the multi-language home for the
GitHub Actions lockfile format. The Go implementation lives in the
`github.com/github/actions-lockfile/go` module under `go/`, kept as a
standalone module so consumers can import it on its own.

## Cutting a release

Releases are cut by CI, not from a maintainer's laptop. Open the **Actions**
tab, run the **Release** workflow from `main`, and choose a bump:

- **patch** — backward-compatible fixes
- **minor** — backward-compatible additions
- **major** — breaking changes (see the version-suffix caveat below)

CI runs `script/release`, which regenerates and verifies the tree, runs the
full build, computes the next version from the latest `go/vX.Y.Z` tag, pushes
the tag, cuts a GitHub Release with generated notes, and warms the Go module
proxy. The first release has no prior tag, so it bases off `v0.0.0` — pick
**minor** to land on `v0.1.0`.

`script/release` is the single source of truth and runs locally too. Preview
without touching anything:

```sh
RELEASE_DRY_RUN=1 script/release minor
```

## Tag conventions

The Go sub-module uses path-prefixed semver tags, per Go's multi-module
repository rules:

```
go/v0.1.0
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
