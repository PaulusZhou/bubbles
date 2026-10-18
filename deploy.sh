#!/usr/bin/env bash
# bubbles 一键部署脚本（新机器，git clone 后执行）
# 幂等：重复执行安全。手动步骤见 DEPLOY.md
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO"

echo "== bubbles 部署 =="
echo "仓库: $REPO"

# 1) 依赖检查
need() { command -v "$1" >/dev/null 2>&1 || { echo "✗ 缺少依赖: $1 ($2)"; exit 1; }; }
need git "xcode-select --install"
need go  "https://go.dev/dl/ 或 brew install go"
if ! command -v cc >/dev/null 2>&1 && ! command -v gcc >/dev/null 2>&1; then
  echo "✗ 缺少 C 编译器（CGO/SQLite 需要）→ xcode-select --install"; exit 1
fi
if ! command -v pi >/dev/null 2>&1; then
  echo "▶ 安装 pi ..."; curl -fsSL https://pi.dev/install.sh | sh
fi
echo "✓ 依赖齐备（git/go/cc/pi）"

# 2) 构建 + 安装 bubbles（含启动 bubblesd）
make install

# 3) invest-system 装配（skills/agents/prompts/subagent 扩展）
"$REPO/invest-system/install.sh"

# 4) pi 联网能力
if ! pi list 2>/dev/null | grep -q "pi-web-access"; then
  echo "▶ 安装 pi-web-access ..."; pi install npm:pi-web-access
fi
echo "✓ pi 包: $(pi list 2>/dev/null | tr '\n' ' ')"

echo ""
echo "✅ 自动步骤完成。剩余手动步骤（详见 DEPLOY.md）："
echo "   1. ~/.bubbles/config.yaml 配置 feishu_chat_id / work_dir: $REPO"
echo "   2. export FEISHU_APP_ID=... FEISHU_APP_SECRET=...（写入 ~/.zshrc）"
echo "   3. 数据文档随包：invest-system/{policy.yaml,decision-log.md}（运行产出也落在此）"
echo "   4. 录入持仓：bubbles holding set <ticker> --name ... --qty ... --cost ..."
echo "   5. ./invest-system/create-tasks.sh 注册定时流水线"
echo "   6. 按 DEPLOY.md 验证清单逐项检查"
