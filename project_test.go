package phpcloak

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProtectAndRestoreWithoutPHPExecutable(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("<?php\nfinal class Demo { public function run($input) { $local = 'ok'; return $input . $local; } }\n")
	path := filepath.Join(appDir, "Demo.php")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", ""); err != nil {
		t.Fatal(err)
	}
	defer os.Setenv("PATH", oldPath)

	cfg := DefaultConfig(root)
	cfg.Mode = ModeStrong
	cfg.Version = "test"
	manifest, err := Protect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Protect without PHP executable: %v", err)
	}
	if len(manifest.Entries) != 1 || manifest.Summary.Mangled != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	protected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(protected, original) {
		t.Fatal("source was not transformed")
	}
	if err := Validate(protected); err != nil {
		t.Fatalf("protected source is invalid: %v", err)
	}

	result, err := Restore(context.Background(), RestoreOptions{Root: root})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if result.Restored != 1 || result.BackupKept {
		t.Fatalf("unexpected restore result: %+v", result)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restore mismatch\nwant: %q\n got: %q", original, restored)
	}
}

func TestSealedModeCreatesRuntimeAndKeyWithoutPHPExecutable(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(appDir, "Secret.php")
	original := []byte("<?php\nreturn ['secret' => 'value'];\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", "")
	defer os.Setenv("PATH", oldPath)

	cfg := DefaultConfig(root)
	cfg.Mode = ModeSealed
	manifest, err := Protect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Protect sealed: %v", err)
	}
	if manifest.Summary.Sealed != 1 {
		t.Fatalf("expected one sealed file: %+v", manifest.Summary)
	}
	for _, generated := range []string{
		filepath.Join(root, runtimeFN),
		filepath.Join(root, filepath.FromSlash(DefaultKeyFile)),
		filepath.Join(root, manifestFN),
	} {
		if _, err := os.Stat(generated); err != nil {
			t.Fatalf("missing generated file %s: %v", generated, err)
		}
	}
	stub, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stub, []byte("'secret'")) || bytes.Contains(stub, []byte("'value'")) {
		t.Fatalf("sealed stub exposes plaintext: %s", stub)
	}
	if err := Validate(stub); err != nil {
		t.Fatalf("sealed stub validation: %v", err)
	}

	if _, err := Restore(context.Background(), RestoreOptions{Root: root}); err != nil {
		t.Fatalf("Restore sealed: %v", err)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, original) {
		t.Fatal("sealed restore did not recover original source")
	}
}

func TestSealedFallsBackForDirMagicConstant(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(appDir, "Path.php")
	if err := os.WriteFile(path, []byte("<?php\nreturn __DIR__ . '/data';\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig(root)
	cfg.Mode = ModeSealed
	manifest, err := Protect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if manifest.Summary.Fallback != 1 || manifest.Entries[0].Action != "sealed-fallback" {
		t.Fatalf("expected sealed fallback: %+v", manifest)
	}
}
