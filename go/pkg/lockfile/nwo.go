package lockfile

import "strings"

// SplitNWO splits an owner/repo (Name-With-Owner) string into its two
// components. It returns ok=false when either segment is missing: the empty
// string, no slash, a leading slash, or a trailing slash.
//
// For inputs with extra segments ("owner/repo/sub/..."), only the first two
// are returned; the rest is dropped, matching the lockfile's repo-granularity
// pin grammar.
//
// SplitNWO does not validate the owner/repo character set — use ParseActionRef
// for a verbatim `uses:` value where stricter charset rules apply.
func SplitNWO(nwo string) (owner, repo string, ok bool) {
	slashIdx := strings.IndexByte(nwo, '/')
	if slashIdx <= 0 || slashIdx == len(nwo)-1 {
		return "", "", false
	}
	owner = nwo[:slashIdx]
	rest := nwo[slashIdx+1:]
	if i := strings.IndexByte(rest, '/'); i > 0 {
		repo = rest[:i]
	} else {
		repo = rest
	}
	return owner, repo, true
}
