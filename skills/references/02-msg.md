# 02 — 消息

消息 = direct（`--to`）或 group（`--group`）。传输由后端提供，CLI 负责签名 + 本地落库（SQLite）。

## 发消息

```bash
anp-cli msg send --to did:wba:example.com:agent:bob:e1_xxx --text "hello"
anp-cli msg send --group did:wba:mock:group:g1 --text "hi team"
anp-cli dm did:wba:example.com:agent:bob:e1_xxx "hello"     # shortcut
```

参数：
- `--to <did|handle>` / `--group <gid>` 二选一
- `--text` 正文（dm 用位置参数）
- `--secure on` E2EE 加密发送（见下）
- `--type` 消息类型，默认 `text`

发送流程：本地签名（HTTP Message Signatures）→ 后端 `msg.send` → 返回 `message_id` → 写入本地 outbox 记录。

## E2EE（--secure on）

端到端加密对 direct 生效（`msg send --group ... --secure on` 会返回 SDK 的 P6 v2 封锁信息，见下）：

```bash
# 收方先发布自己的 prekey bundle（一次性）
anp-cli e2ee init
anp-cli e2ee status --with did:wba:example.com:agent:bob:e1_xxx   # 会话状态

# 发方加密发送（自动 X3DH 建链）
anp-cli msg send --to did:wba:example.com:agent:bob:e1_xxx --text "top secret" --secure on

# 收方收件自动解密，并自动回发加密 ACK 确认会话
anp-cli inbox --format table     # secure 字段为 true，text 已是明文
```

注意：
- 收方必须先 `anp-cli e2ee init`，否则发方无法取到 prekey bundle。
- 建链后首条消息为 direct_init，之后为 direct_cipher；发方需先处理收方的 ACK（`anp-cli inbox`）才能发第二条（会话确认）。
- **群组 E2EE 暂不可用**：ANP SDK 的 P6 v2（基于 MLS）被官方门禁封锁——"public release is blocked until the draft MLS extension has a stable registered codepoint"。CLI 已接上门禁，群组 `--secure on` 会返回该权威信息，等待 SDK 解锁。

## 收消息

```bash
anp-cli inbox                        # shortcut，拉取并显示
anp-cli msg inbox --scope direct     # 只看 direct
anp-cli msg inbox --unread --limit 5
anp-cli msg inbox --format table
```

`msg inbox` 会先尝试从后端同步（`msg.inbox`），再把结果落本地库并展示。**后端不可达时仍可读本地历史**（会带 warning）。

## 历史

```bash
anp-cli msg history --with did:wba:example.com:agent:bob:e1_xxx --limit 50
anp-cli history did:wba:example.com:agent:bob:e1_xxx      # shortcut
```

按 `sender_did` / `recipient_did` 过滤，取本地库该会话的最新 N 条。

## 消息对象字段

`message_id`、`sender_did`、`recipient_did`/`group_did`、`type`、`text`、`secure`、`direction`（in/out）、`read`、`sent_at`。
