// Package lockfile is the source of truth for the GitHub Actions dependency
// lockfile format and its Go parser.
//
// # Getting started
//
// [Parse] is the primary entry point. Hand it the raw bytes of
// .github/workflows/actions.lock and it returns a [File] whose Workflows
// and Dependencies maps let you look up every pinned action for a
// workflow:
//
//	f, err := lockfile.Parse(contents)
//	pins, ok := f.LookupWorkflow(".github/workflows/release.yml")
//
// [File.LookupWorkflow] returns canonical pin key strings. Each key can be
// looked up in File.Dependencies to retrieve the associated [Action]
// metadata (commit hash, branch, tag, repository IDs).
//
// # Parsing uses: strings
//
// [ParseActionRef] parses a single `uses:` string from a workflow step into
// its owner/repo/ref components. It returns nil for anything that is not a
// repository action — expressions, docker:// images, local paths, reusable
// workflow files — so callers never need to classify the input themselves.
//
// [ParseReusableWorkflowRef] is its mirror for the reusable-workflow shape
// (owner/repo/.github/workflows/name.yml@ref) that ParseActionRef deliberately
// rejects. Use [IsLocalReusableWorkflow] for the local ./.github/workflows/...
// shape, which has no owner/repo and is handled differently.
//
// Both parsers split the ref at the FIRST @, not the last — a ref may
// legitimately contain @ (e.g. a branch named "release@2024").
//
// # Security note for contributors
//
// owner/repo/path components pass isValidSegment (fixed character set,
// ".."/"." barred) before reaching any URL or GraphQL builder. Ref validation
// is minimal (non-empty, no colons) — the workflow parser itself does no ref
// character validation, so neither do we. These validators are hand-rolled,
// allocation-free, and single-pass because they run on the hot path. Do not
// replace them with regular expressions.
package lockfile
