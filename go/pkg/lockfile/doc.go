// Package lockfile is the source of truth for the workflow dependency
// lockfile format and pin grammar. It was originally developed as
// actions-workflow-parser/go/lockfile and extracted here so the format
// definition can be open-sourced alongside the CLI that owns it.
//
// # Parsing as a security boundary
//
// ParseActionRef is the choke point for untrusted uses: strings. It
// returns nil for any input that is not a concrete repository action,
// including expression refs (${{ }}), docker:// images, local ./ paths,
// reusable workflow files, and any input containing control characters.
// That rejection happens before these values reach downstream URL and
// GraphQL builders. isValidOwnerOrRepo enforces the GitHub owner/repo
// character set for the same reason.
//
// The validation is hand-rolled (no regexp), allocation-free, and
// single-pass: it runs per dependency on the parse hot path, and the
// reject-lists stay auditable at a glance. The control-character and
// charset rejection are load-bearing for injection safety, so preserve
// the reject-list semantics exactly rather than refactoring them into
// regular expressions.
package lockfile
