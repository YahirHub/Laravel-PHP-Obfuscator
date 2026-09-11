package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var version = "0.5.1"

const (
	backupDir  = ".debofuscated"
	manifestFN = ".phpcloak-manifest.json"
	runtimeFN  = ".phpcloak-runtime.php"
)

//go:embed helper.php
var helperPHP []byte

var defaultExcludes = []string{
	"vendor", "node_modules", "storage", "bootstrap/cache", ".git",
	"resources/views", "public/build", "app/.phpcloak", backupDir,
}

type cfg struct {
	root, mode, php, keyEnv, keyFile string
	includes, excludes               []string
	allPHP, force, verify            bool
}

type entry struct {
	Path           string `json:"path"`
	Action         string `json:"action"`
	Reason         string `json:"reason,omitempty"`
	OriginalSHA256 string `json:"original_sha256"`
	OutputSHA256   string `json:"output_sha256"`
}

type runManifest struct {
	Tool      string  `json:"tool"`
	Version   string  `json:"version"`
	CreatedAt string  `json:"created_at"`
	Mode      string  `json:"mode"`
	Root      string  `json:"root"`
	Backup    string  `json:"backup"`
	Entries   []entry `json:"entries"`
	Summary   struct {
		Minified int `json:"minified"`
		Mangled  int `json:"mangled"`
		Sealed   int `json:"sealed"`
		Fallback int `json:"fallback"`
	} `json:"summary"`
}

type backupFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

type backupManifest struct {
	Tool       string       `json:"tool"`
	Version    string       `json:"version"`
	CreatedAt  string       `json:"created_at"`
	Root       string       `json:"root"`
	RuntimeKey string       `json:"runtime_key,omitempty"`
	Files      []backupFile `json:"files"`
}

type target struct {
	path string
	rel  string
	mode fs.FileMode
	orig []byte
}

