# CLIProxyAPI Fork

这是 [`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI)
的独立 fork。它保留 upstream 的 OpenAI/Gemini/Claude/Codex 兼容代理能力，但本
README 只说明本 fork 做了什么、当前对齐到哪个 upstream 版本，以及如何验证/运行。

完整的 upstream 产品文档请参考 upstream 仓库及其官方文档。本文件不再保留
upstream 原 README 中的赞助、生态项目列表和营销型说明。

## 当前对齐的 upstream 版本

- Upstream 仓库：`router-for-me/CLIProxyAPI`
- 当前 upstream 基线：`upstream/main` 的 `66c5d60b`
- 同步点 upstream tag：`v7.1.11`
- 本 fork 同步提交：`8ccf8cf7 merge: sync upstream main`
- 本 fork 同步 tag：`v0.1.9`
- 同步日期：2026-05-19

后续可能会有仅修改文档的提交位于该同步提交之后；这里描述的代码基线是
`v0.1.9` 对齐 upstream `v7.1.11` 的结果。

## 这个 fork 主要解决什么

这个 fork 面向高并发 CLI proxy 场景：大量 OAuth/API-key 凭据被放入池中，并在
流式响应、WebSocket、Redis usage reporting 等链路下持续轮转和重试。

重点是：

- 保留大账号池下低 churn 的 auth 调度；
- 避免请求路径上做同步持久化写入；
- 保持 WebSocket/session affinity 在重连和重试时稳定；
- 保留并强化 Redis usage queue，避免突发流量导致无界内存增长；
- 合并 upstream 功能更新时，针对本 fork 的高并发链路做适配，而不是盲目覆盖。

## 本 fork 在同步 upstream 时保留的行为

### Auth scheduler / conductor 热路径

- 保留 model-aware auth scheduler 快路径。
- Codex WebSocket 请求在合适场景下仍优先选择支持 WebSocket 的凭据。
- 单模型状态更新尽量走轻量路径，避免大范围 scheduler rebuild。
- Auth 持久化仍然是合并后的异步后台写入，避免阻塞请求路径。
- 保留 stream bootstrap retry：当流式响应在首个有效 payload 前失败时，可以轮转
  到下一个可用 auth，并返回更有意义的管理/API 错误。

### Redis usage queue

- 保留 Redis-compatible usage queue。
- 队列按 item 数量和总 payload 字节数做上限，防止内存无界增长。
- RESP 协议解析保留 array size、line length、bulk size、pop count、auth failure、
  pre-auth/idle deadline 等限制。
- 即使没有配置 remote management key，本地管理密码仍可用于本地 Redis AUTH。
- `LPOP` 返回最早入队的数据，`RPOP` 返回最新入队的数据。
- 保留 upstream Pub/Sub usage streaming，并和本 fork 的队列上限/协议硬化合并。

### Protocol multiplexer

- 每个 accepted connection 独立做协议 sniff，避免阻塞 listener accept loop。
- TLS/HTTP/RESP 路由保留明确的 sniff deadline。
- mux listener handoff 保持非阻塞，并处理关闭状态，避免 HTTP listener 饱和或关闭时
  卡住上游连接。

### OpenAI/Codex 兼容链路

- OpenAI Responses WebSocket 的 pinned auth 逻辑会保留 quota/error status，并在可重试
  的 upstream 失败时释放 pinned auth。
- Codex non-stream 执行可以在收到 `response.completed` 后返回，不必等待 upstream
  服务端关闭响应体。
- route-model stream 执行可以用一个模型做 auth 选择，同时把用户原始请求模型发送给 executor。

### Watcher / config reload

- Config reload 保留本 fork 的低 churn 目标。
- 对 auth/model 的变化尽量做定向更新和 stale state reconciliation，而不是全量重建。

## `v0.1.9` 合并进来的 upstream 功能

`v0.1.9` 同步了 upstream 到 `v7.1.11` 的功能性更新，包括：

- Go module/API 进入 upstream v7 线；
- Home control-plane/client 支持；
- xAI/Grok auth 和 executor 支持；
- Codex client model catalog；
- OpenAI image/video handler 更新；
- Antigravity executor、credits/balance 更新；
- management API 和 auth-file 管理增强；
- registry/catalog 刷新，包括 GPT-5.5 和 Codex client models；
- translator/runtime helper 重构到 `internal/runtime/executor/helps`；
- 移除 upstream 已删除的 qwen/iflow provider 路径；
- upstream 新增 workflow 文件，并已使用具备 workflow scope 的 key 推送。

## 这个 README 刻意删掉了什么

原来的 upstream 风格 README 包含赞助商、生态项目列表、完整产品宣传和通用文档入口。
这些内容已经从本 fork README 中移除。本 README 只回答三件事：

1. 当前对齐的 upstream 版本是什么；
2. 这个 fork 保留/修改了什么；
3. 如何验证和运行。

## 构建和验证

```bash
gofmt -w .
go build -o cli-proxy-api ./cmd/server
go test ./...
```

`v0.1.9` upstream sync 已通过：

```bash
go build -o test-output ./cmd/server
go test ./...
```

同步时还检查了未解决冲突标记，以及 Go 代码中是否仍有旧的
`github.com/router-for-me/CLIProxyAPI/v6` import。

## 运行

```bash
go run ./cmd/server --config config.yaml
```

常用参数：

- `--config <path>`：指定配置文件；
- `--tui`：启动终端 UI；
- `--standalone`：standalone 模式；
- `--local-model`：禁用远程模型目录更新；
- `--no-browser`：OAuth 流程不自动打开浏览器；
- `--oauth-callback-port <port>`：指定 OAuth callback 端口。

## 后续同步策略

继续从 upstream 同步时，功能性更新应该合并；但如果影响本 fork 的高并发链路，需要
结合本 fork 的实现做适配，而不是简单选择 upstream 或 local 版本。重点保护区域：

- `sdk/cliproxy/auth/*` scheduler/conductor/selector；
- `internal/redisqueue` 和 `internal/api/redis_queue_protocol.go`；
- `internal/api` 下的 protocol multiplexer；
- Codex 和 OpenAI Responses WebSocket executor/handler；
- config watcher 和定向 reload 逻辑。

## License

本 fork 继续使用 upstream 的 MIT license。见 [LICENSE](LICENSE)。
