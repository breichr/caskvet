package localscan

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LaunchAgent is one discovered LaunchAgent plist.
type LaunchAgent struct {
	// Path is the plist file location on disk.
	Path string
	// Label is the LaunchAgent label (may be empty for unparseable files).
	Label string
	// Program is the first ProgramArguments entry, or the Program value.
	Program string
	// RunAtLoad is true when the agent starts at login.
	RunAtLoad bool
	// ParseError is set when the plist could not be parsed; the file is
	// still reported because presence itself is a transparency fact.
	ParseError string
}

// LoginItem is one login item reported by System Events.
type LoginItem struct {
	Name string
	Path string
}

// LaunchAgents scans the configured LaunchAgent directories for plist
// files and parses label, program, and RunAtLoad from each. Read-only.
func (s *Scanner) LaunchAgents() ([]LaunchAgent, error) {
	var agents []LaunchAgent
	seen := make(map[string]bool)
	for _, dir := range s.LaunchAgentDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.plist"))
		if err != nil {
			return nil, fmt.Errorf("localscan: scanning %s: %w", dir, err)
		}
		sort.Strings(matches)
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true
			agents = append(agents, parseLaunchAgentPlist(path))
		}
	}
	return agents, nil
}

// parseLaunchAgentPlist reads one plist and extracts the transparency
// facts. Only XML plists are parsed; a binary or unreadable plist is
// reported with ParseError set rather than hidden.
func parseLaunchAgentPlist(path string) LaunchAgent {
	agent := LaunchAgent{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		agent.ParseError = err.Error()
		return agent
	}
	plist, err := parsePlistDict(data)
	if err != nil {
		agent.ParseError = err.Error()
		return agent
	}
	agent.Label, _ = plist["Label"].(string)
	agent.RunAtLoad, _ = plist["RunAtLoad"].(bool)
	if args, ok := plist["ProgramArguments"].([]string); ok && len(args) > 0 {
		agent.Program = args[0]
	} else if program, ok := plist["Program"].(string); ok {
		agent.Program = program
	}
	return agent
}

// parsePlistDict decodes the top-level dict of a simple XML property
// list. Values of interest are strings, booleans, and arrays of strings.
// Binary or non-XML plists yield an error.
func parsePlistDict(data []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result := make(map[string]any)
	depth := 0
	keyDepth := -1
	pendingKey := ""
	arrayKey := ""
	var array []string

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing plist: %w", err)
		}
		switch tok := token.(type) {
		case xml.StartElement:
			depth++
			switch tok.Name.Local {
			case "key":
				keyDepth = depth
			case "string":
				var s string
				_ = decoder.DecodeElement(&s, &tok)
				switch {
				case arrayKey != "":
					array = append(array, s)
				case pendingKey != "":
					result[pendingKey] = s
					pendingKey = ""
				}
			case "array":
				if pendingKey != "" {
					arrayKey = pendingKey
					array = nil
					pendingKey = ""
				}
			case "true", "false":
				if pendingKey != "" {
					result[pendingKey] = tok.Name.Local == "true"
					pendingKey = ""
				}
			}
		case xml.CharData:
			if keyDepth != -1 && depth == keyDepth {
				pendingKey = strings.TrimSpace(string(tok))
				keyDepth = -1
			}
		case xml.EndElement:
			switch tok.Name.Local {
			case "key":
				keyDepth = -1
			case "array":
				if arrayKey != "" {
					result[arrayKey] = array
					arrayKey = ""
				}
			}
			depth--
		}
	}
}

// LoginItems queries System Events for the user's login items. The
// osascript query requires Automation permission; on failure the error
// is returned so callers report "unknown" rather than an empty list.
func (s *Scanner) LoginItems() ([]LoginItem, error) {
	out, err := s.Run.Run("osascript",
		"-e", `tell application "System Events" to get {name, path} of every login item`)
	if err != nil {
		return nil, fmt.Errorf("localscan: enumerating login items: %w", err)
	}

	// AppleScript returns one "Name, /path" pair per line.
	var items []LoginItem
	for _, line := range strings.Split(out.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		item := LoginItem{Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			item.Path = strings.TrimSpace(parts[1])
		}
		items = append(items, item)
	}
	return items, nil
}
