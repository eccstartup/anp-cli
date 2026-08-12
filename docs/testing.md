# anp-cli 测试指南

两种方式：**① 一键自动冒烟**（推荐先跑这个）；**② 手动逐步测试**（理解每一步在干嘛）。

## 0. 前提

- Go 1.22+，`python3`（仅冒烟脚本的静态站点用）。
- 仓库内操作：`cd /Users/eccstartup/code/claude/anp-cli`

## ① 一键冒烟（自动）

```bash
bash scripts/smoke-test.sh            # 全量 62 项（含 daemon 安装/卸载，会短暂装 LaunchAgent 再卸）
bash scripts/smoke-test.sh --skip-daemon   # 跳过系统服务部分
```

做了什么：构建二进制 → 启动内存 mock 后端 → 启动静态站点（供 discovery）→ 按 13 组逐项跑命令并断言，打印 `PASS/FAIL`。全部通过输出 `62 项全部通过 ✓`。

再加 Go 层测试：

```bash
go test ./...        # 单元 + 集成测试（含 E2EE 双 agent 往返、抢注）
go vet ./...         # 静态检查
```

## ② 手动逐步测试

### 第 0 步：启动 mock 后端（模拟 ANP 服务器）

```bash
cd /Users/eccstartup/code/claude/anp-cli
go run ./cmd/mock
# 输出: http://127.0.0.1:XXXXX   ← 记下这个 URL，下文用 $MOCK 代替
```

另开一个终端。下面每条命令用一个独立工作区，避免互相污染；用完 `rm -rf /tmp/anp-t1` 清理。

### 第 1 组：基础命令（无后端也能跑）

```bash
export ANP_WORKSPACE=/tmp/anp-t1
anp-cli version                    # 应看到 cli: anp-cli, sdk: v0.9.2
anp-cli --help
anp-cli doctor                     # 诊断，警告可接受
```

### 第 2 组：初始化 + 身份

```bash
anp-cli init alice --format pretty    # 生成 DID，看 did 字段
anp-cli whoami                        # shortcut，打印身份
anp-cli id show                       # canonical，含完整 did_document
```

**检查点**：`did` 形如 `did:wba:<域名>:agent:alice:e1_<指纹>`；`~/.anp/identities/alice/` 下有 `did.json` + 3 个私钥 PEM。

### 第 3 组：多身份管理

```bash
anp-cli init bob                      # 第二个身份（默认仍为 alice）
anp-cli id list --format table        # 两行，alice 标 current=true
anp-cli id current                    # name=alice
anp-cli id use bob                    # 切换默认
anp-cli whoami                        # 现在 name=bob
anp-cli id use alice && anp-cli whoami --identity bob   # 单次临时选 bob
```

### 第 4 组：配置

```bash
anp-cli config set --did-domain example.com
anp-cli config show                  # did_domain 生效
```

### 第 5 组：注册 handle + 抢注

```bash
export ANP_BACKEND=$MOCK
anp-cli register --handle alice.agent --email a@example.com   # status: registered
# 换一个身份抢同一个 handle：
export ANP_WORKSPACE=/tmp/anp-t1-bob && anp-cli init bob >/dev/null
ANP_BACKEND=$MOCK anp-cli register --handle alice.agent       # code: handle_taken + 备选建议
ANP_BACKEND=$MOCK anp-cli register --handle alice.agent.1     # 换变体成功
```

### 第 6 组：消息（direct + 历史）

```bash
export ANP_WORKSPACE=/tmp/anp-t1
BOB=$(ANP_WORKSPACE=/tmp/anp-t1-bob anp-cli whoami --jq '.data.did' | tr -d '"')
anp-cli msg send --to "$BOB" --text "hello bob"   # 返回 message_id
anp-cli dm "$BOB" "via dm"                        # shortcut
anp-cli msg inbox --format table                  # 看到两条 out 消息
anp-cli msg history --with "$BOB"                 # 会话历史
anp-cli msg send --text x                         # 应报 invalid_argument（缺 --to/--group）
```

### 第 7 组：群组

```bash
GID=$(anp-cli group create --name team --jq '.data.group_did' | tr -d '"')
anp-cli group join --group "$GID"
anp-cli group members --group "$GID" --format table
anp-cli msg send --group "$GID" --text "hi team"
anp-cli group leave --group "$GID"
```

### 第 8 组：E2EE（direct 加密）

```bash
anp-cli e2ee init                     # alice 发布 prekey bundle
ANP_WORKSPACE=/tmp/anp-t1-bob ANP_BACKEND=$MOCK anp-cli e2ee init   # bob 也发布
anp-cli msg send --to "$BOB" --text "top secret" --secure on        # alice 加密发
ANP_WORKSPACE=/tmp/anp-t1-bob ANP_BACKEND=$MOCK anp-cli msg inbox --scope direct
                                      # bob 侧看到明文 "top secret"，secure=true
# 群组加密被 SDK 官方门禁封锁：
anp-cli msg send --group "$GID" --text x --secure on
                                      # 报错含 "P6 v2 public release is blocked"
```

### 第 9 组：签名/验签

```bash
echo hi > /tmp/f.txt
anp-cli proof sign /tmp/f.txt                              # 打印 proof
SIG=$(anp-cli proof sign /tmp/f.txt --jq '.data.signature' | tr -d '"')
anp-cli proof verify /tmp/f.txt --signature "$SIG"          # valid: true
anp-cli proof verify /tmp/f.txt --signature 01020304        # verification_failed
```

### 第 10 组：发现

```bash
mkdir -p /tmp/anp-web && echo '{"name":"OCR","capabilities":["ocr"]}' > /tmp/anp-web/ad.json
( cd /tmp/anp-web && python3 -m http.server 18765 --bind 127.0.0.1 & )   # 起静态站
anp-cli discovery crawl http://127.0.0.1:18765/ad.json     # 索引成功
anp-cli discovery search ocr                               # 搜到 1 条
```

### 第 11 组：runtime

```bash
anp-cli runtime listen --once          # 单次拉取 inbox
anp-cli runtime heartbeat              # 单次心跳
anp-cli runtime install                # 装系统服务（macOS → LaunchAgent）
anp-cli runtime status                 # running / stopped
anp-cli runtime uninstall              # 卸载（plist 被删）
```

### 第 12 组：全局参数

```bash
anp-cli msg send --to "$BOB" --text x --dry-run   # 返回 plan 字段，不真正发送
anp-cli inbox --format table                      # 表格
anp-cli inbox --jq '.data.messages | length'      # jq 过滤
anp-cli id show --json                            # 强制 json
```

### 第 13 组：错误路径

```bash
unset ANP_BACKEND; anp-cli msg inbox   # 报错：no backend configured（code internal_error）
rm -rf /tmp/anp-fresh; ANP_WORKSPACE=/tmp/anp-fresh anp-cli whoami  # not_initialized
```

## 常见问题

- `ANP_ENV[@]: unbound variable`：是脚本在旧版 bash（macOS 3.2）+ `set -u` 下的已知写法问题，已用 `${ANP_ENV[@]+...}` 规避；若仍出现请用 `/bin/bash scripts/smoke-test.sh` 或更新 bash。
- 端口冲突：冒烟脚本动态选端口；手动第 10 组若 18765 被占用，换一个。
- 产物/日志清理：脚本退出自动清理 `/tmp/anp-smoke-*`；手动测试用完 `rm -rf /tmp/anp-t1 /tmp/anp-t1-bob /tmp/anp-web`。
