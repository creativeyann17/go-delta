// pkg/decompress/safepath_test.go
package decompress

import "testing"

func TestSafeJoin(t *testing.T) {
	base := "/data/extract"

	cases := []struct {
		name     string
		entry    string
		wantErr  bool
		wantPath string
	}{
		{"plain file", "foo.txt", false, "/data/extract/foo.txt"},
		{"nested file", "sub/foo.txt", false, "/data/extract/sub/foo.txt"},
		{"parent traversal", "../foo.txt", true, ""},
		{"deep parent traversal", "../../etc/passwd", true, ""},
		{"traversal inside path", "sub/../../foo.txt", true, ""},
		// filepath.Join treats a later absolute-looking arg as just another
		// segment (it doesn't reset to root), so this lands inside base —
		// safe, not a traversal. Covered here to document that behavior.
		{"absolute-looking path stays inside base", "/etc/passwd", false, "/data/extract/etc/passwd"},
		{"sneaky prefix sibling", "../extract-evil/foo.txt", true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeJoin(base, tc.entry)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for entry %q, got path %q", tc.entry, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for entry %q: %v", tc.entry, err)
			}
			if got != tc.wantPath {
				t.Errorf("entry %q: got %q, want %q", tc.entry, got, tc.wantPath)
			}
		})
	}
}
