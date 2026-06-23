package lockfile

import "testing"

var benchV002 = []byte(`version: v0.0.2
workflows:
  .github/workflows/ci.yml:
    - actions/checkout@v4
    - actions/setup-go@v5
    - actions/cache@v4
dependencies:
  actions/checkout@v4:
    ref: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 44036562
    repo_id: 197814629
  actions/setup-go@v5:
    ref: v5
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 44036562
    repo_id: 249058325
  actions/cache@v4:
    ref: v4
    commit: sha1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    owner_id: 44036562
    repo_id: 251882839
    uses:
      - actions/checkout@v4
`)

var benchV001 = []byte(`version: v0.0.1
workflows:
  .github/workflows/ci.yml:
    - actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    - actions/setup-go@v5:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    - actions/cache@v4:sha1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
dependencies:
  actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683:
    tag: v4
    commit: sha1-11bd71901bbe5b1630ceea73d27597364c9af683
    owner_id: 44036562
    repo_id: 197814629
  actions/setup-go@v5:sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
    tag: v5
    commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    owner_id: 44036562
    repo_id: 249058325
  actions/cache@v4:sha1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:
    tag: v4
    commit: sha1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    owner_id: 44036562
    repo_id: 251882839
    uses:
      - actions/checkout@v4:sha1-11bd71901bbe5b1630ceea73d27597364c9af683
`)

func BenchmarkParse_V002(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Parse(benchV002); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse_V001_Compat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Parse(benchV001); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseWithPolicy_V002(b *testing.B) {
	policy := VersionPolicy{Min: "v0.0.1", Max: "v0.0.2"}
	for i := 0; i < b.N; i++ {
		if _, err := ParseWithPolicy(benchV002, policy); err != nil {
			b.Fatal(err)
		}
	}
}
