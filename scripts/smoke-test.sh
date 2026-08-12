#!/usr/bin/env bash
#
# anp-cli 全功能冒烟测试
#
# 启动一个内存 mock 后端 + 一个本地 HTTP 服务器（供 discovery），
# 按功能逐项跑命令并断言，打印 PASS/FAIL。
#
# 用法:  bash scripts/smoke-test.sh [--skip-daemon]
#   --skip-daemon  跳过 runtime 系统服务安装/卸载（不碰 LaunchAgent/systemd）

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/anp-cli"
MOCK_BIN="$REPO_ROOT/bin/mock"

WORK_A="/tmp/anp-smoke-a"   # agent alice 的工作区
WORK_B="/tmp/anp-smoke-b"   # agent bob 的工作区
WORK_X="/tmp/anp-smoke-x"   # 无 backend 的空白工作区
WEB_DIR="/tmp/anp-smoke-web"
MOCK_URL=""
WEB_PORT=""
FAILED=0
PASSED=0

ANP_ENV=()

say()  { printf '\033[1;36m%s\033[0m\n' "$*"; }
ok()   { PASSED=$((PASSED+1)); printf '  \033[1;32mPASS\033[0m  %s\n' "$1"; }
bad()  { FAILED=1; printf '  \033[1;31mFAIL\033[0m  %s\n' "$1"; shift; for line in "$@"; do printf '        %s\n' "$line"; done; }

# run <desc> -- [env V=...] cmd...  断言命令成功。
# 输出是 JSON envelope 时检查 ok==true；非 envelope（table/jq）按退出码判断。
run() {
  local desc="$1"; shift; [ "$1" = "--" ] && shift
  local out rc
  out="$(env ${ANP_ENV[@]+"${ANP_ENV[@]}"} "$@" 2>&1)"; rc=$?
  if printf '%s' "$out" | python3 -c 'import sys,json;d=json.load(sys.stdin);sys.exit(0 if d.get("ok") else 1)' 2>/dev/null; then
    ok "$desc"
  elif [ "$rc" -eq 0 ]; then
    ok "$desc"
  else
    bad "$desc" "$out"
  fi
}

# run_err <desc> <expect-code> -- [env V=...] cmd...  断言失败（error.code 匹配或退出码非 0）
run_err() {
  local desc="$1" code="$2"; shift 2; [ "$1" = "--" ] && shift
  local out rc
  out="$(env ${ANP_ENV[@]+"${ANP_ENV[@]}"} "$@" 2>&1)"; rc=$?
  if printf '%s' "$out" | python3 -c "import sys,json;d=json.load(sys.stdin);sys.exit(0 if d.get('error',{}).get('code')=='$code' else 1)" 2>/dev/null; then
    ok "$desc"
  elif [ "$rc" -ne 0 ]; then
    ok "$desc"
  else
    bad "$desc" "$out"
  fi
}

# run_msg <desc> <substring> -- [env V=...] cmd...  断言输出包含子串
run_msg() {
  local desc="$1" needle="$2"; shift 2; [ "$1" = "--" ] && shift
  local out
  out="$(env ${ANP_ENV[@]+"${ANP_ENV[@]}"} "$@" 2>&1)"
  if printf '%s' "$out" | grep -q "$needle"; then
    ok "$desc"
  else
    bad "$desc" "$out"
  fi
}

cleanup() {
  [ -n "${MOCK_PID:-}" ] && kill "$MOCK_PID" 2>/dev/null
  [ -n "${WEB_PID:-}" ] && kill "$WEB_PID" 2>/dev/null
  rm -rf "$WORK_A" "$WORK_B" "$WORK_X" "$WEB_DIR" /tmp/anp-smoke-mockurl.txt /tmp/anp-smoke-web.log
}
trap cleanup EXIT

# ---------------------------------------------------------------- 0. 构建
say "== 0. 构建 =="
( cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/anp-cli ) || { bad "build anp-cli"; exit 1; }
( cd "$REPO_ROOT" && go build -o "$MOCK_BIN" ./cmd/mock ) || { bad "build mock"; exit 1; }

# ---------------------------------------------------------------- mock 后端
say "== 启动 mock 后端 =="
"$MOCK_BIN" >/tmp/anp-smoke-mockurl.txt 2>/dev/null &
MOCK_PID=$!
for _ in $(seq 1 40); do [ -s /tmp/anp-smoke-mockurl.txt ] && break; sleep 0.25; done
MOCK_URL="$(cat /tmp/anp-smoke-mockurl.txt 2>/dev/null)"
[ -n "$MOCK_URL" ] || { bad "mock 后端未启动"; exit 1; }
echo "  mock: $MOCK_URL"

