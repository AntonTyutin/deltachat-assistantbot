# Agent Instructions

## Go Tooling

Run Go commands through the development container so the project does not depend on the host Go version:

```sh
docker compose run --rm dev go test ./...
docker compose run --rm dev sh -c 'cd lib/assistantbot-core && go test ./...'
docker compose run --rm dev go mod tidy
docker compose run --rm dev sh -c 'cd lib/assistantbot-core && go mod tidy'
docker compose run --rm dev gofmt -w cmd internal lib/assistantbot-core
```

Do not use host `go` for normal development unless you are explicitly debugging the local toolchain.

## Runtime

The production container bundles both the Assistant Bot binary and `deltachat-rpc-server`. Keep transport-agnostic bot logic in `lib/assistantbot-core` (import path `github.com/AntonTyutin/assistantbot-core`). DeltaChat integration stays under `internal/deltachat`; wiring and env config stay in this repository (`internal/runner`, `internal/config`, `cmd/bot`).

Per-chat memory uses two FIFO queues per `chat_id`: `prepare` (`PrepareForReply` via `RunChatMemory`) and `background` (`ProcessMessageUpdate`, outbound storage, reminder delivery). Background work does not block the next message's prepare/reply path. `Decide` / `SendText` are outside both queues. SQLite uses `MaxOpenConns(1)`.

## Commits

Do not add `Co-authored-by:` or any other AI agent attribution to commits.