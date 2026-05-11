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

Optional MCP configuration: set `ASSISTANT_BOT_MCP_SERVERS_FILE` to a JSON file with a top-level `mcpServers` object. Example:

```json
{
  "mcpServers": {
    "weather": {
      "type": "streamable-http",
      "url": "http://127.0.0.1:3000/mcp",
      "headers": { "Authorization": "Bearer token" }
    },
    "local-tool": {
      "type": "stdio",
      "command": "python",
      "args": ["-m", "my_mcp_server"],
      "env": { "API_KEY": "secret" }
    }
  }
}
```

Per-server `system_prompt_append` adds extra guidance to the reply system prompt. Invalid entries or unreachable servers are skipped; the bot logs warnings and continues without those tools.

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

## LLM models

`LLM_MODEL_DEFAULT` applies to every LLM task unless a task-specific override is set. You can supply one model or a fallback list separated by spaces or commas (for example `model-a model-b` or `model-a,model-b`); the bot tries models in order and moves to the next on provider errors or invalid JSON output.

Task-specific variables (same fallback-list format):

- `LLM_MODEL_REPLY` — user-facing replies
- `LLM_MODEL_SUMMARY` — daily summaries
- `LLM_MODEL_TOPIC` — topic updates and rebuilds
- `LLM_MODEL_PROFILE` — participant profile updates and rebuilds

Fine-grained overrides: `LLM_MODEL_TOPIC_UPDATE`, `LLM_MODEL_TOPIC_REBUILD`, `LLM_MODEL_PROFILE_UPDATE`, `LLM_MODEL_PROFILE_REBUILD`.

Token limits: `LLM_MAX_COMPLETION_TOKENS` and per-task `LLM_MAX_COMPLETION_TOKENS_*` variables (see `.env.example`).
