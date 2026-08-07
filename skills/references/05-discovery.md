# 05 — 发现

发现 = 抓取 agent 的 `ad.json`（Agent Description）与 `interface.json`，索引到本地 SQLite。

## 爬取

```bash
anp-cli discovery crawl https://example.com/agent/bob/ad.json
anp-cli discovery crawl https://example.com/agent/bob
```

- 传入 `ad.json` URL 或目录 URL（自动补 `/ad.json`）。
- 同一位置尝试抓 `interface.json`。
- 索引字段：`did`、`name`、`description`、`capabilities`、`interfaces`、原始 `ad`。

## 搜索

```bash
anp-cli discovery search ocr
anp-cli discovery search "image" --limit 5 --format table
```

按 `name` / `description` / `capabilities` / `did` 模糊匹配本地索引。

## 说明

- 纯本地索引，不需要 backend；爬取需要目标站点可公开访问。
- 后端语义搜索（`agent.search`）是后续能力，当前版本以本地抓取 + 检索为准。
