package lockfile

import (
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var validRefNames = []string{
	"v4",
	"v4.1.0",
	"main",
	"release/v1",
	"feature/foo-bar_baz",
	"predicate@1.1.4",
	"release@2024",
	"a@b",
	"HEAD",
	"v6.1.0-1-g373d742f768e", // describe-shaped; policy belongs to consumers
	"pull/1/head",
	"11bd71901bbe5b1630ceea73d27597364c9af68",   // 39 hex
	"11bd71901bbe5b1630ceea73d27597364c9af6833", // 41 hex
	"Main",
	"ünïcode",
	"x.lockx",
	"a.b",
}

var invalidRefNames = []struct {
	in   string
	want error
}{
	{"", ErrRefEmpty},
	{"11bd71901bbe5b1630ceea73d27597364c9af683", ErrRefFullSHA},
	{"11BD71901BBE5B1630CEEA73D27597364C9AF683", ErrRefFullSHA},
	{strings.Repeat("a", 64), ErrRefFullSHA},
	{"refs/tags/v4", ErrRefQualified},
	{"refs/heads/main", ErrRefQualified},
	{"main~1", ErrRefInvalidName},
	{"main^", ErrRefInvalidName},
	{"main^{commit}", ErrRefInvalidName},
	{"main@{1}", ErrRefInvalidName},
	{"@", ErrRefInvalidName},
	{"a..b", ErrRefInvalidName},
	{"main:file", ErrRefInvalidName},
	{"v4 x", ErrRefInvalidName},
	{" v4", ErrRefInvalidName},
	{"v4\t", ErrRefInvalidName},
	{"v4\n", ErrRefInvalidName},
	{"v4\x00", ErrRefInvalidName},
	{"v4\x7f", ErrRefInvalidName},
	{"a?b", ErrRefInvalidName},
	{"a*b", ErrRefInvalidName},
	{"a[b", ErrRefInvalidName},
	{`a\b`, ErrRefInvalidName},
	{"-v4", ErrRefInvalidName},
	{"--upload-pack=x", ErrRefInvalidName},
	{".hidden", ErrRefInvalidName},
	{"a/.hidden", ErrRefInvalidName},
	{"a.lock", ErrRefInvalidName},
	{"a.lock/b", ErrRefInvalidName},
	{"a/", ErrRefInvalidName},
	{"/a", ErrRefInvalidName},
	{"a//b", ErrRefInvalidName},
	{"a.", ErrRefInvalidName},
	{"\xff", ErrRefInvalidName},
}

func TestParseRefName(t *testing.T) {
	for _, s := range validRefNames {
		t.Run("valid/"+s, func(t *testing.T) {
			n, err := ParseRefName(s)
			require.NoError(t, err)
			assert.Equal(t, s, n.String())
			assert.Equal(t, "refs/tags/"+s, n.TagRef())
			assert.Equal(t, "refs/heads/"+s, n.BranchRef())
		})
	}
	for _, tc := range invalidRefNames {
		t.Run("invalid/"+tc.in, func(t *testing.T) {
			n, err := ParseRefName(tc.in)
			require.Error(t, err)
			assert.True(t, n.IsZero())
			assert.ErrorIs(t, err, tc.want)
			var re *RefError
			require.True(t, errors.As(err, &re))
			assert.Equal(t, tc.in, re.Input)
		})
	}
}

func TestParsePinRef(t *testing.T) {
	r, err := ParsePinRef("v4")
	require.NoError(t, err)
	assert.Equal(t, RefKindName, r.Kind())
	n, ok := r.Name()
	require.True(t, ok)
	assert.Equal(t, "v4", n.String())
	_, ok = r.Commit()
	assert.False(t, ok)

	r, err = ParsePinRef("11BD71901BBE5B1630CEEA73D27597364C9AF683")
	require.NoError(t, err)
	assert.Equal(t, RefKindCommit, r.Kind())
	c, ok := r.Commit()
	require.True(t, ok)
	assert.Equal(t, "11bd71901bbe5b1630ceea73d27597364c9af683", c.String())
	assert.Equal(t, "sha1", c.Algo())
	assert.Equal(t, "sha1-11bd71901bbe5b1630ceea73d27597364c9af683", c.Digest())
	_, ok = r.Name()
	assert.False(t, ok)

	r, err = ParsePinRef(strings.Repeat("A", 64))
	require.NoError(t, err)
	c, _ = r.Commit()
	assert.Equal(t, "sha256", c.Algo())

	for _, bad := range []string{"", "main~1", "refs/tags/v4", "v6@{0}"} {
		_, err := ParsePinRef(bad)
		assert.Errorf(t, err, "%q", bad)
	}
	assert.True(t, PinRef{}.IsZero())
	assert.Equal(t, "", PinRef{}.String())
	assert.Equal(t, RefKindInvalid, PinRef{}.Kind())
}

func TestValidateEntryRef(t *testing.T) {
	nameKey := mustPinRef(t, "v4")
	shaKey := mustPinRef(t, "11bd71901bbe5b1630ceea73d27597364c9af683")

	n, err := ValidateEntryRef(nameKey, "v4")
	require.NoError(t, err)
	assert.Equal(t, "v4", n.String())

	_, err = ValidateEntryRef(nameKey, "v4.1.0")
	assert.ErrorIs(t, err, ErrRefMismatch)
	_, err = ValidateEntryRef(nameKey, "V4")
	assert.ErrorIs(t, err, ErrRefMismatch, "names are case-sensitive")

	n, err = ValidateEntryRef(shaKey, "v4.1.0")
	require.NoError(t, err)
	assert.Equal(t, "v4.1.0", n.String())

	_, err = ValidateEntryRef(shaKey, "11bd71901bbe5b1630ceea73d27597364c9af683")
	assert.ErrorIs(t, err, ErrRefFullSHA)
	_, err = ValidateEntryRef(shaKey, "refs/tags/v4")
	assert.ErrorIs(t, err, ErrRefQualified)
	_, err = ValidateEntryRef(shaKey, "")
	assert.ErrorIs(t, err, ErrRefEmpty)
	_, err = ValidateEntryRef(shaKey, "v6.1.0~1")
	assert.ErrorIs(t, err, ErrRefInvalidName)
}

func lockWithEntry(key, ref string) string {
	return "version: v0.0.3\n" +
		"workflows:\n  .github/workflows/ci.yml:\n    - " + key + "\n" +
		"dependencies:\n  " + key + ":\n" +
		"    ref: " + ref + "\n" +
		"    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n" +
		"    owner_id: 1\n    repo_id: 1\n"
}

func TestParse_EntryRefRules(t *testing.T) {
	const sha = "11bd71901bbe5b1630ceea73d27597364c9af683"
	tests := []struct {
		name string
		key  string
		ref  string
		want error
	}{
		{"name key match", "actions/checkout@v4", "v4", nil},
		{"sha key with tag", "actions/checkout@" + sha, "v4.2.2", nil},
		{"sha key with branch containing slash", "actions/checkout@" + sha, "release/v4", nil},
		{"sha key with sha ref", "actions/checkout@" + sha, sha, ErrRefFullSHA},
		{"sha key with refs prefix", "actions/checkout@" + sha, "refs/tags/v4", ErrRefQualified},
		{"sha key with revision syntax", "actions/checkout@" + sha, "'v4~1'", ErrRefInvalidName},
		{"sha key with reflog syntax", "actions/checkout@" + sha, "'main@{1}'", ErrRefInvalidName},
		{"name key mismatch", "actions/checkout@v4", "v5", ErrRefMismatch},
		{"empty ref", "actions/checkout@v4", `""`, nil}, // reported by required/non-empty checks
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse([]byte(lockWithEntry(tt.key, tt.ref)))
			if tt.name == "empty ref" {
				require.Error(t, err)
				return
			}
			if tt.want != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.want)
				var pe *ParseError
				require.True(t, errors.As(err, &pe))
				assert.Equal(t, 7, pe.Line)
				return
			}
			require.NoError(t, err)
			pin, ok := ParsePin(tt.key)
			require.True(t, ok)
			a := f.Dependencies[pin.String()]
			assert.False(t, a.Ref.IsZero())
			assert.Equal(t, strings.Trim(tt.ref, "'"), a.Ref.String())
		})
	}
}

