package instlayout

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPortable(t *testing.T) {
	dir := t.TempDir()
	data, app := Resolve(dir)
	if data != dir || app != dir {
		t.Fatalf("portable: data=%q app=%q", data, app)
	}
	if st, err := os.Stat(filepath.Join(dir, "configs")); err != nil || !st.IsDir() {
		t.Fatal("portable should create configs dir")
	}
}

func TestInstalledUsesAppDataButStillUpdatable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte("installer"), 0644); err != nil {
		t.Fatal(err)
	}
	data, app := Resolve(dir)
	if data != "" || app != dir {
		t.Fatalf("installed: data=%q app=%q", data, app)
	}
	if _, err := os.Stat(filepath.Join(dir, "configs")); !os.IsNotExist(err) {
		t.Fatal("installed layout must not create configs in the install dir")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("probe file left behind: %v", entries)
	}
}

func TestMarkerDirectoryIsNotAMarker(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, MarkerName), 0755)
	if IsInstalled(dir) {
		t.Fatal("a directory named like the marker must not count")
	}
}

func TestReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforced here")
	}
	for _, installed := range []bool{false, true} {
		dir := t.TempDir()
		if installed {
			os.WriteFile(filepath.Join(dir, MarkerName), nil, 0644)
		}
		os.Chmod(dir, 0555)
		data, app := Resolve(dir)
		os.Chmod(dir, 0755)
		if data != "" || app != "" {
			t.Fatalf("installed=%v read-only: data=%q app=%q", installed, data, app)
		}
	}
}

func TestEmpty(t *testing.T) {
	if d, a := Resolve(""); d != "" || a != "" {
		t.Fatal("empty dir")
	}
}
