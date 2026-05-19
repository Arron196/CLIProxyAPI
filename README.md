# CLIProxyAPI Fork

This repository is an independent fork of
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI).
It keeps the upstream OpenAI/Gemini/Claude/Codex-compatible proxy surface, but
this README intentionally focuses only on what this fork adds or preserves.

For the full upstream product documentation, use the upstream repository and
the official docs linked from that project.

## Upstream alignment

- Upstream repository: `router-for-me/CLIProxyAPI`
- Current upstream baseline: `upstream/main` at `66c5d60b`
- Upstream release tag at the sync point: `v7.1.11`
- Fork sync commit: `8ccf8cf7 merge: sync upstream main`
- Fork release tag for the sync: `v0.1.9`
- Sync date: 2026-05-19

Later documentation-only commits may sit on top of the sync commit. The code
baseline described here is the `v0.1.9` sync against upstream `v7.1.11`.

## What this fork is for

This fork is maintained for high-concurrency CLI proxy deployments where many
OAuth/API-key credentials are pooled and rotated under streaming, WebSocket,
and Redis usage-reporting traffic.

The fork focuses on:

- preserving low-churn auth scheduling under large account pools;
- keeping request-path persistence asynchronous;
- keeping WebSocket/session affinity stable when clients reconnect or retry;
- keeping Redis usage queue support bounded and safe under burst traffic;
- adapting upstream functional changes without regressing the fork's
  concurrency-sensitive paths.

## Fork-specific behavior preserved in the upstream sync

### Auth scheduling and conductor hot paths

- Model-aware auth scheduler fast paths are preserved.
- Codex WebSocket routing still prefers WebSocket-capable credentials when
  appropriate.
- Per-model state updates avoid broad scheduler rebuilds where the fork has a
  cheaper update path.
- Auth persistence is coalesced and performed asynchronously, instead of doing
  request-path storage writes.
- Stream bootstrap retry handling is kept so pre-payload failures can rotate to
  the next eligible auth and produce useful management/API errors.

### Redis usage queue

- The Redis-compatible usage queue remains enabled in this fork.
- The queue is bounded by item count and total payload bytes to prevent
  unbounded memory growth.
- RESP parsing keeps limits for array size, line length, bulk size, pop count,
  auth failure count, and idle/pre-auth deadlines.
- Local management password authentication works even when no remote management
  key is configured.
- `LPOP` returns oldest queued items and `RPOP` returns newest queued items.
- Upstream Pub/Sub usage streaming is retained and combined with the fork's
  queue bounds and protocol hardening.

### Protocol multiplexer

- Connection sniffing runs per accepted connection, not inside the listener
  accept loop.
- TLS/HTTP/RESP routing keeps explicit sniff deadlines.
- The mux listener handoff remains non-blocking and closed-state safe, avoiding
  stalls when the downstream HTTP listener is saturated or closing.

### OpenAI/Codex compatibility

- OpenAI Responses WebSocket pinned-auth handling preserves quota/error status
  propagation and releases pinned auths on retryable upstream failures.
- Codex non-stream execution can return after `response.completed` instead of
  waiting for an upstream server to close the response body.
- Route-model stream execution can select an auth with one model while sending
  the original requested model to the executor.

### Watcher and config churn

- Config reload behavior keeps the fork's low-churn intent for auth/model
  updates.
- Stale model-state reconciliation and targeted restarts are preferred over
  broad rebuilds when possible.

## Upstream functionality included in `v0.1.9`

The `v0.1.9` sync pulls functional upstream updates through `v7.1.11`, including:

- Go module/API move to the upstream v7 line;
- Home control-plane/client support;
- xAI/Grok auth and executor support;
- Codex client model catalog support;
- OpenAI image/video handler updates;
- Antigravity executor and credit/balance updates;
- management API additions and auth-file improvements;
- registry/catalog refreshes, including GPT-5.5 and Codex client models;
- translator/runtime helper refactors under `internal/runtime/executor/helps`;
- removal of upstream-deleted qwen/iflow provider paths;
- upstream workflow files, now pushed with a key that has GitHub workflow scope.

## Removed from this README

The previous upstream-style README contained sponsor blocks, ecosystem lists,
and full product marketing/documentation. Those sections were removed here on
purpose. This fork README is only meant to explain:

1. which upstream version this fork is aligned to;
2. what this fork preserves or changes;
3. how to verify and run the fork.

## Build and verification

```bash
gofmt -w .
go build -o cli-proxy-api ./cmd/server
go test ./...
```

The `v0.1.9` upstream sync was verified with:

```bash
go build -o test-output ./cmd/server
go test ./...
```

The verification also checked for unresolved merge markers and stale
`github.com/router-for-me/CLIProxyAPI/v6` Go imports.

## Running

```bash
go run ./cmd/server --config config.yaml
```

Common flags:

- `--config <path>`: select a config file;
- `--tui`: start with the terminal UI;
- `--standalone`: run in standalone mode;
- `--local-model`: disable remote model catalog updates;
- `--no-browser`: do not open browser-based OAuth flows automatically;
- `--oauth-callback-port <port>`: choose the OAuth callback port.

## Sync policy for this fork

When syncing from upstream, functional updates should be merged, but changes
that touch high-concurrency paths must be adapted rather than blindly taking
upstream or local code. The protected areas are:

- `sdk/cliproxy/auth/*` scheduler/conductor/selector paths;
- Redis usage queue and protocol files under `internal/redisqueue` and
  `internal/api/redis_queue_protocol.go`;
- protocol multiplexer files under `internal/api`;
- Codex and OpenAI Responses WebSocket executors/handlers;
- config watcher and targeted reload logic.

## License

This fork keeps the upstream MIT license. See [LICENSE](LICENSE).
