package privileged

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComponentIDTracksFileContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "component")
	if err := os.WriteFile(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := ComponentID(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := ComponentID(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("component identity ignored changed content")
	}
}
