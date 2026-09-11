package phpcloak

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
	cfg.HeaderText = "Propiedad de X empresa\nUso exclusivo"
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
	for _, want := range [][]byte{[]byte("Propiedad de X empresa"), []byte("Uso exclusivo")} {
		if !bytes.Contains(stub, want) {
			t.Fatalf("sealed stub is missing header line %q: %s", want, stub)
		}
	}
	if err := Validate(stub); err != nil {
		t.Fatalf("sealed stub validation: %v", err)
	}
	runtimeSource, err := os.ReadFile(filepath.Join(root, runtimeFN))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{[]byte("Propiedad de X empresa"), []byte("Uso exclusivo")} {
		if !bytes.Contains(runtimeSource, want) {
			t.Fatalf("runtime is missing header line %q", want)
		}
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

func TestPHPHeaderCommentMultilineAndSafety(t *testing.T) {
	header, err := phpHeaderComment("Propiedad de X empresa\r\nUso exclusivo\rTercera línea")
	if err != nil {
		t.Fatalf("phpHeaderComment: %v", err)
	}
	source := []byte("<?php echo 'ok';")
	withHeader, err := addPHPHeader(source, header)
	if err != nil {
		t.Fatalf("addPHPHeader: %v", err)
	}
	text := string(withHeader)
	for _, want := range []string{"Propiedad de X empresa", "Uso exclusivo", "Tercera línea", "echo 'ok';"} {
		if !strings.Contains(text, want) {
			t.Fatalf("header/source is missing %q: %s", want, text)
		}
	}
	if !strings.HasPrefix(text, "<?php\n/*\n") {
		t.Fatalf("header must be inserted after PHP opening tag: %q", text)
	}
	if err := Validate(withHeader); err != nil {
		t.Fatalf("Validate(withHeader): %v", err)
	}

	for _, invalid := range []string{"Empresa */ código", "Empresa ?> código", "Empresa\x00código"} {
		if _, err := phpHeaderComment(invalid); err == nil {
			t.Fatalf("expected unsafe header %q to be rejected", invalid)
		}
	}
}
