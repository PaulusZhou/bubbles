#!/usr/bin/env bash
# bubbles 一键部署脚本（新机器，git clone 后执行）
# 幂等：重复执行安全。手动步骤见 DEPLOY.md
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO"

# 与 Makefile 保持一致：go 常装在 /usr/local/go/bin（不在默认 PATH）
export PATH="/usr/local/go/bin:/usr/local/bin:$PATH"

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

# 3) 引导 config.yaml（install.sh 以 work_dir 为目标工作区，必须先于装配）
mkdir -p "$HOME/.bubbles"
if [ ! -f "$HOME/.bubbles/config.yaml" ]; then
  cat > "$HOME/.bubbles/config.yaml" <<EOF
# bubbles 配置（deploy.sh 引导生成，按需修改后重跑 ./deploy.sh 或 invest-system/install.sh）
feishu_chat_id: ""             # 飞书会话 ID
feishu_drive_folder: ""        # 云文档文件夹 token（可选）
work_dir: $REPO                # ★ 投资工作区（skills/agents/prompts 与数据都装到这里）
agent: pi
pi_path: pi
pi_model: mimo-v2.6-pro
EOF
  echo "✓ 已生成 ~/.bubbles/config.yaml（work_dir: ${REPO}）"
else
  echo "✓ 已有 ~/.bubbles/config.yaml（work_dir: $(python3 -c "import yaml,os;print(yaml.safe_load(open(os.path.expanduser('~/.bubbles/config.yaml'))).get('work_dir'))")）"
fi
echo "  ℹ 如需安装到别的工作区：改 config 的 work_dir 后重跑 ./deploy.sh"

# 4) invest-system 装配（同步 .pi/{skills,agents,prompts} + 数据骨架到 work_dir）
"$REPO/invest-system/install.sh"

# 5) pi 联网能力
if ! pi list 2>/dev/null | grep -q "pi-web-access"; then
  echo "▶ 安装 pi-web-access ..."; pi install npm:pi-web-access
fi
echo "✓ pi 包: $(pi list 2>/dev/null | tr '\n' ' ')"

echo ""
echo "✅ 自动步骤完成。剩余手动步骤（详见 DEPLOY.md）："
echo "   1. ~/.bubbles/config.yaml 补 feishu_chat_id（work_dir 已就位）"
echo "   2. export FEISHU_APP_ID=... FEISHU_APP_SECRET=...（写入 ~/.bashrc / ~/.zshrc）"
echo "   3. 录入持仓：bubbles holding set <ticker> --name ... --qty ... --cost ..."
echo "   4. ./invest-system/create-tasks.sh 注册定时流水线"
echo "   5. 云文档同步：npx @larksuite/cli@latest install && lark-cli auth login --domain drive,docs,markdown"
echo "   6. 按 DEPLOY.md 验证清单逐项检查（skill 验证用 pi --print --approve）"
