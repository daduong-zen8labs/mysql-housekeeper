package mysqlutil

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
	}{
		{"8.0.36", 8, 0},
		{"8.4.0-log", 8, 4},
		{"8.0.36-0ubuntu0.22.04.1", 8, 0},
		{"5.7.42", 5, 7},
	}
	for _, tc := range cases {
		maj, min, err := parseVersion(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if maj != tc.major || min != tc.minor {
			t.Fatalf("%q: got %d.%d want %d.%d", tc.in, maj, min, tc.major, tc.minor)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	if QuoteIdent("foo") != "`foo`" {
		t.Fatal(QuoteIdent("foo"))
	}
	if QuoteIdent("a`b") != "`a``b`" {
		t.Fatal(QuoteIdent("a`b"))
	}
}