func main() {
	root, command, args, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(2)
	}

	switch command {
	case "version":
		fmt.Println("phpcloak", version)
	case "help":
		usage()
	case "obfuscate":
		if root == "" {
			fail(errors.New("--root is required before obfuscate"))
		}
		if err := runObfuscate(root, args); err != nil {
			fail(err)
		}
	case "restore":
		if root == "" {
			fail(errors.New("--root is required before restore"))
		}
		if err := runRestore(root, args); err != nil {
			fail(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", command)
		usage()
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func parseCLI(args []string) (root, command string, rest []string, err error) {
	if len(args) == 0 {
		return "", "help", nil, nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h" || a == "help":
			return root, "help", nil, nil
		case a == "--version" || a == "-v" || a == "version":
			return root, "version", nil, nil
		case a == "--root" || a == "-r":
			if i+1 >= len(args) {
				return "", "", nil, errors.New(a + " requires a directory")
			}
			root = args[i+1]
			i++
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		case !strings.HasPrefix(a, "-"):
			return root, a, args[i+1:], nil
		default:
			return "", "", nil, fmt.Errorf("global option %q must come after the command, or use --root before it", a)
		}
	}
	return root, "help", nil, nil
}

func usage() {
	fmt.Print(`phpcloak - Laravel/PHP in-place source protection

Usage:
  phpcloak --root /ruta/a/laravel obfuscate [options]
  phpcloak --root /ruta/a/laravel restore [options]
  phpcloak version

Obfuscate options:
  --mode sealed|aggressive|strong|safe  sealed encrypts whole PHP files with AES-256-GCM (default sealed)
  --include LIST         roots to protect (default app,routes,modules,packages)
  --exclude LIST         extra comma-separated exclusions
  --all-php              process every PHP file except excluded paths
  --php PATH             PHP executable (default php)
  --key-env NAME         optional env override for sealed key (advanced; disabled by default)
  --key-file PATH        local key file relative to project (default app/.phpcloak/phpcloak.key)
  --verify=true|false    run php -l before replacing originals (default true)
  --force                replace an existing .debofuscated backup

Restore options:
  --keep-backup          restore originals but keep .debofuscated

Examples:
  phpcloak --root /var/www/myapp obfuscate
  phpcloak --root /var/www/myapp obfuscate --mode sealed --verify=true
  phpcloak --root /var/www/myapp restore
`)
}

func runObfuscate(root string, args []string) error {
	f := flag.NewFlagSet("obfuscate", flag.ContinueOnError)
	mode := f.String("mode", "sealed", "sealed, aggressive, strong or safe")
	include := f.String("include", "app,routes,modules,packages", "include roots")
	exclude := f.String("exclude", "", "extra excludes")
	allPHP := f.Bool("all-php", false, "all php")
	force := f.Bool("force", false, "replace backup")
	verify := f.Bool("verify", true, "lint output")
	php := f.String("php", "php", "php executable")
	keyEnv := f.String("key-env", "", "optional runtime key environment variable")
	keyFile := f.String("key-file", "app/.phpcloak/phpcloak.key", "local runtime key file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	if *mode != "sealed" && *mode != "aggressive" && *mode != "strong" && *mode != "safe" {
		return errors.New("--mode must be sealed, aggressive, strong or safe")
	}
	if *keyEnv != "" && !validEnvName(*keyEnv) {
		return errors.New("--key-env must contain only letters, digits and underscore and may not start with a digit")
	}
	if !validRelativeKeyPath(*keyFile) {
		return errors.New("--key-file must be a relative path inside the project")
	}
	if _, err := exec.LookPath(*php); err != nil {
		return fmt.Errorf("PHP not found: %w", err)
	}
	c := cfg{
		root: root, mode: *mode, php: *php, keyEnv: *keyEnv, keyFile: norm(*keyFile),
		includes: splitList(*include), excludes: append(defaultExcludes, splitList(*exclude)...),
		allPHP: *allPHP, force: *force, verify: *verify,
	}
	return obfuscateInPlace(c)
}

func runRestore(root string, args []string) error {
	f := flag.NewFlagSet("restore", flag.ContinueOnError)
	keep := f.Bool("keep-backup", false, "keep backup")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	return restore(root, *keep)
}

func obfuscateInPlace(c cfg) error {
	root, err := filepath.Abs(c.root)
	if err != nil {
		return err
	}
	st, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	if !st.IsDir() {
		return errors.New("--root must be a directory")
	}

	backupRoot := filepath.Join(root, backupDir)
	if _, err := os.Stat(backupRoot); err == nil {
		if !c.force {
			return fmt.Errorf("backup already exists at %s; restore first or use --force to replace it", backupRoot)
		}
		if err := os.RemoveAll(backupRoot); err != nil {
			return fmt.Errorf("remove previous backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	targets, err := collectTargets(root, c)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no PHP files matched the selected include/exclude rules")
	}

	if c.mode == "sealed" {
		if _, err := os.Stat(filepath.Join(root, runtimeFN)); err == nil {
			return fmt.Errorf("%s already exists; remove it or restore the previous protected build first", runtimeFN)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	originalDir := filepath.Join(backupRoot, "original")
	stageDir := filepath.Join(backupRoot, "staged")
	if err := os.MkdirAll(originalDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		_ = os.RemoveAll(backupRoot)
		return err
	}

	bm := backupManifest{
		Tool: "phpcloak", Version: version, CreatedAt: time.Now().UTC().Format(time.RFC3339), Root: root,
	}
	if c.mode == "sealed" {
		bm.RuntimeKey = c.keyFile
	}
	for _, t := range targets {
		backupPath := filepath.Join(originalDir, filepath.FromSlash(t.rel))
		if err := writeBytes(backupPath, t.orig, t.mode.Perm()); err != nil {
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("backup %s: %w", t.rel, err)
		}
		bm.Files = append(bm.Files, backupFile{Path: t.rel, SHA256: digest(t.orig), Mode: uint32(t.mode.Perm())})
	}
	if err := writeJSON(filepath.Join(backupRoot, "backup.json"), bm, 0600); err != nil {
		_ = os.RemoveAll(backupRoot)
		return err
	}

	var sealedKey []byte
	var sealedKeyText string
	var sealedKeyGenerated bool
	if c.mode == "sealed" {
		sealedKey, sealedKeyText, sealedKeyGenerated, err = sealedProjectKey(c.keyEnv, filepath.Join(root, filepath.FromSlash(c.keyFile)))
		if err != nil {
			_ = os.RemoveAll(backupRoot)
			return err
		}
		if err := writeBytes(filepath.Join(backupRoot, "sealed.key"), []byte(sealedKeyText+"\n"), 0600); err != nil {
			_ = os.RemoveAll(backupRoot)
			return err
		}
		if err := writeBytes(filepath.Join(stageDir, ".sealed-runtime-key"), []byte(sealedKeyText+"\n"), 0600); err != nil {
			_ = os.RemoveAll(backupRoot)
			return err
		}
		runtimeStage := filepath.Join(stageDir, runtimeFN)
		if err := writeBytes(runtimeStage, sealedRuntimePHP(c.keyEnv, c.keyFile), 0644); err != nil {
			_ = os.RemoveAll(backupRoot)
			return err
		}
		if c.verify {
			if err := phpLint(c.php, runtimeStage); err != nil {
				_ = os.RemoveAll(backupRoot)
				return fmt.Errorf("lint runtime: %w", err)
			}
		}
	}

	helperPath, err := writeTempHelper()
	if err != nil {
		_ = os.RemoveAll(backupRoot)
		return err
	}
	defer os.Remove(helperPath)

	rm := runManifest{
		Tool: "phpcloak", Version: version, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Mode: c.mode, Root: root, Backup: backupRoot,
	}

	for _, t := range targets {
		minified, err := phpMinify(c.php, t.path)
		if err != nil {
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("minify %s: %w", t.rel, err)
		}
		result := minified
		e := entry{Path: t.rel, Action: "minified", OriginalSHA256: digest(t.orig)}
		rm.Summary.Minified++

		if c.mode == "sealed" {
			if sealedUnsafe(minified) {
				mangled, note, err := phpMangle(c.php, helperPath, minified, "aggressive")
				if err != nil {
					_ = os.RemoveAll(backupRoot)
					return fmt.Errorf("sealed fallback %s: %w", t.rel, err)
				}
				result = mangled
				e.Action = "sealed-fallback"
				e.Reason = "uses __FILE__ or __DIR__; aggressive transform used to preserve path semantics; " + note
				rm.Summary.Fallback++
			} else {
				sealed, err := sealPHP(minified, sealedKey, t.path, root)
				if err != nil {
					_ = os.RemoveAll(backupRoot)
					return fmt.Errorf("seal %s: %w", t.rel, err)
				}
				result = sealed
				e.Action = "sealed"
				e.Reason = "AES-256-GCM"
				rm.Summary.Sealed++
			}
		} else if c.mode == "strong" || c.mode == "aggressive" {
			mangled, note, err := phpMangle(c.php, helperPath, minified, c.mode)
			if err != nil {
				_ = os.RemoveAll(backupRoot)
				return fmt.Errorf("mangle %s: %w", t.rel, err)
			}
			result = mangled
			e.Reason = note
			if strings.HasPrefix(note, "renamed:") {
				e.Action = "mangled"
				rm.Summary.Mangled++
			} else {
				rm.Summary.Fallback++
			}
		}

		stagePath := filepath.Join(stageDir, filepath.FromSlash(t.rel))
		if err := writeBytes(stagePath, result, t.mode.Perm()); err != nil {
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("stage %s: %w", t.rel, err)
		}
		if c.verify {
			if err := phpLint(c.php, stagePath); err != nil {
				_ = os.RemoveAll(backupRoot)
				return fmt.Errorf("lint %s: %w", t.rel, err)
			}
		}
		e.OutputSHA256 = digest(result)
		rm.Entries = append(rm.Entries, e)
	}

	// All files are staged and optionally linted before touching the project.
	if c.mode == "sealed" {
		localKeyPath := filepath.Join(root, filepath.FromSlash(c.keyFile))
		if err := atomicCopy(filepath.Join(stageDir, ".sealed-runtime-key"), localKeyPath, 0600); err != nil {
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("install sealed runtime key: %w", err)
		}
		if err := atomicCopy(filepath.Join(stageDir, runtimeFN), filepath.Join(root, runtimeFN), 0644); err != nil {
			if sealedKeyGenerated {
				_ = os.Remove(localKeyPath)
			}
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("install sealed runtime: %w", err)
		}
	}
	modified := make([]target, 0, len(targets))
	for _, t := range targets {
		stagePath := filepath.Join(stageDir, filepath.FromSlash(t.rel))
		if err := atomicCopy(stagePath, t.path, t.mode.Perm()); err != nil {
			rollbackErr := rollbackTargets(originalDir, modified)
			if rollbackErr != nil {
				return fmt.Errorf("replace %s: %v; rollback also failed: %v; originals remain in %s", t.rel, err, rollbackErr, backupRoot)
			}
			_ = os.Remove(filepath.Join(root, runtimeFN))
			if c.mode == "sealed" && sealedKeyGenerated {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(c.keyFile)))
			}
			_ = os.RemoveAll(backupRoot)
			return fmt.Errorf("replace %s: %w (changes rolled back)", t.rel, err)
		}
		modified = append(modified, t)
	}

	sort.Slice(rm.Entries, func(i, j int) bool { return rm.Entries[i].Path < rm.Entries[j].Path })
	if err := writeJSON(filepath.Join(root, manifestFN), rm, 0644); err != nil {
		rollbackErr := rollbackTargets(originalDir, modified)
		if rollbackErr != nil {
			return fmt.Errorf("write manifest: %v; rollback also failed: %v; originals remain in %s", err, rollbackErr, backupRoot)
		}
		_ = os.RemoveAll(backupRoot)
		return fmt.Errorf("write manifest: %w (changes rolled back)", err)
	}
	_ = os.RemoveAll(stageDir)

	fmt.Printf("phpcloak %s complete\n", version)
	fmt.Printf("  root:      %s\n", root)
	fmt.Printf("  backup:    %s\n", backupRoot)
	fmt.Printf("  mode:      %s\n", c.mode)
	fmt.Printf("  files:     %d\n", len(targets))
	fmt.Printf("  sealed:    %d\n", rm.Summary.Sealed)
	fmt.Printf("  mangled:   %d\n", rm.Summary.Mangled)
	fmt.Printf("  fallback:  %d\n", rm.Summary.Fallback)
	if c.mode == "sealed" {
		fmt.Printf("  runtime key: %s\n", filepath.Join(root, filepath.FromSlash(c.keyFile)))
		fmt.Printf("  backup key:  %s\n", filepath.Join(backupRoot, "sealed.key"))
		if c.keyEnv != "" {
			fmt.Printf("  env override: %s (optional)\n", c.keyEnv)
		}
		fmt.Printf("  ready:       no environment variable is required\n")
		fmt.Printf("  WARNING:     never deploy %s; it contains original source\n", backupDir)
	}
	return nil
}

func restore(root string, keepBackup bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	backupRoot := filepath.Join(root, backupDir)
	metaPath := filepath.Join(backupRoot, "backup.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no restorable backup found at %s", backupRoot)
		}
		return err
	}
	var bm backupManifest
	if err := json.Unmarshal(b, &bm); err != nil {
		return fmt.Errorf("invalid backup metadata: %w", err)
	}
	if len(bm.Files) == 0 {
		return errors.New("backup metadata contains no files")
	}

	// Validate the complete backup before modifying the project.
	for _, f := range bm.Files {
		src := filepath.Join(backupRoot, "original", filepath.FromSlash(f.Path))
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("backup missing %s: %w", f.Path, err)
		}
		if digest(data) != f.SHA256 {
			return fmt.Errorf("backup integrity check failed for %s", f.Path)
		}
	}

	for _, f := range bm.Files {
		src := filepath.Join(backupRoot, "original", filepath.FromSlash(f.Path))
		dst := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := atomicCopy(src, dst, fs.FileMode(f.Mode)); err != nil {
			return fmt.Errorf("restore %s: %w", f.Path, err)
		}
	}
	_ = os.Remove(filepath.Join(root, manifestFN))
	_ = os.Remove(filepath.Join(root, runtimeFN))
	keyRel := bm.RuntimeKey
	if keyRel == "" {
		keyRel = "app/.phpcloak/phpcloak.key"
	}
	keyPath := filepath.Join(root, filepath.FromSlash(keyRel))
	_ = os.Remove(keyPath)
	_ = os.Remove(filepath.Dir(keyPath)) // remove .phpcloak only when empty
	if !keepBackup {
		if err := os.RemoveAll(backupRoot); err != nil {
			return fmt.Errorf("files restored, but could not remove backup: %w", err)
		}
	}
	fmt.Printf("phpcloak restore complete\n")
	fmt.Printf("  root:     %s\n", root)
	fmt.Printf("  restored: %d files\n", len(bm.Files))
	if keepBackup {
		fmt.Printf("  backup:   kept at %s\n", backupRoot)
	} else {
		fmt.Printf("  backup:   removed\n")
	}
	return nil
}

