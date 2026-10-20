package lockfile

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Sentinel reasons wrapped by [RefError]. Match them with errors.Is.
var (
	// ErrRefEmpty reports an empty ref.
	ErrRefEmpty = errors.New("ref is empty")
	// ErrRefFullSHA reports a full commit SHA where a tag or branch name is
	// required.
	ErrRefFullSHA = errors.New("ref must be a tag or branch name, not a commit SHA")
	// ErrRefQualified reports a fully qualified "refs/..." name. Lockfile refs
	// are short tag or branch names.
	ErrRefQualified = errors.New(`ref must be a short tag or branch name without a "refs/" prefix`)
	// ErrRefInvalidName reports a name that is not a valid git ref name.
	ErrRefInvalidName = errors.New("ref is not a valid git ref name")
	// ErrRefMismatch reports an entry ref that differs from its pin key ref.
	ErrRefMismatch = errors.New("entry ref does not match pin key ref")
	// ErrRefNeedsRelock reports a legacy lockfile entry whose ref cannot be
	// expressed as a tag or branch name. Re-resolve the dependency.
	ErrRefNeedsRelock = errors.New("legacy entry has no tag or branch ref; re-lock the dependency")
)

// RefError is returned when ref text fails validation. Err is one of the
// ErrRef* sentinels.
type RefError struct {
	Input string
	Err   error
}

func (e *RefError) Error() string {
	return fmt.Sprintf("invalid ref %q: %v", e.Input, e.Err)
}

func (e *RefError) Unwrap() error { return e.Err }

// RefKind distinguishes the two forms the text after "@" can take.
type RefKind int

const (
	// RefKindInvalid is the zero PinRef; no parser returns it.
	RefKindInvalid RefKind = iota
	// RefKindName is a symbolic tag or branch name ([RefName]).
	RefKindName
	// RefKindCommit is a full commit SHA ([CommitID]).
	RefKindCommit
)

func (k RefKind) String() string {
	switch k {
	case RefKindName:
		return "name"
	case RefKindCommit:
		return "commit"
	default:
		return "invalid"
	}
}

// RefName is a validated short tag or branch name. The library cannot tell
// tags from branches; resolve it by exact lookup of [RefName.TagRef] and
// [RefName.BranchRef]. Never pass [RefName.String] to anything that evaluates
// revision expressions (rev-parse, commit-ish API parameters).
//
// The zero value is not a valid name; see [RefName.IsZero].
type RefName struct {
	s string
}

// ParseRefName validates s as a short tag or branch name. It accepts what
// `git check-ref-format --allow-onelevel` accepts, except: a leading "-",
// a "refs/" prefix, and a 40- or 64-character hex string are rejected, and
// the input must be valid UTF-8. Names are case-sensitive and not folded.
func ParseRefName(s string) (RefName, error) {
	if err := checkRefName(s); err != nil {
		return RefName{}, &RefError{Input: s, Err: err}
	}
	return RefName{s: s}, nil
}

// String returns the name as written, for serialization and display only.
func (n RefName) String() string { return n.s }

// IsZero reports whether n is the zero value (absent or not validated).
func (n RefName) IsZero() bool { return n.s == "" }

// TagRef returns the fully qualified tag ref, "refs/tags/<name>".
func (n RefName) TagRef() string { return "refs/tags/" + n.s }

// BranchRef returns the fully qualified branch ref, "refs/heads/<name>".
func (n RefName) BranchRef() string { return "refs/heads/" + n.s }

// MarshalYAML emits the name as a plain string.
func (n RefName) MarshalYAML() (interface{}, error) { return n.s, nil }

// UnmarshalYAML decodes a validated name. Text that fails [ParseRefName]
// decodes to the zero RefName without error; [Parse] reports invalid refs with
// positions for in-scope entries, and out-of-scope entries stay zero.
func (n *RefName) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := ParseRefName(s)
	if err != nil {
		*n = RefName{}
		return nil
	}
	*n = parsed
	return nil
}

// CommitID is a full commit hash: 40 hex digits (SHA-1) or 64 (SHA-256),
// canonicalized to lowercase.
type CommitID struct {
	hex string
}

