package localscan

import (
	"encoding/json"
	"fmt"
)

// CaskInfo is cask metadata from one read-only `brew info --cask
// --json=v2` call. Unlike the Homebrew core JSON API, brew info covers
// casks from every tap, installed or not.
type CaskInfo struct {
	Token             string   `json:"token"`
	Tap               string   `json:"tap"`
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Homepage          string   `json:"homepage"`
	Deprecated        bool     `json:"deprecated"`
	DeprecationDate   string   `json:"deprecation_date,omitempty"`
	DeprecationReason string   `json:"deprecation_reason,omitempty"`
	Replacement       string   `json:"replacement,omitempty"`
	Disabled          bool     `json:"disabled"`
	AppBundles        []string `json:"app_bundles"`
	Launchctl         []string `json:"launchctl"`
	LoginItems        []string `json:"login_items"`
}

// brewInfoOutput mirrors `brew info --cask --json=v2`.
type brewInfoOutput struct {
	Formulae []struct{} `json:"formulae"`
	Casks    []struct {
		Token             string                       `json:"token"`
		Tap               string                       `json:"tap"`
		Name              []string                     `json:"name"`
		Version           string                       `json:"version"`
		Homepage          string                       `json:"homepage"`
		Deprecated        bool                         `json:"deprecated"`
		DeprecationDate   string                       `json:"deprecation_date"`
		DeprecationReason json.RawMessage              `json:"deprecation_reason"`
		Replacement       *string                      `json:"replacement"`
		Disabled          bool                         `json:"disabled"`
		Artifacts         []map[string]json.RawMessage `json:"artifacts"`
	} `json:"casks"`
}

// CaskInfo fetches metadata for the given tokens (installed or not) via
// one read-only brew info invocation. Results are keyed by token;
// tokens brew does not know are absent from the map.
func (s *Scanner) CaskInfo(tokens []string) (map[string]CaskInfo, error) {
	if len(tokens) == 0 {
		return map[string]CaskInfo{}, nil
	}
	args := append([]string{"info", "--cask", "--json=v2"}, tokens...)
	out, err := s.Run.Run(s.BrewBin, args...)
	if err != nil {
		return nil, fmt.Errorf("localscan: brew info --cask: %w", err)
	}
	return parseCaskInfo([]byte(out.Stdout))
}

// parseCaskInfo decodes the brew info JSON. Artifact shapes follow the
// cask DSL and tolerate string and array forms.
func parseCaskInfo(body []byte) (map[string]CaskInfo, error) {
	var data brewInfoOutput
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("localscan: decoding brew info: %w", err)
	}
	infos := make(map[string]CaskInfo, len(data.Casks))
	for _, c := range data.Casks {
		if c.Token == "" {
			continue
		}
		info := CaskInfo{
			Token:             c.Token,
			Tap:               c.Tap,
			Version:           c.Version,
			Homepage:          c.Homepage,
			Deprecated:        c.Deprecated,
			DeprecationDate:   c.DeprecationDate,
			DeprecationReason: reasonString(c.DeprecationReason),
			Disabled:          c.Disabled,
		}
		if len(c.Name) > 0 {
			info.Name = c.Name[0]
		}
		if c.Replacement != nil {
			info.Replacement = *c.Replacement
		}
		for _, artifact := range c.Artifacts {
			if apps, ok := stringList(artifact["app"]); ok {
				info.AppBundles = append(info.AppBundles, apps...)
			}
			if paths, ok := stringList(artifact["launchctl"]); ok {
				info.Launchctl = append(info.Launchctl, paths...)
			}
			if items, ok := loginItems(artifact["login_item"]); ok {
				info.LoginItems = append(info.LoginItems, items...)
			}
		}
		infos[c.Token] = info
	}
	return infos, nil
}

// reasonString accepts a plain string or an {"reason": "..."} object.
func reasonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Reason
	}
	return ""
}

// stringList accepts a JSON string or array of strings.
func stringList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many, true
	}
	return nil, false
}

// loginItems accepts the documented login_item artifact shapes: a plain
// string, an array of strings, or an object with an "item" or "path" key.
func loginItems(raw json.RawMessage) ([]string, bool) {
	if items, ok := stringList(raw); ok {
		return items, true
	}
	var obj struct {
		Item string `json:"item"`
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		for _, v := range []string{obj.Item, obj.Path} {
			if v != "" {
				return []string{v}, true
			}
		}
	}
	return nil, false
}
