package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHeaderTextEscapesAndFile(t *testing.T) {
	text, err := resolveHeaderText(`Propiedad de X empresa\nUso exclusivo`, "")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Propiedad de X empresa\nUso exclusivo" {
		t.Fatalf("unexpected decoded header: %q", text)
	}

	path := filepath.Join(t.TempDir(), "header.txt")
	if err := os.WriteFile(path, []byte("Empresa X\nConfidencial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := resolveHeaderText("", path)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != "Empresa X\nConfidencial\n" {
		t.Fatalf("unexpected file header: %q", fromFile)
	}

	if _, err := resolveHeaderText("texto", path); err == nil {
		t.Fatal("expected --header-text and --header-file conflict")
	}
}