// ParseCommitID validates s as a full commit SHA (case-insensitive).
func ParseCommitID(s string) (CommitID, error) {
	if !IsFullSha(s) {
		return CommitID{}, &RefError{Input: s, Err: ErrRefInvalidName}
	}
	return CommitID{hex: strings.ToLower(s)}, nil
}

// String returns the lowercase hex digest.
func (c CommitID) String() string { return c.hex }

// Algo returns "sha1" or "sha256".
func (c CommitID) Algo() string {
	if len(c.hex) == 64 {
		return "sha256"
	}
	return "sha1"
}

// Digest returns the algo-prefixed form used by [Action.Commit], e.g.
// "sha1-<hex>".
func (c CommitID) Digest() string { return c.Algo() + "-" + c.hex }

// IsZero reports whether c is the zero value.
func (c CommitID) IsZero() bool { return c.hex == "" }

// PinRef is the parsed text after "@" in a pin key or `uses:` value: either a
// full commit SHA or a validated tag/branch name. Construct it only with
// [ParsePinRef], [NamePinRef], or [CommitPinRef].
type PinRef struct {
	kind   RefKind
	name   RefName
	commit CommitID
}

// ParsePinRef classifies s: a 40- or 64-character hex string is a commit;
// anything else must satisfy [ParseRefName].
func ParsePinRef(s string) (PinRef, error) {
	if IsFullSha(s) {
		c, _ := ParseCommitID(s)
		return CommitPinRef(c), nil
	}
	n, err := ParseRefName(s)
	if err != nil {
		return PinRef{}, err
	}
	return NamePinRef(n), nil
}

// NamePinRef wraps a validated name.
func NamePinRef(n RefName) PinRef {
	if n.IsZero() {
		return PinRef{}
	}
	return PinRef{kind: RefKindName, name: n}
}

// CommitPinRef wraps a validated commit.
func CommitPinRef(c CommitID) PinRef {
	if c.IsZero() {
		return PinRef{}
	}
	return PinRef{kind: RefKindCommit, commit: c}
}

// Kind reports which form r holds.
func (r PinRef) Kind() RefKind { return r.kind }

// IsZero reports whether r is the zero value.
func (r PinRef) IsZero() bool { return r.kind == RefKindInvalid }

// Name returns the tag/branch name when Kind is RefKindName.
func (r PinRef) Name() (RefName, bool) { return r.name, r.kind == RefKindName }

// Commit returns the commit when Kind is RefKindCommit.
func (r PinRef) Commit() (CommitID, bool) { return r.commit, r.kind == RefKindCommit }

// String returns the canonical text (commit hex is lowercase), for
// serialization and display only.
func (r PinRef) String() string {
	switch r.kind {
	case RefKindName:
		return r.name.String()
	case RefKindCommit:
		return r.commit.String()
	default:
		return ""
	}
}

// ValidateEntryRef checks a dependency entry's recorded ref against its pin
// key ref. The entry ref must be a valid [RefName]; when the key ref is a
// name, the two must be identical. When the key ref is a commit, the entry ref
// records the tag or branch the commit was found on and may be any valid name.
func ValidateEntryRef(key PinRef, entry string) (RefName, error) {
	n, err := ParseRefName(entry)
	if err != nil {
		return RefName{}, err
	}
	if kn, ok := key.Name(); ok && kn != n {
		return RefName{}, &RefError{Input: entry, Err: fmt.Errorf("%w %q", ErrRefMismatch, kn.String())}
	}
	return n, nil
}

func checkRefName(s string) error {
	if s == "" {
		return ErrRefEmpty
	}
	if IsFullSha(s) {
		return ErrRefFullSHA
	}
	if strings.HasPrefix(s, "refs/") {
		return ErrRefQualified
	}
	if !utf8.ValidString(s) {
		return ErrRefInvalidName
	}
	if s == "@" || s[0] == '-' || s[0] == '/' || strings.HasSuffix(s, "/") || strings.HasSuffix(s, ".") {
		return ErrRefInvalidName
	}
	if strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.Contains(s, "//") {
		return ErrRefInvalidName
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return ErrRefInvalidName
		}
		switch c {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return ErrRefInvalidName
		}
	}
	for _, comp := range strings.Split(s, "/") {
		if strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return ErrRefInvalidName
		}
	}
	return nil
}
