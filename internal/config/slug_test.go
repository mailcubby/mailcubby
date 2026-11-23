package config

import "testing"

func TestSlugify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		raw      string
		fallback string
		want     string
	}{
		{name: "simple", raw: "Shop Staging", fallback: "default", want: "shop-staging"},
		{name: "diacritics", raw: "Loja São João & Ç", fallback: "default", want: "loja-sao-joao-c"},
		{name: "ligatures", raw: "Cœur Æon Straße", fallback: "default", want: "coeur-aeon-strasse"},
		{name: "collapse dashes", raw: "foo---bar___baz", fallback: "default", want: "foo-bar-baz"},
		{name: "trim dashes", raw: "  ---hello---  ", fallback: "default", want: "hello"},
		{name: "empty uses fallback", raw: "!!!", fallback: "default", want: "default"},
		{name: "reserved gets -1", raw: "API", fallback: "default", want: "api-1"},
		{name: "reserved admin", raw: "admin", fallback: "default", want: "admin-1"},
		{name: "truncate 63", raw: "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789", fallback: "default", want: "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0"},
		{name: "punycode prefix collapsed", raw: "xn--example", fallback: "default", want: "xn-example"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Slugify(tc.raw, tc.fallback)
			if got != tc.want {
				t.Fatalf("Slugify(%q, %q) = %q, want %q", tc.raw, tc.fallback, got, tc.want)
			}
			if err := ValidateSlug(got); err != nil {
				t.Fatalf("Slugify(%q) produced invalid slug %q: %v", tc.raw, got, err)
			}
		})
	}
}
