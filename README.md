# actions-lockfile

The authoritative definition of the GitHub Actions dependency lockfile
format, plus a Go parser for it. The lockfile records the resolved transitive
dependency graph for a repository's workflows so tools can audit and verify the
exact action pins in use.

## Installation

```sh
go get github.com/github/actions-lockfile/go/pkg/lockfile
```

The Go module lives under [`go/`](./go/) so the repository can grow
additional language bindings around the same lockfile schema.

## Usage

### Parse a lockfile and look up a workflow's pins

```go
package main

import (
	"fmt"
	"os"

	lockfile "github.com/github/actions-lockfile/go/pkg/lockfile"
)

func main() {
	contents, err := os.ReadFile(lockfile.Path) // ".github/workflows/actions.lock"
	if err != nil {
		panic(err)
	}

	file, err := lockfile.Parse(contents)
	if err != nil {
		panic(err)
	}

	pins, ok := file.LookupWorkflow(".github/workflows/release.yml")
	if !ok {
		fmt.Println("workflow not present in lockfile")
		return
	}
	for _, key := range pins {
		fmt.Println(key) // e.g. actions/checkout@v6.0.2:sha1-de0fac2e...
	}
}
```

### Surface structured parse errors

`Parse` returns a `*lockfile.ParseError` carrying line and column for semantic
failures, so callers can anchor diagnostics on the lockfile itself instead of
scraping yaml.v3's error string.

```go
file, err := lockfile.Parse(contents)
if err != nil {
	var perr *lockfile.ParseError
	if errors.As(err, &perr) {
		fmt.Printf("%s:%d:%d: %s\n", lockfile.Path, perr.Line, perr.Column, perr.Msg)
		return
	}
	panic(err)
}
_ = file
```

## Schema

The lockfile is a YAML document whose shape is defined by a JSON Schema 2020-12
document embedded in the package and reachable via `lockfile.Schema()`.

The current schema version is `v0.0.1`
([`schema/lockfile-v0.0.1.json`](https://github.com/github/actions-lockfile/blob/main/schema/lockfile-v0.0.1.json)).
The on-disk file lives at
[`Path`](https://github.com/github/actions-lockfile/blob/main/go/pkg/lockfile/lockfile.go)
(`.github/workflows/actions.lock`) and has three top-level keys:

```yaml
version: v0.0.1
workflows:
  # workflow path → flat, transitive list of canonical pin keys
  .github/workflows/release.yml:
    - actions/checkout@v6.0.2:sha1-de0fac2e...
dependencies:
  # canonical pin key → resolved action metadata
  actions/checkout@v6.0.2:sha1-de0fac2e...:
    tag: v6.0.2
    branch: main
    commit: sha1-de0fac2e...
    owner_id: 44036562
    repo_id: 197814629
```

A canonical pin key is `OWNER/REPO@REF:ALGO-HEX`. The same key appears in both
`workflows` (as flat transitive lists) and `dependencies` (as deduplicated
graph entries with `uses:` links to direct dependencies).

## Compatibility and stability

- The Go module follows [semver](https://semver.org/). The publicly documented
  exported surface is intended to be stable across minor versions.
- The lockfile schema is versioned independently. The current schema version
  is `v0.0.1`, embedded in the package and emitted as the `version` field of
  every lockfile.
- Pre-1.0, the package reserves the right to remove any incidentally-exported
  helper not covered by the [Usage](#usage) and
  [What this package does](#what-this-package-does) sections. Those sections
  define the intended stable surface.
- Schema changes follow the rules in `RELEASING.md`: backward-compatible
  additions can ship in a minor schema version; breaking changes require a
  new schema `$id` and bumped `version` const.

## Related projects

- [`github/gh-actions-pin`](https://github.com/github/gh-actions-pin) —
  produces and maintains lockfiles.
- [`actions/languageservices/workflow-parser`](https://github.com/actions/languageservices/tree/main/workflow-parser) —
  parses workflow YAML. Sibling library: `workflow-parser` reads the `.yml`
  source, `actions-lockfile` reads the resolved `.lock` artifact derived
  from it.

This package is format infrastructure. It does not resolve actions, update
pins, or assess vulnerability risk. Tools that do those things consume this
package to read the lockfile.

## Development

```sh
make test
make lint
```

Schema changes (any modification to `schema/lockfile-vX.Y.Z.json`, generated
language bindings, or the fields emitted in `dependencies` / `workflows`)
require coordination with `github/gh-actions-pin` because the CLI is the
schema's primary producer.

## License

MIT — see [`LICENSE`](./LICENSE).
