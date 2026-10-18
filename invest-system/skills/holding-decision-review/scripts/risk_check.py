#!/usr/bin/env python3
"""risk_check.py — 决策书程序化门槛（软约束 → 硬阻断）

用法:
  python3 scripts/risk_check.py <决策书.md> [--policy policy.yaml] [--stock-type growth|value|cyclical|speculative]

规则来源: policy.yaml（INVEST_ROOT 或 invest-system 包根，--policy 可指定）
退出码: 0 = 放行(或仅警告)  1 = 阻断  2 = 输入错误
"""
import argparse
import os
import re
import sys

import yaml


def load_policy(path: str | None) -> dict:
    if path:
        return yaml.safe_load(open(path))
    here = os.path.dirname(os.path.abspath(__file__))
    candidates = []
    # INVEST_ROOT（默认 = invest-system 包根）优先，其次 cwd 附近
    kit_root = os.path.normpath(os.path.join(here, "..", "..", ".."))
    invest = os.environ.get("INVEST_ROOT", kit_root)
    candidates.append(os.path.join(invest, "policy.yaml"))
    candidates.append(os.path.join(kit_root, "policy.yaml"))
    cwd = os.getcwd()
    candidates.append(os.path.join(cwd, "policy.yaml"))
    for c in candidates:
        c = os.path.normpath(c)
        if os.path.exists(c):
            return yaml.safe_load(open(c))
    raise FileNotFoundError("policy.yaml not found; pass --policy")


def extract_record(md_text: str) -> dict:
    m = re.search(r"```ya?ml\s*\n(.*?)\n```", md_text, re.S)
    if not m:
        raise ValueError("decision_record YAML block not found in decision book")
    block = yaml.safe_load(m.group(1))
    if not isinstance(block, dict) or "decision_record" not in block:
        raise ValueError("YAML block has no decision_record key")
    return block["decision_record"]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("book")
    ap.add_argument("--policy", default=None)
    ap.add_argument("--stock-type", default=None, choices=["growth", "value", "cyclical", "speculative"])
    args = ap.parse_args()

    try:
        rec = extract_record(open(args.book).read())
        pol = load_policy(args.policy)
    except Exception as e:
        print(f"✗ 输入错误: {e}")
        return 2

    blocks, warns = [], []
    decision = rec.get("decision", "")
    ready = rec.get("readiness", {}) or {}
    pos = rec.get("position", {}) or {}
    bear = (rec.get("bear_review", {}) or {}).get("verdict", "")
    risk = (rec.get("risk_review", {}) or {}).get("verdict", "")
    missing = rec.get("missing_info", []) or []
    rules = pol.get("rules", {}) or {}

    def check(ok: bool, msg: str, hard: bool = True):
        print(("  ✓ " if ok else ("  ✗ " if hard else "  ⚠ ")) + msg)
        if not ok and hard:
            blocks.append(msg)

    print(f"== risk_check: {rec.get('ticker')} / {decision} ==")

    # A. 决策合法性
    check(decision in {"buy", "sell", "hold", "add", "reject"}, f"决策五选一: {decision}")

    # B. 就绪度门
    ready_ok = all(ready.get(k) is True for k in ("hypothesis", "data", "debate", "falsification", "risk", "premortem"))
    if decision in {"buy", "add", "sell"}:
        check(ready_ok, f"就绪度门 6 项全真才可 {decision}（实际: {ready}）")
    check(bool(missing) != ready_ok, "missing_info ⇔ readiness 缺项（一致性）")

    # C. 操作要素
    if decision in {"buy", "add"}:
        check(pos.get("stop_loss") is not None, "buy/add 必填 stop_loss")
        check(pos.get("target") is not None, "buy/add 必填 target")
        check(bool(rec.get("review_date")), "buy/add 必填 review_date")
        stop_pct = None
        if pos.get("stop_loss") and rec.get("ref_price"):
            stop_pct = pos["stop_loss"] / rec["ref_price"] - 1
        if stop_pct is not None:
            hard_max = rules.get("sell_stop_loss", {}).get("hard_max_pct", -0.35)
            check(stop_pct >= hard_max, f"止损深度 {stop_pct:.0%} ≥ hard_max {hard_max:.0%}")

    # D. 对抗结论
    if decision in {"buy", "add"}:
        check(bear != "oppose", f"bear 反对时禁止 buy/add（实际: {bear or '缺'}）")
    if risk == "block":
        check(False, "risk block 时禁止一切执行类决策")

    # E. 估值带分类
    st = args.stock_type
    bands = pol.get("valuation_bands", {}) or {}
    if st and decision in {"buy", "add"}:
        band = bands.get(st)
        if band == "disabled":
            check(False, f"stock_type={st}: policy 禁用估值带/默认否决（需人工豁免留痕）")
        elif isinstance(band, list) and rec.get("pe_ttm"):
            check(band[0] <= rec["pe_ttm"] <= band[1],
                  f"PE {rec['pe_ttm']} ∈ {st} 带 {band}")
    if rec.get("pe_ttm") and not st and decision in {"buy", "add"}:
        warns.append("未指定 stock_type，估值带未校验")

    # F. 熔断与沙盒
    sb = pol.get("sandbox", {}) or {}
    if sb.get("mode") and decision in {"buy", "add", "sell"}:
        print(f"  ⚠ 沙盒模式开启：{decision} 只落库标记，不推真仓")
    if rec.get("circuit_breaker_active"):
        check(decision not in {"buy", "add"}, "熔断中禁止新开仓")

    for w in warns:
        print(f"  ⚠ {w}")
    if blocks:
        print(f"\n✗ 阻断（{len(blocks)} 项）：")
        for b in blocks:
            print(f"  - {b}")
        return 1
    print("\n✓ 放行" + ("（含警告）" if warns else ""))
    return 0


if __name__ == "__main__":
    sys.exit(main())
