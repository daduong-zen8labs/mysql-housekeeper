package main

import "testing"

func TestVersionString(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v0.1.0"
	if versionString() != "v0.1.0" {
		t.Fatal(versionString())
	}
	version = "dev"
	if versionString() == "" {
		t.Fatal("empty version")
	}
}

func TestRunHelpAndUnknown(t *testing.T) {
	if code := run([]string{"help"}); code != exitOK {
		t.Fatalf("help=%d", code)
	}
	if code := run([]string{"version"}); code != exitOK {
		t.Fatalf("version=%d", code)
	}
	if code := run(nil); code != exitConfig {
		t.Fatalf("empty=%d", code)
	}
	if code := run([]string{"nope"}); code != exitConfig {
		t.Fatalf("unknown=%d", code)
	}
	if code := run([]string{"plan"}); code != exitConfig {
		t.Fatalf("plan without -c=%d", code)
	}
}
