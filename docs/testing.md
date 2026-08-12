# anp-cli 手把手测试清单

> 不需要读代码。**照着下面一条条做**，每步给你"命令 → 期望输出 → 这证明了什么"。
> 用 `[ ]` 打勾，做完一遍你就懂了 CLI 在干嘛。
> mock 是干嘛的、为什么用 mock、怎么接真服务器：见 [mock-and-real-server.md](mock-and-real-server.md)。

## 准备（一次就好）

```bash
cd /Users/eccstartup/code/claude/anp-cli
go build -o bin/anp-cli ./cmd/anp-cli     # 构建 CLI
```

**开一个终端 A，起假服务器（保持它一直跑）：**
```bash
go run ./scripts/mockrun
# 输出形如: http://127.0.0.1:54321   ← 记下这个地址，下面用 $MOCK 代替
```

**终端 B** 里设好沙盒（每轮测试用完 `rm -rf /tmp/anp-hw` 即重置）：
```bash
export ANP_WORKSPACE=/tmp/anp-hw
export ANP_BACKEND=http://127.0.0.1:54321   # 换成你终端 A 看到的实际地址
```

---

## 5 分钟最小路径（先跑通这条，你就学会了测试循环）

- [ ] **1. 初始化身份**
  ```bash
  anp-cli init me
  ```
  ✅ 输出含 `did`，形如 `did:wba:localhost:agent:me:e1_xxxxx`。
  💡 证明：CLI 生成了密钥对 + DID 文档，写进了 `/tmp/anp-hw/identities/me/`。

- [ ] **2. 看身份**
  ```bash
  anp-cli whoami --jq '.data.did'
  ```
  ✅ 打印同一个 did。
  💡 证明：身份被保存并能读回（`--jq` 只取你要的字段，大 JSON 不用怕）。

- [ ] **3. 发消息**
  ```bash
  anp-cli msg send --to did:wba:test:user:bob --text "hello" --jq '.data.message_id'
  ```
  ✅ 打印 `msg_1`。
  💡 证明：签名 → 发 HTTP 给 mock → mock 收下 → 本地落库，整条链路通。

- [ ] **4. 收件箱**
  ```bash
  anp-cli msg inbox --format table
  ```
  ✅ 表格里能看到刚才那条 `hello`（direction=out）。
  💡 证明：本地 SQLite 存了消息，inbox 能读出来。

- [ ] **5. 签名**
  ```bash
  anp-cli proof sign /tmp/anp-hw/identities/me/did.json --jq '.data.signature'
  ```
  ✅ 打印一串十六进制签名。
  💡 证明：Ed25519 签名正常（改文件后验签会失败，见第 9 组）。

---

## 完整清单（13 组）

### 第 1 组 基础命令（不需要服务器）
- [ ] `anp-cli version` → ✅ 见 `cli: anp-cli, sdk: v0.9.2`
- [ ] `anp-cli --help` → ✅ 列出所有命令
- [ ] `unset ANP_BACKEND; anp-cli doctor` → ✅ 诊断报告（有警告也正常）
- [ ] `anp-cli schema msg.send` → ✅ 单个命令的参数/输出契约
- [ ] `anp-cli config show` → ✅ 当前配置（backend / did_domain / identity）

### 第 2 组 多身份（每个人 = 独立 DID）
- [ ] `anp-cli init bob` → ✅ 成功（**默认身份仍是 me**，init 不会抢默认）
- [ ] `anp-cli id list --format table` → ✅ 两行，me 标记 current
- [ ] `anp-cli id use bob` → ✅ 切默认
- [ ] `anp-cli whoami` → ✅ 现在是 bob
- [ ] `anp-cli id use me && anp-cli whoami --identity bob` → ✅ 临时用 bob 一次

### 第 3 组 配置
- [ ] `anp-cli config set --did-domain example.com` → ✅ 成功
- [ ] `anp-cli config show` → ✅ did_domain=example.com（已有身份的 DID 不变）

### 第 4 组 注册 handle + 抢注
- [ ] `anp-cli register --handle me.agent --email a@example.com` → ✅ status: registered
- [ ] 抢注（用另一个身份抢同一个）：
  ```bash
  ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli init bob >/dev/null
  ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli register --handle me.agent
  ```
  ✅ 报错 `code: handle_taken` + 提示换变体
- [ ] `ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli register --handle me.agent.1` → ✅ 成功

