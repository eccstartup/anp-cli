# 00 — 安装与初始化

## 安装

```bash
go install github.com/eccstartup/anp-cli/cmd/anp-cli@latest
# 或本地构建
cd anp-cli && make build   # 产出 ./bin/anp-cli
```

## 初始化工作区

```bash
anp-cli init                # 创建 ~/.anp/ + 随机生成身份名（如 agent-a3b9f2c1）
anp-cli init alice          # 创建 ~/.anp/ + 指定身份名 alice
ANP_BACKEND=https://awiki.ai anp-cli init bob   # 绑定后端并命名身份 bob
```

`init` 会：
- 创建工作区（默认 `~/.anp/`，`ANP_WORKSPACE` 可覆盖）
- 写入最小 `config.yaml`（identity、backend、did_domain）
- 生成 e1 profile 的 `did:wba` 身份 + key-1/2/3 密钥

## 配置后端

三种方式（优先级从高到低）：
1. 环境变量 `ANP_BACKEND=https://awiki.ai`
2. `anp-cli config set --backend https://awiki.ai`（写入 `~/.anp/config.yaml`）
3. 默认无后端（离线命令仍可用）

DID 域名同理：`anp-cli config set --did-domain example.com`，未配置时从后端 host 推导（IP 或 localhost 回退 `localhost`）。

## 诊断

```bash
anp-cli doctor          # 检查 build/config/backend/identity store/database
anp-cli status          # 工作区状态总览
anp-cli version         # 版本与 SDK 信息
```

## 常见问题

- `no identity selected` → 先 `anp-cli init`。
- `no backend configured` → 设置 `ANP_BACKEND` 或 `anp-cli config set --backend`。
- 权限问题 → 确认 `~/.anp/identities` 下私钥文件权限为 0600。
