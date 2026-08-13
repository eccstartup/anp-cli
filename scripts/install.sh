#!/usr/bin/env bash
#
# anp-cli 安装脚本
#
# 用法:
#   ./scripts/install.sh            # 构建并安装到 PATH 上的用户目录
#   ANP_INSTALL_DIR=/opt/bin ./scripts/install.sh   # 指定安装目录
#
# 行为:
#   - 从本地源码用 go build 构建（不依赖 GitHub 发布/registry）
#   - 自动选择第一个"在 PATH 上且可写"的目录:
#       /usr/local/bin  >  ~/.local/bin  >  ~/bin
#     都不满足时回退 ~/.local/bin 并提示把路径加入 PATH
#   - 可选安装 shell 补全（bash / zsh / fish）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MODULE_PATH="github.com/eccstartup/anp-cli"
BINARY_NAME="anp-cli"
DEFAULT_VERSION="0.1.0"

info()  { printf '\033[1;34m[install]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[install]\033[0m %s\n' "$*"; }
die()   { printf '\033[1;31m[install]\033[0m %s\n' "$*" >&2; exit 1; }

# --- 0. 依赖检查 -----------------------------------------------------------
command -v go >/dev/null 2>&1 || die "未找到 go。请先安装 Go 1.26+：https://go.dev/dl/"
GO_VERSION="$(go version | sed -E 's/.*go([0-9]+\.[0-9]+).*/\1/')"
# 最低版本 = sort -V 的最小值；若最小值仍是 go 自身版本且不是 1.26，说明 go < 1.26
if [ "$(printf '%s\n' "$GO_VERSION" '1.26' | sort -V | head -1)" = "$GO_VERSION" ] && [ "$GO_VERSION" != "1.26" ]; then
  die "Go 版本过低（$GO_VERSION），需要 Go 1.26+。"
fi

# --- 1. 版本探测 ------------------------------------------------------------
VERSION="${ANP_VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || true)"
fi
[ -z "$VERSION" ] && VERSION="$DEFAULT_VERSION"
COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || true)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)"

LDFLAGS="-s -w -X ${MODULE_PATH}/internal/buildinfo.Version=${VERSION}"
[ -n "$COMMIT" ] && LDFLAGS="$LDFLAGS -X ${MODULE_PATH}/internal/buildinfo.Commit=${COMMIT}"
[ -n "$DATE" ]   && LDFLAGS="$LDFLAGS -X ${MODULE_PATH}/internal/buildinfo.Date=${DATE}"

# --- 2. 选择安装目录 --------------------------------------------------------
on_path() {
  local dir="$1"
  case ":$PATH:" in
    *":$dir:"*) return 0 ;;
  esac
  return 1
}

if [ -n "${ANP_INSTALL_DIR:-}" ]; then
  INSTALL_DIR="$ANP_INSTALL_DIR"
else
  INSTALL_DIR=""
  for candidate in "/usr/local/bin" "$HOME/.local/bin" "$HOME/bin"; do
    if [ -d "$candidate" ] && [ -w "$candidate" ] && on_path "$candidate"; then
      INSTALL_DIR="$candidate"
      break
    fi
  done
  if [ -z "$INSTALL_DIR" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
    if ! on_path "$INSTALL_DIR"; then
      warn "安装目录 $INSTALL_DIR 不在 PATH 上。"
      warn "请把它加入 PATH，例如在 ~/.zshrc 里加一行:"
      warn "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    fi
  fi
fi
mkdir -p "$INSTALL_DIR" || die "无法创建安装目录: $INSTALL_DIR"

# --- 3. 构建 ----------------------------------------------------------------
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

info "构建 $BINARY_NAME $VERSION (commit=${COMMIT:-none}) ..."
( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$TMP_DIR/$BINARY_NAME" ./cmd/anp-cli ) \
  || die "构建失败。请确认依赖可用（go mod tidy）。"

# --- 4. 安装 ----------------------------------------------------------------
TARGET="$INSTALL_DIR/$BINARY_NAME"
install -m 0755 "$TMP_DIR/$BINARY_NAME" "$TARGET" || die "写入 $TARGET 失败"
info "已安装: $TARGET"

# --- 5. 验证 ----------------------------------------------------------------
if ! "$TARGET" version --format pretty >/dev/null 2>&1; then
  warn "安装完成但版本检查异常，可运行 $TARGET version 查看。"
fi
"$TARGET" version --jq '"  " + .data.cli + " " + .data.version + "  (sdk " + .data.sdk_version + ", " + .data.os + "/" + .data.arch + ")"' 2>/dev/null \
  || "$TARGET" version --format pretty

# --- 6. 补全（可选）--------------------------------------------------------
install_completion() {
  local shell="$1" file
  case "$shell" in
    bash) file="$HOME/.bashrc"; "$TARGET" completion bash >/dev/null 2>&1 ;;
    zsh)  file="$HOME/.zshrc";  "$TARGET" completion zsh  >/dev/null 2>&1 ;;
    fish) file="$HOME/.config/fish/completions"; mkdir -p "$file"; file="$file/$BINARY_NAME.fish";
          "$TARGET" completion fish > "$file" 2>/dev/null; info "fish 补全写入 $file"; return 0 ;;
    *) return 1 ;;
  esac
  [ -n "$file" ] || return 0
  if grep -q "anp-cli completion" "$file" 2>/dev/null; then
    info "$shell 补全已存在，跳过。"
    return 0
  fi
  printf '\n# anp-cli shell completion\nif command -v anp-cli >/dev/null 2>&1; then\n  eval "$(anp-cli completion %s)"\nfi\n' "$shell" >> "$file"
  info "$shell 补全已追加到 $file（新终端生效）。"
}

if [ "${ANP_NO_COMPLETION:-}" != "1" ]; then
  case "${SHELL##*/}" in
    zsh)  install_completion zsh ;;
    bash) install_completion bash ;;
    fish) install_completion fish ;;
    *)    info "未识别的 shell（${SHELL}），跳过补全；可运行 '$TARGET completion <bash|zsh|fish>' 手动生成。" ;;
  esac
fi

info "完成。现在可以在终端运行: $BINARY_NAME --help"
info "查看架构图: docs/architecture.md   协议: docs/protocol.md"
