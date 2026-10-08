package webdist

import (
	"io/fs"
	"testing"
)

func TestBundleVersionIsExplicit(t *testing.T) {
	for _, v := range []string{"2.1.2", "3.0.0"} {
		assets, err := Bundle(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fs.ReadFile(assets, "index.html"); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"", "1.2.0", "../bundle", "3.0.0-preview"} {
		if _, err := Bundle(v); err == nil {
			t.Fatalf("unexpected fallback for %q", v)
		}
	}
}
