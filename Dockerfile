ARG DELTACHAT_VERSION=v2.49.0

FROM debian:bookworm-slim AS deltachat-rpc
ARG DELTACHAT_VERSION
ARG TARGETARCH
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && case "${TARGETARCH}" in \
         amd64) DC_ARCH=x86_64-linux ;; \
         arm64) DC_ARCH=aarch64-linux ;; \
         *) echo "unsupported TARGETARCH=${TARGETARCH}"; exit 1 ;; \
       esac \
    && curl -fsSL \
         --retry 5 --retry-delay 5 --retry-all-errors \
         --max-time 300 \
         --speed-time 5 --speed-limit 1000 \
         -o /deltachat-rpc-server \
         "https://github.com/chatmail/core/releases/download/${DELTACHAT_VERSION}/deltachat-rpc-server-${DC_ARCH}" \
    && chmod +x /deltachat-rpc-server

FROM golang:1.25-bookworm AS build

ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
COPY lib/assistantbot-core/go.mod lib/assistantbot-core/go.sum ./lib/assistantbot-core/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY lib/assistantbot-core/ lib/assistantbot-core/
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
COPY --from=deltachat-rpc --chown=nobody:nogroup /deltachat-rpc-server /deltachat-rpc-server
ENV DC_ACCOUNTS_PATH=/data/deltachat-accounts
ENV DELTACHAT_RPC_SERVER_COMMAND=/deltachat-rpc-server
VOLUME ["/data"]
USER nobody:nogroup
ENTRYPOINT ["/assistantbot"]
CMD ["run"]
