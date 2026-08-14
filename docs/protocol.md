# ANP CLI 后端协议（anp-jsonrpc-v1）

`anp-cli` 是协议级 CLI：不绑定任何网站，只消费 ANP 协议。本文件定义 CLI 与任意 ANP 标准后端之间的网络协议。后端地址由 `ANP_BACKEND` 或 `~/.anp/config.yaml` 的 `backend` 指定。

本实现**遵循 ANP 分层协议**：Base 语义层（明文 `transport-protected`，合规默认形态）之上叠加可选的安全覆盖层（E2EE）。

## 传输

- 所有调用：`POST {backend}/rpc`
- 请求头：`Content-Type: application/json`
- 身份认证：HTTP Message Signatures（RFC 9421），用当前身份 did:wba 文档 + Ed25519 key-1。
  - 签名覆盖组件：`@method`、`@target-uri`、`@authority`、`content-digest`
  - `keyid = <did>#key-1`（服务级签名时为 `<serviceDid>#key-1`，见 P8）
  - 由 SDK `authentication.GenerateHTTPSignatureHeaders` 生成 `Signature-Input` / `Signature` / `Content-Digest`
  - 后端验证：`authentication.VerifyHTTPMessageSignature(doc, method, url, headers, body)`
- 请求体：JSON-RPC 2.0

