package mcpclient

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
)

var mcpServerIDRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,62}$`)

// MCPServerEntry is one MCP server in the Cursor-style mcpServers config file.
// Supported type values: "stdio", "streamable-http", "sse".
type MCPServerEntry struct {
	Type               string            `json:"type"`
	URL                string            `json:"url,omitempty"`
	Command            string            `json:"command,omitempty"`
	Args               []string          `json:"args,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	SystemPromptAppend string            `json:"system_prompt_append,omitempty"`
}

type mcpServersFileRoot struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

// ValidateMCPServerURL checks an http(s) MCP endpoint URL: scheme, host, and no embedded credentials.
func ValidateMCPServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse MCP url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("MCP url scheme must be http or https")
	}
	if u.User != nil {
		return fmt.Errorf("MCP url must not contain user info")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("MCP url has empty host")
	}
	return nil
}

func hasNUL(s string) bool {
	return strings.Contains(s, "\x00")
}

func (e MCPServerEntry) Validate(serverID string) error {
	if !mcpServerIDRe.MatchString(serverID) {
		return fmt.Errorf("invalid MCP server id %q (use letters, digits, underscore, hyphen; start with a letter)", serverID)
	}
	typ := strings.TrimSpace(strings.ToLower(e.Type))
	switch typ {
	case "":
		return fmt.Errorf("missing type")
	case "stdio":
		if strings.TrimSpace(e.Command) == "" {
			return fmt.Errorf("stdio server requires non-empty command")
		}
		if hasNUL(e.Command) {
			return fmt.Errorf("command contains NUL byte")
		}
		for _, a := range e.Args {
			if hasNUL(a) {
				return fmt.Errorf("args entry contains NUL byte")
			}
		}
		if err := validateStringMap(e.Env, "env"); err != nil {
			return err
		}
		if len(e.URL) > 0 {
			return fmt.Errorf("stdio server must not set url")
		}
		if len(e.Headers) > 0 {
			return fmt.Errorf("stdio server must not set headers")
		}
	case "streamable-http", "sse":
		urlStr := strings.TrimSpace(e.URL)
		if urlStr == "" {
			return fmt.Errorf("missing url")
		}
		if err := ValidateMCPServerURL(urlStr); err != nil {
			return fmt.Errorf("url: %w", err)
		}
		if strings.TrimSpace(e.Command) != "" || len(e.Args) > 0 {
			return fmt.Errorf("%s server must not set command or args", typ)
		}
		if len(e.Env) > 0 {
			return fmt.Errorf("%s server must not set env", typ)
		}
		if err := validateStringMap(e.Headers, "headers"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported type %q (supported: stdio, streamable-http, sse)", e.Type)
	}
	if hasNUL(e.SystemPromptAppend) {
		return fmt.Errorf("system_prompt_append contains NUL byte")
	}
	return nil
}

func validateStringMap(m map[string]string, field string) error {
	for k, v := range m {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%s: empty key", field)
		}
		if strings.ContainsRune(k, '=') {
			return fmt.Errorf("%s: key %q must not contain '='", field, k)
		}
		if hasNUL(k) || hasNUL(v) {
			return fmt.Errorf("%s: NUL byte in key or value", field)
		}
	}
	return nil
}

// MergedProcessEnv returns os.Environ() with entries from overrides applied (add or replace by key).
func MergedProcessEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	env := slices.Clone(os.Environ())
	for k, v := range overrides {
		kv := k + "=" + v
		replaced := false
		for i, e := range env {
			ek, _, ok := strings.Cut(e, "=")
			if ok && ek == k {
				env[i] = kv
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, kv)
		}
	}
	return env
}

// LoadMCPServersFromFile reads ASSISTANT_BOT_MCP_SERVERS_FILE.
func LoadMCPServersFromFile() (map[string]MCPServerEntry, []string) {
	path := strings.TrimSpace(os.Getenv("ASSISTANT_BOT_MCP_SERVERS_FILE"))
	if path == "" {
		return nil, []string{"ASSISTANT_BOT_MCP_SERVERS_FILE is not set; MCP integration is disabled"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read MCP servers file %q: %v", path, err)}
	}
	data = trimLeadingBOM(data)
	if strings.TrimSpace(string(data)) == "" {
		return nil, []string{fmt.Sprintf("MCP servers file %q is empty; MCP integration is disabled", path)}
	}
	var root mcpServersFileRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, []string{fmt.Sprintf("MCP servers file %q: invalid JSON: %v", path, err)}
	}
	if root.MCPServers == nil {
		return nil, []string{fmt.Sprintf("MCP servers file %q: missing \"mcpServers\" object; MCP integration is disabled", path)}
	}
	if len(root.MCPServers) == 0 {
		return nil, []string{fmt.Sprintf("MCP servers file %q: \"mcpServers\" is empty; MCP integration is disabled", path)}
	}

	out := make(map[string]MCPServerEntry, len(root.MCPServers))
	var warnings []string
	for id, rawMsg := range root.MCPServers {
		var entry MCPServerEntry
		if err := json.Unmarshal(rawMsg, &entry); err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: invalid entry JSON: %v", id, err))
			continue
		}
		if err := entry.Validate(id); err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: %v", id, err))
			continue
		}
		out[id] = entry
	}
	if len(out) == 0 {
		w := append([]string(nil), warnings...)
		w = append(w, fmt.Sprintf("no valid MCP servers in %q after validation; MCP integration is disabled", path))
		return nil, w
	}
	return out, warnings
}

func trimLeadingBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
