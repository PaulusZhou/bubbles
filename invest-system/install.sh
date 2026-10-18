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
    echo "⚠ 未找到 subagent 扩展源（${PI_EXAMPLES}），agent 委派不可用，请手动安装"
  fi
else
  echo "✓ subagent 扩展已存在"
fi

# 1) 同步最新 skills / agents / prompts 到工作区 .pi/（pi 项目级标准布局，覆盖为最新）
PI_WORK="$WORK_DIR/.pi"
sync_dir() {
  local src="$KIT/$1" dst="$PI_WORK/$1"
  if command -v rsync >/dev/null 2>&1; then
    mkdir -p "$dst"
    rsync -a --delete "$src/" "$dst/"
  else
    mkdir -p "$PI_WORK" && rm -rf "$dst" && cp -R "$src" "$dst"
  fi
}
sync_dir skills
sync_dir agents
sync_dir prompts
echo "✓ .pi/{skills,agents,prompts} 已更新到工作区（覆盖为包内最新）"

# 1b) 清理旧版用户级软链/注册（避免与 .pi 项目发现重复注册）
python3 - "$PI_DIR" "$KIT" "$WORK_DIR" <<'PYEOF2'
import json, os, sys
pi_dir, kit, work = sys.argv[1], sys.argv[2], sys.argv[3]
removed = 0
for sub in ("skills", "prompts", "agents"):
    d = os.path.join(pi_dir, sub)
    if not os.path.isdir(d):
        continue
    for name in os.listdir(d):
        p = os.path.join(d, name)
        if os.path.islink(p):
            tgt = os.readlink(p)
            if tgt.startswith(kit) or tgt.startswith(work):
                os.remove(p)
                removed += 1
print(f"✓ 清理旧用户级软链 {removed} 个")
sp = os.path.join(pi_dir, "settings.json")
if os.path.exists(sp):
    d = json.load(open(sp))
    skills = d.get("skills", [])
    kept = [x for x in skills if not (x.startswith(kit) or x.startswith(work))]
    if kept != skills:
        d["skills"] = kept
        json.dump(d, open(sp, "w"), ensure_ascii=False, indent=2)
        print("✓ settings.json 已移除旧 skills 注册（改由 .pi 项目发现）")
PYEOF2

# 2) 数据文件：只在缺失时补默认（policy.yaml / decision-log.md 是数据，不覆盖）
#    产出目录（stocks/ holding/ review/ ...）不预建——写文件时按需自动创建
[ -f "$WORK_DIR/policy.yaml" ] || cp "$KIT/policy.yaml" "$WORK_DIR/policy.yaml"
[ -f "$WORK_DIR/decision-log.md" ] || cp "$KIT/decision-log.md" "$WORK_DIR/decision-log.md"
echo "✓ 数据文件: policy.yaml / decision-log.md（产出目录按需自动创建）"

echo ""
echo "✅ 安装完成（.pi 项目布局）。"
echo "   · 定时任务无需处理：bubblesd 以 --approve 启动 pi，项目资源自动受信"
echo "   · 交互使用：首次在工作区运行 pi 时同意信任提示（或 pi --approve）"
echo "   验证：pi --print --approve -p '列出你系统提示里所有 skill 的 name，逗号分隔'"
echo "   预期 13 个：futuapi holding-decision-review ima-skill industry-deep-analysis"
echo "   industry-monitor multi-tavily-search mx-data mx-moni mx-search"
echo "   private-company-analysis signal-monitor stock-buying-evaluation stock-trade-review"
