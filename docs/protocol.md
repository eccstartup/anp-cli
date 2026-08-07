# ANP CLI 后端协议（anp-jsonrpc-v1）

`anp` 是协议级 CLI：不绑定任何网站，只消费 ANP 协议。本文件定义 CLI 对任意后端的网络协议，后端地址由 `ANP_BACKEND` 或 `~/.anp/config.yaml` 的 `backend` 指定。

## 传输

- 所有调用：`POST {backend}/rpc`
- 请求头：`Content-Type: application/json`
- 身份认证：HTTP Message Signatures（RFC 9421），用当前身份 did:wba 文档 + Ed25519 key-1。
  - 签名覆盖组件：`@method`、`@target-uri`、`@authority`、`content-digest`
  - `keyid = <did>#key-1`
  - 由 SDK `authentication.GenerateHTTPSignatureHeaders` 生成 `Signature-Input` / `Signature` / `Content-Digest`
  - 后端验证：`authentication.VerifyHTTPMessageSignature(doc, method, url, headers, body)`
- 请求体：JSON-RPC 2.0

```json
{ "jsonrpc": "2.0", "method": "msg.send", "params": { "...": "..." }, "id": 1 }
```

响应：
```json
{ "jsonrpc": "2.0", "result": { "...": "..." }, "id": 1 }
```
或
```json
{ "jsonrpc": "2.0", "error": { "code": -32000, "message": "..." }, "id": 1 }
```

## 方法

### 身份 / handle

| method | params | result |
|--------|--------|--------|
| `did.resolve` | `{target}`（DID 或 handle） | `{did, did_document}` |
| `did.register_document` | `{did, did_document}` | `{did, status}` |
| `handle.register` | `{handle, did, phone?, email?, otp?}` | `{did, handle, status}` |
| `handle.recover` | `{handle, phone?, email?, otp?}` | `{did, handle?, status}` |

`did.register_document` 在 E2EE 初始化时调用（`anp-cli e2ee init`），让对端可以解析本机 DID 文档；它是后端唯一免签名校验的方法（bootstrap）。

### 消息

| method | params | result |
|--------|--------|--------|
| `msg.send` | `{to?\|group?, body:{type, text}, secure}` | `{message_id, thread_id?, sent_at, state}` |
| `msg.inbox` | `{scope: all\|direct\|group, unread?, limit?}` | `{messages: [message]}` |
| `msg.history` | `{with, limit?}` | `{messages: [message]}` |

`message` 对象：
```json
{ "message_id": "msg_1", "sender_did": "...", "recipient_did": "...", "group_did": "",
  "type": "text", "text": "hello", "secure": false, "sent_at": "..." }
```

### 群组

| method | params | result |
|--------|--------|--------|
| `group.create` | `{name, members?}` | `{group_did, name, members}` |
| `group.join` | `{group}` | `{status}` |
| `group.leave` | `{group}` | `{status}` |
| `group.members` | `{group}` | `{members: [member]}` |

### E2EE（`--secure on`）

E2EE 使用 ANP SDK `direct_e2ee`（X3DH + 双棘轮），线格式遵循 SDK 参考客户端。三个附加方法：

| method | params | result |
|--------|--------|--------|
| `direct.send` | `{meta:{...}, body:{...}}` | `{message_id, state, sent_at}` |
| `direct.e2ee.publish_prekey_bundle` | `{meta, body:{prekey_bundle, one_time_prekeys}}` | `{status, owner_did}` |
| `direct.e2ee.get_prekey_bundle` | `{meta, body:{target_did, require_opk}}` | `{prekey_bundle, one_time_prekey?}` |

- `msg.send` 的 `secure` 标志发送密文时改用 `direct.send`，`meta.content_type` 为 `application/anp-direct-init+json`（首条，含 X3DH 参数）或 `application/anp-direct-cipher+json`（后续，双棘轮）。
- 入站 `msg.inbox` 的 secure 条目携带完整 `{meta, body}` 信封；CLI 本地解密后才落库。
- 建立流程：收方先 `anp-cli e2ee init` 发布自己的 prekey bundle；发方首条消息取收方 bundle + 一次性预密钥（OPK）做 X3DH；收方解密后自动回发加密 ACK，确认发方会话。
- OPK 用尽时 `get_prekey_bundle` 返回 `anp.direct.e2ee.opk_unavailable`，发方自动降级为无 OPK 建链。

## 错误码约定（CLI 侧）

CLI 把 JSON-RPC error 渲染为 envelope 的 `error` 字段；本地参数错误用 exit code 2（`invalid_argument`），未初始化用 3（`not_initialized`），未找到用 5（`not_found`），验签失败用 6（`verification_failed`），网络/后端错误为 1（`internal_error`，可重试标记由后端错误决定）。

## 兼容性

- `@target-uri` 使用完整 URL 字符串签名（与 SDK verify 一致）。第三方后端若按 path-only 签名，需在接入时对齐。
- E2EE 线格式直接复用 SDK `direct_e2ee` 参考客户端，`direct.*` 方法与 meta/body 结构见上文；接收方必须先 `e2ee init` 发布 bundle 才能被安全建链。
- **群组 E2EE 暂不可用**：ANP SDK 的 P6 v2（基于 MLS）被官方门禁封锁（`group_e2ee.EnsureP6V2PublicReleaseReady()` 返回 `ErrP6V2PublicReleaseBlocked`，"blocked until the draft MLS extension has a stable registered codepoint"）。CLI 已将群组 `--secure on` 接到该门禁，SDK 解锁后即可接线 P6 客户端。
