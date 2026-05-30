package mcpclient

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

var mcpServerIDRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,62}$`)

// MCPServerEntry is one tool server in the mcpServers YAML config file.
// Supported type values: "stdio", "streamable-http", "sse", "openrouter_tool".
type MCPServerEntry struct {
	Type               string            `yaml:"type"`
	URL                string            `yaml:"url,omitempty"`
	Command            string            `yaml:"command,omitempty"`
	Args               []string          `yaml:"args,omitempty"`
	Env                map[string]string `yaml:"env,omitempty"`
	Headers            map[string]string `yaml:"headers,omitempty"`
	SystemPromptAppend string            `yaml:"system_prompt_append,omitempty"`
	ToolsFilter        string            `yaml:"tools_filter,omitempty"`
	Tool               string            `yaml:"tool,omitempty"`
	Tasks              []string          `yaml:"tasks,omitempty"`
	Parameters         map[string]any    `yaml:"parameters,omitempty"`
}

var defaultToolTasks = []string{llm.TaskGenerateChatReply, llm.TaskChatWithTools}

type mcpServersFileRoot struct {
	MCPServers map[string]MCPServerEntry `yaml:"mcpServers"`
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
		if err := e.validateOpenRouterExclusiveFields("stdio"); err != nil {
			return err
		}
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
		if err := e.validateTransportOnlyFields(typ); err != nil {
			return err
		}
		urlStr := strings.TrimSpace(e.URL)
		if urlStr == "" {
			return fmt.Errorf("missing url")
		}
		if err := ValidateMCPServerURL(urlStr); err != nil {
			return fmt.Errorf("url: %w", err)
		}
		if err := validateStringMap(e.Headers, "headers"); err != nil {
			return err
		}
	case "openrouter_tool":
		if err := e.validateOpenRouterOnlyFields(); err != nil {
			return err
		}
		if _, err := normalizeOpenRouterTool(e.Tool); err != nil {
			return err
		}
		if len(e.Tasks) == 0 {
			return fmt.Errorf("openrouter_tool requires non-empty tasks")
		}
	default:
		return fmt.Errorf("unsupported type %q (supported: stdio, streamable-http, sse, openrouter_tool)", e.Type)
	}
	if err := validateTaskIDs(e.Tasks); err != nil {
		return err
	}
	if hasNUL(e.SystemPromptAppend) {
		return fmt.Errorf("system_prompt_append contains NUL byte")
	}
	if hasNUL(e.ToolsFilter) {
		return fmt.Errorf("tools_filter contains NUL byte")
	}
	if _, err := e.toolsFilterRE(); err != nil {
		return err
	}
	return nil
}

func (e MCPServerEntry) validateOpenRouterExclusiveFields(typ string) error {
	if strings.TrimSpace(e.Tool) != "" {
		return fmt.Errorf("%s server must not set tool", typ)
	}
	if len(e.Parameters) > 0 {
		return fmt.Errorf("%s server must not set parameters", typ)
	}
	return nil
}

func (e MCPServerEntry) validateTransportOnlyFields(typ string) error {
	if err := e.validateOpenRouterExclusiveFields(typ); err != nil {
		return err
	}
	if strings.TrimSpace(e.Command) != "" || len(e.Args) > 0 {
		return fmt.Errorf("%s server must not set command or args", typ)
	}
	if len(e.Env) > 0 {
		return fmt.Errorf("%s server must not set env", typ)
	}
	return nil
}

func (e MCPServerEntry) validateOpenRouterOnlyFields() error {
	if strings.TrimSpace(e.Command) != "" || len(e.Args) > 0 {
		return fmt.Errorf("openrouter_tool must not set command or args")
	}
	if strings.TrimSpace(e.URL) != "" {
		return fmt.Errorf("openrouter_tool must not set url")
	}
	if len(e.Env) > 0 {
		return fmt.Errorf("openrouter_tool must not set env")
	}
	if len(e.Headers) > 0 {
		return fmt.Errorf("openrouter_tool must not set headers")
	}
	if strings.TrimSpace(e.ToolsFilter) != "" {
		return fmt.Errorf("openrouter_tool must not set tools_filter")
	}
	return nil
}

func validateTaskIDs(tasks []string) error {
	for _, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			return fmt.Errorf("tasks contains empty entry")
		}
		if !llm.IsTaskID(task) {
			return fmt.Errorf("unknown task %q", task)
		}
	}
	return nil
}

func entryTasks(entry MCPServerEntry) []string {
	if len(entry.Tasks) > 0 {
		return append([]string(nil), entry.Tasks...)
	}
	return append([]string(nil), defaultToolTasks...)
}

func entryMatchesTask(entryTasks []string, task string) bool {
	for _, t := range entryTasks {
		if t == task {
			return true
		}
	}
	return false
}

func (e MCPServerEntry) toolsFilterRE() (*regexp.Regexp, error) {
	pattern := strings.TrimSpace(e.ToolsFilter)
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("tools_filter: %w", err)
	}
	return re, nil
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
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, []string{fmt.Sprintf("MCP servers file %q: invalid YAML: %v", path, err)}
	}
	if root.MCPServers == nil {
		return nil, []string{fmt.Sprintf("MCP servers file %q: missing \"mcpServers\" object; MCP integration is disabled", path)}
	}
	if len(root.MCPServers) == 0 {
		return nil, []string{fmt.Sprintf("MCP servers file %q: \"mcpServers\" is empty; MCP integration is disabled", path)}
	}

	out := make(map[string]MCPServerEntry, len(root.MCPServers))
	var warnings []string
	for id, entry := range root.MCPServers {
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