func TestParse_InvalidKeyRefRejected(t *testing.T) {
	for _, key := range []string{"actions/checkout@main~1", "actions/checkout@refs/tags/v4", "'actions/checkout@main@{1}'"} {
		_, err := Parse([]byte(lockWithEntry(key, "v4")))
		assert.Errorf(t, err, "key %s", key)
	}
}

func TestParse_OutOfScopeInvalidEntryRefIsZero(t *testing.T) {
	const sha = "11bd71901bbe5b1630ceea73d27597364c9af683"
	y := "version: v0.0.3\n" +
		"workflows:\n  .github/workflows/a.yml:\n    - actions/checkout@v4\n" +
		"  .github/workflows/b.yml:\n    - actions/cache@" + sha + "\n" +
		"dependencies:\n" +
		"  actions/checkout@v4:\n    ref: v4\n    commit: sha1-" + sha + "\n    owner_id: 1\n    repo_id: 1\n" +
		"  actions/cache@" + sha + ":\n    ref: " + sha + "\n    commit: sha1-" + sha + "\n    owner_id: 2\n    repo_id: 2\n"

	_, err := Parse([]byte(y))
	require.ErrorIs(t, err, ErrRefFullSHA)

	f, err := Parse([]byte(y), ".github/workflows/a.yml")
	require.NoError(t, err)
	assert.Equal(t, "v4", f.Dependencies["actions/checkout@v4"].Ref.String())
	assert.True(t, f.Dependencies["actions/cache@"+sha].Ref.IsZero())
}

