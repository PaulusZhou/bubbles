# decision-record — 决策结果结构化 Schema（v1）

> 决策的唯一结构化形态。三处消费：①决策书内嵌（人读+归档）②decision-log 行（审计）③holdings DB（系统状态）。
> 任何决策书缺 decision-record 视为流程未完成。

## Schema

```yaml
decision_record:
  id: D-YYYYMMDD-NN          # 决策日志 ID，全局递增
  date: 2026-10-18
  ticker: HK.01810           # 标的代码
  decision: hold             # buy | sell | hold | add | reject  （五选一，强制）
  decision_reason: 纪律       # thesis | discipline | falsified | insufficient_evidence | rebalance
  hypothesis_id: H1          # 对应假设卡
  readiness:                 # 决策就绪度门 6 项
    hypothesis: true         # 假设卡完整（含证伪条件/窗口/目标止损）
    data: false              # 驱动因子当期数据
    debate: true             # 多空证据都收集（空头复核已跑）
    falsification: true      # 证伪条件未触发
    risk: true               # 风控硬规则过
    premortem: true          # 预验尸已写
  missing_info:              # readiness 有 false 时必填，反向驱动调研
    - "小米汽车毛利率 → 11月Q3财报后补"
  position:                  # 操作要素（reject/hold 无操作时只填 stop/target）
    action: none             # none | buy | sell | add | reduce | clear
    qty: null
    stop_loss: 25.08
    target: 35.0
  review_date: 2026-11-30    # 强制复核日（买入后 ≤30 天）
  stock_type: growth         # growth | value | cyclical | speculative（决定估值检查带）
  ref_price: 26.66           # 决策时参考价（risk_check 算止损深度用）
  pe_ttm: 14.9               # 决策时 PE（risk_check 估值带校验用）
  snapshot_index: _snapshots/2026-10-18/hk01810/   # point-in-time 证据快照目录（必填）
  replace_ticker: null       # 持仓满员时必填：替换谁 + 理由写入 notes
  bear_review:               # 空头复核结论（第 2 步产物）
    verdict: oppose          # oppose | conditional | no_objection
    weakest_link: "汽车毛利率"
  risk_review:               # 风控结论（第 3 步产物）
    verdict: pass            # pass | pass_conditional | block
    violations: []           # 违反的规则名列表
    exemptions_requested: [] # 人工豁免请求
  evidence: {for: 5, against: 4, sources_dated: true}
  outcome:                   # 回填规则（防结果偏差）
    check_30d: null          # 30 天只核对【驱动因子数据更新/证伪条件是否触发】，不评盈亏
    verify_12m: null         # 12 个月评定假设兑现度（兑现/部分/未兑现/证伪）
    attribution: null        # 归因：{beta: x%, alpha: x%}（收益拆分，防止把运气当能力）
    post_sell_60d: null      # 卖出后 60 天回访：卖出理由事后看对不对
    discipline_score: null   # A按计划 | B轻微偏离 | C违纪（与盈亏无关）
```

## 决策值语义

| decision | 触发场景 | position.action |
|---|---|---|
| buy | 新标的，假设验证通过 + 估值合理 | buy |
| add | 持仓加仓，前提条件满足 | add |
| hold | 继续持有；或依据不足（配合 missing_info） | none |
| sell | 止损/证伪/目标价/替换（含减仓） | sell / reduce / clear |
| reject | 假设被证伪或估值不达标，进否决归档（必填重新考虑条件） | none |

## 三处落库规则
1. **决策书 md**：末尾 `decision_record:` 代码块原样附上
2. **decision-log.md**：一行映射（id/ticker/decision/依据充分性=6项计数/position/review_date/replace/bear+risk verdict）
3. **holdings DB**（`bubbles holding set`）：
   - `decision` ← id
   - `state` ← decision 映射（buy/add→holding、sell→sold 或 pending_decision 待拍板、reject→falsified、hold→不变）
   - `stop_loss`/`target`/`review_date`/`hypothesis` ← position/review_date/hypothesis_id

## 校验规则（程序化强制：`scripts/risk_check.py <决策书.md> --stock-type <类型>`）
- decision ∈ 五选一，无歧义表述
- readiness 全 true 才允许 buy/add/sell 立即执行；有 false 时只允许 hold/reject
- missing_info 非空 ⇔ readiness 有 false
- bear_review.verdict == oppose 时 decision 不得为 buy/add
- buy/add 必填 stop_loss、target、review_date、snapshot_index
- 止损深度 ≥ hard_max_pct（默认 -35%）；估值带按 stock_type 分类（cyclical/speculative 默认否决需人工豁免）
- **risk_check 退出码 1 = 阻断，决策书不得提交**；沙盒模式（policy sandbox.mode）下买卖只落库标记不推真仓
- 冷却期：同标的两次决策书间隔 ≥ decision_cooldown_days（止损/证伪强制触发除外）
- 熔断：circuit_breaker_active = true 时禁止 buy/add
