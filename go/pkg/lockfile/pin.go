package lockfile

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"

	"strings"
)

// Pin holds the parsed components of a dependency pin key.
//
//	"OWNER/REPO@REF"
//
// The pin identifies a downloaded action tarball at repo+ref granularity —
// matching the runner, which downloads `owner/repo@ref` once per ref and
// reuses the tree for any sub-action path. Sub-action paths (e.g. the
// `save` in `actions/cache/save@v4`) are graph traversal details, not pin
// identity, and do not appear in this serialized form.
type Pin struct {
	NWO   string // "actions/checkout"
	Owner string // "actions"
	Repo  string // "checkout"
	Ref   string // "v4"
}

// Canonical returns a copy of p with all case-insensitive components
// (owner, repo) normalized to lowercase. Ref preserves source casing —
// git refs are case-sensitive.
//
// This is the single normalization point for the lockfile pin grammar:
// String, IndexKey, and ParsePin all funnel through it, so callers never
// need their own ToLower bookkeeping when handing pins through this package.
func (p Pin) Canonical() Pin {
	p.Owner = strings.ToLower(p.Owner)
	p.Repo = strings.ToLower(p.Repo)
	p.NWO = p.Owner + "/" + p.Repo
	return p
}

// String returns the canonical pin form: "OWNER/REPO@REF".
// This doubles as the actions-map key in the lockfile.
func (p Pin) String() string {
	c := p.Canonical()
	return c.NWO + "@" + c.Ref
}

// IndexKey returns the normalized lookup key for this pin: "OWNER/REPO@REF".
// Identical to String() — retained for API compatibility.
func (p Pin) IndexKey() string {
	return p.String()
}

// IndexKey builds the normalized lookup key for a dependency entry:
// "OWNER/REPO@REF".
func IndexKey(owner, repo, ref string) string {
	return Pin{Owner: owner, Repo: repo, Ref: ref}.IndexKey()
}

// ParsePin parses a pin string of the canonical form:
//
//	"OWNER/REPO@REF"
//
// Returns ok=false for any input that does not match — including all of:
//   - Missing "@" separator between repo and ref
//   - A sub-action path in the repo portion (e.g. "owner/repo/sub@ref")
//     — the lockfile grammar is strictly repo-scoped, matching the runner's
//     tarball download identity
//
// On success, owner and repo are normalized to lowercase. Ref preserves
// source casing — git refs are case-sensitive. The returned Pin is always
// in canonical form.
func ParsePin(s string) (Pin, bool) {
	atIdx := strings.IndexByte(s, '@')
	if atIdx <= 0 || atIdx == len(s)-1 {
		return Pin{}, false
	}
	repoPath := s[:atIdx]
	ref := s[atIdx+1:]

	// Sub-action paths are not part of the lockfile pin grammar: the runner
	// downloads at repo+ref granularity. Reject any extra slashes in the
	// repo portion so hand-edited lockfiles don't drift into a path-bearing
	// format.
	if strings.Count(repoPath, "/") != 1 {
		return Pin{}, false
	}
	owner, repo, ok := SplitNWO(repoPath)
	if !ok {
		return Pin{}, false
	}

	// Validate the ref with the same denylist used by ParseActionRef to
	// reject shell metacharacters, whitespace, and traversal sequences.
	// Reject empty refs and colons (colon is the legacy pin key separator).
	if !isValidRef(ref) {
		return Pin{}, false
	}

	return Pin{
		Owner: owner,
		Repo:  repo,
		Ref:   ref,
	}.Canonical(), true
}

// IsFullSha reports whether s looks like a full commit hash (SHA-1 or
// SHA-256). Callers use this to distinguish bare-SHA `uses:` refs from
// symbolic refs.
func IsFullSha(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ShortSHA returns the first 12 characters of a SHA, or the full string
// if shorter. Used for human-readable log and diagnostic output.
func ShortSHA(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func digestLength(algo string) (int, bool) {
	switch algo {
	case "sha1":
		return sha1.Size * 2, true
	case "sha256":
		return sha256.Size * 2, true
	default:
		return 0, false
	}
}

func isValidDigest(algo, digest string) bool {
	expectedDigestLength, ok := digestLength(algo)
	if !ok || len(digest) != expectedDigestLength {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