func TestParse_V001SHAOnlyKeyNeedsRelock(t *testing.T) {
	const sha = "11bd71901bbe5b1630ceea73d27597364c9af683"
	y := "version: v0.0.1\n" +
		"workflows:\n  .github/workflows/ci.yml:\n    - actions/checkout@" + sha + ":sha1-" + sha + "\n" +
		"dependencies:\n" +
		"  actions/checkout@" + sha + ":sha1-" + sha + ":\n    commit: sha1-" + sha + "\n    owner_id: 1\n    repo_id: 1\n"
	_, err := Parse([]byte(y))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRefNeedsRelock)

	withTag := strings.Replace(y, "    commit:", "    tag: v4\n    commit:", 1)
	f, err := Parse([]byte(withTag))
	require.NoError(t, err)
	assert.Equal(t, "v4", f.Dependencies["actions/checkout@"+sha].Ref.String())

	badTag := strings.Replace(y, "    commit:", "    tag: v4~1\n    commit:", 1)
	_, err = Parse([]byte(badTag))
	assert.ErrorIs(t, err, ErrRefNeedsRelock)
}

func TestRefName_YAMLRoundTrip(t *testing.T) {
	a := Action{Ref: mustRefName(t, "release/v1"), Commit: "sha1-x", OwnerID: 1, RepoID: 1}
	out, err := yaml.Marshal(a)
	require.NoError(t, err)
	assert.Contains(t, string(out), "ref: release/v1\n")

	var back Action
	require.NoError(t, yaml.Unmarshal(out, &back))
	assert.Equal(t, a.Ref, back.Ref)

	out, err = yaml.Marshal(Action{Commit: "sha1-x", OwnerID: 1, RepoID: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "ref:")
}

func TestParseActionRefStrict(t *testing.T) {
	a, err := ParseActionRefStrict("actions/checkout@v4")
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, RefKindName, a.Ref.Kind())

	for _, in := range []string{
		"actions/checkout@main~1",
		"actions/checkout@main@{1}",
		"actions/checkout@refs/heads/main",
		"actions/checkout@v4:x",
		"actions/checkout@",
		"actions/cache/save@-x",
	} {
		a, err := ParseActionRefStrict(in)
		assert.Nilf(t, a, "%q", in)
		var re *RefError
		assert.Truef(t, errors.As(err, &re), "%q: %v", in, err)
		assert.Nilf(t, ParseActionRef(in), "%q", in)
	}

	for _, in := range []string{"./local", "docker://alpine", "actions/checkout", "a/b@${{ x }}", "o/r/.github/workflows/ci.yml@main~1"} {
		a, err := ParseActionRefStrict(in)
		assert.Nilf(t, a, "%q", in)
		assert.NoErrorf(t, err, "%q", in)
	}
}

func TestParseReusableWorkflowRefStrict(t *testing.T) {
	r, err := ParseReusableWorkflowRefStrict("o/r/.github/workflows/ci.yml@v1")
	require.NoError(t, err)
	require.NotNil(t, r)

	r, err = ParseReusableWorkflowRefStrict("o/r/.github/workflows/ci.yml@main~1")
	assert.Nil(t, r)
	assert.ErrorIs(t, err, ErrRefInvalidName)

	r, err = ParseReusableWorkflowRefStrict("o/r@main~1")
	assert.Nil(t, r)
	assert.NoError(t, err)
}

