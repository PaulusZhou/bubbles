#!/usr/bin/env bash
# 注册 invest-system 的 bubbles 定时流水线（5 条）
# 用法: ./create-tasks.sh [--dry-run]
# 依赖: bubblesd 运行中（bubbles 会自动拉起）；agent 配置为 pi
#
# 触发设计：
#   信号扫描   每小时(交易时段) → 红信号立即飞书 + 强制触发持仓决策
#   持仓决策-收盘 每交易日15:30 → 只跑触发检查，有触发才决策
#   持仓决策-周度  周六09:00   → 流程1全量（假设兑现追踪）
#   新股决策-周度  周六10:00   → 流程2全量（候选→尽调→对抗→拍板）
#   月度审计      每月28日     → 记分卡 + decision-log 回填
set -euo pipefail

KIT="$(cd "$(dirname "$0")" && pwd)"
WORK="${INVEST_ROOT:-}"
if [ -z "$WORK" ]; then
  WORK="$(python3 - <<'EOF'
import os, yaml
p = os.path.expanduser("~/.bubbles/config.yaml")
try:
    print(yaml.safe_load(open(p)).get("work_dir") or "")
except Exception:
    print("")
EOF
)"
fi
[ -z "$WORK" ] && WORK="$KIT"   # 工作区：INVEST_ROOT > config work_dir > 包目录
DRY_RUN="${1:-}"

run() {
  if [[ "$DRY_RUN" == "--dry-run" ]]; then
    echo "[dry-run] bubbles $*"
  else
    bubbles "$@"
  fi
}

# 1) 信号扫描（事件触发源：红信号→强制决策）
run create -n "投资系统-信号扫描" -s "0 */1 9-15 * * 1-5" -p "工作目录 ${WORK}。
执行 signal-monitor 技能 Step 1-3：聚合资讯、事件红黄绿分级、信号分发。
🔴 重大信号立即 feishu.notify 并注明'触发持仓决策：{标的}/{驱动因子}'，然后对受影响持仓执行 holding-decision-review 技能流程；
🟡 进 events/YYYY-MM-DD.md 汇总；🟢 只留档。每条信号带来源+日期。"

# 2) 持仓决策-收盘（触发检查，无触发不动）
run create -n "投资系统-持仓决策-收盘" -s "30 15 * * 1-5" -p "工作目录 ${WORK}。
执行 holding-decision-review 技能，本次范围：Step 0-3 + Step 5 触发检查（止损/目标/证伪/复核日到期）。
有触发 → 对该标的走完整 Step 3-8（含 invest-bear/invest-risk 对抗与 decision-record 落库）；
无触发 → 只更新 bubbles holding 最新价并输出'今日无触发'。结论发飞书。"

# 3) 持仓决策-周度（流程1全量，每标的独立子流程防上下文衰减）
run create -n "投资系统-持仓决策-周度" -s "0 9 * * 6" -p "工作目录 ${WORK}。
执行 holding-decision-review 技能 Step 0-9 全量（含投资环境四维）。
【每标的独立子流程】逐标的单独跑：假设兑现核对→定向验证（快照落盘 _snapshots/）→invest-bear 证伪（自行取证≥3 新源）→invest-risk 风控→五选一决策→decision-record。
每份决策书过 scripts/risk_check.py（退出码 1=阻断）。缺失信息创建 bubbles 一次性任务跟进。
每份定稿决策书上传飞书：`lark-cli markdown +create --file <决策书> --as user`，URL 附到消息。
待拍板清单发飞书；任何飞书通知失败重试至多 3 次，仍失败写入日志并标 🔴。"

# 4) 新股决策-周度（流程2全量）
run create -n "投资系统-新股决策-周度" -s "0 10 * * 6" -p "工作目录 ${WORK}。
执行 invest-system/prompts/新股决策.md 流程（skill: signal-monitor + industry-deep-analysis 候选生成
→ stock-buying-evaluation 尽调 → invest-analyst/bear/risk 对抗 → 组合适配 → buy/reject decision-record）。
候选 2-3 只；观察池超 15 只先淘汰最弱。决策书与待拍板清单发飞书。"

# 5) 月度审计（目标追踪 + 回填 + 系统心跳）
run create -n "投资系统-月度审计" -s "0 10 28 * *" -p "工作目录 ${WORK}。
1) 对照 policy.yaml 出记分卡（收益/回撤/持仓数/金额/现金比例）；
2) 回填 decision-log outcome：30 天项只核对驱动因子/证伪条件（不评盈亏）；12 个月项评假设兑现 + beta/alpha 归因拆分；
3) 卖出后 60 天回访（卖得对不对）+ 否决回访（重新考虑条件是否触发，错杀/躲过统计）；
4) 【系统心跳】bubbles list 检查 5 条流水线近 30 天执行成功率，失败/超时告警；
5) policy 参数修订提案 + 沙盒退出评估（连续 2 次纪律 A 级可建议 sandbox.mode=false）；
6) 文档同步：新决策书用 `lark-cli markdown +create --file <文件> --as user` 上传并把 URL 附到月报（不需要脚本，逐个传即可）。月报发飞书。"

echo "✅ 5 条流水线注册完成（bubbles list 查看；bubbles pause/resume 可停启）"
