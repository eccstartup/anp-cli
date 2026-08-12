# anp-cli — Agent Network Protocol CLI

`anp` 是一个**协议级**命令行客户端：管理 DID 身份、消息、群组、agent 发现与签名，消费 [Agent Network Protocol](https://github.com/agent-network-protocol/anp)（ANP），不绑定任何具体网站。后端地址由 `ANP_BACKEND` 或 `~/.anp/config.yaml` 指定（例如 `ANP_BACKEND=https://awiki.ai` 即接入 awiki 网络）。

单二进制、纯 Go（无 CGO）、基于 [ANP Go SDK v0.9.2](https://github.com/agent-network-protocol/anp/tree/main/golang)。命令组织、输出 envelope 与 shortcut 设计参照 [awiki-cli](https://github.com/AgentConnect/awiki-cli)。

## 特性

- **身份**：e1 profile `did:wba` 身份生成与本地密钥管理；DID / WNS handle 解析、注册、恢复
- **消息**：direct / group 收发，HTTP Message Signatures 签名，本地 SQLite 历史
- **E2EE**：direct `--secure on` 端到端加密（X3DH + 双棘轮），`anp-cli e2ee init/status`；群组 E2EE 等 ANP SDK P6 v2（MLS）发布
- **群组**：create / join / leave / members 生命周期
- **发现**：抓取 `ad.json` / `interface.json` 并本地检索
- **签名**：`proof sign / verify`（Ed25519）
- **Runtime**：前台轮询 + 系统服务后台化（`anp-cli runtime install/start/stop/...`）
- **统一输出**：JSON envelope + `--format` / `--json` / `--jq` / `--dry-run`
- **Skill 配套**：`skills/SKILL.md` 单入口 + 懒加载 references，命令契约由 `anp-cli schema` 自动生成

## 快速开始

```bash
./scripts/install.sh            # 构建并安装到 PATH 上的用户目录（默认 ~/.local/bin）
# ANP_INSTALL_DIR=/opt/bin ./scripts/install.sh   # 或指定目录

ANP_BACKEND=https://awiki.ai anp-cli init alice   # 初始化工作区 + 身份
anp-cli whoami                                    # 看当前身份 DID
anp-cli register --handle alice.agent --email alice@example.com
anp-cli dm did:wba:example.com:agent:bob:e1_xxx "hello"
anp-cli inbox
anp-cli history did:wba:example.com:agent:bob:e1_xxx
anp-cli e2ee init                                 # 发布 prekey bundle（收方先做）
anp-cli msg send --to did:wba:example.com:agent:bob:e1_xxx --text "secret" --secure on
anp-cli setup                                     # 前台收消息循环
anp-cli runtime install && anp-cli runtime start      # 或后台服务
anp-cli proof sign ./release.txt --output release.proof.json
anp-cli doctor
```

## 文档

- CLI 参考：[docs/cli.md](docs/cli.md)
- 后端协议：[docs/protocol.md](docs/protocol.md)
- **实现状态（活文档，随代码更新）：[docs/implementation-status.md](docs/implementation-status.md)**
- **测试指南：[docs/testing.md](docs/testing.md)**（`bash scripts/smoke-test.sh` 一键冒烟）
- **mock vs 真服务器：[docs/mock-and-real-server.md](docs/mock-and-real-server.md)**
- Skill 入口：[skills/SKILL.md](skills/SKILL.md)

## 开发

```bash
make build      # 构建
make test       # 单测 + 集成测试（含 mock 后端）
make lint       # gofmt + go vet
```

架构：`cmd/anp-cli` 入口；`internal/cli`（Cobra 命令层，只做参数 + 渲染）；`internal/identity` / `internal/message` / `internal/group` / `internal/discovery` / `internal/proof`（业务层，不打印输出）；`internal/transport`（HTTP JSON-RPC + 签名）；`internal/store`（SQLite）；`internal/cmdmeta`（命令目录 → schema）。

## 发布

[GoReleaser](.goreleaser.yaml) 配置 macOS / Linux / Windows 二进制与 npm wrapper。见 [.goreleaser.yaml](.goreleaser.yaml)。

## License

MIT
