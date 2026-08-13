# 跨平台实现（Windows / Linux）变更计划

> **状态：规划文档，尚未实现。** 本文列出把 anp-cli 从「macOS 优先」扩展到 Windows / Linux 所需的全部改动。当前代码在 macOS 上可编译运行，但在 Windows 上会**编译失败**（`unix.Flock` 与 `syscall.SIGTERM`）。

## 1. 现状：哪些代码是 Unix 专属

| 位置 | 问题 | 影响 |
|------|------|------|
| `internal/identity/store.go` `lockIndex`/`unlockIndex` | `golang.org/x/sys/unix.Flock` | **Windows 编译失败** |
| `internal/config/config.go` `lockFile`/`unlockFile` | `golang.org/x/sys/unix.Flock` | **Windows 编译失败** |
| `internal/cli/runtime.go` `runPollLoop` | `syscall.SIGTERM` | Windows 编译/运行警告（信号语义不同） |
| `internal/cli/daemon.go` `Option: service.KeyValue{"UserService": true}` | macOS 语义 | Linux/Windows 上含义不同 |
| `os.WriteFile(..., 0o600)` / `os.MkdirAll(..., 0o700)` | Unix 权限位 | Windows 上被忽略（非致命） |
| `scripts/install.sh` / `scripts/smoke-test.sh` | bash 脚本 | Windows 无法直接运行 |
| `Makefile` `rm -rf` / `gofmt` | Unix 工具 | 可被 `go build` / CI 替代 |

已跨平台、无需改动的部分：`os.UserHomeDir()`（Windows 返回 `%USERPROFILE%`）、`filepath` 路径分隔、`kardianos/service`（原生支持 LaunchAgent/systemd/Windows Service）、`modernc.org/sqlite`（纯 Go，无 cgo）、`scripts/mockrun`（Go 程序）。

## 2. 核心改动

### 2.1 文件锁 —— 抽成跨平台小包（优先）

现在 `identity` 和 `config` 各自手写 `unix.Flock`。抽成一个内部包 `internal/fslock`，用 build tag 分流：

```
internal/fslock/
  lock.go          // 公共 API: Lock(path) (*os.File, error) / Unlock(f)
  lock_unix.go     //go:build unix     → unix.Flock(LOCK_EX/LOCK_UN)
  lock_windows.go  //go:build windows  → windows.LockFileEx / UnlockFileEx
```

- `lock_unix.go` 复用现有逻辑（`unix.Flock`）。
- `lock_windows.go` 用 `golang.org/x/sys/windows` 的 `LockFileEx`（`LOCKFILE_EXCLUSIVE_LOCK`）对 `.lock` 文件加排他锁，`UnlockFileEx` 释放。`golang.org/x/sys` 已是间接依赖，**零新增依赖**。
- 两个调用点改为 `fslock.Lock(...)` / `fslock.Unlock(...)`，删除各自的手写实现。

**备选**：引入 `github.com/gofrs/flock`（一个 ~200 行、纯 Go、跨平台的文件锁库）。代价是多一个直接依赖；好处是少写 `windows.LockFileEx` 的样板。推荐优先走 build tag（不引入新依赖），若想省事则用 `gofrs/flock`。

### 2.2 信号处理 —— 去掉 `syscall.SIGTERM`

`internal/cli/runtime.go:51`：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
```

- `os.Interrupt` 跨平台（Windows 上映射 Ctrl-C），保留。
- `syscall.SIGTERM` 是 Unix 语义，Windows 上没有等价的「进程终止信号」。改为只捕获 `os.Interrupt`，或抽一个 `notifyStopSignals()` 辅助函数：
  - `signals_unix.go`（`//go:build unix`）：`os.Interrupt, syscall.SIGTERM`
  - `signals_windows.go`（`//go:build windows`）：仅 `os.Interrupt`
- 这样 Windows 上 Ctrl-C 正常终止前台 `runtime listen` / `setup`；后台服务由 `kardianos/service` 的 `Stop()` 驱动（见 2.3），不依赖信号。

### 2.3 服务管理器 —— `kardianos/service` 已够，但需按 OS 分流配置

`kardianos/service` 已经跨平台（macOS=LaunchAgent、Linux=systemd、Windows=Windows Service），无需换库。要改的是 `daemonService` 里的 `Option`：

```go
Option: service.KeyValue{"UserService": true},
```

- **macOS**：`UserService: true` 表示用户级 LaunchAgent（`~/Library/LaunchAgents`），无需 sudo。保留。
- **Linux**：`UserService: true` 会让 systemd 走 `systemctl --user`（用户级 unit）。是否要用户级还是系统级（需 root）取决于部署场景。建议：Linux 默认用户级（保持无需 root），或提供 `--system` flag 切换。
- **Windows**：`UserService` 无效；Windows Service 一律系统级，`runtime install` 需要管理员权限（UAC 提权）。`kardianos/service` 会忽略该 option，但要**在文档/报错里提示需要管理员**。

实现方式：把 option 拆到 build-tagged 或运行时判断（`runtime.GOOS`）的辅助函数 `serviceOptions()`，按平台返回不同 `service.KeyValue`。`Arguments` / `EnvVars` / `WorkingDirectory` 三平台通用。