# ---------------------------------------------------------------- discovery 静态站点
say "== 启动 ad.json 静态站点 =="
mkdir -p "$WEB_DIR"
printf '%s' '{"name":"OCR Service","description":"converts images to text","capabilities":["ocr","vision"]}' > "$WEB_DIR/ad.json"
# 先取一个空闲端口，再在该端口起静态服务器
WEB_PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
python3 -m http.server "$WEB_PORT" --bind 127.0.0.1 --directory "$WEB_DIR" >/dev/null 2>&1 &
WEB_PID=$!
sleep 1
echo "  web: http://127.0.0.1:$WEB_PORT/ad.json"

# ---------------------------------------------------------------- 1. 基础命令
say "== 1. 基础命令 =="
ANP_ENV=()
run "version 输出构建信息" -- "$BIN" version
run "help 可用" -- "$BIN" --help
ANP_ENV=(ANP_WORKSPACE="$WORK_A")
run "doctor 诊断（无 backend）" -- "$BIN" doctor
run "schema 列出命令契约（无 backend）" -- "$BIN" schema
# init 需要 backend，这样生成的 DID 文档带 ANPMessageService.serviceDid（e2ee 依赖）
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "init alice 生成 DID" -- "$BIN" init alice
run "id show 显示身份" -- "$BIN" id show
run "whoami shortcut" -- "$BIN" whoami
run "status 工作区状态" -- "$BIN" status
run "config show" -- "$BIN" config show

# ---------------------------------------------------------------- 2. 多身份
say "== 2. 多身份管理 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "init bob 第二个身份" -- "$BIN" init bob
run "id list 列出两个身份" -- "$BIN" id list
run "id current 默认是 alice" -- "$BIN" id current
run "id use bob 切换默认" -- "$BIN" id use bob
run "whoami 现在默认是 bob" -- "$BIN" whoami
run "id use alice 切回" -- "$BIN" id use alice
run "--identity bob 单次选择" -- "$BIN" whoami --identity bob

# ---------------------------------------------------------------- 3. 配置
say "== 3. 配置 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "config set --did-domain" -- "$BIN" config set --did-domain example.com
run "config show 看到 did_domain" -- "$BIN" config show

# ---------------------------------------------------------------- 4. 身份注册 + 抢注
say "== 4. handle 注册 / 抢注 =="
run "alice 注册 handle" -- "$BIN" register --handle alice.agent --email a@example.com
ANP_ENV=(ANP_WORKSPACE="$WORK_B" ANP_BACKEND="$MOCK_URL")
run "bob init" -- "$BIN" init bob
run_err "bob 抢注同一 handle → handle_taken" handle_taken -- "$BIN" register --handle alice.agent
run "bob 换变体注册成功" -- "$BIN" register --handle alice.agent.1

# ---------------------------------------------------------------- 5. describe
say "== 5. Agent Description =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A")
run "describe --set 写入 ad.json" -- "$BIN" describe --set '{"name":"agent alice","capabilities":["msg"]}'
run "describe 读取 ad.json" -- "$BIN" describe
run "describe --name 局部更新" -- "$BIN" describe --name "alice v2"

# ---------------------------------------------------------------- 6. 消息（direct + 历史）
say "== 6. 消息 =="
ALICE_DID="$(env ANP_WORKSPACE="$WORK_A" "$BIN" whoami --jq '.data.did' | tr -d '"')"
BOB_DID="$(env ANP_WORKSPACE="$WORK_B" "$BIN" whoami --jq '.data.did' | tr -d '"')"
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "msg send 发消息给 bob" -- "$BIN" msg send --to "$BOB_DID" --text "hello bob"
run "dm shortcut" -- "$BIN" dm "$BOB_DID" "via dm"
run "msg inbox" -- "$BIN" msg inbox
run "msg history --with bob" -- "$BIN" msg history --with "$BOB_DID"
run "history shortcut" -- "$BIN" history "$BOB_DID"
run_err "msg send 缺目标 → invalid_argument" invalid_argument -- "$BIN" msg send --text x
ANP_ENV=(ANP_WORKSPACE="$WORK_B" ANP_BACKEND="$MOCK_URL")
run "bob inbox 可见" -- "$BIN" inbox

# ---------------------------------------------------------------- 7. 群组
say "== 7. 群组 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "group create" -- "$BIN" group create --name "team"
GID="$(env ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL" "$BIN" group create --name team2 --jq '.data.group_did' | tr -d '"')"
run "group join" -- "$BIN" group join --group "$GID"
run "group members" -- "$BIN" group members --group "$GID"
run "group send 消息" -- "$BIN" msg send --group "$GID" --text "hi team"
run "group leave" -- "$BIN" group leave --group "$GID"