```json
{ "jsonrpc": "2.0", "method": "direct.send", "params": { "...": "..." }, "id": 1 }
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
| `handle.register` | `{handle, did}` | `{did, handle, status}` |
| `handle.recover` | `{handle}` | `{did, handle, status}` |

- **handle 是 WNS 标准格式 `localpart.domain`**（如 `alice.example.com`），用 SDK `wns.ValidateHandle` 校验。
- `handle.register` 冲突语义：handle 已被**其他 DID** 注册时返回 `handle_taken`（exit code 4）。
- handle 的注册 / 重绑定**认证完全靠私钥签名**（HTTP Message Signatures，`authDID`）；`handle.recover` 只允许当前 owner（同 DID）重新绑定，跨 DID 恢复因无独立验证通道（无短信/邮件验证）而不支持。
- `did.register_document` 在 E2EE 初始化时调用，让对端可解析本机 DID 文档；新 DID 免签名注册（bootstrap），已存在 DID 需签名。

> email / phone 是 **CLI 本地身份元数据**（联系方式，存于 `index.json`），不传给后端、不参与认证。CLI 的 `register --email/--phone` 仅把它们记录到本地身份。

### direct 消息（P3 base 明文 + P5 E2EE）

`direct.send` 一个方法承载两种 profile：

| profile | security_profile | 说明 |
|---------|------------------|------|
| `anp.direct.base.v1` | `transport-protected` | 明文（默认），body 三选一 `text`/`payload`/`payload_b64u` |
| `anp.direct.e2ee.v1` | `direct-e2ee` | 端到端加密（X3DH + 双棘轮 + ChaCha20-Poly1305） |

| method | params | result |
|--------|--------|--------|
| `direct.send` | `{meta:{...}, body:{...}, auth?}` | `{accepted, message_id, operation_id, target_did, body}` |
| `direct.e2ee.publish_prekey_bundle` | `{meta, body:{prekey_bundle, one_time_prekeys}}` | `{published, owner_did, bundle_id, published_at}` |
| `direct.e2ee.get_prekey_bundle` | `{meta, body:{target_did, require_opk}}` | `{target_did, prekey_bundle, one_time_prekey?}` |

- 明文 direct 的 `meta.content_type`：`text/plain`（普通文本）或 `application/anp-attachment-manifest+json`（附件清单，见 P7）。
- 明文 direct 的 `auth.origin_proof` 是 MUST（P3）；E2EE 是可选（P5）。
- `direct.send` 的 E2EE 路径：`content_type` 为 `application/anp-direct-init+json`（首条，含 X3DH 参数）或 `application/anp-direct-cipher+json`（后续，双棘轮）。
- 收消息走 `msg.inbox`（投递是后端实现细节，ANP 标准不定义 inbox 方法）：

| method | params | result |
|--------|--------|--------|
| `msg.inbox` | `{limit?}` | `{messages: [{server_seq, meta, body}]}` |

- 建链流程：收方先 `e2ee init` 发布 bundle；发方取 bundle + 一次性预密钥（OPK）做 X3DH；收方解密后自动回发加密 ACK。
- OPK 用尽时 `get_prekey_bundle` 返回 `anp.direct.e2ee.opk_unavailable`，发方自动降级为无 OPK 建链。

### 群组（P4 base 明文 + P6 E2EE 控制面）

群组 base 语义（`anp.group.base.v1`，`transport-protected` 明文投递，合规基础形态）：

| method | 说明 |
|--------|------|
| `group.create` | 创建群组（`group_policy` MUST） |
| `group.get_info` | 查询群组信息（含成员列表/策略） |
| `group.join` / `group.add` / `group.remove` / `group.leave` | 成员管理 |
| `group.update_profile` / `group.update_policy` | 更新资料 / 策略 |
| `group.send` | 群组消息（`body.payload` 承载，见 P9 mentions） |
| `group.rebind_member` | 成员身份重新绑定 |

群组 E2EE（`anp.group.e2ee.v1`，基于 MLS）——**server 控制面已实现**（`group.e2ee.publish_key_package` / `get_key_package` / `create` / `add` / `remove` / `send` / `notice`，MLS 对象按不透明 b64u 存储/转发）。**CLI 侧 MLS 计算暂未接线**：Go SDK 的 `ExecProvider` 依赖 Rust `anp-mls` 二进制，但该二进制在对应版本 Rust crate（0.9.3）中已改为库调用而移除，Go 侧无纯 Go MLS 实现——需官方 Go SDK 更新或接入 Rust 库后才能完成 CLI 侧加密/解密。

### 附件（P7）

控制面 4 方法（`anp.attachment.v1`，`transport-protected`，`target.kind=service`）：

| method | 说明 |
|--------|------|
| `attachment.create_slot` | 申请上传槽位，返回 `{attachment_id, slot_id, upload_uri, object_uri, commit_token, expires_at}` |
| `attachment.commit_object` | 提交已上传对象，返回 `{committed, attachment_id, object_uri, committed_at}` |
| `attachment.abort_object` | 终止上传槽位 |
| `attachment.get_download_ticket` | 获取下载票据，返回 `{download_ticket_b64u, expires_at, ticket_binding}` |

数据面（独立 HTTPS 通道，非 JSON-RPC）：
- `PUT <upload_uri>` 上传对象字节
- `GET <object_uri>`（`Authorization: Bearer <download_ticket>`）下载，接收方校验 size + sha-256

消息面：附件通过 `direct.send`（content_type=`application/anp-attachment-manifest+json`）承载，body 结构 `{payload: {attachments:[{attachment_id, filename, mime_type, size, digest, access_info, encryption_info}], caption?, primary_attachment_id?}}`。

### 提及（P9）

不新增方法，仅群消息 body 字段约定：`group.send` 的 `body.payload` 可含 `mentions` 数组（`{id, range:{start,end,unit}, target:{kind:human|agent|group_selector, did?|selector?}, mention_role?}`）。`meta.content_type` 须为 `application/json`。direct 消息不支持提及（v1 范围外）。

### 联邦（P8）

- 服务级签名：配置 `serviceDid` 时，外层 HTTP 签名 `keyid` 切换为 `<serviceDid>#key-1`（区别于 agent 身份签名）。
- 幂等去重：后端按 `sender_did + method + operation_id` 去重，重试返回等价响应。

## 错误码约定

- 应用层错误用 `-32000` 起；DID 未找到 `-32002`，handle 被占 `-32001`，OPK 不可用 `-32003`，参数错误 `-32004`。
- 群组控制面错误码段 `5000-5012`；附件控制面错误码段 `6000-6013`（`anp.attachment.slot_not_found` / `digest_mismatch` / `unauthorized_requester` 等）。
- E2EE 错误消息遵循 ANP 标准字符串：`anp.direct.e2ee.bundle_not_found`、`anp.direct.e2ee.opk_unavailable` 等（SDK 客户端靠这些字符串识别并降级）。

## 兼容性

- `@target-uri` 使用完整 URL 字符串签名（与 SDK verify 一致）。
- E2EE 线格式直接复用 SDK `direct_e2ee`，与 Rust / Python 官方实现互通（SDK integration 测试覆盖 fixture 互解）。
- 任何实现 ANP 标准协议（did:wba + RFC 9421 签名 + base/e2ee profile）的客户端都能与本后端互通。
