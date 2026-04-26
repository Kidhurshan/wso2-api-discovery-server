package config

import "testing"

func TestExpand(t *testing.T) {
	t.Setenv("ADS_TEST_FOO", "foo-value")
	t.Setenv("ADS_TEST_EMPTY", "")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text passes through", "no vars here", "no vars here"},
		{"set var expands", "${ADS_TEST_FOO}", "foo-value"},
		{"empty value expands to empty", "${ADS_TEST_EMPTY}", ""},
		{"missing var with no default expands to empty", "${ADS_TEST_MISSING}", ""},
		{"missing var uses default", "${ADS_TEST_MISSING:-fallback}", "fallback"},
		{"set var ignores default", "${ADS_TEST_FOO:-fallback}", "foo-value"},
		{"empty var still expands to empty (does not use default)", "${ADS_TEST_EMPTY:-fallback}", ""},
		{"multiple vars in one string", "x=${ADS_TEST_FOO} y=${ADS_TEST_MISSING:-zz}", "x=foo-value y=zz"},
		{"adjacent literals", "pre${ADS_TEST_FOO}post", "prefoo-valuepost"},
		{"unmatched dollar passes through", "$NOTAVAR ${ADS_TEST_FOO}", "$NOTAVAR foo-value"},
		{"name starting with digit is not expanded", "${1ABC}", "${1ABC}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Expand(tc.in)
			if got != tc.want {
				t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