**日志路径**（当前 `service.Config` 未设 `LogOutput`）：
- macOS LaunchAgent：`kardianos` 默认把 stdout/stderr 重定向到 plist 指定的 `~/Library/Logs/anp-runtime.log`。
- Linux systemd：输出进 journal（`journalctl -u anp-runtime`）。
- Windows：服务无控制台，输出进 Windows 事件日志或 `kardianos` 指定的文件。
- 计划：为 `service.Config` 增加 `LogOutput`（指向 `<workspace>/runtime.log`），三平台统一落文件，便于排障。

### 2.4 文件权限 —— 无需改代码，文档说明即可

`0o600` / `0o700` 在 Windows 上被忽略（Windows 用 ACL/继承控制权限）。代码无需改，但跨平台文档要写明：**Windows 下私钥文件靠用户目录 ACL 保护，权限位不生效**，仍建议把 workspace 放在用户目录下（默认 `%USERPROFILE%\.anp` 已满足）。

## 3. 构建与打包

### 3.1 交叉编译

Go 本身支持交叉编译，加一个目标矩阵：

```bash
# 产出三平台二进制（纯 Go，无 cgo，可直接交叉编译）
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -o bin/anp-cli-darwin-amd64  ./cmd/anp-cli
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o bin/anp-cli-linux-amd64   ./cmd/anp-cli
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/anp-cli-windows-amd64.exe ./cmd/anp-cli
```

`CGO_ENABLED=0` 关键：`modernc.org/sqlite` 是纯 Go SQLite，关闭 cgo 后三平台都能出静态/无依赖二进制。

### 3.2 CI

在 GitHub Actions 加矩阵（`ubuntu-latest` / `windows-latest` / `macos-latest`），每个跑：

```yaml
- run: go build ./...
- run: go vet ./...
- run: go test ./...
```

并在 Windows runner 上 `go build` 保证 `internal/fslock/lock_windows.go` 真的编译过（这是当前 CI 抓不到的）。

### 3.3 安装脚本

- **Unix**：`scripts/install.sh` 已可用，Linux 也能跑（依赖 bash + `install`，Debian/Ubuntu 自带）。
- **Windows**：新增 `scripts/install.ps1`，等价逻辑：
  - `go build` → 装到 `%LOCALAPPDATA%\anp-cli\anp-cli.exe`（或 `%USERPROFILE%\.local\bin`）。
  - 提示把安装目录加入 `PATH`（`setx PATH ...`）。
  - 生成 PowerShell 补全（`anp-cli completion powershell`，需在 catalog 补一条 `completion.powershell`，见 4）。
- **冒烟测试**：`scripts/smoke-test.sh` 依赖 bash + `python3`。Windows 上要么用 Git Bash/WSL 跑，要么新增 `smoke-test.ps1`（用 `Start-Job` 起 mock，`ConvertFrom-Json` 断言）。mock 后端 `scripts/mockrun` 是 Go 程序，直接 `go run` 即可，跨平台。

## 4. 次要项

- **`completion.powershell`**：catalog 目前只有 bash/zsh/fish。Windows 用户需要 PowerShell 补全，在 `cmdmeta/catalog.go` 和补全命令里加一条。
- **`Makefile`**：`rm -rf` / `gofmt` 是 Unix 工具。Windows 开发者直接用 `go build` / `go fmt`；如需统一，可加 `Taskfile.yml` 或保留 Makefile 仅面向 Unix。
- **默认 workspace 路径**：`~/.anp` 在 Windows 上解析为 `C:\Users\<user>\.anp`，`os.UserHomeDir()` 已正确返回，无需改。文档里补一句 Windows 默认路径。

## 5. 推荐实施顺序

1. **`internal/fslock` 抽包**（2.1）—— 解决 Windows 编译失败，是硬前提。
2. **信号处理分流**（2.2）—— 解决 `syscall.SIGTERM` 可移植性。
3. **`daemonService` 按 OS 分流 option + LogOutput**（2.3）—— 让三平台后台服务行为正确。
4. **CI 矩阵 + 交叉编译**（3.1/3.2）—— 让「能编译」被持续验证。
5. **`install.ps1` / `smoke-test.ps1` / `completion.powershell`**（3.3/4）—— 补 Windows 使用体验。

完成 1–3 后，`GOOS=windows go build ./...` 应零错误；完成 4–5 后 Windows 用户可独立安装、自测、使用。

## 6. 影响面小结

| 改动 | 文件 | 类型 |
|------|------|------|
| 抽文件锁包 | `internal/fslock/*`（新）+ `store.go`/`config.go` 改调用 | 新增 + 重构 |
| 信号分流 | `internal/cli/runtime.go` + `signals_*.go`（新） | 重构 |
| 服务配置分流 | `internal/cli/daemon.go` | 修改 |
| PowerShell 补全 | `cmdmeta/catalog.go` + 补全命令 | 新增 |
| 安装/冒烟脚本 | `scripts/install.ps1`、`scripts/smoke-test.ps1` | 新增 |
| CI 矩阵 | `.github/workflows/*.yml` | 新增 |