### 第 5 组 消息 + 历史
- [ ] `BOB=$(ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli whoami --jq '.data.did' | tr -d '"')`
- [ ] `anp-cli msg send --to "$BOB" --text "hello bob"` → ✅ message_id
- [ ] `anp-cli dm "$BOB" "via dm"` → ✅ message_id（shortcut）
- [ ] `anp-cli msg inbox --format table` → ✅ 看到两条 out 消息
- [ ] `anp-cli msg history --with "$BOB"` → ✅ 与 bob 的会话记录
- [ ] `anp-cli msg send --text x` → ✅ 报错 invalid_argument（缺 --to/--group）

### 第 6 组 群组
- [ ] `GID=$(anp-cli group create --name team --jq '.data.group_did' | tr -d '"')`
- [ ] `anp-cli group join --group "$GID"` → ✅
- [ ] `anp-cli group members --group "$GID" --format table` → ✅
- [ ] `anp-cli msg send --group "$GID" --text "hi team"` → ✅
- [ ] `anp-cli group leave --group "$GID"` → ✅

### 第 7 组 E2EE（direct 加密）
- [ ] `anp-cli e2ee init` → ✅ 发布 prekey bundle
- [ ] `ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli e2ee init` → ✅ bob 也发布
- [ ] `anp-cli msg send --to "$BOB" --text "top secret" --secure on` → ✅
- [ ] `ANP_WORKSPACE=/tmp/anp-hw-bob anp-cli msg inbox --scope direct` → ✅ 看到明文 "top secret"（secure=true）
- [ ] `anp-cli msg send --group "$GID" --text x --secure on` → ✅ 报错含 `P6 v2 public release is blocked`（SDK 官方封锁群加密）

### 第 8 组 签名/验签
- [ ] `echo hi > /tmp/h.txt; anp-cli proof sign /tmp/h.txt --output /tmp/h.proof.json` → ✅
- [ ] `anp-cli proof verify /tmp/h.txt --signature /tmp/h.proof.json` → ✅ `valid: true`
- [ ] `anp-cli proof verify /tmp/h.txt --signature 01020304` → ✅ 报错 verification_failed
- [ ] 改文件后验签：`echo hi2 > /tmp/h.txt; anp-cli proof verify /tmp/h.txt --signature /tmp/h.proof.json` → ✅ 失败（内容变了）

### 第 9 组 发现（爬取 ad.json）
- [ ] 起一个静态站：`mkdir -p /tmp/anp-web; echo '{"name":"OCR","capabilities":["ocr"]}' > /tmp/anp-web/ad.json; (cd /tmp/anp-web && python3 -m http.server 18765 --bind 127.0.0.1 &)`
- [ ] `anp-cli discovery crawl http://127.0.0.1:18765/ad.json` → ✅ 索引成功
- [ ] `anp-cli discovery search ocr` → ✅ 搜到 1 条

### 第 10 组 runtime（收消息循环）
- [ ] `anp-cli runtime listen --once` → ✅ 单次拉取
- [ ] `anp-cli runtime heartbeat` → ✅ 心跳
- [ ] `anp-cli runtime install` → ✅ 装系统服务（macOS → LaunchAgent）
- [ ] `anp-cli runtime status` → ✅ running/stopped
- [ ] `anp-cli runtime uninstall` → ✅ 卸载

### 第 11 组 全局参数
- [ ] `anp-cli msg send --to "$BOB" --text x --dry-run` → ✅ 返回 `plan` 字段（没真发）
- [ ] `anp-cli inbox --format table` → ✅ 表格
- [ ] `anp-cli inbox --jq '.data.messages | length'` → ✅ 数字
- [ ] `anp-cli id show --json` → ✅ json

### 第 12 组 错误路径
- [ ] `unset ANP_BACKEND; anp-cli msg inbox` → ✅ 报 no backend（internal_error）
- [ ] `ANP_WORKSPACE=/tmp/anp-empty anp-cli whoami` → ✅ 报 not_initialized

### 第 13 组 一键收尾
- [ ] `bash scripts/smoke-test.sh --skip-daemon` → ✅ `结果: N 项全部通过 ✓`
- [ ] `go test ./...` → ✅ 全绿

---

## 收尾清理
```bash
pkill -f mockrun      # 停掉假服务器
rm -rf /tmp/anp-hw /tmp/anp-hw-bob /tmp/anp-web /tmp/h.txt /tmp/h.proof.json
```
