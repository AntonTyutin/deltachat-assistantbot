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

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/metrics"
)

const toolNameSep = "__"

// Registry holds MCP sessions, OpenRouter server tool definitions, and routes for MCP tools.
type Registry struct {
	sessions      map[string]*mcp.ClientSession
	routes        map[string]route
	toolEntries   []toolEntry
	promptAppends []promptAppend
	recorder      metrics.Recorder
}

type toolEntry struct {
	def   llm.ToolDefinition
	tasks []string
}

type promptAppend struct {
	text  string
	tasks []string
}

func (r *Registry) addPromptAppend(text string, tasks []string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.promptAppends = append(r.promptAppends, promptAppend{
		text:  text,
		tasks: append([]string(nil), tasks...),
	})
}

type route struct {
	serverID string
	toolName string
}

// Connect registers configured tool servers. MCP servers (stdio/streamable-http/sse) are dialed,
// their tools listed and exposed as "serverID__originalToolName" function tools; openrouter_tool
// entries are registered as native OpenRouter server tools without a session. Entries that fail to
// connect, list tools, or validate are skipped; messages for logging are returned in warnings.
// If no entry registers successfully, reg is nil.
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
		tasks := entryTasks(entry)

		if strings.TrimSpace(strings.ToLower(entry.Type)) == "openrouter_tool" {
			def, err := entry.openRouterToolDefinition()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("tool server %q: %v", serverID, err))
				continue
			}
			reg.toolEntries = append(reg.toolEntries, toolEntry{def: def, tasks: tasks})
			reg.addPromptAppend(entry.SystemPromptAppend, tasks)
			continue
		}

		transport, err := transportForEntry(entry, httpClient)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("tool server %q: %v", serverID, err))
			continue
		}
		session, err := mcpClient.Connect(ctx, transport, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("tool server %q: connect failed: %v", serverID, err))
			continue
		}
		reg.sessions[serverID] = session

		toolsFilter, err := entry.toolsFilterRE()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("tool server %q: %v", serverID, err))
			_ = session.Close()
			delete(reg.sessions, serverID)
			continue
		}

		var toolErr error
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				toolErr = err
				break
			}
			if toolsFilter != nil && !toolsFilter.MatchString(tool.Name) {
				continue
			}
			prefixed := serverID + toolNameSep + tool.Name
			reg.routes[prefixed] = route{serverID: serverID, toolName: tool.Name}
			ft, convErr := mcpToolToOpenAI(prefixed, tool)
			if convErr != nil {
				toolErr = fmt.Errorf("convert tool %q: %w", tool.Name, convErr)
				break
			}
			reg.toolEntries = append(reg.toolEntries, toolEntry{
				def:   llm.FunctionToolDefinition(ft),
				tasks: tasks,
			})
		}
		if toolErr != nil {
			warnings = append(warnings, fmt.Sprintf("tool server %q: %v", serverID, toolErr))
			_ = session.Close()
			delete(reg.sessions, serverID)
			reg.removeToolsForServer(serverID)
			continue
		}
		reg.addPromptAppend(entry.SystemPromptAppend, tasks)
	}

	if len(reg.toolEntries) == 0 {
		_ = reg.Close()
		if len(warnings) > 0 {
			warnings = append(warnings, "no tool servers registered successfully; continuing without tools")
		}
		return nil, warnings
	}
	return reg, warnings
}

func (r *Registry) removeToolsForServer(serverID string) {
	prefix := serverID + toolNameSep
	for k := range r.routes {
		if strings.HasPrefix(k, prefix) {
			delete(r.routes, k)
		}
	}
	kept := r.toolEntries[:0:0]
	for _, e := range r.toolEntries {
		if e.def.Function == nil || !strings.HasPrefix(e.def.Function.Name, prefix) {
			kept = append(kept, e)
		}
	}
	r.toolEntries = kept
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

// ToolsForTask returns tool definitions enabled for the given LLM task.
func (r *Registry) ToolsForTask(task string) []llm.ToolDefinition {
	if r == nil {
		return nil
	}
	out := make([]llm.ToolDefinition, 0, len(r.toolEntries))
	for _, e := range r.toolEntries {
		if entryMatchesTask(e.tasks, task) {
			out = append(out, e.def)
		}
	}
	return out
}

// HasToolsForTask reports whether any tools are registered for the task.
func (r *Registry) HasToolsForTask(task string) bool {
	return r != nil && len(r.ToolsForTask(task)) > 0
}

// SystemPromptAppendForTask returns extra system prompt guidance for tools enabled for the task,
// joined by newlines in deterministic order.
func (r *Registry) SystemPromptAppendForTask(task string) string {
	if r == nil || len(r.promptAppends) == 0 {
		return ""
	}
	var texts []string
	for _, pa := range r.promptAppends {
		if entryMatchesTask(pa.tasks, task) {
			texts = append(texts, pa.text)
		}
	}
	slices.Sort(texts)
	return strings.Join(texts, "\n")
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