// TestRefName_SchemaParity checks the v0.0.3 schema's ref constraints agree
// with ParseRefName on the test corpus.
func TestRefName_SchemaParity(t *testing.T) {
	var doc struct {
		Defs struct {
			Action struct {
				Properties struct {
					Ref struct {
						Pattern string `json:"pattern"`
						Not     struct {
							AnyOf []struct {
								Pattern string `json:"pattern"`
							} `json:"anyOf"`
						} `json:"not"`
					} `json:"ref"`
				} `json:"properties"`
			} `json:"action"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(Schema()), &doc))
	ref := doc.Defs.Action.Properties.Ref
	must := regexp.MustCompile(ref.Pattern)
	var nots []*regexp.Regexp
	for _, p := range ref.Not.AnyOf {
		nots = append(nots, regexp.MustCompile(p.Pattern))
	}
	schemaOK := func(s string) bool {
		if s == "" || !must.MatchString(s) {
			return false
		}
		for _, re := range nots {
			if re.MatchString(s) {
				return false
			}
		}
		return true
	}
	corpus := append([]string{}, validRefNames...)
	for _, tc := range invalidRefNames {
		if utf8.ValidString(tc.in) {
			corpus = append(corpus, tc.in)
		}
	}
	for _, s := range corpus {
		_, err := ParseRefName(s)
		assert.Equalf(t, err == nil, schemaOK(s), "schema/parser disagree on %q", s)
	}
}

// TestRefName_GitCheckRefFormat checks that everything ParseRefName accepts,
// git also accepts, and that the corpus git rejects is rejected.
func TestRefName_GitCheckRefFormat(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	gitOK := func(s string) bool {
		return exec.Command(git, "check-ref-format", "--allow-onelevel", s).Run() == nil
	}
	for _, s := range validRefNames {
		assert.Truef(t, gitOK(s), "git rejects %q but ParseRefName accepts it", s)
	}
	for _, tc := range invalidRefNames {
		if tc.in == "" || strings.ContainsRune(tc.in, 0) {
			continue
		}
		if gitOK(tc.in) {
			// Deliberately stricter than git: SHA-shaped, refs/ prefix, leading
			// '-' or '/', invalid UTF-8.
			_, perr := ParseRefName(tc.in)
			assert.Truef(t, errors.Is(perr, ErrRefFullSHA) || errors.Is(perr, ErrRefQualified) ||
				strings.HasPrefix(tc.in, "-") || strings.HasPrefix(tc.in, "/") || !utf8.ValidString(tc.in),
				"git accepts %q; ParseRefName rejects it without a documented reason", tc.in)
		}
	}
}

func FuzzParsePinRef(f *testing.F) {
	for _, s := range validRefNames {
		f.Add(s)
	}
	for _, tc := range invalidRefNames {
		f.Add(tc.in)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParsePinRef(s)
		if err != nil {
			require.True(t, r.IsZero())
			var re *RefError
			require.True(t, errors.As(err, &re))
			return
		}
		out := r.String()
		again, err := ParsePinRef(out)
		require.NoError(t, err)
		require.Equal(t, r, again)
		if n, ok := r.Name(); ok {
			require.Equal(t, s, n.String())
			require.False(t, strings.ContainsAny(out, " ~^:?*[\\\x00\x7f"))
			require.NotContains(t, out, "..")
			require.NotContains(t, out, "@{")
			require.False(t, strings.HasPrefix(out, "refs/") || strings.HasPrefix(out, "-"))
			require.False(t, IsFullSha(out))
		}
	})
}

func FuzzParsePin(f *testing.F) {
	f.Add("actions/checkout@v4")
	f.Add("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683")
	f.Add("a/b@predicate@1.1.4")
	f.Add("a/b@main~1")
	f.Fuzz(func(t *testing.T, s string) {
		p, ok := ParsePin(s)
		if !ok {
			return
		}
		require.False(t, p.Ref.IsZero())
		again, ok := ParsePin(p.String())
		require.True(t, ok)
		require.Equal(t, p, again)
	})
}

func FuzzParseActionRefStrict(f *testing.F) {
	f.Add("actions/checkout@v4")
	f.Add("actions/cache/save@v4")
	f.Add("o/r/.github/workflows/ci.yml@main")
	f.Add("a/b@main~1")
	f.Fuzz(func(t *testing.T, s string) {
		a, err := ParseActionRefStrict(s)
		if err != nil {
			require.Nil(t, a)
			return
		}
		if a != nil {
			require.False(t, a.Ref.IsZero())
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add(lockWithEntry("actions/checkout@v4", "v4"))
	f.Add(lockWithEntry("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683", "v4"))
	f.Add(lockWithEntry("actions/checkout@v4", "'main~1'"))
	f.Fuzz(func(t *testing.T, s string) {
		lf, err := Parse([]byte(s))
		if err != nil {
			return
		}
		for key, a := range lf.Dependencies {
			pin, ok := ParsePin(key)
			require.True(t, ok, key)
			require.False(t, a.Ref.IsZero(), key)
			_, err := ValidateEntryRef(pin.Ref, a.Ref.String())
			require.NoError(t, err, key)
		}
	})
}

func mustRefName(t testing.TB, s string) RefName {
	t.Helper()
	n, err := ParseRefName(s)
	require.NoError(t, err)
	return n
}
