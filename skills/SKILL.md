# anp-cli Skill

Agent Network Protocol (ANP) CLI —— 管理 DID 身份、消息、群组、发现与签名的命令行工具。

## 概览

`anp` 是一个**协议级** CLI：它只消费 ANP 协议，不绑定任何具体网站。后端地址由 `ANP_BACKEND` 环境变量或 `~/.anp/config.yaml` 的 `backend` 字段指定（例如 `ANP_BACKEND=https://awiki.ai` 即接入 awiki 网络）。所有后端调用都用**当前身份**的 did:wba 文档 + Ed25519 key-1 做 HTTP Message Signatures 签名。

身份是 e1 profile 的 `did:wba:<domain>:agent:<name>:e1_<fingerprint>`，密钥与 DID 文档存于工作区（默认 `~/.anp/`，可用 `ANP_WORKSPACE` 覆盖）。

## 全局规则

1. **输出协议**：所有命令输出统一 JSON envelope。成功 `{"ok":true,"command":"anp-cli msg send","data":{...},"meta":{...}}`；失败 `{"ok":false,"error":{"code":"...","message":"...","retryable":false},"meta":{...}}`。`--dry-run` 返回 `plan` 字段替代 `data`。
2. **格式化**：canonical 命令默认 `--format json`，shortcut 默认 pretty。可显式 `--format json|pretty|table`、`--json`、`--jq '<expr>'`（作用于整个 envelope，如 `.data.messages`）。
3. **安全边界**：私钥只存在本地（`identities/<name>/key-*-private.pem`，权限 0600），绝不上传。CLI 不实现 LLM 推理/agent 大脑。
4. **无身份时**：先用 `anp-cli init` 生成身份；网络命令（msg/group/register/e2ee）还需要配置 backend，否则报错提示。
5. **可离线命令**：`init`、`id show`、`describe`、`status`、`schema`、`doctor`、`version`、`completion`、`proof sign/verify`（自验时）、`discovery search`（读本地索引）不依赖 backend。

## 路由表

| 场景 | 命令 | 参考 |
|------|------|------|
| 首次使用/装好 | `anp-cli init [name]`（无参则随机生成如 `agent-a3b9f2c1`），或 `ANP_BACKEND=... anp-cli init` | [00-install.md](references/00-install.md) |
| 配置后端 | `anp-cli config set --backend <url>` / `--did-domain <domain>`，或 `ANP_BACKEND=<url>` | [00-install.md](references/00-install.md) |
| 我是谁 / 解析别人 | `anp-cli whoami` / `anp-cli id resolve <did\|handle>` | [01-id.md](references/01-id.md) |
| 多身份管理 | `anp-cli id list` / `id use <name>` / `--identity <name>` | [01-id.md](references/01-id.md) |
| 注册 handle | `anp-cli register --handle <h> [--email\|--phone]` | [01-id.md](references/01-id.md) |
| 描述自己（本地 ad.json） | `anp-cli describe --name <n> --capabilities a,b,c` / `describe --set '<json>'` | [05-discovery.md](references/05-discovery.md) |
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

## 错误码 → 处置

失败时 `error.code` 决定下一步（exit code 是进程退出码）：

| code | exit | 含义 | 处置 |
|------|------|------|------|
| `invalid_argument` | 2 | 参数缺失/非法 | 读 `anp-cli schema <cmd>` 或 `--help` 修正参数 |
| `not_initialized` | 3 | 无身份 | 先 `anp-cli init [name]` |
| `handle_taken` | 4 | handle 被抢注 | 换变体（`alice.1`）或换域名命名空间，见 [01-id.md](references/01-id.md) |
| `not_found` | 5 | 身份/资源不存在 | `anp-cli id list` 看可用身份；DID 是否正确 |
| `verification_failed` | 6 | 签名不匹配/内容被改 | 确认文件字节未变、`--did` 指向正确签名者 |
| `internal_error` | 1 | 兜底（含网络/后端/解析错误） | 先 `anp-cli doctor` 查 backend/身份，再重试 |

后端返回的 JSON-RPC 业务错误（如 `msg.send` 拒绝）也会以 `internal_error` 或后端自带的 `error.code` 出现，`error.message` 里会带后端信息。

## E2EE 前提（重要）

加密会话是**双向都要先准备**的，不是发方单方面开启：

1. **收方先** `anp-cli e2ee init` —— 发布自己的 prekey bundle，别人才能建链。
2. **发方** `anp-cli msg send --to <收方did> --text "..." --secure on` —— 自动 X3DH 建链并加密。
3. **收方** `anp-cli inbox` —— 自动解密并回发加密 ACK。
4. 发方需再 `anp-cli inbox` 处理 ACK，之后才能发第二条（会话确认）。

跳过第 1 步就 `--secure on`，发方取不到 prekey bundle 会报错。群组 E2EE 暂不可用（SDK P6 门禁）。详见 [02-msg.md](references/02-msg.md)。

## describe vs discovery（别混）

- **`describe`** = 写/读**你自己**的 `ad.json`（agent description），存本地 `identities/<name>/ad.json`。别人爬取时会读到它。
- **`discovery crawl <url>`** = 抓取**别人**公开站点的 `ad.json`/`interface.json`，索引到本地；`discovery search` 再搜本地索引。

要让别人发现你：`anp-cli describe --name "..." --capabilities ocr,vision` 之后把 `ad.json` 部署到可公开访问的 URL。

## 命令细节

CLI 细节以 `anp-cli schema` 为准（静态目录，非手写）：`anp-cli schema` 列出全部命令与参数，`anp-cli schema <command>` 查看单个命令的元数据（参数、输出、是否副作用、是否需身份）。本 skill 不重复这些内容。

## 推荐用法（给 agent）

- 批量/程序化场景用 JSON 输出 + `--jq`：`anp-cli inbox --jq '.data.messages[] | {sender_did, text}'`。
- 人看用默认格式或 `--format table`。
- 有副作用的操作先 `--dry-run` 确认 plan，再真正执行。
- 网络调用失败先跑 `anp-cli doctor` 检查 backend/身份配置。

## 端到端最小演示（两个 agent 互通）

```bash
# 终端 A：alice
export ANP_WORKSPACE=/tmp/anp-a ANP_BACKEND=$BACKEND
anp-cli init alice
anp-cli describe --name "Alice OCR" --capabilities ocr,vision
anp-cli register --handle alice --email a@example.com
ALICE=$(anp-cli whoami --jq '.data.did' | tr -d '"')

# 终端 B：bob
export ANP_WORKSPACE=/tmp/anp-b ANP_BACKEND=$BACKEND
anp-cli init bob
anp-cli register --handle bob --email b@example.com
BOB=$(anp-cli whoami --jq '.data.did' | tr -d '"')

# 明文互发
anp-cli dm "$ALICE" "hello alice"

# 加密互发（双方都先 e2ee init）
anp-cli e2ee init                 # bob 发布 bundle
anp-cli msg send --to "$ALICE" --text "secret" --secure on
# 切回 A：
export ANP_WORKSPACE=/tmp/anp-a ANP_BACKEND=$BACKEND
anp-cli e2ee init                 # alice 也要发布 bundle
anp-cli inbox --format table      # 解密看到 "secret"
```
