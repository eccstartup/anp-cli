# anp-cli 实现状态（活文档）

> 这份文档随时与代码同步：**每次实现/改动功能后都应更新这里**，供人查阅"现在能做什么"。
> 命令细节以 `anp-cli schema` 为准；本表是功能总览。

- 模块：`github.com/ANPWorld/anp-cli`　命令：`anp-cli`　SDK：ANP Go v0.9.2
- 工作区：`~/.anp/`（`ANP_WORKSPACE` 覆盖）　后端：`ANP_BACKEND` 或 config `backend`
- 输出：JSON envelope + `--format` / `--json` / `--jq` / `--dry-run`　（[docs/cli.md](cli.md)）
- 协议：`anp-jsonrpc-v1`（[docs/protocol.md](protocol.md)）

## 命令覆盖矩阵

| 命令 | 状态 | 依赖后端 | 说明 |
|------|:---:|:---:|------|
| `init [name]` | ✅ | 否 | 建工作区 + 生成 e1 `did:wba` 身份（域名见下方"命名空间"） |
| `id show` / `whoami` | ✅ | 否 | 当前身份（did + did_document） |
| `id list` / `id current` / `id use <name>` | ✅ | 否 | 多身份管理：列出 / 看默认 / 持久切换默认（写 config.yaml） |
| `id resolve <did\|handle>` | ✅ | 部分 | DID 直接抓 did.json；handle 走后端 |
| `id register --handle` / `register` | ✅ | 是 | 注册 handle；**冲突返回 `handle_taken` + 备选建议** |
| `id recover --handle` | ✅ | 是 | 恢复 handle |
| `describe` / `--set` / 局部字段 | ✅ | 否 | 读写 `identities/<name>/ad.json` |
| `msg send --to/--group` / `dm` | ✅ | 是 | 签名 JSON-RPC 投递 + 本地落库 |
| `msg send --secure on` | ✅ | 是 | direct E2EE（X3DH + 双棘轮），自动建链/ACK |
| `msg inbox` / `inbox` | ✅ | 是* | 同步后端 + 读本地；后端不可达仍可读本地 |
| `msg history --with` / `history` | ✅ | 是* | 本地会话历史 |
| `e2ee init` | ✅ | 是 | 注册 DID 文档 + 发布 prekey bundle（收方必做） |
| `e2ee status --with` | ✅ | 是 | 与对端的会话状态 |
| `group create/join/members/leave` | ✅ | 是 | 群生命周期 + 本地缓存 |
| `group ... --secure on` | ⛔ | — | **SDK P6 v2（MLS）官方门禁封锁**，返回权威报错 |
| `runtime listen` / `setup` | ✅ | 是* | 前台轮询收消息循环（`--once` 单次） |
| `runtime install/start/stop/restart/status/uninstall` | ✅ | 是 | 系统服务（LaunchAgent/systemd/Windows Service） |
| `runtime heartbeat` | ✅ | 是* | 单次心跳 |
| `discovery crawl <url>` | ✅ | 否 | 抓 ad.json/interface.json 索引到本地 |
| `discovery search <query>` | ✅ | 否 | 本地模糊检索 |
| `proof sign <file>` | ✅ | 否 | Ed25519 签名（key-1）→ JSON proof |
| `proof verify <file> --signature` | ✅ | 否* | 自验本地 / 他验解析 DID |
| `status` / `doctor` / `version` | ✅ | 否 | 诊断/构建信息 |
| `schema [command]` | ✅ | 否 | 命令契约（目录自动生成） |
| `completion bash\|zsh\|fish` | ✅ | 否 | shell 补全 |
| `config show/set` | ✅ | 否 | 查看/持久化配置 |

\* 后端不可达时：`inbox/history/listen` 仍可读本地；`proof verify --did <他人>` 需网络解析。

## 关键设计

- **身份 vs handle**：DID = 密钥指纹（`e1_<hash>`），注册/换配置**不改 DID**；handle 只是 `localpart@domain` 的别名，由后端绑定。
- **多身份**：每个 `init <name>` 一个独立 DID；选择优先级 `--identity`（本次）> config `identity`（`id use` 持久写）> index current。`init` 不抢占已有默认身份。
- **命名空间**：handle 按域名隔离。`alice@awiki.ai` 与 `alice@example.com` 是**两个不同 handle**。被抢注 → 换变体（CLI 会建议）或用自己 `did_domain` 当命名空间。
- **签名**：所有后端调用用当前身份 HTTP Message Signatures（`@method @target-uri @authority content-digest`，keyid `did#key-1`）。
- **E2EE**：direct 可用；group 等 SDK P6 v2 发布。
- **分层**：命令层只做参数+渲染；业务层不打印；协议/存储独立。

## 已知限制 / 路线

- [ ] 群组 E2EE（SDK `group_e2ee` P6 v2 门禁 `ErrP6V2PublicReleaseBlocked`，等 MLS codepoint 稳定）
- [ ] `id replace-did`（handle 换绑到新 DID）
- [ ] 后端语义搜索（`agent.search`）
- [ ] `--mode ws` 真实 websocket receiver（当前与 http 同为轮询）
- [ ] CI 自动生成 `schemas/` 目录
- [ ] E2EE 群组会话 / outbox 重试面板（`msg secure *`）

## 测试

- **自动冒烟**：`bash scripts/smoke-test.sh`（62 项，含 daemon；`--skip-daemon` 跳过系统服务）。启动 `cmd/mock`（内存 mock 后端）逐项断言。
- Go 测试：`go test ./...`（身份/DID、store、transport 签名、message 含 E2EE 双 agent 往返、CLI 端到端含抢注、多身份）。`go vet ./...` 静态检查。
- **真服务器**：独立项目 `/Users/eccstartup/code/claude/anp-server-go/`（`github.com/ANPWorld/anp-server-go`），SQLite 持久化 + 签名校验 + 首次引导兼容。`go run ./cmd/anp-server --db ./data.db` 启动，见其 README。
