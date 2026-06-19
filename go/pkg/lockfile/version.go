package lockfile

import (
	"fmt"
	"regexp"
	"strconv"
)

// SemVer holds parsed semantic version components.
//
// API stability: the type and its comparison helpers (Greater, Narrows,
// UpgradeOver, MajorTag, MinorTag, IsFull) are part of the exported surface
// because the tag recommendation engine in downstream consumers relies on
// them. Their semantics are deliberately non-strict-semver (see below) and are
// committed to as-is; they are not internal helpers despite the
// recommendation-engine flavor.
//
// GitHub Actions has no first-class version scheme — a uses: ref can be
// any git ref (tag, branch, SHA, or even "main"). In practice most
// action authors follow semver-ish conventions, but the ecosystem
// diverges from strict semver in ways that golang.org/x/mod/semver
// cannot handle: bare versions without a "v" prefix ("2.0.0"), partial
// versions ("v4", "v4.2"), and arbitrary suffixes all appear in the
// wild. x/mod/semver rejects bare and partial tags, and doesn't expose
// individual components — we need Major/Minor/Patch to compute
// MajorTag, MinorTag, and IsFull for the tag recommendation engine.
// SemVer holds parsed semantic version components.
//
// API stability: the type and its comparison helpers (Greater, Narrows,
// UpgradeOver, MajorTag, MinorTag, IsFull) are part of the exported surface
// because the tag recommendation engine in downstream consumers relies on
// them. Their semantics are deliberately non-strict-semver (see below) and are
// committed to as-is; they are not internal helpers despite the
// recommendation-engine flavor.
//
// GitHub Actions has no first-class version scheme — a uses: ref can be
// any git ref (tag, branch, SHA, or even "main"). In practice most
// action authors follow semver-ish conventions, but the ecosystem
// diverges from strict semver in ways that golang.org/x/mod/semver
// cannot handle: bare versions without a "v" prefix ("2.0.0"), partial
// versions ("v4", "v4.2"), and arbitrary suffixes all appear in the
// wild. x/mod/semver rejects bare and partial tags, and doesn't expose
// individual components — we need Major/Minor/Patch to compute
// MajorTag, MinorTag, and IsFull for the tag recommendation engine.
type SemVer struct {
	Prefix string // "v" or "" — whether the original tag had a "v" prefix
	Major  int
	Minor  int
	Patch  int
	// Rest is everything after the patch number, e.g. "-beta.1" for
	// "v1.2.3-beta.1". An empty Rest means the version is stable (no
	// pre-release suffix). See [SemVer.IsStable].
	Rest string
	Raw  string // original tag string as written (e.g. "v4" or "2.0.0-rc.1")
}

var versionRE = regexp.MustCompile(`^(v?)(\d+)(?:\.(\d+))?(?:\.(\d+))?(.*)$`)

// ParseSemVer parses a version tag into its components. Returns false if the
// tag doesn't look like a version (or is a full SHA that happens to start with
// a digit).
func ParseSemVer(tag string) (SemVer, bool) {
	if IsFullSha(tag) {
		return SemVer{}, false
	}
	m := versionRE.FindStringSubmatch(tag)
	if m == nil {
		return SemVer{}, false
	}
	major, err := strconv.Atoi(m[2])
	if err != nil {
		return SemVer{}, false
	}
	minor := 0
	if m[3] != "" {
		if minor, err = strconv.Atoi(m[3]); err != nil {
			return SemVer{}, false
		}
	}
	patch := 0
	if m[4] != "" {
		if patch, err = strconv.Atoi(m[4]); err != nil {
			return SemVer{}, false
		}
	}
	return SemVer{
		Prefix: m[1],
		Major:  major,
		Minor:  minor,
		Patch:  patch,
		Rest:   m[5],
		Raw:    tag,
	}, true
}

// MajorTag returns the major-only tag string (e.g. "v4").
func (s SemVer) MajorTag() string { return fmt.Sprintf("%s%d", s.Prefix, s.Major) }

// MinorTag returns the major.minor tag string (e.g. "v4.2").
func (s SemVer) MinorTag() string { return fmt.Sprintf("%s%d.%d", s.Prefix, s.Major, s.Minor) }

// Greater reports whether s should be preferred over o: higher
// major.minor.patch wins; on a tie a stable version beats a pre-release, a
// v-prefixed tag beats the same bare version, then a lexicographic compare of
// the raw tags provides a deterministic final tie-break.
func (s SemVer) Greater(o SemVer) bool {
	sv := [3]int{s.Major, s.Minor, s.Patch}
	ov := [3]int{o.Major, o.Minor, o.Patch}
	for i := 0; i < 3; i++ {
		if sv[i] != ov[i] {
			return sv[i] > ov[i]
		}
	}
	if s.IsStable() != o.IsStable() {
		return s.IsStable()
	}
	if (s.Prefix == "v") != (o.Prefix == "v") {
		return s.Prefix == "v"
	}
	return s.Raw > o.Raw
}

// IsStable returns true if the version has no pre-release suffix (Rest == "").
// "v1.2.3" is stable; "v1.2.3-beta.1" is not.
func (s SemVer) IsStable() bool { return s.Rest == "" }

// IsFull returns true if the version has all three components
// (major.minor.patch) and no pre-release suffix. Tags like "v4" or "v4.2"
// return false. IsFull is the prerequisite for a locked dependency: only a
// full version uniquely identifies a release.
func (s SemVer) IsFull() bool {
	return s.Rest == "" && s.Raw != s.MajorTag() && s.Raw != s.MinorTag()
}

// IsMutable reports whether the version is a partial (major-only or
// major.minor) tag — the opposite of [SemVer.IsFull]. A partial tag like
// "v4" or "v4.2" is "mutable" because the author can silently move it to
// a new patch commit without changing the tag name, which makes it unsafe
// to trust without a SHA pin. Use [SemVer.Narrows] to find a full patch
// version that narrows a mutable tag.
func (s SemVer) IsMutable() bool { return !s.IsFull() }

// IsMajorOnly reports whether the raw tag is a bare major version (e.g. "v4").
func (s SemVer) IsMajorOnly() bool { return s.Raw == s.MajorTag() }

// Narrows reports whether s is a more specific patch version of other.
// e.g. other="v4", s="v4.1.0" → true; other="v4.2", s="v4.2.1" → true.
func (s SemVer) Narrows(other SemVer) bool {
	if !s.IsFull() || s.Major != other.Major {
		return false
	}
	if !other.IsMajorOnly() && other.Minor != s.Minor {
		return false
	}
	return true
}

// UpgradeOver reports whether s represents a real version upgrade over other.
// Returns false for noops where other is already at or more specific than s.
func (s SemVer) UpgradeOver(other SemVer) bool {
	if s.Rest != "" {
		return false
	}
	if s.Major < other.Major {
		return false
	}
	if s.Major == other.Major {
		if s.IsMajorOnly() {
			return false
		}
		if s.Minor < other.Minor {
			return false
		}
		if s.Minor == other.Minor && s.Patch <= other.Patch {
			return false
		}
	}
	return true
}
