# Releasing `actions-lockfile`

`github.com/github/actions-lockfile` is the multi-language home for the
GitHub Actions lockfile format. The Go implementation lives in the
`github.com/github/actions-lockfile/go` module under `go/`, kept as a
standalone module so consumers can import it on its own. The Ruby
implementation is the independently versioned `actions-lockfile` gem under
`ruby/`.

## Cutting a Go release

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

## Cutting a Ruby release

Ruby releases are independent of Go and use `ruby/vX.Y.Z` tags. Update
`GitHub::Actions::Lockfile::VERSION` in
`ruby/lib/actions/lockfile/version.rb`, merge it to `main`, then tag and push:

```sh
git tag ruby/v0.1.0
git push origin ruby/v0.1.0
```

`.github/workflows/release-ruby.yml` verifies that the tag matches the gem
version, tests and builds from `ruby/`, and publishes with RubyGems Trusted
Publishing. It uses the GitHub `release` environment and OIDC; do not add a
long-lived RubyGems token.

Before the first publish, repository maintainers must arrange the approved
RubyGems.org ownership and trusted-publisher setup for `actions-lockfile`. The
trusted publisher must use:

| Field | Value |
| --- | --- |
| Gem name | `actions-lockfile` |
| Repository owner | `github` |
| Repository name | `actions-lockfile` |
| Workflow filename | `release-ruby.yml` |
| Environment | `release` |

Do not push the first Ruby release tag until that setup is in place.

## Format invariants

Shared lockfile invariants live outside language implementations:

- `schema/lockfile-v0.0.1.json` and `schema/lockfile-v0.0.2.json` are the
  published schemas.
- `go/pkg/lockfile/schema_gen.go` is generated from the root schemas for Go
  consumers. Run `make generate` after schema changes; tests enforce that the
  language implementations still match the root contract.

## Local development

Run all package tests directly:

```sh
make generate
make test
```
