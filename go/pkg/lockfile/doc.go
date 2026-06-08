// Package lockfile is the source of truth for the workflow dependency
// lockfile format and pin grammar.
//
// # Parsing as a security boundary
//
// ParseActionRef is the choke point. Untrusted uses: strings enter; only
// concrete repository actions leave. Everything else — expressions, docker://
// images, local paths, reusable workflows, control characters — returns nil,
// before it can reach a URL or GraphQL builder.
//
// owner/repo/path pass isValidSegment: a fixed character set, ".."/"." barred.
// Drop-in safe. The ref is looser by necessity — git refs carry slashes, dots,
// even another @ — so isValidRef only guarantees it cannot escape a quoted
// literal or smuggle a traversal. A ref still needs escaping before it touches
// a URL path. Owner/repo do not.
//
// Hand-rolled, no regexp, allocation-free, single-pass: it runs per dependency
// on the hot path and the reject-lists must stay auditable at a glance. They
// are load-bearing. Do not refactor them into regular expressions.
package lockfile
