#!/usr/bin/env bash
# invest-system 安装脚本（幂等，可重复执行）
# 装配内容：13 个 skill + 2 个流程 prompt + 3 个 agent 定义 → ~/.pi/agent/
# 依赖：pi >= 1.3.0；subagent 扩展（缺省时自动安装）；bubblesd（决策落库用）
set -euo pipefail

KIT="$(cd "$(dirname "$0")" && pwd)"
PI_DIR="$HOME/.pi/agent"
PI_EXAMPLES="$PI_DIR/install/releases/1.3.0/node_modules/@earendil-works/pi-coding-agent/examples/extensions/subagent"

echo "== invest-system 安装 =="
echo "包目录: $KIT"

# 0) subagent 扩展（agent 运行引擎）
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

# 1) skills → ~/.pi/agent/skills/（软链，改包内文件即时生效）
mkdir -p "$PI_DIR/skills" "$PI_DIR/prompts" "$PI_DIR/agents"
for d in "$KIT"/skills/*/; do
  ln -sfn "${d%/}" "$PI_DIR/skills/$(basename "$d")"
done
echo "✓ skills: $(ls "$PI_DIR/skills" | wc -l | tr -d ' ') 个已链接"

# 2) prompts → ~/.pi/agent/prompts/
for f in "$KIT"/prompts/*.md; do
  ln -sf "$f" "$PI_DIR/prompts/$(basename "$f")"
done
echo "✓ prompts: /持仓决策 /新股决策 已链接"

# 3) agents → ~/.pi/agent/agents/
for f in "$KIT"/agents/*.md; do
  ln -sf "$f" "$PI_DIR/agents/$(basename "$f")"
done
echo "✓ agents: $(ls "$PI_DIR/agents" | wc -l | tr -d ' ') 个已链接（invest-scout/analyst/bear/risk/reviewer）"

# 4) 数据目录初始化（policy.yaml / decision-log.md 随包在 git 中，位于 $KIT 根）
INVEST_ROOT="${INVEST_ROOT:-$KIT}"
mkdir -p "$INVEST_ROOT"/{stocks,holding/daily,industry,events,review,rss-digest,_snapshots,mx_data/output}
echo "✓ 数据目录: ${INVEST_ROOT}（policy.yaml / decision-log.md 随包提供）"

# 5) settings.json：skills 指向本包（替换旧注册，保留 packages 等其他配置）
python3 - "$KIT" <<'EOF'
import json, os, sys
kit = os.path.abspath(sys.argv[1])
p = os.path.expanduser("~/.pi/agent/settings.json")
d = json.load(open(p)) if os.path.exists(p) else {}
d["skills"] = [os.path.join(kit, "skills")]
json.dump(d, open(p, "w"), ensure_ascii=False, indent=2)
print("✓ settings.json skills →", d["skills"][0])
EOF

echo ""
echo "✅ 安装完成。验证："
echo "   pi --print -p '列出你系统提示里所有 skill 的 name，逗号分隔'"
echo "   预期 13 个：futuapi holding-decision-review ima-skill industry-deep-analysis"
echo "   industry-monitor multi-tavily-search mx-data mx-moni mx-search"
echo "   private-company-analysis signal-monitor stock-buying-evaluation stock-trade-review"
