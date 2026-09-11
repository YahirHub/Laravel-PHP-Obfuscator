package phpcloak

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	backupDir  = ".debofuscated"
	manifestFN = ".phpcloak-manifest.json"
	runtimeFN  = ".phpcloak-runtime.php"
)

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

func Protect(ctx context.Context, cfg Config) (*Manifest, error) {
	cfg = normalizeConfig(cfg)
	if !cfg.Mode.Valid() {
		return nil, fmt.Errorf("invalid mode %q", cfg.Mode)
	}
	if cfg.Root == "" {
		return nil, errors.New("root is required")
	}
	if cfg.KeyEnv != "" && !validEnvName(cfg.KeyEnv) {
		return nil, errors.New("key environment name may contain only letters, digits and underscore and may not start with a digit")
	}
	if !validRelativeKeyPath(cfg.KeyFile) {
		return nil, errors.New("key file must be a relative path inside the project")
	}
	headerBlock, err := phpHeaderComment(cfg.HeaderText)
	if err != nil {
		return nil, err
	}

	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	if !st.IsDir() {
		return nil, errors.New("root must be a directory")
	}

	backupRoot := filepath.Join(root, backupDir)
	if _, err := os.Stat(backupRoot); err == nil {
		if !cfg.Force {
			return nil, fmt.Errorf("backup already exists at %s; restore first or enable Force", backupRoot)
		}
		if err := os.RemoveAll(backupRoot); err != nil {
			return nil, fmt.Errorf("remove previous backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	targets, err := collectTargets(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("no PHP files matched the selected include/exclude rules")
	}
	if cfg.Mode == ModeSealed {
		if _, err := os.Stat(filepath.Join(root, runtimeFN)); err == nil {
			return nil, fmt.Errorf("%s already exists; remove it or restore the previous protected build first", runtimeFN)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	originalDir := filepath.Join(backupRoot, "original")
	stageDir := filepath.Join(backupRoot, "staged")
	if err := os.MkdirAll(originalDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		_ = os.RemoveAll(backupRoot)
		return nil, err
	}
	cleanupOnError := func(err error) (*Manifest, error) {
		_ = os.RemoveAll(backupRoot)
		return nil, err
	}

	now := time.Now().UTC()
	bm := backupManifest{Tool: "phpcloak", Version: cfg.Version, CreatedAt: now.Format(time.RFC3339), Root: root}
	if cfg.Mode == ModeSealed {
		bm.RuntimeKey = cfg.KeyFile
	}
	for _, t := range targets {
		if err := checkContext(ctx); err != nil {
			return cleanupOnError(err)
		}
		backupPath := filepath.Join(originalDir, filepath.FromSlash(t.rel))
		if err := writeBytes(backupPath, t.orig, t.mode.Perm()); err != nil {
			return cleanupOnError(fmt.Errorf("backup %s: %w", t.rel, err))
		}
		bm.Files = append(bm.Files, backupFile{Path: t.rel, SHA256: digest(t.orig), Mode: uint32(t.mode.Perm())})
	}
	if err := writeJSON(filepath.Join(backupRoot, "backup.json"), bm, 0600); err != nil {
		return cleanupOnError(err)
	}

	var sealedKey []byte
	var sealedKeyText string
	var sealedKeyGenerated bool
	if cfg.Mode == ModeSealed {
		sealedKey, sealedKeyText, sealedKeyGenerated, err = sealedProjectKey(cfg.KeyEnv, filepath.Join(root, filepath.FromSlash(cfg.KeyFile)))
		if err != nil {
			return cleanupOnError(err)
		}
		if err := writeBytes(filepath.Join(backupRoot, "sealed.key"), []byte(sealedKeyText+"\n"), 0600); err != nil {
			return cleanupOnError(err)
		}
		if err := writeBytes(filepath.Join(stageDir, ".sealed-runtime-key"), []byte(sealedKeyText+"\n"), 0600); err != nil {
			return cleanupOnError(err)
		}
		runtimeSource := sealedRuntimePHP(cfg.KeyEnv, cfg.KeyFile)
		runtimeSource, err = addPHPHeader(runtimeSource, headerBlock)
		if err != nil {
			return cleanupOnError(fmt.Errorf("add header to runtime: %w", err))
		}
		if cfg.Validate {
			if err := Validate(runtimeSource); err != nil {
				return cleanupOnError(fmt.Errorf("validate runtime: %w", err))
			}
		}
		if err := writeBytes(filepath.Join(stageDir, runtimeFN), runtimeSource, 0644); err != nil {
			return cleanupOnError(err)
		}
	}

	manifest := &Manifest{
		Tool:      "phpcloak",
		Version:   cfg.Version,
		CreatedAt: now.Format(time.RFC3339),
		Mode:      cfg.Mode,
		Root:      root,
		Backup:    backupRoot,
	}

	for _, t := range targets {
		if err := checkContext(ctx); err != nil {
			return cleanupOnError(err)
		}
		entry := Entry{Path: t.rel, Action: "minified", OriginalSHA256: digest(t.orig)}
		manifest.Summary.Minified++
		var transformed []byte

		switch cfg.Mode {
		case ModeSealed:
			minified, minErr := Minify(t.orig)
			if minErr != nil {
				return cleanupOnError(fmt.Errorf("minify %s: %w", t.rel, minErr))
			}
			if sealedUnsafeSource(t.orig) {
				res, transformErr := Transform(t.orig, ModeAggressive)
				if transformErr != nil {
					return cleanupOnError(fmt.Errorf("sealed fallback %s: %w", t.rel, transformErr))
				}
				transformed = res.Source
				entry.Action = "sealed-fallback"
				entry.Reason = "uses __FILE__, __DIR__ or __halt_compiler; aggressive transform preserves path semantics; " + res.Stats.Note()
				manifest.Summary.Fallback++
			} else {
				transformed, err = sealPHP(minified, sealedKey, t.path, root)
				if err != nil {
					return cleanupOnError(fmt.Errorf("seal %s: %w", t.rel, err))
				}
				entry.Action = "sealed"
				entry.Reason = "AES-256-GCM"
				manifest.Summary.Sealed++
			}
		case ModeAggressive, ModeStrong, ModeSafe:
			res, transformErr := Transform(t.orig, cfg.Mode)
			if transformErr != nil {
				return cleanupOnError(fmt.Errorf("transform %s: %w", t.rel, transformErr))
			}
			transformed = res.Source
			if cfg.Mode == ModeAggressive || cfg.Mode == ModeStrong {
				entry.Action = "mangled"
				entry.Reason = res.Stats.Note()
				manifest.Summary.Mangled++
			}
		}

		transformed, err = addPHPHeader(transformed, headerBlock)
		if err != nil {
			return cleanupOnError(fmt.Errorf("add header to %s: %w", t.rel, err))
		}
		if cfg.Validate {
			if err := Validate(transformed); err != nil {
				return cleanupOnError(fmt.Errorf("validate %s: %w", t.rel, err))
			}
		}
		stagePath := filepath.Join(stageDir, filepath.FromSlash(t.rel))
		if err := writeBytes(stagePath, transformed, t.mode.Perm()); err != nil {
			return cleanupOnError(fmt.Errorf("stage %s: %w", t.rel, err))
		}
		entry.OutputSHA256 = digest(transformed)
		manifest.Entries = append(manifest.Entries, entry)
	}

	if cfg.Mode == ModeSealed {
		localKeyPath := filepath.Join(root, filepath.FromSlash(cfg.KeyFile))
		if err := atomicCopy(filepath.Join(stageDir, ".sealed-runtime-key"), localKeyPath, 0600); err != nil {
			return cleanupOnError(fmt.Errorf("install sealed runtime key: %w", err))
		}
		if err := atomicCopy(filepath.Join(stageDir, runtimeFN), filepath.Join(root, runtimeFN), 0644); err != nil {
			if sealedKeyGenerated {
				_ = os.Remove(localKeyPath)
			}
			return cleanupOnError(fmt.Errorf("install sealed runtime: %w", err))
		}
	}

	modified := make([]target, 0, len(targets))
	for _, t := range targets {
		stagePath := filepath.Join(stageDir, filepath.FromSlash(t.rel))
		if err := atomicCopy(stagePath, t.path, t.mode.Perm()); err != nil {
			rollbackErr := rollbackTargets(originalDir, modified)
			if rollbackErr != nil {
				return nil, fmt.Errorf("replace %s: %v; rollback also failed: %v; originals remain in %s", t.rel, err, rollbackErr, backupRoot)
			}
			_ = os.Remove(filepath.Join(root, runtimeFN))
			if cfg.Mode == ModeSealed && sealedKeyGenerated {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(cfg.KeyFile)))
			}
			_ = os.RemoveAll(backupRoot)
			return nil, fmt.Errorf("replace %s: %w (changes rolled back)", t.rel, err)
		}
		modified = append(modified, t)
	}

	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	if err := writeJSON(filepath.Join(root, manifestFN), manifest, 0644); err != nil {
		rollbackErr := rollbackTargets(originalDir, modified)
		if rollbackErr != nil {
			return nil, fmt.Errorf("write manifest: %v; rollback also failed: %v; originals remain in %s", err, rollbackErr, backupRoot)
		}
		_ = os.RemoveAll(backupRoot)
		return nil, fmt.Errorf("write manifest: %w (changes rolled back)", err)
	}
	_ = os.RemoveAll(stageDir)
	return manifest, nil
}

func Restore(ctx context.Context, opts RestoreOptions) (*RestoreResult, error) {
	if opts.Root == "" {
		return nil, errors.New("root is required")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	backupRoot := filepath.Join(root, backupDir)
	metaPath := filepath.Join(backupRoot, "backup.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no restorable backup found at %s", backupRoot)
		}
		return nil, err
	}
	var bm backupManifest
	if err := json.Unmarshal(b, &bm); err != nil {
		return nil, fmt.Errorf("invalid backup metadata: %w", err)
	}
	if len(bm.Files) == 0 {
		return nil, errors.New("backup metadata contains no files")
	}
	for _, f := range bm.Files {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		src := filepath.Join(backupRoot, "original", filepath.FromSlash(f.Path))
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("backup missing %s: %w", f.Path, err)
		}
		if digest(data) != f.SHA256 {
			return nil, fmt.Errorf("backup integrity check failed for %s", f.Path)
		}
	}
	for _, f := range bm.Files {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		src := filepath.Join(backupRoot, "original", filepath.FromSlash(f.Path))
		dst := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := atomicCopy(src, dst, fs.FileMode(f.Mode)); err != nil {
			return nil, fmt.Errorf("restore %s: %w", f.Path, err)
		}
	}
	_ = os.Remove(filepath.Join(root, manifestFN))
	_ = os.Remove(filepath.Join(root, runtimeFN))
	keyRel := bm.RuntimeKey
	if keyRel == "" {
		keyRel = DefaultKeyFile
	}
	keyPath := filepath.Join(root, filepath.FromSlash(keyRel))
	_ = os.Remove(keyPath)
	_ = os.Remove(filepath.Dir(keyPath))
	if !opts.KeepBackup {
		if err := os.RemoveAll(backupRoot); err != nil {
			return nil, fmt.Errorf("files restored, but could not remove backup: %w", err)
		}
	}
	return &RestoreResult{Root: root, Restored: len(bm.Files), BackupPath: backupRoot, BackupKept: opts.KeepBackup}, nil
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig(cfg.Root)
	if cfg.Mode == "" {
		cfg.Mode = defaults.Mode
	}
	if len(cfg.Includes) == 0 {
		cfg.Includes = defaults.Includes
	}
	cfg.Includes = uniqueNormalizedPaths(cfg.Includes)
	if len(cfg.Excludes) == 0 {
		cfg.Excludes = defaults.Excludes
	} else {
		cfg.Excludes = append(append([]string(nil), defaults.Excludes...), cfg.Excludes...)
	}
	cfg.Excludes = uniqueNormalizedPaths(cfg.Excludes)
	if cfg.KeyFile == "" {
		cfg.KeyFile = defaults.KeyFile
	}
	if cfg.Version == "" {
		cfg.Version = BuildVersion()
	}
	cfg.KeyFile = norm(cfg.KeyFile)
	return cfg
}

func uniqueNormalizedPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = norm(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func collectTargets(ctx context.Context, root string, cfg Config) ([]target, error) {
	var out []target
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := checkContext(ctx); err != nil {
			return err
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
			if excluded(rel, cfg.Excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if !shouldProcess(rel, cfg) {
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

func shouldProcess(rel string, cfg Config) bool {
	if strings.ToLower(filepath.Ext(rel)) != ".php" || excluded(rel, cfg.Excludes) {
		return false
	}
	if cfg.AllPHP {
		return true
	}
	for _, root := range cfg.Includes {
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

func norm(s string) string {
	s = filepath.ToSlash(filepath.Clean(s))
	return strings.TrimPrefix(s, "./")
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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
	defer os.Remove(tmpName)
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
	return os.Rename(tmpName, dst)
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

func phpHeaderComment(text string) ([]byte, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.Trim(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	if strings.ContainsRune(text, '\x00') {
		return nil, errors.New("header text may not contain NUL bytes")
	}
	if strings.Contains(text, "*/") {
		return nil, errors.New("header text may not contain */ because it would close the PHP comment")
	}
	if strings.Contains(text, "?>") {
		return nil, errors.New("header text may not contain ?>")
	}

	var b strings.Builder
	b.WriteString("\n/*\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(" *")
		if line != "" {
			b.WriteByte(' ')
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	b.WriteString(" */\n")
	return []byte(b.String()), nil
}

func addPHPHeader(source, header []byte) ([]byte, error) {
	if len(header) == 0 {
		return source, nil
	}
	tokens, err := lexPHP(source)
	if err != nil {
		return nil, err
	}
	for _, tok := range tokens {
		if tok.kind != tokenOpenTag {
			continue
		}
		insertAt := tok.pos + len(tok.text)
		out := make([]byte, 0, len(source)+len(header))
		out = append(out, source[:insertAt]...)
		out = append(out, header...)
		out = append(out, source[insertAt:]...)
		return out, nil
	}
	return nil, errors.New("PHP opening tag not found while adding header")
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

func sealedUnsafeSource(src []byte) bool {
	tokens, err := lexPHP(src)
	if err != nil {
		return true
	}
	for _, tok := range tokens {
		if tok.kind != tokenIdentifier {
			continue
		}
		switch strings.ToLower(tok.text) {
		case "__file__", "__dir__", "__halt_compiler":
			return true
		}
	}
	return false
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