func collectTargets(root string, c cfg) ([]target, error) {
	var out []target
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = norm(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if excluded(rel, c.excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if !shouldProcess(rel, c) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, target{path: path, rel: rel, mode: info.Mode(), orig: b})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func rollbackTargets(originalDir string, modified []target) error {
	var first error
	for i := len(modified) - 1; i >= 0; i-- {
		t := modified[i]
		src := filepath.Join(originalDir, filepath.FromSlash(t.rel))
		if err := atomicCopy(src, t.path, t.mode.Perm()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func atomicCopy(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".phpcloak-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	defer cleanup()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(path string, v any, mode fs.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeBytes(path, b, mode)
}

func writeBytes(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode.Perm())
}

func writeTempHelper() (string, error) {
	f, err := os.CreateTemp("", "phpcloak-helper-*.php")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(helperPHP); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func phpMinify(php, path string) ([]byte, error) {
	cmd := exec.Command(php, "-w", path)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
	}
	if len(b) == 0 {
		return nil, errors.New("empty PHP output")
	}
	return b, nil
}

func phpMangle(php, helper string, src []byte, mode string) ([]byte, string, error) {
	cmd := exec.Command(php, helper, mode)
	cmd.Stdin = strings.NewReader(string(src))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	note := strings.TrimSpace(stderr.String())
	if note == "" {
		note = "fallback:unchanged"
	}
	return []byte(stdout.String()), note, nil
}

func phpLint(php, path string) error {
	cmd := exec.Command(php, "-l", path)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

func shouldProcess(rel string, c cfg) bool {
	if strings.ToLower(filepath.Ext(rel)) != ".php" {
		return false
	}
	if excluded(rel, c.excludes) {
		return false
	}
	if c.allPHP {
		return true
	}
	for _, root := range c.includes {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

func excluded(rel string, xs []string) bool {
	rel = norm(rel)
	for _, x := range xs {
		x = norm(x)
		if rel == x || strings.HasPrefix(rel, x+"/") {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = norm(strings.TrimSpace(p))
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

func norm(s string) string {
	s = filepath.ToSlash(filepath.Clean(s))
	return strings.TrimPrefix(s, "./")
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func validRelativeKeyPath(s string) bool {
	if strings.TrimSpace(s) == "" || filepath.IsAbs(s) {
		return false
	}
	c := filepath.Clean(s)
	if c == "." || c == ".." {
		return false
	}
	r := filepath.ToSlash(c)
	return !strings.HasPrefix(r, "../")
}

func validEnvName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func sealedProjectKey(envName, keyPath string) ([]byte, string, bool, error) {
	if envName != "" {
		if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
			key, err := base64.StdEncoding.DecodeString(v)
			if err != nil || len(key) != 32 {
				return nil, "", false, fmt.Errorf("environment %s must contain a Base64-encoded 32-byte key", envName)
			}
			return key, v, false, nil
		}
	}
	if b, err := os.ReadFile(keyPath); err == nil {
		v := strings.TrimSpace(string(b))
		key, decErr := base64.StdEncoding.DecodeString(v)
		if decErr != nil || len(key) != 32 {
			return nil, "", false, fmt.Errorf("local key file %s is invalid", keyPath)
		}
		return key, v, false, nil
	} else if !os.IsNotExist(err) {
		return nil, "", false, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", false, err
	}
	return key, base64.StdEncoding.EncodeToString(key), true, nil
}

func sealedUnsafe(src []byte) bool {
	s := string(src)
	return strings.Contains(s, "__FILE__") || strings.Contains(s, "__DIR__") || strings.Contains(s, "__halt_compiler")
}

func sealPHP(src, key []byte, targetPath, root string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, src, nil)
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]

	runtimePath := filepath.Join(root, runtimeFN)
	rel, err := filepath.Rel(filepath.Dir(targetPath), runtimePath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	loader := fmt.Sprintf("<?php\nrequire_once __DIR__.%s;\nreturn \\PhpCloakRuntime::load(%s,%s,%s);\n",
		phpQuote("/"+strings.TrimPrefix(rel, "./")),
		phpQuote(base64.StdEncoding.EncodeToString(nonce)),
		phpQuote(base64.StdEncoding.EncodeToString(tag)),
		phpQuote(base64.StdEncoding.EncodeToString(ciphertext)),
	)
	return []byte(loader), nil
}

func phpQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return "'" + s + "'"
}

func sealedRuntimePHP(envName, keyFile string) []byte {
	keyRel := strings.ReplaceAll(filepath.ToSlash(keyFile), "'", "")
	keyLookup := "        $secret = '';\n"
	if envName != "" {
		env := strings.ReplaceAll(envName, "'", "")
		keyLookup += "        $envSecret = getenv('" + env + "');\n" +
			"        if (is_string($envSecret) && trim($envSecret) !== '') { $secret = trim($envSecret); }\n"
	}
	keyLookup += "        if ($secret === '') {\n" +
		"            $localKey = __DIR__ . '/" + keyRel + "';\n" +
		"            if (is_file($localKey)) { $secret = trim((string) file_get_contents($localKey)); }\n" +
		"        }\n" +
		"        if ($secret === '') { throw new RuntimeException('Missing phpcloak local runtime key'); }\n"

	s := `<?php
final class PhpCloakRuntime
{
    public static function load(string $nonce64, string $tag64, string $cipher64)
    {
` + keyLookup + `        $key = base64_decode($secret, true);
        $nonce = base64_decode($nonce64, true);
        $tag = base64_decode($tag64, true);
        $cipher = base64_decode($cipher64, true);
        if ($key === false || strlen($key) !== 32 || $nonce === false || $tag === false || $cipher === false) {
            throw new RuntimeException('Invalid phpcloak runtime material');
        }
        $plain = openssl_decrypt($cipher, 'aes-256-gcm', $key, OPENSSL_RAW_DATA, $nonce, $tag);
        if ($plain === false) {
            throw new RuntimeException('Unable to decrypt protected PHP payload');
        }
        $tmp = tempnam(sys_get_temp_dir(), 'pc_');
        if ($tmp === false) {
            throw new RuntimeException('Unable to allocate phpcloak runtime file');
        }
        if (file_put_contents($tmp, $plain, LOCK_EX) === false) {
            @unlink($tmp);
            throw new RuntimeException('Unable to materialize protected PHP payload');
        }
        try {
            return require $tmp;
        } finally {
            @unlink($tmp);
        }
    }
}
`
	return []byte(s)
}

func parseMode(s string) (fs.FileMode, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return fs.FileMode(n), nil
}

// Kept for backwards-compatible backup metadata readers if a future version stores modes as strings.
var _ = parseMode
