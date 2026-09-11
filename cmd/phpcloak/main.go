package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"
)

var version = "dev"

func main() {
	root, command, args, err := parseCLI(os.Args[1:])
	if err != nil {
		failUsage(err)
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
		failUsage(fmt.Errorf("unknown command %q", command))
	}
}

func runObfuscate(root string, args []string) error {
	f := flag.NewFlagSet("obfuscate", flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	mode := f.String("mode", string(phpcloak.ModeSealed), "sealed, aggressive, strong or safe")
	include := f.String("include", "app,routes,modules,packages", "include roots")
	exclude := f.String("exclude", "", "extra exclusions")
	allPHP := f.Bool("all-php", false, "process all PHP files")
	force := f.Bool("force", false, "replace an existing backup")
	validate := true
	f.BoolVar(&validate, "validate", true, "validate transformed PHP with the native Go validator")
	f.BoolVar(&validate, "verify", true, "alias of --validate")
	keyEnv := f.String("key-env", "", "optional runtime key environment variable")
	keyFile := f.String("key-file", phpcloak.DefaultKeyFile, "local runtime key path relative to project")
	headerText := f.String("header-text", "", `visible PHP header text; literal \n creates line breaks`)
	headerFile := f.String("header-file", "", "read visible PHP header text from a UTF-8 file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	header, err := resolveHeaderText(*headerText, *headerFile)
	if err != nil {
		return err
	}

	cfg := phpcloak.DefaultConfig(root)
	cfg.Mode = phpcloak.Mode(*mode)
	cfg.Includes = splitList(*include)
	cfg.Excludes = splitList(*exclude)
	cfg.AllPHP = *allPHP
	cfg.Force = *force
	cfg.Validate = validate
	cfg.KeyEnv = *keyEnv
	cfg.KeyFile = *keyFile
	cfg.HeaderText = header
	cfg.Version = version

	manifest, err := phpcloak.Protect(context.Background(), cfg)
	if err != nil {
		return err
	}
	fmt.Printf("phpcloak %s complete\n", version)
	fmt.Printf("  root:      %s\n", manifest.Root)
	fmt.Printf("  backup:    %s\n", manifest.Backup)
	fmt.Printf("  mode:      %s\n", manifest.Mode)
	fmt.Printf("  files:     %d\n", len(manifest.Entries))
	fmt.Printf("  sealed:    %d\n", manifest.Summary.Sealed)
	fmt.Printf("  mangled:   %d\n", manifest.Summary.Mangled)
	fmt.Printf("  fallback:  %d\n", manifest.Summary.Fallback)
	if manifest.Mode == phpcloak.ModeSealed {
		fmt.Printf("  runtime key: %s\n", cfg.KeyFile)
		fmt.Printf("  WARNING: never deploy .debofuscated; it contains original source\n")
	}
	return nil
}

func runRestore(root string, args []string) error {
	f := flag.NewFlagSet("restore", flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	keep := f.Bool("keep-backup", false, "restore originals but keep .debofuscated")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	result, err := phpcloak.Restore(context.Background(), phpcloak.RestoreOptions{Root: root, KeepBackup: *keep})
	if err != nil {
		return err
	}
	fmt.Println("phpcloak restore complete")
	fmt.Printf("  root:     %s\n", result.Root)
	fmt.Printf("  restored: %d files\n", result.Restored)
	if result.BackupKept {
		fmt.Printf("  backup:   kept at %s\n", result.BackupPath)
	} else {
		fmt.Println("  backup:   removed")
	}
	return nil
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

func resolveHeaderText(text, file string) (string, error) {
	if text != "" && file != "" {
		return "", errors.New("--header-text and --header-file are mutually exclusive")
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read header file: %w", err)
		}
		return string(b), nil
	}
	text = strings.ReplaceAll(text, `\r\n`, "\n")
	text = strings.ReplaceAll(text, `\n`, "\n")
	text = strings.ReplaceAll(text, `\r`, "\n")
	return text, nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func usage() {
	fmt.Print(`phpcloak - Laravel/PHP source protection implemented in pure Go

Usage:
  phpcloak --root /path/to/laravel obfuscate [options]
  phpcloak --root /path/to/laravel restore [options]
  phpcloak version

Obfuscate options:
  --mode sealed|aggressive|strong|safe  protection mode (default sealed)
  --include LIST         roots to protect (default app,routes,modules,packages)
  --exclude LIST         extra comma-separated exclusions
  --all-php              process every PHP file except excluded paths
  --key-env NAME         optional environment override for sealed key
  --key-file PATH        local key file (default app/.phpcloak/phpcloak.key)
  --header-text TEXT     visible comment header; literal \n creates line breaks
  --header-file PATH     read a multiline visible comment header from a UTF-8 file
  --validate=true|false  native structural validation before replacement (default true)
  --verify=true|false    compatibility alias of --validate
  --force                replace an existing .debofuscated backup

No PHP executable is required by phpcloak.
`)
}

func failUsage(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	usage()
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
