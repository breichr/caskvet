// Package localscan inspects the macOS-local state of installed Homebrew
// casks: installed cask versions, app bundle locations, code signing and
// Gatekeeper state, LaunchAgents, and login items.
//
// It is the only package allowed to touch the filesystem outside the
// collector cache, and it never modifies anything — read-only, no
// exceptions.
package localscan

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Output is the captured result of one local command execution.
type Output struct {
	Stdout string
	Stderr string
}

// Runner executes a local command. Production code uses ExecRunner; tests
// inject recorded fixture output. The error may be an *exec.ExitError —
// callers that care about exit status use errors.As on it.
type Runner interface {
	Run(name string, args ...string) (Output, error)
}

// ExecRunner runs real commands via os/exec.
type ExecRunner struct{}

// Run executes name with args and captures stdout and stderr separately.
func (ExecRunner) Run(name string, args ...string) (Output, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Output{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

// ExitCode extracts the process exit status from a Runner error.
// A nil error is code 0; a non-process error is -1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// Scanner inspects local state. All paths are overridable so tests can
// point the scanner at fixture trees instead of the real machine.
type Scanner struct {
	Run Runner

	// BrewBin is the brew executable path (default: "brew").
	BrewBin string

	// CaskroomDir is the Homebrew Caskroom (default: derived from
	// `brew --prefix caskroom`).
	CaskroomDir string

	// ApplicationsDirs are searched, in order, for app bundles
	// (default: /Applications, ~/Applications).
	ApplicationsDirs []string

	// LaunchAgentDirs are scanned for agent plists (default:
	// /Library/LaunchAgents and ~/Library/LaunchAgents).
	LaunchAgentDirs []string

	// Home overrides the user home directory for path expansion
	// (default: os.UserHomeDir).
	Home string
}

// ScannerOption configures a Scanner.
type ScannerOption func(*Scanner)

// New returns a Scanner with production defaults. Dirs that cannot be
// resolved are skipped at scan time rather than failing here.
func New(opts ...ScannerOption) *Scanner {
	s := &Scanner{
		Run:     ExecRunner{},
		BrewBin: "brew",
	}
	if home, err := os.UserHomeDir(); err == nil {
		s.Home = home
		s.ApplicationsDirs = []string{"/Applications", filepath.Join(home, "Applications")}
		s.LaunchAgentDirs = []string{
			"/Library/LaunchAgents",
			filepath.Join(home, "Library", "LaunchAgents"),
		}
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithRunner overrides the command runner.
func WithRunner(r Runner) ScannerOption {
	return func(s *Scanner) { s.Run = r }
}

// WithHome overrides the home directory used to expand default paths.
func WithHome(home string) ScannerOption {
	return func(s *Scanner) {
		s.Home = home
		s.ApplicationsDirs = []string{"/Applications", filepath.Join(home, "Applications")}
		s.LaunchAgentDirs = []string{
			"/Library/LaunchAgents",
			filepath.Join(home, "Library", "LaunchAgents"),
		}
	}
}

// WithCaskroomDir overrides the Caskroom location.
func WithCaskroomDir(dir string) ScannerOption {
	return func(s *Scanner) { s.CaskroomDir = dir }
}

// WithApplicationsDirs overrides the app bundle search directories.
func WithApplicationsDirs(dirs []string) ScannerOption {
	return func(s *Scanner) { s.ApplicationsDirs = dirs }
}

// WithLaunchAgentDirs overrides the LaunchAgent scan directories.
func WithLaunchAgentDirs(dirs []string) ScannerOption {
	return func(s *Scanner) { s.LaunchAgentDirs = dirs }
}

// discoverCaskroom resolves the Caskroom path once per Scanner. It is
// `<brew --prefix>/Caskroom`.
func (s *Scanner) discoverCaskroom() (string, error) {
	if s.CaskroomDir != "" {
		return s.CaskroomDir, nil
	}
	out, err := s.Run.Run(s.BrewBin, "--prefix")
	if err != nil {
		return "", fmt.Errorf("localscan: discovering Caskroom: %w", err)
	}
	prefix := strings.TrimSpace(out.Stdout)
	if prefix == "" {
		return "", fmt.Errorf("localscan: discovering Caskroom: empty output")
	}
	s.CaskroomDir = filepath.Join(prefix, "Caskroom")
	return s.CaskroomDir, nil
}
