package appdata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshInstallUsesTheDefaultFolder(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDefault(base); err != nil {
		t.Fatal(err)
	}
	l := Load(base)
	if l.Data != DefaultData {
		t.Fatalf("data folder %q, want %q", l.Data, DefaultData)
	}
	if got, want := l.DataDir(base), filepath.Join(base, "Recu-Settings"); got != want {
		t.Fatalf("data dir %q, want %q", got, want)
	}
}

func TestExistingFilesKeepTheirPlace(t *testing.T) {
	for _, name := range []string{"settings.json", "queue.json"} {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := EnsureDefault(base); err != nil {
			t.Fatal(err)
		}
		if l := Load(base); l.Data != "" {
			t.Errorf("%s next to the program: data folder became %q, want it left alone", name, l.Data)
		}
		if _, err := os.Stat(filepath.Join(base, FileName)); err == nil {
			t.Errorf("%s next to the program: a pointer file was written", name)
		}
	}
}

func TestAChosenFolderIsNotOverwritten(t *testing.T) {
	base := t.TempDir()
	if err := Save(base, Locations{Data: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefault(base); err != nil {
		t.Fatal(err)
	}
	if got := Load(base).Data; got != "Elsewhere" {
		t.Fatalf("data folder %q, want the chosen one kept", got)
	}
}

// Choosing "next to the program" in Settings moves the files there and removes
// the pointer file; the next start must not send them back to the default.
func TestNextToTheProgramStaysChosen(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDefault(base); err != nil {
		t.Fatal(err)
	}
	if err := Save(base, Locations{}); err != nil { // what Settings does for a blank folder
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(base, "settings.json"), []byte("{}"), 0600) // moved there
	if err := EnsureDefault(base); err != nil {
		t.Fatal(err)
	}
	if got := Load(base).Data; got != "" {
		t.Fatalf("data folder %q, want blank (next to the program)", got)
	}
}

func TestFreshInstallInAFolderThatDoesNotExistYet(t *testing.T) {
	base := filepath.Join(t.TempDir(), "new", "folder")
	if err := EnsureDefault(base); err != nil {
		t.Fatal(err)
	}
	if got := Load(base).Data; got != DefaultData {
		t.Fatalf("data folder %q, want %q", got, DefaultData)
	}
}
