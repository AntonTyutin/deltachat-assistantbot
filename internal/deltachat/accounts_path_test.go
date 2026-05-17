package deltachat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAccountsDirOK(t *testing.T) {
	dir := t.TempDir()
	if err := validateAccountsDir(dir); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAccountsDirMissing(t *testing.T) {
	err := validateAccountsDir(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestValidateAccountsDirNotWritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "readonly")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	err := validateAccountsDir(dir)
	if err == nil {
		t.Fatal("expected error for non-writable directory")
	}
}
