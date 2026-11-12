package ingest

import "testing"

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \t\n ", ""},
		{"already clean", "Foo Bar", "foo bar"},
		{"double spaces", "Foo  Bar", "foo bar"},
		{"tabs and newlines", "Foo\tBar\nBaz", "foo bar baz"},
		{"leading trailing", "  Foo Bar  ", "foo bar"},
		{"unicode case", "CAFÉ", "café"},
		{"mixed case", "Central Park", "central park"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeName(tc.in); got != tc.want {
				t.Errorf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeNameEquality(t *testing.T) {
	pairs := [][2]string{
		{"Foo Bar", "foo  bar"},
		{"  Central   Park ", "central park"},
		{"Hồ Tây", "hồ tây"},
	}
	for _, p := range pairs {
		if normalizeName(p[0]) != normalizeName(p[1]) {
			t.Errorf("expected %q == %q after normalization", p[0], p[1])
		}
	}
}
