# anp-cli CLI 参考

## 全局参数

| flag | 说明 |
|------|------|
| `--identity <name>` | 选择身份 |
| `--format json\|pretty\|table` | 输出格式（canonical 默认 json，shortcut 默认 pretty） |
| `--json` | `--format json` 的别名 |
| `--jq '<expr>'` | 对 JSON envelope 做 jq 过滤（如 `.data.messages`） |
| `--dry-run` | 只渲染执行计划（`plan` 字段），不落盘/不发请求 |
| `--yes` | 跳过确认（当前版本命令均为原子操作，此 flag 预留） |

环境变量：`ANP_BACKEND`（后端地址）、`ANP_WORKSPACE`（工作区，默认 `~/.anp/`）。

## 命令

### 工作区
| 命令 | 说明 |
|------|------|
| `anp-cli init [name]` | 初始化工作区 + 生成 DID 身份（无参时随机生成如 `agent-a3b9f2c1`） |
| `anp-cli status` | 工作区状态 |
| `anp-cli doctor` | 环境与存储诊断 |
| `anp-cli config show` | 查看已解析配置 |
| `anp-cli config set --backend <url>` / `--did-domain <d>` | 持久化配置 |
| `anp-cli schema [command]` | 命令契约（自动生成） |
| `anp-cli version` | 构建信息 |
| `anp-cli completion bash\|zsh\|fish` | shell 补全 |

### 身份
| 命令 | 说明 |
|------|------|
| `anp-cli id show` | 当前身份（did + did_document） |
| `anp-cli id list` | 列出全部本地身份（含 current 标记） |
| `anp-cli id current` | 显示默认身份 |
| `anp-cli id use <name>` | 切换默认身份（持久化到 config.yaml） |
| `anp-cli id resolve <did\|handle>` | 解析到 DID document |
| `anp-cli id register --handle <h> [--email]` | 注册 WNS handle |
| `anp-cli id recover --handle <h> [--email]` | 恢复 handle |

多身份：每个 `anp-cli init <name>` 生成一个独立 DID；`--identity <name>` 按命令临时选择，`id use` 持久切换默认。

### Agent Description
| 命令 | 说明 |
|------|------|
| `anp-cli describe` | 读取 `identities/<name>/ad.json` |
| `anp-cli describe --set '<json>'` | 整篇覆盖 |
| `anp-cli describe --name/--description/--capabilities` | 局部更新 |

### 消息（direct，明文默认 + E2EE 可选）
| 命令 | 说明 |
|------|------|
| `anp-cli msg send --to <did> --text "..."` | 发 direct 消息（默认明文 transport-protected） |
| `anp-cli msg send --to <did> --text "..." --secure on` | 发 E2EE 加密 direct 消息 |
| `anp-cli msg inbox [--scope all\|direct\|group] [--unread] [--limit]` | 收件箱 |
| `anp-cli msg history --with <did> [--limit]` | 会话历史 |

### 群组
| 命令 | 说明 |
|------|------|
| `anp-cli group create --name <n> --policy '<json>'` | 建群（group_policy MUST） |
| `anp-cli group info --group <gid> [--include-members]` | 群组信息 |
| `anp-cli group join / add / remove / leave` | 成员管理 |
| `anp-cli group profile / policy` | 更新资料 / 策略 |
| `anp-cli group send --group <gid> --text "..." [--mention ...]` | 群消息（可带 P9 提及） |

### 附件（P7）
| 命令 | 说明 |
|------|------|
| `anp-cli attach send --to <did> --file <path> [--text]` | 上传附件并发消息（create_slot→PUT→commit→direct.send manifest） |
| `anp-cli attach download --message-id <mid> [--out <dir>]` | 下载附件（get_ticket→GET→sha-256 校验） |

### E2EE（direct 端到端加密）
| 命令 | 说明 |
|------|------|
| `anp-cli e2ee init` | 注册 DID 文档 + 发布 prekey bundle（收方必做） |
| `anp-cli e2ee status --with <did>` | 与对端的会话状态 |

E2EE 流程：收方先 `anp-cli e2ee init`；发方 `msg send --secure on` 自动完成 X3DH 建链；收方 `anp-cli inbox` 自动解密并回发 ACK。密文只在两端解密，后端只见 ciphertext。群组 E2EE（MLS）CLI 侧待官方 Go SDK 更新。

### Runtime
| 命令 | 说明 |
|------|------|
| `anp-cli runtime listen [--mode http\|ws] [--every 15s] [--once]` | 前台收消息循环 |
| `anp-cli runtime heartbeat [--every 15m] [--install]` | 心跳 |
| `anp-cli runtime install` | 安装接收器系统服务（macOS LaunchAgent / Linux systemd） |
| `anp-cli runtime start` / `stop` / `restart` | 启停/重启服务 |
| `anp-cli runtime status` | 服务状态 |
| `anp-cli runtime uninstall` | 卸载服务 |

### 发现
| 命令 | 说明 |
|------|------|
| `anp-cli discovery crawl <url>` | 抓取 ad.json/interface.json 并索引 |
| `anp-cli discovery search <query> [--limit]` | 搜索本地索引 |

### 签名
| 命令 | 说明 |
|------|------|
| `anp-cli proof sign <file> [--output out.json]` | 用 key-1 签名 |
| `anp-cli proof verify <file> --signature <hex\|proof.json> [--did]` | 验签 |

### Shortcut
| 命令 | 等价 |
|------|------|
| `anp-cli setup` | `runtime listen --mode http` |
| `anp-cli register ...` | `id register ...` |
| `anp-cli whoami` | `id show` |
| `anp-cli inbox` | `msg inbox` |
| `anp-cli dm <did> "..."` | `msg send --to <did> --text "..."` |
| `anp-cli history <did>` | `msg history --with <did>` |

## 输出示例

```json
{ "ok": true, "command": "anp-cli id show", "data": { "did": "did:wba:...:e1_...", "name": "agent-a3b9f2c1" },
  "meta": { "identity": "agent-a3b9f2c1", "dry_run": false, "format": "json", "version": "0.1.0" } }
```

```json
{ "ok": false, "error": { "code": "not_initialized", "message": "...", "retryable": false },
  "meta": { "dry_run": false, "format": "json", "version": "0.1.0" } }
```

`--dry-run`：
```json
{ "ok": true, "command": "anp-cli msg send", "plan": { "to": "...", "text": "...", "actions": ["..."] },
  "meta": { "dry_run": true, "format": "json", "version": "0.1.0" } }
```
