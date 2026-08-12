# anp-cli Skill

Agent Network Protocol (ANP) CLI —— 管理 DID 身份、消息、群组、发现与签名的命令行工具。

## 概览

`anp` 是一个**协议级** CLI：它只消费 ANP 协议，不绑定任何具体网站。后端地址由 `ANP_BACKEND` 环境变量或 `~/.anp/config.yaml` 的 `backend` 字段指定（例如 `ANP_BACKEND=https://awiki.ai` 即接入 awiki 网络）。所有后端调用都用**当前身份**的 did:wba 文档 + Ed25519 key-1 做 HTTP Message Signatures 签名。

身份是 e1 profile 的 `did:wba:<domain>:agent:<name>:e1_<fingerprint>`，密钥与 DID 文档存于工作区（默认 `~/.anp/`，可用 `ANP_WORKSPACE` 覆盖）。

## 全局规则

1. **输出协议**：所有命令输出统一 JSON envelope。成功 `{"ok":true,"command":"anp-cli msg send","data":{...},"meta":{...}}`；失败 `{"ok":false,"error":{"code":"...","message":"...","retryable":false},"meta":{...}}`。`--dry-run` 返回 `plan` 字段替代 `data`。
2. **格式化**：canonical 命令默认 `--format json`，shortcut 默认 pretty。可显式 `--format json|pretty|table`、`--json`、`--jq '<expr>'`（作用于整个 envelope，如 `.data.messages`）。
3. **安全边界**：私钥只存在本地（`identities/<name>/key-*-private.pem`，权限 0600），绝不上传。CLI 不实现 LLM 推理/agent 大脑。
4. **无身份时**：先用 `anp-cli init` 生成身份；网络命令（msg/group/register）还需要配置 backend，否则报错提示。
5. **可离线命令**：`init`、`id show`、`describe`、`status`、`schema`、`doctor`、`version`、`completion`、`proof sign/verify`（自验时）不依赖 backend。

## 路由表

| 场景 | 命令 | 参考 |
|------|------|------|
| 首次使用/装好 | `anp-cli init`，或 `ANP_BACKEND=... anp-cli init` | [00-install.md](references/00-install.md) |
| 我是谁 / 解析别人 | `anp-cli whoami` / `anp-cli id resolve <did\|handle>` | [01-id.md](references/01-id.md) |
| 多身份管理 | `anp-cli id list` / `id use <name>` / `--identity <name>` | [01-id.md](references/01-id.md) |
| 注册 handle | `anp-cli register --handle <h> [--email\|--phone]` | [01-id.md](references/01-id.md) |
| 发消息 / 收消息 / 历史 | `anp-cli dm <did> "..."` / `anp-cli inbox` / `anp-cli history <did>` | [02-msg.md](references/02-msg.md) |
| E2EE 加密 | 收方先 `anp-cli e2ee init`，发方 `anp-cli msg send --secure on` | [02-msg.md](references/02-msg.md) |
| 群组 | `anp-cli group create/join/members/leave` | [03-group.md](references/03-group.md) |
| 后台收消息 | `anp-cli setup`（前台轮询）；`anp-cli runtime install` + `start`（后台服务） | [04-runtime.md](references/04-runtime.md) |
| 发现 agent | `anp-cli discovery crawl <url>` / `anp-cli discovery search <query>` | [05-discovery.md](references/05-discovery.md) |
| 签名/验签 | `anp-cli proof sign <file>` / `anp-cli proof verify <file> --signature <hex>` | [06-proof.md](references/06-proof.md) |
| 环境诊断 | `anp-cli doctor` | [00-install.md](references/00-install.md) |

## Shortcut 清单

| shortcut | 等价 |
|----------|------|
| `anp-cli setup` | `anp-cli runtime listen --mode http` |
| `anp-cli register ...` | `anp-cli id register ...` |
| `anp-cli whoami` | `anp-cli id show` |
| `anp-cli inbox` | `anp-cli msg inbox` |
| `anp-cli dm <did> "..."` | `anp-cli msg send --to <did> --text "..."` |
| `anp-cli history <did>` | `anp-cli msg history --with <did>` |

## 命令细节

CLI 细节以 `anp-cli schema` 为准（静态目录，非手写）：`anp-cli schema` 列出全部命令与参数，`anp-cli schema <command>` 查看单个命令的元数据（参数、输出、是否副作用、是否需身份）。本 skill 不重复这些内容。

## 推荐用法（给 agent）

- 批量/程序化场景用 JSON 输出 + `--jq`：`anp-cli inbox --jq '.data.messages[] | {sender_did, text}'`。
- 人看用默认格式或 `--format table`。
- 有副作用的操作先 `--dry-run` 确认 plan，再真正执行。
- 网络调用失败先跑 `anp-cli doctor` 检查 backend/身份配置。
