# Agent Instructions

## Go Tooling

Run Go commands through the development container so the project does not depend on the host Go version:

```sh
docker compose run --rm dev go test ./...
docker compose run --rm dev go mod tidy
docker compose run --rm dev gofmt -w cmd internal
```

Do not use host `go` for normal development unless you are explicitly debugging the local toolchain.

## Runtime

The production container bundles both the Assistant Bot binary and `deltachat-rpc-server`. Keep DeltaChat integration code isolated under `internal/deltachat` so memory, storage, LLM, and reply policy code do not depend directly on Chatmail RPC types.

## Commits

Do not add `Co-authored-by:` or any other AI agent attribution to commits.