# anp-cli 架构图

> Mermaid 图。在支持 Mermaid 的查看器（GitHub、Typora、VS Code Mermaid 插件、claude.ai 等）中渲染。

## 1. 分层架构

```mermaid
flowchart TD
    User["用户 / Agent 调用 anp"]
    Main["cmd/anp-cli/main.go → cli.Execute()"]

    subgraph L1["命令层 internal/cli（只做参数解析 + 渲染，不写业务）"]
        App["app.go：Execute / 统一错误 / envelope 渲染"]
        CmdTree["root.go：Cobra 命令树（从 cmdmeta 目录生成）"]
        Handlers["Handler 分发表<br/>id / msg / discovery / proof / e2ee / runtime / daemon"]
        Out["output.go：--format / --jq / --dry-run / table"]
        Schema["schema：anp-cli schema（从目录自动生成）"]
    end

    subgraph L2["业务层（不 fmt.Println）"]
        SID["identity<br/>DID 生成 / 文件存储 / 解析 / handle"]
        SMsg["message<br/>send / inbox / history + 入站解密"]
        SE2["e2ee<br/>direct_e2ee 封装（prekey bundle / 会话）"]
        SDisc["discovery<br/>crawl ad.json / search 本地索引"]
        SProof["proof<br/>sign / verify（Ed25519）"]
    end

    subgraph L3["协议 + 存储层"]
        Cfg["config<br/>工作区 / ANP_BACKEND / config.yaml"]
        Tp["transport<br/>HTTP Message Signatures + JSON-RPC 2.0 客户端"]
        Store["store<br/>SQLite：消息 / 联系人 / 发现索引"]
        Doc["doctor<br/>环境诊断"]
    end

    subgraph L4["ANP Go SDK v0.9.3（纯加密，不含传输）"]
        SdkAuth["authentication<br/>DID-WBA 文档 / HTTP 签名 / DID 解析"]
        SdkD2["direct_e2ee<br/>X3DH + 双棘轮（参考客户端）"]
        SdkG2["group_e2ee<br/>MLS 执行依赖 anp-mls 二进制（已移除，Go 侧断链）"]
        SdkP["proof / wns<br/>签名验证 / handle"]
    end

    subgraph EXT["外部"]
        BE["ANP 后端 {backend}/rpc"]
        DB["anp.db (SQLite)"]
        WS["~/.anp/<br/>config.yaml / 身份密钥 / e2ee 会话"]
    end

    User --> Main
    Main --> App
    App --> CmdTree
    CmdTree --> Handlers
    App --> Out
    CmdTree --> Schema

    Handlers --> SID
    Handlers --> SMsg
    Handlers --> SE2
    Handlers --> SGrp
    Handlers --> SDisc
    Handlers --> SProof

    SID --> Cfg
    SMsg --> Cfg
    SE2 --> Cfg
    SGrp --> Cfg
    SDisc --> Cfg

    SID --> SdkAuth
    SMsg --> SE2
    SE2 --> SdkD2
    SGrp --> Tp
    SDisc --> Tp
    SProof --> SdkP

    SMsg --> Store
    SGrp --> Store
    SDisc --> Store

    Tp --> SdkAuth
    Tp --> BE
    Store --> DB
    SID --> WS
    SE2 --> WS
    Cfg --> WS
    Doc --> Store
```

## 2. 一条消息的完整旅程：`anp-cli msg send`（端到端加密）

```mermaid
sequenceDiagram
    participant U as 用户
    participant CLI as anp-cli CLI
    participant ID as identity
    participant TP as transport
    participant SDK as ANP SDK
    participant BE as ANP 后端
    participant DB as SQLite

    U->>CLI: anp-cli msg send --to did:x --text hi
    CLI->>ID: 加载 active identity（key-1 + DID 文档）
    CLI->>TP: NewClient(backend, signer)
    TP->>SDK: GenerateHTTPSignatureHeaders(doc, key-1, body)
    TP->>BE: POST /rpc  direct.send  {meta, body, auth}（E2EE 密文）
    BE-->>TP: {accepted, message_id, operation_id, target_did, body}
    CLI->>DB: 落库 outbound 消息
    CLI-->>U: {"ok":true,"data":{...},"meta":{...}}
```

## 3. E2EE 安全建链（X3DH + 自动 ACK）

```mermaid
sequenceDiagram
    participant A as Alice CLI
    participant BE as ANP 后端
    participant B as Bob CLI

    Note over B: 收方先执行 anp-cli e2ee init
    B->>BE: did.register_document + publish_prekey_bundle
    A->>BE: did.register_document + publish_prekey_bundle

    A->>BE: direct.e2ee.get_prekey_bundle {target_did: Bob}
    BE-->>A: {prekey_bundle, one_time_prekey}
    A->>A: X3DH 派生会话密钥（SDK direct_e2ee）

    A->>BE: direct.send（content_type=direct_init，密文）
    BE->>B: msg.inbox 返回 {meta, body} 密文
    B->>B: 解密 → 明文落库
    B->>BE: 自动回发加密 ACK（direct_cipher）

    A->>BE: msg.inbox
    BE-->>A: ACK 密文
    A->>A: 解密 ACK → 会话确认（pending → established）
    A->>BE: direct.send（content_type=direct_cipher，后续消息）
```

## 4. 工作区布局（~/.anp/）

```mermaid
flowchart LR
    WS["~/.anp/ (ANP_WORKSPACE)"]
    CFG["config.yaml<br/>backend / did_domain / identity"]
    IDS["identities/"]
    NAMED["<name>/<br/>did.json + key-1/2/3 PEM + ad.json"]
    E2["e2ee/<br/>prekey bundle / OPK / 会话"]
    DBC["anp.db<br/>消息 / 联系人 / 群 / 发现索引"]
    DIS["discovered/<br/>爬取的 agent 索引"]

    WS --> CFG
    WS --> IDS
    IDS --> NAMED
    WS --> E2
    WS --> DBC
    WS --> DIS
```

## 说明

- **分层原则**：命令层只负责参数 + 渲染，业务层不 `fmt.Println`，协议/存储独立，加密全部委托 ANP SDK。
- **协议**：`POST {backend}/rpc`，JSON-RPC 2.0 + HTTP Message Signatures，见 [protocol.md](protocol.md)。
- **群组**：群组 base 语义（`group.*`，transport-protected 明文）已实现；群组 E2EE（`group.e2ee.*`，MLS）server 控制面已实现，CLI 侧 MLS 计算待官方 Go SDK 更新（Go SDK 的 anp-mls 二进制被新版 Rust crate 移除、改为库调用，Go 侧无纯 Go MLS 实现）。
