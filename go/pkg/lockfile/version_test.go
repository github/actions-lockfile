package lockfile

import "testing"

func TestParseSemver_RejectsOverflow(t *testing.T) {
	overflow := []string{
		"99999999999999999999.0.0",
		"v1.99999999999999999999.0",
		"v1.0.99999999999999999999",
	}
	for _, tag := range overflow {
		if _, ok := ParseSemVer(tag); ok {
			t.Errorf("ParseSemVer(%q) = ok; want rejected", tag)
		}
	}
}

func TestVersion_Greater(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool // a.Greater(b)
	}{
		{"higher patch", "v1.2.4", "v1.2.3", true},
		{"lower patch", "v1.2.3", "v1.2.4", false},
		{"higher major beats lower v-prefix mismatch", "2.0.0", "v1.0.0", true},
		{"bare equals v on version, v wins", "1.2.3", "v1.2.3", false},
		{"v beats bare on tie", "v1.2.3", "1.2.3", true},
		{"stable beats prerelease", "v1.2.3", "v1.2.3-rc.1", true},
		{"prerelease loses to stable", "v1.2.3-rc.1", "v1.2.3", false},
		{"partial v1 vs full v1.0.0 tie, lexical raw", "v1", "v1.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, ok := ParseSemVer(tt.a)
			if !ok {
				t.Fatalf("ParseSemVer(%q) failed", tt.a)
			}
			b, ok := ParseSemVer(tt.b)
			if !ok {
				t.Fatalf("ParseSemVer(%q) failed", tt.b)
			}
			if got := a.Greater(b); got != tt.want {
				t.Errorf("%q.Greater(%q) = %v; want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSemVer_IsMutable(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"v4", true},
		{"v4.2", true},
		{"v1", true},
		{"v4.2.1", false},
		{"v1.0.0", false},
		// SHAs aren't parsed as SemVer at all.
		{"main", false},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			sv, ok := ParseSemVer(tc.ref)
			if !ok {
				if tc.want {
					t.Fatalf("ParseSemVer(%q) failed, expected mutable", tc.ref)
				}
				return
			}
			if got := sv.IsMutable(); got != tc.want {
				t.Errorf("SemVer(%q).IsMutable() = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestSemVer_IsMajorOnly(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"v4", true},
		{"v12", true},
		{"v4.2", false},
		{"v4.2.1", false},
	}
	for _, tc := range cases {
		sv, _ := ParseSemVer(tc.ref)
		if got := sv.IsMajorOnly(); got != tc.want {
			t.Errorf("SemVer(%q).IsMajorOnly() = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestSemVer_Narrows(t *testing.T) {
	cases := []struct {
		mutable, narrowed string
		want              bool
	}{
		{"v4", "v4.1.0", true},
		{"v4.2", "v4.2.1", true},
		{"v4", "v5.0.0", false},
		{"v4.2", "v4.3.0", false},
	}
	for _, tc := range cases {
		mv, _ := ParseSemVer(tc.mutable)
		nv, _ := ParseSemVer(tc.narrowed)
		if got := nv.Narrows(mv); got != tc.want {
			t.Errorf("SemVer(%q).Narrows(%q) = %v, want %v", tc.narrowed, tc.mutable, got, tc.want)
		}
	}
}

func TestSemVer_UpgradeOver(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v4.1.0", "v4.0.0", true},
		{"v5.0.0", "v4.2.1", true},
		{"v4.0.0", "v4.0.0", false}, // same
		{"v4", "v4.0.0", false},     // mutable latest — noop
		{"v3", "v4.0.0", false},     // downgrade
	}
	for _, tc := range cases {
		lat, _ := ParseSemVer(tc.latest)
		cur, _ := ParseSemVer(tc.current)
		if got := lat.UpgradeOver(cur); got != tc.want {
			t.Errorf("SemVer(%q).UpgradeOver(%q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
