package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

const toolNameSep = "__"

// Registry holds MCP sessions and maps prefixed tool names back to servers.
type Registry struct {
	sessions           map[string]*mcp.ClientSession
	routes             map[string]route
	tools              []openai.Tool
	systemPromptAppend []string
	recorder           metrics.Recorder
}

type route struct {
	serverID string
	toolName string
}

// Connect dials each configured MCP server, lists tools, and builds OpenAI tool definitions
// with names "serverID__originalToolName". Servers that fail to connect or list tools are skipped;
// messages for logging are returned in warnings. If every server fails, reg is nil.
func Connect(ctx context.Context, servers map[string]MCPServerEntry, httpClient *http.Client, recorder metrics.Recorder) (reg *Registry, warnings []string) {
	if len(servers) == 0 {
		return nil, nil
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	ids := make([]string, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	reg = &Registry{
		sessions: make(map[string]*mcp.ClientSession, len(servers)),
		routes:   make(map[string]route),
		recorder: recorder,
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "Assistant Bot", Version: "1"}, nil)

	for _, serverID := range ids {
		entry := servers[serverID]
		appendText := strings.TrimSpace(entry.SystemPromptAppend)
		if appendText != "" {
			reg.systemPromptAppend = append(reg.systemPromptAppend, appendText)
		}
		transport, err := transportForEntry(entry, httpClient)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: %v", serverID, err))
			reg.systemPromptAppend = dropLastIfMatch(reg.systemPromptAppend, appendText)
			continue
		}
		session, err := mcpClient.Connect(ctx, transport, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: connect failed: %v", serverID, err))
			reg.systemPromptAppend = dropLastIfMatch(reg.systemPromptAppend, appendText)
			continue
		}
		reg.sessions[serverID] = session

		var toolErr error
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				toolErr = err
				break
			}
			prefixed := serverID + toolNameSep + tool.Name
			reg.routes[prefixed] = route{serverID: serverID, toolName: tool.Name}
			ft, convErr := mcpToolToOpenAI(prefixed, tool)
			if convErr != nil {
				toolErr = fmt.Errorf("convert tool %q: %w", tool.Name, convErr)
				break
			}
			reg.tools = append(reg.tools, ft)
		}
		if toolErr != nil {
			warnings = append(warnings, fmt.Sprintf("MCP server %q: %v", serverID, toolErr))
			_ = session.Close()
			delete(reg.sessions, serverID)
			var rmRoutes []string
			for k := range reg.routes {
				if strings.HasPrefix(k, serverID+toolNameSep) {
					rmRoutes = append(rmRoutes, k)
				}
			}
			for _, k := range rmRoutes {
				delete(reg.routes, k)
			}
			var kept []openai.Tool
			prefix := serverID + toolNameSep
			for _, t := range reg.tools {
				if t.Function == nil || !strings.HasPrefix(t.Function.Name, prefix) {
					kept = append(kept, t)
				}
			}
			reg.tools = kept
			reg.systemPromptAppend = dropLastIfMatch(reg.systemPromptAppend, appendText)
		}
	}
	slices.Sort(reg.systemPromptAppend)

	if len(reg.sessions) == 0 {
		_ = reg.Close()
		if len(warnings) > 0 {
			warnings = append(warnings, "no MCP servers connected successfully; continuing without MCP tools")
		}
		return nil, warnings
	}
	return reg, warnings
}

func transportForEntry(entry MCPServerEntry, httpClient *http.Client) (mcp.Transport, error) {
	switch strings.TrimSpace(strings.ToLower(entry.Type)) {
	case "streamable-http":
		hc := httpClientWithHeaders(httpClient, entry.Headers)
		return &mcp.StreamableClientTransport{
			Endpoint:             strings.TrimRight(strings.TrimSpace(entry.URL), "/"),
			HTTPClient:           hc,
			DisableStandaloneSSE: true,
		}, nil
	case "sse":
		hc := httpClientWithHeaders(httpClient, entry.Headers)
		return &mcp.SSEClientTransport{
			Endpoint:   strings.TrimSpace(entry.URL),
			HTTPClient: hc,
		}, nil
	case "stdio":
		// Do not use CommandContext here: Connect is called with a short-lived
		// timeout context, and canceling it would kill the MCP subprocess while
		// sessions remain open. Process lifetime is tied to Registry.Close().
		cmd := exec.Command(strings.TrimSpace(entry.Command), entry.Args...)
		cmd.Env = MergedProcessEnv(entry.Env)
		return &mcp.CommandTransport{Command: cmd}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP type %q", entry.Type)
	}
}

