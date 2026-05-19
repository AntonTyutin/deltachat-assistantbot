FROM rust:1-bookworm AS deltachat-rpc

RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libssl-dev clang cmake \
    && rm -rf /var/lib/apt/lists/*
RUN cargo install --git https://github.com/chatmail/core deltachat-rpc-server --locked

FROM golang:1.25-bookworm AS build

ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X assistantbot/internal/version.Version=${VERSION}" -o /out/assistantbot ./cmd/bot

FROM debian:bookworm-slim AS bot
WORKDIR /
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libssl3 libsqlite3-0 \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data/deltachat-accounts \
    && chown -R nobody:nogroup /data
COPY --from=build --chown=nobody:nogroup /out/assistantbot /assistantbot
COPY --from=deltachat-rpc --chown=nobody:nogroup /usr/local/cargo/bin/deltachat-rpc-server /deltachat-rpc-server
ENV DC_ACCOUNTS_PATH=/data/deltachat-accounts
ENV DELTACHAT_RPC_SERVER_COMMAND=/deltachat-rpc-server
VOLUME ["/data"]
USER nobody:nogroup
ENTRYPOINT ["/assistantbot"]
CMD ["run"]
