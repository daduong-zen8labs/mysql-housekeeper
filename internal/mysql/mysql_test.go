package mysqlutil

import "testing"

func TestParseMajorVersion(t *testing.T) {
	cases := []struct {
		in    string
		major int
	}{
		{"8.0.36", 8},
		{"8.4.0-log", 8},
		{"8.0.36-0ubuntu0.22.04.1", 8},
		{"5.7.42", 5},
	}
	for _, tc := range cases {
		maj, err := parseMajorVersion(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if maj != tc.major {
			t.Fatalf("%q: got %d want %d", tc.in, maj, tc.major)
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
