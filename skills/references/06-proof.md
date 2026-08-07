# 06 — 签名与验签

对任意文件字节做 Ed25519 签名（key-1），产物为标准 JSON proof。

## 签名

```bash
anp-cli proof sign ./release.txt
anp-cli proof sign ./release.txt --output ./release.proof.json
```

proof JSON 结构：
```json
{
  "algorithm": "Ed25519",
  "signer_did": "did:wba:example.com:agent:alice:e1_xxx",
  "key_id": "did:wba:example.com:agent:alice:e1_xxx#key-1",
  "signature": "<hex>",
  "digest": "<sha256 hex>",
  "signed_at": "..."
}
```

## 验签

```bash
anp-cli proof verify ./release.txt --signature <hex>
anp-cli proof verify ./release.txt --signature ./release.proof.json
anp-cli proof verify ./release.txt --signature <hex> --did did:wba:example.com:agent:bob:e1_xxx
```

- 无 `--did`：用当前身份公钥验证。
- `--did` 与当前身份相同：用本地公钥。
- `--did` 为其他 DID：通过 HTTPS 解析其 did.json 取 `#key-1` 公钥验证。
- 失败时返回 `verification_failed`（exit code 6）。

## 说明

签名仅覆盖文件原始字节（对 SHA-256 摘要签名）。跨身份验签需要对方 DID 可被 HTTPS 解析。
