package app

import "testing"

func TestWriteSourceFileRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "src/../../escape", `C:\outside`, "src\\..\\escape", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			if err := writeSourceFile(t.TempDir(), SnapshotFile{Path: name}); err == nil {
				t.Fatalf("accepted unsafe source path %q", name)
			}
		})
	}
}
