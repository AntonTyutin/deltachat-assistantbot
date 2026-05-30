package mcpclient

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidateMCPServerURL(t *testing.T) {
	t.Parallel()
	if err := ValidateMCPServerURL("http://127.0.0.1:8080/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMCPServerURL("http://10.0.0.5/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMCPServerURL("https://example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMCPServerURL("ftp://10.0.0.5/mcp"); err == nil {
		t.Fatal("expected error for ftp")
	}
	if err := ValidateMCPServerURL("http://user@10.0.0.5/mcp"); err == nil {
		t.Fatal("expected error for userinfo")
	}
	if err := ValidateMCPServerURL("http:///nohost"); err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestMCPServerEntryValidateStreamableHTTP(t *testing.T) {
	t.Parallel()
	e := MCPServerEntry{
		Type:               "streamable-http",
		URL:                "http://127.0.0.1:9/mcp",
		SystemPromptAppend: "Use English city names for weather calls.",
	}
	if err := e.Validate("weather"); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate("bad id"); err == nil {
		t.Fatal("expected invalid id error")
	}
}

func TestMCPServerEntryValidateStdio(t *testing.T) {
	t.Parallel()
	ok := MCPServerEntry{Type: "stdio", Command: "/bin/true", Args: []string{"--help"}}
	if err := ok.Validate("local"); err != nil {
		t.Fatal(err)
	}
	bad := MCPServerEntry{Type: "stdio"}
	if err := bad.Validate("local"); err == nil {
		t.Fatal("expected error without command")
	}
}

func TestMergedProcessEnv(t *testing.T) {
	t.Parallel()
	baseN := len(os.Environ())
	got := MergedProcessEnv(map[string]string{
		"ASSISTANTBOT_MCP_TEST_VAR": "v1",
	})
	if len(got) < baseN {
		t.Fatalf("expected at least %d env entries, got %d", baseN, len(got))
	}
	var found bool
	for _, e := range got {
		if strings.HasPrefix(e, "ASSISTANTBOT_MCP_TEST_VAR=") && strings.Contains(e, "v1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("override missing: %v", got)
	}
}

func TestMCPServerEntryValidateToolsFilter(t *testing.T) {
	t.Parallel()
	ok := MCPServerEntry{Type: "stdio", Command: "true", ToolsFilter: "^get_weather"}
	if err := ok.Validate("weather"); err != nil {
		t.Fatal(err)
	}
	bad := MCPServerEntry{Type: "stdio", Command: "true", ToolsFilter: "[unclosed"}
	if err := bad.Validate("weather"); err == nil {
		t.Fatal("expected invalid tools_filter error")
	}
}

func TestMCPServerEntryValidateOpenRouterTool(t *testing.T) {
	t.Parallel()
	ok := MCPServerEntry{
		Type:  "openrouter_tool",
		Tool:  "web_search",
		Tasks: []string{"generate_chat_reply"},
		Parameters: map[string]any{
			"max_total_results": 3,
		},
	}
	if err := ok.Validate("search"); err != nil {
		t.Fatal(err)
	}
	missingTasks := MCPServerEntry{Type: "openrouter_tool", Tool: "datetime"}
	if err := missingTasks.Validate("clock"); err == nil {
		t.Fatal("expected missing tasks error")
	}
	withCommand := MCPServerEntry{
		Type:    "openrouter_tool",
		Tool:    "datetime",
		Tasks:   []string{"generate_chat_reply"},
		Command: "true",
	}
	if err := withCommand.Validate("clock"); err == nil {
		t.Fatal("expected command forbidden error")
	}
	badTool := MCPServerEntry{
		Type:  "openrouter_tool",
		Tool:  "unknown",
		Tasks: []string{"generate_chat_reply"},
	}
	if err := badTool.Validate("bad"); err == nil {
		t.Fatal("expected unsupported tool error")
	}
}

func TestMCPServerEntryValidateTasksOnStdio(t *testing.T) {
	t.Parallel()
	ok := MCPServerEntry{
		Type:    "stdio",
		Command: "true",
		Tasks:   []string{"chat_with_tools"},
	}
	if err := ok.Validate("local"); err != nil {
		t.Fatal(err)
	}
	badTask := MCPServerEntry{
		Type:    "stdio",
		Command: "true",
		Tasks:   []string{"not_a_task"},
	}
	if err := badTask.Validate("local"); err == nil {
		t.Fatal("expected unknown task error")
	}
}

func TestLoadMCPServersFromFileUnset(t *testing.T) {
	t.Setenv("ASSISTANT_BOT_MCP_SERVERS_FILE", "")
	servers, warns := LoadMCPServersFromFile()
	if servers != nil {
		t.Fatalf("expected nil servers, got %v", servers)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %v", warns)
	}
}

func TestLoadMCPServersFromFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	content := `mcpServers:
  weather:
    type: streamable-http
    url: http://127.0.0.1:9/mcp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASSISTANT_BOT_MCP_SERVERS_FILE", path)
	servers, warns := LoadMCPServersFromFile()
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(servers) != 1 {
		t.Fatalf("servers: %v", servers)
	}
	e := servers["weather"]
	if e.URL != "http://127.0.0.1:9/mcp" || e.Type != "streamable-http" {
		t.Fatalf("entry: %+v", e)
	}
}

func TestLoadMCPServersFromFileStdio(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	content := `mcpServers:
  noop:
    type: stdio
    command: true
    args: []
    env:
      FOO: bar
    tools_filter: "^noop_"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASSISTANT_BOT_MCP_SERVERS_FILE", path)
	servers, warns := LoadMCPServersFromFile()
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	e := servers["noop"]
	if e.Type != "stdio" || e.Command != "true" || !slices.Equal(e.Args, []string{}) {
		t.Fatalf("entry: %+v", e)
	}
	if e.Env["FOO"] != "bar" {
		t.Fatalf("env: %+v", e.Env)
	}
	if e.ToolsFilter != "^noop_" {
		t.Fatalf("tools_filter: %q", e.ToolsFilter)
	}
}

func TestLoadMCPServersFromFileSkipsInvalidServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	content := `mcpServers:
  bad:
    type: stdio
  good:
    type: streamable-http
    url: http://127.0.0.1:9/mcp
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASSISTANT_BOT_MCP_SERVERS_FILE", path)
	servers, warns := LoadMCPServersFromFile()
	if len(servers) != 1 {
		t.Fatalf("expected 1 valid server, got %v warnings=%v", servers, warns)
	}
	if _, ok := servers["good"]; !ok {
		t.Fatalf("missing good server: %v", servers)
	}
	if len(warns) < 1 {
		t.Fatalf("expected warning for bad server, got %v", warns)
	}
}

func TestLoadMCPServersFromFileInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	if err := os.WriteFile(path, []byte("mcpServers:\n  bad: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASSISTANT_BOT_MCP_SERVERS_FILE", path)
	servers, warns := LoadMCPServersFromFile()
	if servers != nil {
		t.Fatal("expected nil servers")
	}
	if len(warns) != 1 {
		t.Fatalf("warns: %v", warns)
	}
}