# ---------------------------------------------------------------- 8. E2EE
say "== 8. E2EE =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "alice e2ee init 发布 bundle" -- "$BIN" e2ee init
ANP_ENV=(ANP_WORKSPACE="$WORK_B" ANP_BACKEND="$MOCK_URL")
run "bob e2ee init 发布 bundle" -- "$BIN" e2ee init
run "bob e2ee status（无会话）" -- "$BIN" e2ee status --with "$ALICE_DID"
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "alice msg send --secure on" -- "$BIN" msg send --to "$BOB_DID" --text "top secret" --secure on
ANP_ENV=(ANP_WORKSPACE="$WORK_B" ANP_BACKEND="$MOCK_URL")
run "bob inbox 解密后可见明文" -- "$BIN" msg inbox --scope direct
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run_msg "群组 --secure on → SDK P6 门禁报错" "P6 v2 public release is blocked" -- "$BIN" msg send --group "$GID" --text x --secure on

# ---------------------------------------------------------------- 9. 签名
say "== 9. proof 签名/验签 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A")
echo "hello world" > /tmp/anp-smoke-file.txt
run "proof sign 签名文件" -- "$BIN" proof sign /tmp/anp-smoke-file.txt
SIG="$(env ANP_WORKSPACE="$WORK_A" "$BIN" proof sign /tmp/anp-smoke-file.txt --jq '.data.signature' | tr -d '"')"
run "proof verify 验签（十六进制）" -- "$BIN" proof verify /tmp/anp-smoke-file.txt --signature "$SIG"
run "proof sign --output 写文件" -- "$BIN" proof sign /tmp/anp-smoke-file.txt --output /tmp/anp-smoke.proof.json
run "proof verify 用 proof 文件" -- "$BIN" proof verify /tmp/anp-smoke-file.txt --signature /tmp/anp-smoke.proof.json
run_err "proof verify 篡改签名 → verification_failed" verification_failed -- "$BIN" proof verify /tmp/anp-smoke-file.txt --signature 01020304
rm -f /tmp/anp-smoke-file.txt /tmp/anp-smoke.proof.json

# ---------------------------------------------------------------- 10. 发现
say "== 10. discovery =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A")
run "discovery crawl 抓 ad.json" -- "$BIN" discovery crawl "http://127.0.0.1:$WEB_PORT/ad.json"
run "discovery search ocr" -- "$BIN" discovery search ocr

# ---------------------------------------------------------------- 11. runtime（轮询/心跳）
say "== 11. runtime 轮询/心跳 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "runtime listen --once 单次拉取" -- "$BIN" runtime listen --once
run "runtime heartbeat 单次心跳" -- "$BIN" runtime heartbeat

# ---------------------------------------------------------------- 12. 后台服务
if [ "${1:-}" != "--skip-daemon" ]; then
  say "== 12. runtime 系统服务（会短暂安装 LaunchAgent/systemd 再卸载）=="
  ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
  run "runtime install 安装服务" -- "$BIN" runtime install
  run "runtime status" -- "$BIN" runtime status
  run "runtime uninstall 卸载服务" -- "$BIN" runtime uninstall
else
  say "== 12. runtime 系统服务（--skip-daemon，跳过）=="
  ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
  run "runtime install --dry-run 规划" -- "$BIN" runtime install --dry-run
fi

# ---------------------------------------------------------------- 13. 全局参数 + 错误路径
say "== 13. 全局参数 + 错误路径 =="
ANP_ENV=(ANP_WORKSPACE="$WORK_A" ANP_BACKEND="$MOCK_URL")
run "msg send --dry-run 返回 plan" -- "$BIN" msg send --to "$BOB_DID" --text dry --dry-run
run "inbox --format table" -- "$BIN" inbox --format table
run "inbox --jq 过滤" -- "$BIN" inbox --jq '.data.messages | length'
run "id show --json" -- "$BIN" id show --json
ANP_ENV=(ANP_WORKSPACE="$WORK_X")
run "空白工作区 init" -- "$BIN" init x
run_err "无 backend 时网络命令报错" internal_error -- "$BIN" msg inbox
run_err "未初始化时 whoami 报错" not_initialized -- "$BIN" whoami --identity nobody

# ---------------------------------------------------------------- 汇总
say ""
if [ "$FAILED" = 1 ]; then
  printf '\033[1;31m结果: %d 通过, 有失败项\033[0m\n' "$PASSED"
  exit 1
else
  printf '\033[1;32m结果: %d 项全部通过 ✓\033[0m\n' "$PASSED"
fi
