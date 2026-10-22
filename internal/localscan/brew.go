package localscan

import (
	"encoding/json"
	"fmt"
)

// InstalledCask is one cask installed on this machine, as reported by
// Homebrew.
type InstalledCask struct {
	Token         string
	Version       string
	PinnedVersion string
}

// brewListOutput mirrors `brew list --cask --json --versions`.
type brewListOutput struct {
	Formulae []struct{} `json:"formulae"`
	Casks    []struct {
		Token         string   `json:"token"`
		Versions      []string `json:"versions"`
		PinnedVersion *string  `json:"pinned_version"`
	} `json:"casks"`
}

// InstalledCasks enumerates the installed casks. It never contacts the
// network; `brew list` reads local state only.
func (s *Scanner) InstalledCasks() ([]InstalledCask, error) {
	out, err := s.Run.Run(s.BrewBin, "list", "--cask", "--json", "--versions")
	if err != nil {
		return nil, fmt.Errorf("localscan: brew list --cask: %w", err)
	}
	return parseInstalledCasks([]byte(out.Stdout))
}

// CaskTaps returns the tap each cask came from, via one read-only
// `brew info --cask --json=v2` invocation for all tokens. Third-party
// taps matter: those casks are not in the Homebrew core JSON API.
func (s *Scanner) CaskTaps(tokens []string) (map[string]string, error) {
	if len(tokens) == 0 {
		return map[string]string{}, nil
	}
	args := append([]string{"info", "--cask", "--json=v2"}, tokens...)
	out, err := s.Run.Run(s.BrewBin, args...)
	if err != nil {
		return nil, fmt.Errorf("localscan: brew info --cask: %w", err)
	}
	var data struct {
		Casks []struct {
			Token string `json:"token"`
			Tap   string `json:"tap"`
		} `json:"casks"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &data); err != nil {
		return nil, fmt.Errorf("localscan: decoding brew info: %w", err)
	}
	taps := make(map[string]string, len(data.Casks))
	for _, c := range data.Casks {
		if c.Token != "" {
			taps[c.Token] = c.Tap
		}
	}
	return taps, nil
}

// parseInstalledCasks decodes the brew list JSON. A version list with
// multiple entries yields the first (most recent install); an empty list
// yields an empty version, which callers must treat as unknown.
func parseInstalledCasks(body []byte) ([]InstalledCask, error) {
	var data brewListOutput
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("localscan: decoding brew list: %w", err)
	}
	casks := make([]InstalledCask, 0, len(data.Casks))
	for _, c := range data.Casks {
		if c.Token == "" {
			continue
		}
		version := ""
		if len(c.Versions) > 0 {
			version = c.Versions[0]
		}
		pinned := ""
		if c.PinnedVersion != nil {
			pinned = *c.PinnedVersion
		}
		casks = append(casks, InstalledCask{
			Token:         c.Token,
			Version:       version,
			PinnedVersion: pinned,
		})
	}
	return casks, nil
}
