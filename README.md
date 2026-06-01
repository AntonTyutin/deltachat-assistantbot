# Assistant Bot for DeltaChat Messenger

Personal DeltaChat LLM assistant for group chats. The bot follows the language used in each chat.

The bot keeps structured memory about participants and chats, stores the last 20 raw messages per chat, updates topics and profiles, answers only when reply rules say it should, and persists application data in encrypted SQLite fields. Optional [MCP](https://modelcontextprotocol.io/) servers extend it with external tools: each server can run locally over **stdio** (a subprocess with JSON-RPC on stdin/stdout) or remotely over **streamable HTTP**. Tool names are exposed to the LLM with a `serverId__toolName` prefix.

## Requirements

- Go 1.25 or newer
- `deltachat-rpc-server` on `PATH` (or set `DELTACHAT_RPC_SERVER_COMMAND`)
- An OpenAI-compatible LLM API (`LLM_BASE_URL`, `LLM_API_KEY`)

Install the RPC server, for example:

```sh
cargo install --git https://github.com/chatmail/core deltachat-rpc-server --locked
```

For stdio MCP servers, the configured `command` must be on `PATH` (or use an absolute path). For streamable HTTP, the bot needs network access to the MCP URL.

## Build

From the repository root:

```sh
go build -o assistantbot ./cmd/bot
```

The bot starts `deltachat-rpc-server` as a subprocess over stdio; account data lives under `DC_ACCOUNTS_PATH` (default `/data/deltachat-accounts`).

## Configuration

Copy `.env.example` to `.env` and fill in at least:

- `ASSISTANT_BOT_DB_KEY` — long random secret for database encryption
- `LLM_API_KEY` — provider API key

Optional:

- `DC_ACCOUNTS_PATH` — DeltaChat account storage (default `/data/deltachat-accounts`)
- `DELTACHAT_RPC_SERVER_COMMAND` — path to `deltachat-rpc-server` (default `deltachat-rpc-server`)

Optional tool configuration: set `ASSISTANT_BOT_MCP_SERVERS_FILE` to a YAML file with a top-level `mcpServers` object. Entries can be MCP servers (`stdio`, `streamable-http`, `sse`) or OpenRouter [server tools](https://openrouter.ai/docs/guides/features/server-tools/overview) (`openrouter_tool`). Example:

```yaml
mcpServers:
  web_search:
    type: openrouter_tool
    tool: web_search
    tasks:
      - generate_chat_reply
    parameters:
      max_total_results: 3
  example:
    type: streamable-http
    url: http://127.0.0.1:3000/mcp
    headers:
      Authorization: Bearer token
    tools_filter: "^search_|^fetch_"
    system_prompt_append: Optional extra guidance for this server's tools.
  local-tool:
    type: stdio
    command: python
    args:
      - -m
      - my_mcp_server
    env:
      API_KEY: secret
```

For MCP servers, tool names exposed to the LLM are prefixed: `serverId__toolName`. OpenRouter server tools use native types such as `openrouter:web_search` and are executed by OpenRouter (no client-side handler).

Per-server `tools_filter` (MCP only) is an optional regular expression matched against each tool's original name (before the `serverId__` prefix); only matching tools are exposed to the LLM. Omit it to include all tools from the server.

Optional `tasks` limits which LLM tasks include tools from that entry (`generate_chat_reply`, `chat_with_tools`, …). MCP servers without `tasks` are included in all tool-using tasks. `openrouter_tool` entries require `tasks`.

Per-server `system_prompt_append` is appended for tool calls on top of the `generate_chat_reply` prompt from the YAML file (or `default` if that key is missing). Invalid entries or unreachable MCP servers are skipped; the bot logs warnings and continues without those tools.

## Usage

Load environment variables from `.env`, then run commands (for example `set -a && source .env && set +a`).

One-time account setup (requires a DeltaChat setup QR string):

```sh
./assistantbot setup-account --qr-data "DCACCOUNT:..."
```

This configures the single DeltaChat account on the RPC server and prints its address. It does not start the event loop.

Run the bot:

```sh
./assistantbot run
```

`run` uses the only account on the server. If no account exists yet, startup fails with a clear error.

Other commands:

```sh
./assistantbot invite-link
./assistantbot edit-profile --name "Helper Bot" --bio "Your group assistant" --photo /path/to/avatar.jpg
```

Any subset of `--name`, `--bio`, and `--photo` is allowed for `edit-profile`.

## DeltaChat RPC

DeltaChat access uses `github.com/chatmail/rpc-client-go/v2` behind `internal/deltachat.Client`. The bot starts `deltachat-rpc-server` via the library’s `IOTransport` (same approach as the [echobot example](https://github.com/chatmail/rpc-client-go/blob/main/examples/echobot_full/echobot.go)). Application code outside `internal/deltachat` should not import Chatmail RPC types directly.

Each bot instance runs its own RPC server subprocess and supports exactly one account.

## LLM system prompts

`ASSISTANT_BOT_LLM_PROMPTS_FILE` points to a YAML file with system prompts (required to run the bot). Top-level keys:

- `default` — fallback prompt for any task without its own key
- optional task ids: `generate_chat_reply`, `update_participant_profile`, `update_chat_topic`, `classify_message_topic` (each replaces `default` when set)

Use YAML multiline blocks (`|`) for long prompts and examples. See [`config/llm-prompts.example.yaml`](config/llm-prompts.example.yaml).

MCP servers and OpenRouter server tools share the MCP YAML config (`ASSISTANT_BOT_MCP_SERVERS_FILE`). When the bot generates replies with tools, per-entry `system_prompt_append` is appended to the `generate_chat_reply` system prompt from the prompts YAML file (or to `default` if `generate_chat_reply` is not set).

## LLM models

`LLM_MODEL_DEFAULT` applies to every LLM task unless a task-specific override is set. You can supply one model or a pool separated by spaces or commas (for example `model-a model-b` or `model-a,model-b`); each LLM request picks a model from the pool at random so metrics and logs can compare providers.

On failure the client retries with another random pick (possibly the same model) and exponential backoff. `LLM_RETRY_BACKOFF_MULTIPLIER` sets the backoff factor (default `2`). Reply generation (`generate_chat_reply`) uses up to 3 attempts with a 400ms base delay; background memory tasks use up to 5 attempts with a 1s base delay.

Task-specific variables (same fallback-list format):

- `LLM_MODEL_REPLY` — user-facing replies
- `LLM_MODEL_TOPIC` — topic updates and classification
- `LLM_MODEL_PROFILE` — participant profile updates

Token limits: `LLM_MAX_COMPLETION_TOKENS` (fallback, default 2048) and optional per-task overrides `LLM_MAX_COMPLETION_TOKENS_REPLY`, `LLM_MAX_COMPLETION_TOKENS_TOPIC`, `LLM_MAX_COMPLETION_TOKENS_PROFILE` (see `.env.example`). Reply generation with memory/MCP tools should use the global fallback or at least ~1024 tokens; very low reply limits truncate JSON replies.

Prometheus histogram `dc_assistantbot_llm_prompt_part_bytes` records UTF-8 byte sizes per task and part: `system` (YAML task prompt), `tools` (system append from MCP and memory tools), `tools_definitions` (serialized tool definitions), `user` (initial user message).
