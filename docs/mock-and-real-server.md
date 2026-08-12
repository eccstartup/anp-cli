# mock 与真实服务器：测试时该用谁？

## 一、mock 到底是干嘛的？

`scripts/mockrun/` 是一个**装在内存里的假 ANP 服务器**（`go run ./scripts/mockrun` 启动）。

## 二、mock vs 真服务器

| | scripts/mockrun | anp-server |
|---|---|---|
| **位置** | CLI 项目内 `scripts/mockrun/` | 独立项目 `/Users/eccstartup/code/claude/anp-server-go/` |
| **存储** | 内存，进程重启即清空 | SQLite 持久化，重启不丢 |
| **鉴权** | 不校验签名 | 首次引导期接受所有请求；有 DID 后严格签名校验 |
| **用途** | 冒烟测试、本地快速验证 CLI 逻辑 | 真实集成测试、验证签名/持久化 |

## 三、什么时候用哪个

| 阶段 | 用谁 | 目的 |
|------|------|------|
| 日常开发 / 改代码 | `go test` + `bash scripts/smoke-test.sh`（自动起 mock） | 快速确认没改坏 |
| 学功能 / 手动验证 | `go run ./scripts/mockrun` 起 mock，然后 `anp-cli` 操作 | 理解行为 |
| 验证持久化 / 签名 | `anp-server --db ./data.db` 起真服务器 | 确认重启不丢数据、签名正确 |
| 上线前集成 | 真实后端（`ANP_BACKEND=真实地址`） | 验证互通 |

## 四、真服务器怎么启动

```bash
cd /Users/eccstartup/code/claude/anp-server-go
bash scripts/start.sh                 # 后台启动
bash scripts/start.sh --foreground    # 前台启动
bash scripts/stop.sh                  # 停止
```