func dropLastIfMatch(slice []string, value string) []string {
	if value == "" || len(slice) == 0 {
		return slice
	}
	if slice[len(slice)-1] == value {
		return slice[:len(slice)-1]
	}
	return slice
}

func mcpToolToOpenAI(prefixedName string, t *mcp.Tool) (openai.Tool, error) {
	params := t.InputSchema
	if params == nil {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        prefixedName,
			Description: t.Description,
			Parameters:  params,
		},
	}, nil
}

// OpenAITools returns tool definitions for the chat completion API.
func (r *Registry) OpenAITools() []openai.Tool {
	if r == nil {
		return nil
	}
	return r.tools
}

// HasTools reports whether any MCP tools are connected.
func (r *Registry) HasTools() bool {
	return r != nil && len(r.tools) > 0
}

// SystemPromptAppend returns extra system prompt guidance supplied by MCP server config.
func (r *Registry) SystemPromptAppend() string {
	if r == nil || len(r.systemPromptAppend) == 0 {
		return ""
	}
	return strings.Join(r.systemPromptAppend, "\n")
}

// ExecuteTool runs a prefixed tool name on the correct MCP session.
func (r *Registry) ExecuteTool(ctx context.Context, prefixedName string, argumentsJSON string) (_ string, err error) {
	start := time.Now()
	serverID, toolName := "unknown", prefixedName
	defer func() {
		rec := metrics.Noop
		if r != nil && r.recorder != nil {
			rec = r.recorder
		}
		outcome := "success"
		if err != nil {
			outcome = mcpOutcome(err)
		}
		rec.RecordMCPTool(serverID, toolName, outcome, time.Since(start))
	}()

	if r == nil {
		return "", fmt.Errorf("nil mcp registry")
	}
	rt, ok := r.routes[prefixedName]
	if !ok {
		return "", fmt.Errorf("unknown MCP tool %q", prefixedName)
	}
	serverID, toolName = rt.serverID, rt.toolName
	session := r.sessions[rt.serverID]
	if session == nil {
		return "", fmt.Errorf("no session for server %q", rt.serverID)
	}

	var args any
	if strings.TrimSpace(argumentsJSON) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", fmt.Errorf("tool arguments json: %w", err)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      rt.toolName,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}
	return formatToolResult(res), nil
}

func mcpOutcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if strings.Contains(err.Error(), "unknown MCP tool") {
		return metrics.OutcomeUnknownTool
	}
	if strings.Contains(err.Error(), "no session for server") {
		return metrics.OutcomeNoSession
	}
	if strings.Contains(err.Error(), "tool arguments json") {
		return metrics.OutcomeInvalidArgs
	}
	return "error"
}

// ExecuteAnyTool tries each provided prefixed tool name until one succeeds.
func (r *Registry) ExecuteAnyTool(ctx context.Context, prefixedNames []string, argumentsJSON string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("nil mcp registry")
	}
	var errs []error
	for _, name := range prefixedNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		result, err := r.ExecuteTool(ctx, name, argumentsJSON)
		if err == nil {
			return result, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("no mcp tools to execute")
	}
	return "", errors.Join(errs...)
}

// FindToolsByNamePart returns prefixed tool names whose unprefixed tool name
// contains one of the provided fragments.
func (r *Registry) FindToolsByNamePart(parts ...string) []string {
	if r == nil || len(parts) == 0 {
		return nil
	}
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			normalizedParts = append(normalizedParts, part)
		}
	}
	if len(normalizedParts) == 0 {
		return nil
	}
	matches := make([]string, 0)
	for prefixed, rt := range r.routes {
		toolName := strings.ToLower(strings.TrimSpace(rt.toolName))
		for _, part := range normalizedParts {
			if strings.Contains(toolName, part) {
				matches = append(matches, prefixed)
				break
			}
		}
	}
	slices.Sort(matches)
	return matches
}

func formatToolResult(res *mcp.CallToolResult) string {
	var b strings.Builder
	if res.IsError {
		b.WriteString("error: ")
	}
	for _, c := range res.Content {
		switch t := c.(type) {
		case *mcp.TextContent:
			b.WriteString(t.Text)
		default:
			raw, err := json.Marshal(c)
			if err != nil {
				b.WriteString(fmt.Sprintf("%v", c))
			} else {
				b.Write(raw)
			}
		}
	}
	if res.StructuredContent != nil {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			b.WriteString(fmt.Sprintf("%v", res.StructuredContent))
		} else {
			b.Write(raw)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "(empty tool result)"
	}
	return s
}

// Close terminates MCP sessions.
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	for id, s := range r.sessions {
		if s == nil {
			continue
		}
		if err := s.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close mcp session %q: %w", id, err))
		}
	}
	clear(r.sessions)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
