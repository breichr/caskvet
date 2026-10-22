package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// tokenPattern is the character set of Homebrew cask tokens, including
// the "@" of versioned tokens such as "1password-cli@1".
var tokenPattern = regexp.MustCompile(`^[a-z0-9@-]+$`)

// CaskMeta is the upstream (Homebrew JSON API) view of one cask. Missing
// upstream data stays zero-valued here; interpreting "missing" as
// "unknown" is the score layer's job.
type CaskMeta struct {
	Token             string   `json:"token"`
	Names             []string `json:"names"`
	Desc              string   `json:"desc"`
	Homepage          string   `json:"homepage"`
	Version           string   `json:"version"`
	Deprecated        bool     `json:"deprecated"`
	DeprecationDate   string   `json:"deprecation_date,omitempty"`
	DeprecationReason string   `json:"deprecation_reason,omitempty"`
	ReplacementToken  string   `json:"replacement_token,omitempty"`
	Disabled          bool     `json:"disabled"`
	DisableDate       string   `json:"disable_date,omitempty"`
	DisableReason     string   `json:"disable_reason,omitempty"`
	AppBundles        []string `json:"app_bundles"`
	LaunchctlPaths    []string `json:"launchctl_paths"`
	LoginItems        []string `json:"login_items"`
}

// rawCask mirrors the Homebrew JSON API cask object. Field shapes follow
// the documented schema; variants (string vs object) are tolerated.
type rawCask struct {
	Token             string                       `json:"token"`
	Name              []string                     `json:"name"`
	Desc              string                       `json:"desc"`
	Homepage          string                       `json:"homepage"`
	Version           string                       `json:"version"`
	Deprecated        bool                         `json:"deprecated"`
	DeprecationDate   string                       `json:"deprecation_date"`
	DeprecationReason json.RawMessage              `json:"deprecation_reason"`
	Replacement       string                       `json:"replacement"`
	Disabled          bool                         `json:"disabled"`
	DisableDate       string                       `json:"disable_date"`
	DisableReason     string                       `json:"disable_reason"`
	Artifacts         []map[string]json.RawMessage `json:"artifacts"`
}

// CaskResult pairs a cask token with its metadata or fetch error.
type CaskResult struct {
	Token string
	Meta  CaskMeta
	Err   error
}

// caskURL returns the JSON API URL for a cask token.
func (c *Client) caskURL(token string) string {
	return c.homebrewBaseURL + "/" + token + ".json"
}

// Cask fetches one cask's upstream metadata.
func (c *Client) Cask(ctx context.Context, token string) (CaskMeta, error) {
	if !tokenPattern.MatchString(token) {
		return CaskMeta{}, fmt.Errorf("collector: %q: %w", token, ErrInvalidToken)
	}
	body, err := c.fetch(ctx, c.caskURL(token), false)
	if err != nil {
		return CaskMeta{}, err
	}
	return parseCaskMeta(body)
}

// Casks fetches metadata for many casks with bounded concurrency. Results
// are keyed by token; individual failures do not fail the batch.
func (c *Client) Casks(ctx context.Context, tokens []string) map[string]CaskResult {
	results := make(map[string]CaskResult, len(tokens))
	var mu sync.Mutex
	seen := make(map[string]bool, len(tokens))

	unique := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}

	merge(ctx, unique, c.maxWorkers, func(ctx context.Context, token string) {
		meta, err := c.Cask(ctx, token)
		mu.Lock()
		results[token] = CaskResult{Token: token, Meta: meta, Err: err}
		mu.Unlock()
	})
	return results
}

// parseCaskMeta decodes a Homebrew JSON API cask payload.
func parseCaskMeta(body []byte) (CaskMeta, error) {
	var raw rawCask
	if err := json.Unmarshal(body, &raw); err != nil {
		return CaskMeta{}, fmt.Errorf("collector: decoding cask: %w", err)
	}
	if raw.Token == "" {
		return CaskMeta{}, fmt.Errorf("collector: decoding cask: missing token")
	}
	meta := CaskMeta{
		Token:             raw.Token,
		Names:             raw.Name,
		Desc:              raw.Desc,
		Homepage:          raw.Homepage,
		Version:           raw.Version,
		Deprecated:        raw.Deprecated,
		DeprecationDate:   raw.DeprecationDate,
		DeprecationReason: reasonString(raw.DeprecationReason),
		ReplacementToken:  raw.Replacement,
		Disabled:          raw.Disabled,
		DisableDate:       raw.DisableDate,
		DisableReason:     raw.DisableReason,
	}
	for _, artifact := range raw.Artifacts {
		if apps, ok := stringList(artifact["app"]); ok {
			meta.AppBundles = append(meta.AppBundles, apps...)
		}
		if paths, ok := stringList(artifact["launchctl"]); ok {
			meta.LaunchctlPaths = append(meta.LaunchctlPaths, paths...)
		}
		if items, ok := loginItems(artifact["login_item"]); ok {
			meta.LoginItems = append(meta.LoginItems, items...)
		}
	}
	sort.Strings(meta.AppBundles)
	return meta, nil
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
