# 04 — Runtime（收消息循环）

Receiver = 轮询后端 inbox 并落本地库的循环。两种模式：前台运行（Ctrl-C 停止）或后台系统服务（LaunchAgent / systemd / Windows Service）。

## 前台

```bash
anp-cli setup                       # shortcut = runtime listen --mode http
anp-cli runtime listen --mode http --every 15s
anp-cli runtime listen --once       # 只拉一次就退出（适合脚本）
```

每次轮询输出一行 JSON 到 stdout；通知/错误写到 stderr。

## 后台服务

```bash
anp-cli runtime install      # 安装系统服务（macOS 装到 ~/Library/LaunchAgents）
anp-cli runtime start        # 启动服务
anp-cli runtime status       # 状态：running / stopped
anp-cli runtime stop         # 停止
anp-cli runtime restart      # 重启
anp-cli runtime uninstall    # 卸载
```

- 服务运行 `anp-cli runtime listen-service`（隐藏命令），由系统服务管理器启停。
- 服务继承安装时的工作区与 backend（写入 LaunchAgent 的 `EnvironmentVariables`）。
- 服务日志默认写到 `~/anp-runtime.out.log` / `~/anp-runtime.err.log`（macOS）。
- Linux 下为 systemd unit，Windows 下为 Windows Service。

## 心跳

```bash
anp-cli runtime heartbeat           # 单次心跳：同步 inbox 并报告耗时
anp-cli runtime heartbeat --install --every 15m   # 登记周期心跳配置（供外部调度）
```

## 注意

- `--mode ws` 目前与 http 相同（轮询），为后端 websocket inbox 预留。
- 系统级 daemon 化已由 `runtime install/start/stop/...` 提供；`anp-cli setup` 仍为前台快捷方式。
