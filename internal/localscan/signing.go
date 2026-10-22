package localscan

import (
	"fmt"
	"strings"
)

// CodesignInfo is the code-signing state of one app bundle, as reported
// by `codesign -dv --verbose=4`.
type CodesignInfo struct {
	// Signed is true when the bundle carries a valid non-ad-hoc signature.
	Signed bool
	// AdHoc is true when the bundle is signed ad hoc (no identity).
	AdHoc bool
	// TeamIdentifier is the Apple team ID (10-character alphanumeric).
	TeamIdentifier string
	// Authorities are the full certificate chain lines, e.g.
	// "Developer ID Application: Name (TEAMID)".
	Authorities []string
	// Identifier is the bundle identifier.
	Identifier string
	// Executable is the path of the signed executable.
	Executable string
}

// GatekeeperInfo is the Gatekeeper assessment of one app bundle, as
// reported by `spctl -a -vv`.
type GatekeeperInfo struct {
	// Accepted is true when Gatekeeper accepts the bundle.
	Accepted bool
	// Source is the spctl source, e.g. "Notarized Developer ID".
	Source string
	// Origin is the signing certificate description.
	Origin string
	// Message is the raw first line of spctl output.
	Message string
}

// Notarized reports whether Gatekeeper confirmed notarization. Apple
// system software counts as trusted, not notarized; the distinction is
// left to the caller.
func (g GatekeeperInfo) Notarized() bool {
	return strings.Contains(g.Source, "Notarized")
}

// Codesign runs codesign -dv --verbose=4 against an app bundle path.
// A bundle that is not signed at all is not an error: it is reported as
// Signed=false so the score layer can explain Gatekeeper consequences.
func (s *Scanner) Codesign(appPath string) (CodesignInfo, error) {
	out, err := s.Run.Run("codesign", "-dv", "--verbose=4", appPath)
	// codesign writes its details to stderr, and exits non-zero when the
	// bundle is unsigned. Combined text is the parseable surface.
	combined := out.Stdout + out.Stderr
	if ExitCode(err) == -1 {
		return CodesignInfo{}, fmt.Errorf("localscan: codesign %s: %w", appPath, err)
	}
	info := ParseCodesignOutput(combined)
	if ExitCode(err) != 0 && !info.Unsigned() {
		return info, fmt.Errorf("localscan: codesign %s: exit %d: %s", appPath, ExitCode(err), firstLine(combined))
	}
	return info, nil
}

// Unsigned reports whether the output says the bundle carries no
// signature at all.
func (c CodesignInfo) Unsigned() bool {
	return !c.Signed && !c.AdHoc
}

// ParseCodesignOutput decodes codesign -dv --verbose=4 text. It is pure
// and tested against recorded fixture output.
func ParseCodesignOutput(text string) CodesignInfo {
	var info CodesignInfo
	for _, line := range lines(text) {
		switch {
		case strings.HasPrefix(line, "Executable="):
			info.Executable = strings.TrimPrefix(line, "Executable=")
		case strings.HasPrefix(line, "Identifier="):
			info.Identifier = strings.TrimPrefix(line, "Identifier=")
		case strings.HasPrefix(line, "Authority="):
			info.Authorities = append(info.Authorities, strings.TrimPrefix(line, "Authority="))
		case strings.HasPrefix(line, "TeamIdentifier="):
			info.TeamIdentifier = strings.TrimPrefix(line, "TeamIdentifier=")
		case line == "Signature=adhoc":
			info.AdHoc = true
		case strings.Contains(line, "code object is not signed at all"):
			// Unsigned bundles: no further fields will appear.
			return CodesignInfo{}
		}
	}
	info.Signed = info.TeamIdentifier != "" && !info.AdHoc
	if info.TeamIdentifier == "not set" {
		info.TeamIdentifier = ""
	}
	return info
}

// Gatekeeper runs spctl -a -vv against an app bundle path. A rejection
// (non-zero exit) is a valid result, not an error: Accepted=false.
func (s *Scanner) Gatekeeper(appPath string) (GatekeeperInfo, error) {
	out, err := s.Run.Run("spctl", "-a", "-vv", appPath)
	combined := out.Stdout + out.Stderr
	if ExitCode(err) == -1 {
		return GatekeeperInfo{}, fmt.Errorf("localscan: spctl %s: %w", appPath, err)
	}
	return ParseGatekeeperOutput(combined), nil
}

// ParseGatekeeperOutput decodes spctl -a -vv text. It is pure and tested
// against recorded fixture output.
func ParseGatekeeperOutput(text string) GatekeeperInfo {
	var info GatekeeperInfo
	for _, line := range lines(text) {
		switch {
		case strings.HasPrefix(line, "source="):
			info.Source = strings.TrimPrefix(line, "source=")
		case strings.HasPrefix(line, "origin="):
			info.Origin = strings.TrimPrefix(line, "origin=")
		case strings.HasSuffix(line, ": accepted") || strings.HasSuffix(line, ": rejected"):
			info.Message = line
			info.Accepted = strings.HasSuffix(line, ": accepted")
		}
	}
	return info
}

// lines splits text into trimmed, non-empty lines.
func lines(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstLine(text string) string {
	for _, line := range lines(text) {
		return line
	}
	return ""
}
