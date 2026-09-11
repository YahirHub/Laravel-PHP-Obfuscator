package phpcloak

import (
	"runtime/debug"
	"strings"
)

const modulePath = "github.com/YahirHub/Laravel-PHP-Obfuscator"

// Version can be overridden at link time. BuildVersion also discovers the
// semantic module version when phpcloak is imported by another Go program.
var Version = "dev"

func BuildVersion() string {
	if Version != "" && Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	if info.Main.Path == modulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath && dep.Version != "" && dep.Version != "(devel)" {
			return strings.TrimPrefix(dep.Version, "v")
		}
	}
	return Version
}

type Mode string

const (
	ModeSealed     Mode = "sealed"
	ModeAggressive Mode = "aggressive"
	ModeStrong     Mode = "strong"
	ModeSafe       Mode = "safe"

	DefaultKeyFile = "app/.phpcloak/phpcloak.key"
)

var DefaultExcludes = []string{
	"vendor", "node_modules", "storage", "bootstrap/cache", ".git",
	"resources/views", "public/build", "app/.phpcloak", ".debofuscated",
}

func (m Mode) Valid() bool {
	switch m {
	case ModeSealed, ModeAggressive, ModeStrong, ModeSafe:
		return true
	default:
		return false
	}
}

type Config struct {
	Root     string
	Mode     Mode
	Includes []string
	Excludes []string
	AllPHP   bool
	Force    bool
	Validate bool
	KeyEnv   string
	KeyFile  string
	Version  string
}

func DefaultConfig(root string) Config {
	return Config{
		Root:     root,
		Mode:     ModeSealed,
		Includes: []string{"app", "routes", "modules", "packages"},
		Excludes: append([]string(nil), DefaultExcludes...),
		Validate: true,
		KeyFile:  DefaultKeyFile,
		Version:  BuildVersion(),
	}
}

type Entry struct {
	Path           string `json:"path"`
	Action         string `json:"action"`
	Reason         string `json:"reason,omitempty"`
	OriginalSHA256 string `json:"original_sha256"`
	OutputSHA256   string `json:"output_sha256"`
}

type Summary struct {
	Minified int `json:"minified"`
	Mangled  int `json:"mangled"`
	Sealed   int `json:"sealed"`
	Fallback int `json:"fallback"`
}

type Manifest struct {
	Tool      string  `json:"tool"`
	Version   string  `json:"version"`
	CreatedAt string  `json:"created_at"`
	Mode      Mode    `json:"mode"`
	Root      string  `json:"root"`
	Backup    string  `json:"backup"`
	Entries   []Entry `json:"entries"`
	Summary   Summary `json:"summary"`
}

type RestoreOptions struct {
	Root       string
	KeepBackup bool
}

type RestoreResult struct {
	Root       string
	Restored   int
	BackupPath string
	BackupKept bool
}

type TransformStats struct {
	VariablesRenamed int
	StringsHidden    int
	MethodsHidden    int
	HelpersHidden    int
	RenameDisabled   bool
}

type TransformResult struct {
	Source []byte
	Stats  TransformStats
}

func (s TransformStats) Note() string {
	note := "renamed:" + itoa(s.VariablesRenamed) +
		";strings:" + itoa(s.StringsHidden) +
		";methods:" + itoa(s.MethodsHidden) +
		";helpers:" + itoa(s.HelpersHidden)
	if s.RenameDisabled {
		note += ";variables:preserved(dynamic-php)"
	}
	return note
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
