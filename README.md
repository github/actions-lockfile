# gh-actions-lockfile

> [!WARNING]
> This is experimental.

Pin GitHub Actions workflow dependencies to exact commit SHAs and verify them on every run. Like `go.sum` for Actions.

## Install

```bash
gh extension install github/actions-lockfile
```

Or build from source:

```bash
go install github.com/github/actions-lockfile/cmd/gh-actions-lockfile@latest
```

## Usage

### Pin

Resolve all `uses:` action references in a workflow to their current commit SHAs:

```bash
gh actions-lockfile pin .github/workflows/ci.yml
```

This appends a `dependencies:` section to the workflow file:

```yaml
name: ci
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - run: go test ./...

dependencies:
  - github.com/actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
  - github.com/actions/setup-go@v5:sha1-d35c59abb061a4a6fb18e82ac0862c26744d6ab5
```

For composite actions, it reads `action.yml` via the GraphQL API to discover and resolve transitive dependencies recursively.

If a pinned SHA changes on re-pin (tag was force-pushed), `pin` stops and reports which dependencies changed.

Flags:

- `--dry-run` -- resolve and print without writing
- `--diff` -- show what changed vs existing deps

### Validate

Re-resolve all refs against the live API and compare against pinned SHAs:

```bash
gh actions-lockfile validate .github/workflows/ci.yml
```

Detects:

- **TAMPERED** -- SHA doesn't match live resolution (tag was force-pushed, namespace taken over)
- **MISSING** -- `uses:` ref in workflow has no `dependencies:` entry
- **STALE** -- `dependencies:` entry not discoverable from workflow `uses:` refs

## How it works

1. Parse workflow YAML, extract `uses:` repository action references
2. Batch-resolve refs to commit SHAs via GitHub GraphQL API (batches of 20)
3. For composite actions, read `action.yml` via `Commit.file()` to discover nested `uses:` refs
4. Recurse up to depth 10 (matching the runner's `CompositeActionsMaxDepth`)
5. Write the resolved SHAs as a `dependencies:` section in the workflow file

## What's covered

- Repository actions (`owner/repo@ref`)
- Path actions (`owner/repo/path@ref`)
- Composite action recursion
- SHA-pinned ref validation
- Tamper detection
- Force-push detection on re-pin
- SHA-256 forward compatibility

## Not covered

- Docker actions (`docker://image:tag`)
- Local path actions (`./path`)

## Auth

Uses `gh` CLI credentials. Set `GH_TOKEN` or run `gh auth login`.

## Development

```bash
make build       # build the binary
make test        # run all tests
make test-unit   # fast, no network
make lint        # go vet
make corpus      # dry-run pin against real-world workflows
```

### Project structure

```
cmd/gh-actions-lockfile/     CLI entry point + cobra commands
pkg/lockfile/                Workflow parsing, dependency model, YAML read/write
pkg/resolver/                GraphQL batch resolution + composite recursion
pkg/actionmeta/              action.yml parsing (execution type, nested uses)
pkg/pin/                     Pin command logic
pkg/validate/                Validate command logic
dev/                         Internal dev tools (fake Launch server, runner harness)
testdata/                    Test workflow fixtures
```

### Runner enforcement (dev/)

The `dev/` directory contains tools for testing runner-side lockfile enforcement:

- `dev/fakelaunch/` -- Fake Launch server speaking the runner's HTTP protocol
- `dev/harness-dotnet/` -- C# harness that feeds job messages to the real runner binary
- `dev/jobmsg/` -- Job message builder

These are not published as part of the extension.
