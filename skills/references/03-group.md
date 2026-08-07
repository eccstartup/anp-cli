# 03 — 群组

群组 = 后端维护的 group DID + 成员列表。CLI 走 JSON-RPC `group.*` 方法并缓存本地。

## 生命周期

```bash
anp-cli group create --name "Team Alpha" --members '["did:wba:...:bob:e1_x"]'
anp-cli group join --group did:wba:mock:group:g1
anp-cli group members --group did:wba:mock:group:g1 --format table
anp-cli group leave --group did:wba:mock:group:g1
```

- `create` 返回 `group_did`（此后 `--group` 用它）。
- `join` / `leave` 是副作用操作，先 `--dry-run` 再看。
- `members` 返回成员列表（`did`/`handle`）。
- 本地缓存群记录到 SQLite `groups` 表；`leave` 会删除本地记录。

## 给群发消息

```bash
anp-cli msg send --group did:wba:mock:group:g1 --text "hi team"
```

（见 [02-msg.md](02-msg.md)）
