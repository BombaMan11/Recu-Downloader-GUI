package main

import (
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A plain `go build` for Windows must give Recu.exe its icon, with no extra tool
// or step: the .syso files next to main.go are what carry it.
func TestWindowsBuildHasTheIcon(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	out := filepath.Join(t.TempDir(), "Recu.exe")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, ".")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, b)
	}
	f, err := pe.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Section(".rsrc") == nil {
		t.Error("Recu.exe has no resources, so no icon")
	}
}
