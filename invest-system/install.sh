#!/usr/bin/env bash
# invest-system 安装脚本（幂等，可重复执行）
# 目标工作区 = ~/.bubbles/config.yaml 的 work_dir（INVEST_ROOT 可覆盖，兜底为包目录）
# 动作：把最新的 skills/agents/prompts 同步进工作区 → 数据骨架 → pi 装配（软链到工作区）
# 依赖：pi >= 1.3.0；subagent 扩展（缺省时自动安装）；bubblesd（决策落库用）
set -euo pipefail

KIT="$(cd "$(dirname "$0")" && pwd)"
PI_DIR="$HOME/.pi/agent"
PI_EXAMPLES="$PI_DIR/install/releases/1.3.0/node_modules/@earendil-works/pi-coding-agent/examples/extensions/subagent"

echo "== invest-system 安装 =="
echo "包目录: $KIT"

# 0) 目标工作区：INVEST_ROOT > config work_dir > 包目录
WORK_DIR="${INVEST_ROOT:-}"
if [ -z "$WORK_DIR" ]; then
  WORK_DIR="$(python3 - <<'EOF'
import os, yaml
p = os.path.expanduser("~/.bubbles/config.yaml")
try:
    print(yaml.safe_load(open(p)).get("work_dir") or "")
except Exception:
    print("")
EOF
)"
fi
[ -z "$WORK_DIR" ] && WORK_DIR="$KIT"
mkdir -p "$WORK_DIR"
echo "工作区: $WORK_DIR"

# 0b) subagent 扩展（agent 运行引擎）
if [ ! -d "$PI_DIR/extensions/subagent" ]; then
  if [ -d "$PI_EXAMPLES" ]; then
    mkdir -p "$PI_DIR/extensions/subagent"
    cp "$PI_EXAMPLES/index.ts" "$PI_EXAMPLES/agents.ts" "$PI_DIR/extensions/subagent/"
    echo "✓ subagent 扩展已安装（agent 运行引擎）"
  else
    echo "⚠ 未找到 subagent 扩展源（$PI_EXAMPLES），agent 委派不可用，请手动安装"
  fi
else
  echo "✓ subagent 扩展已存在"
fi

# 1) 同步最新 skills / agents / prompts 到工作区（代码性质，覆盖为最新）
sync_dir() {
  local src="$KIT/$1" dst="$WORK_DIR/$1"
  if command -v rsync >/dev/null 2>&1; then
    mkdir -p "$dst"
    rsync -a --delete "$src/" "$dst/"
  else
    rm -rf "$dst" && cp -R "$src" "$dst"
  fi
}
sync_dir skills
sync_dir agents
sync_dir prompts
echo "✓ skills/agents/prompts 已更新到工作区（覆盖为包内最新）"

# 2) 数据文件：只在缺失时补默认（policy.yaml / decision-log.md 是数据，不覆盖）
[ -f "$WORK_DIR/policy.yaml" ] || cp "$KIT/policy.yaml" "$WORK_DIR/policy.yaml"
[ -f "$WORK_DIR/decision-log.md" ] || cp "$KIT/decision-log.md" "$WORK_DIR/decision-log.md"
mkdir -p "$WORK_DIR"/{stocks,holding/daily,industry,events,review,rss-digest,_snapshots,mx_data/output}
echo "✓ 数据骨架: ${WORK_DIR}（policy.yaml / decision-log.md 存在则保留）"

# 3) pi 装配：软链指向工作区（安装即生效，重新 install 刷新）
mkdir -p "$PI_DIR/skills" "$PI_DIR/prompts" "$PI_DIR/agents"
for d in "$WORK_DIR"/skills/*/; do
  ln -sfn "${d%/}" "$PI_DIR/skills/$(basename "$d")"
done
for f in "$WORK_DIR"/prompts/*.md; do
  ln -sf "$f" "$PI_DIR/prompts/$(basename "$f")"
done
for f in "$WORK_DIR"/agents/*.md; do
  ln -sf "$f" "$PI_DIR/agents/$(basename "$f")"
done
echo "✓ pi 装配: $(ls "$PI_DIR/skills" | wc -l | tr -d ' ') skills / $(ls "$PI_DIR/agents" | wc -l | tr -d ' ') agents 已链接"

# 4) settings.json：skills 指向工作区
python3 - "$WORK_DIR" <<'EOF'
import json, os, sys
work = os.path.abspath(sys.argv[1])
p = os.path.expanduser("~/.pi/agent/settings.json")
d = json.load(open(p)) if os.path.exists(p) else {}
d["skills"] = [os.path.join(work, "skills")]
json.dump(d, open(p, "w"), ensure_ascii=False, indent=2)
print("✓ settings.json skills →", d["skills"][0])
EOF

echo ""
echo "✅ 安装完成。验证："
echo "   pi --print -p '列出你系统提示里所有 skill 的 name，逗号分隔'"
echo "   预期 13 个：futuapi holding-decision-review ima-skill industry-deep-analysis"
echo "   industry-monitor multi-tavily-search mx-data mx-moni mx-search"
echo "   private-company-analysis signal-monitor stock-buying-evaluation stock-trade-review"
