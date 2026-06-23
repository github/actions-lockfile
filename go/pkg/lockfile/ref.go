package lockfile

// SplitRef classifies a lockfile ref value back into its tag/branch
// constituents. If ref parses as a stable semver (via ParseSemVer +
// IsStable), it's returned as tag; otherwise it's returned as branch.
// An empty ref yields both empty.
func SplitRef(ref string) (tag, branch string) {
	if ref == "" {
		return "", ""
	}
	if sv, ok := ParseSemVer(ref); ok && sv.IsStable() {
		return ref, ""
	}
	return "", ref
}

// BestRef picks the single ref value for lockfile serialization.
// Tag wins if non-empty, else branch. Both empty yields empty.
func BestRef(tag, branch string) string {
	if tag != "" {
		return tag
	}
	return branch
}
