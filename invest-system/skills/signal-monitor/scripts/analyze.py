#!/usr/bin/env python3
"""
RSS投资资讯分析工具

功能：
1. 从RSS源获取原始数据
2. 过滤投资相关内容
3. 分类、排序
4. 生成摘要报告
"""

import json
import os
import re
import ssl
import urllib.request
import urllib.error
import xml.etree.ElementTree as ET
from datetime import datetime, timezone, timedelta
from pathlib import Path
from collections import defaultdict

# ==================== 配置 ====================

# 投资核心关键词（必须包含）
INVESTMENT_CORE_KEYWORDS = [
    # 股票代码
    r'\b[A-Z]{2,5}\b',  # 美股代码
    r'HK\.\d{5}',        # 港股代码
    r'\b\d{6}\b',        # A股代码
    # 公司名称（常见巨头）
    'Apple', 'Tesla', 'Nvidia', 'NVIDIA', 'Microsoft', 'Google', 'Amazon',
    'Meta', 'Facebook', 'Netflix', 'Intel', 'AMD', 'Qualcomm',
    '腾讯', '阿里巴巴', '小米', '华为', '字节跳动', '百度', '京东',
    'Toyota', 'GM', 'Ford', 'Volkswagen', 'BMW', 'Mercedes',
    'JPMorgan', 'Goldman', 'Morgan Stanley', 'Bank of America',
    'Visa', 'Mastercard', 'PayPal', 'Coinbase', 'Square',
    # 投资词汇
    '买入', '卖出', '评级', '目标价', '上调', '下调', '增持', '减持',
    '财报', '营收', '利润', 'EPS', '盈利', '亏损', '季报', '年报',
    'IPO', '上市', '退市', '并购', '收购', '融资', '投资',
    '市值', '估值', 'PE', 'PB', 'DCF', '估值',
    '涨', '跌', '上涨', '下跌', '飙升', '暴跌',
    '美联储', '央行', '利率', '加息', '降息', 'QE', 'QT',
    'CPI', 'PPI', 'GDP', '失业率', '非农',
    'AI', '人工智能', '大模型', 'LLM', 'GPT', 'ChatGPT',
    '自动驾驶', '无人驾驶', 'FSD', 'Robotaxi',
    '新能源', '电动车', 'EV', '电池', '锂', '光伏',
    '芯片', '半导体', '晶圆', 'GPU', 'H100', 'B100',
    '人形机器人', '机器人', 'Atlas', 'Optimus', 'Figure',
    'crypto', 'bitcoin', 'BTC', 'ethereum', 'ETH', '以太坊',
    '美联储', 'FOMC', '鲍威尔', '耶伦',
    '通胀', '通缩', '滞胀', '衰退', '软着陆', '硬着陆',
    '分析师', '预测', '展望', '展望未来',
]

# 排除词（过滤噪音）
EXCLUDE_KEYWORDS = [
    '天气', 'weather', 'temperature', 'forecast',
    '体育', 'sports', 'football', 'basketball', 'soccer',
    '娱乐', 'celebrity', 'movie', 'music', 'celebrity',
    '八卦', ' gossip', 'rumor',
    '天气', '降雨', '降雪', '台风',
]

# 分类规则
CATEGORY_RULES = {
    '巨头动态': [
        'AAPL', 'Apple', 'MSFT', 'Microsoft', 'GOOGL', 'Google', 'Alphabet',
        'AMZN', 'Amazon', 'NVDA', 'Nvidia', 'NVIDIA', 'TSLA', 'Tesla',
        'META', 'Meta', '腾讯', '小米', '阿里巴巴', '字节跳动', '百度',
        'Intel', 'AMD', 'Qualcomm', 'Netflix', 'Disney', 'Warner',
    ],
    '宏观政策': [
        '美联储', 'FOMC', '鲍威尔', '耶伦', '央行', '利率', '加息', '降息',
        'CPI', 'PPI', 'GDP', '失业率', '非农', '通胀', '通缩', 'QE', 'QT',
        '货币政策', '财政政策', '刺激', '紧缩', '软着陆', '硬着陆',
    ],
    '行业趋势': [
        'AI', '人工智能', '大模型', 'LLM', 'GPT', 'ChatGPT', 'Gemini',
        '自动驾驶', 'FSD', 'Robotaxi', '无人驾驶',
        '新能源', '电动车', 'EV', '电池', '锂', '光伏', '太阳能',
        '芯片', '半导体', '晶圆', 'GPU', 'H100', 'B100',
        '人形机器人', '机器人', 'Atlas', 'Optimus', 'Figure', '宇树', '优必选',
        '云计算', 'SaaS', '云服务', 'AWS', 'Azure', '阿里云',
    ],
    '深度分析': [
        '分析', '评级', '预测', '估值', '目标价', '展望', '研报',
        'Seeking Alpha', 'Stratechery', 'Bloomberg Opinion', 'Economist',
        '华尔街', '分析师', '机构', '看法', '观点',
    ],
    'Crypto': [
        'bitcoin', 'BTC', 'ethereum', 'ETH', '以太坊', 'Crypto',
        'Coinbase', 'Binance', '加密货币', '比特币', '以太',
    ],
    '市场信号': [
        'IPO', '上市', '退市', '并购', '收购', '融资', '投资',
        'SPAC', '上市', '募资', '申购', '打新',
    ],
}

# 来源优先级
SOURCE_PRIORITY = {
    'high': 3,
    'medium': 2,
    'low': 1,
}

# ==================== 路径配置 ====================

SCRIPT_DIR = Path(__file__).resolve().parent
SKILL_DIR = SCRIPT_DIR.parent  # rss-monitor 根目录
RSS_SCRIPT = SCRIPT_DIR / 'fetch_rss.py'  # 同目录下
RSS_SOURCES = SKILL_DIR / 'rss_sources.json'

TEMP_DIR = Path('/tmp/rss-monitor')
OUTPUT_DIR = Path(os.environ.get('INVEST_ROOT', os.path.normpath(os.path.join(os.path.dirname(__file__), '..', '..')))) / 'rss-digest'


def ensure_dirs():
    """确保输出目录存在"""
    TEMP_DIR.mkdir(parents=True, exist_ok=True)
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)




# ==================== RSS获取 ====================

def get_rss_data():
    """获取RSS数据（优先复用已有数据，存/tmp）"""
    # 检查今天是否已有原始数据（/tmp目录）
    today = datetime.now().strftime('%Y-%m-%d')
    existing_file = TEMP_DIR / f'rss_raw_{today}.json'

    if existing_file.exists():
        print(f"📂 找到已缓存数据: {existing_file.name}")
        with open(existing_file, 'r', encoding='utf-8') as f:
            return json.load(f)

    # 调用fetch_rss.py获取新数据
    print("📡 开始获取RSS数据...")
    import subprocess

    result = subprocess.run(
        ['python3', str(RSS_SCRIPT), '1', '--output', str(existing_file)],
        capture_output=True,
        text=True,
        timeout=120
    )

    if result.returncode != 0:
        print(f"❌ 获取失败: {result.stderr}")
        return None

    if existing_file.exists():
        with open(existing_file, 'r', encoding='utf-8') as f:
            return json.load(f)

    return None


# ==================== 关键词过滤 ====================

def contains_investment_keywords(text):
    """检查文本是否包含投资关键词"""
    if not text:
        return False

    # 检查排除词
    for exclude in EXCLUDE_KEYWORDS:
        if exclude.lower() in text.lower():
            return False

    # 检查核心关键词
    for keyword in INVESTMENT_CORE_KEYWORDS:
        if re.search(keyword, text, re.IGNORECASE):
            return True

    return False


def calculate_match_score(item):
    """计算内容匹配度分数"""
    title = item.get('title', '')
    description = item.get('description', '')
    content = item.get('content', '')
    text = f"{title} {description} {content}"

    # 计算匹配的关键词数量
    score = 0
    for keyword in INVESTMENT_CORE_KEYWORDS:
        if re.search(keyword, text, re.IGNORECASE):
            score += 1

    return min(score, 10)  # 最高10分


def categorize_item(item):
    """对内容进行分类"""
    title = item.get('title', '')
    description = item.get('description', '')
    content = item.get('content', '')
    text = f"{title} {description} {content}"

    for category, keywords in CATEGORY_RULES.items():
        for kw in keywords:
            if kw in text:
                return category

    return '其他'


# ==================== 重要性评分 ====================

def calculate_importance(item, raw_data):
    """计算资讯重要性分数"""
    score = 0

    # 1. 来源优先级 (30%)
    priority = item.get('source_priority', 'medium')
    score += SOURCE_PRIORITY.get(priority, 2) * 3  # 最高9分

    # 2. 内容匹配度 (40%)
    match_score = calculate_match_score(item)
    score += match_score * 4  # 最高40分

    # 3. 时间新鲜度 (20%)
    pub_date = item.get('pub_date', '')
    if pub_date:
        try:
            # 尝试解析RFC 822格式
            parsed = datetime.strptime(pub_date, "%a, %d %b %Y %H:%M:%S %z")
            hours_old = (datetime.now(timezone.utc) - parsed).total_seconds() / 3600
            # 越新分数越高，24小时内为满分
            time_score = max(0, 10 - hours_old / 2.4)
            score += time_score * 2  # 最高20分
        except:
            score += 5  # 无法解析时间给5分
    else:
        score += 5

    # 4. 内容长度 (10%)
    content_len = len(item.get('content', '') or item.get('description', ''))
    if content_len > 500:
        score += 10
    elif content_len > 200:
        score += 5
    else:
        score += 2

    return score


def classify_importance(score):
    """根据分数分类"""
    if score >= 60:
        return '🔴'
    elif score >= 40:
        return '🟡'
    else:
        return '🟢'


# ==================== 生成报告 ====================

def generate_digest(raw_data):
    """生成资讯摘要"""
    items = raw_data.get('data', [])
    print(f"📥 原始数据: {len(items)} 条")

    # 过滤投资相关内容
    filtered = []
    for item in items:
        title = item.get('title', '')
        description = item.get('description', '')
        content = item.get('content', '')

        if contains_investment_keywords(title) or contains_investment_keywords(description):
            filtered.append(item)

    print(f"✅ 过滤后: {len(filtered)} 条")

    # 分类
    categorized = defaultdict(list)
    for item in filtered:
        category = categorize_item(item)
        item['category'] = category
        categorized[category].append(item)

    # 计算重要性并排序
    for category, items_list in categorized.items():
        for item in items_list:
            item['importance_score'] = calculate_importance(item, raw_data)
            item['importance_level'] = classify_importance(item['importance_score'])

        # 按重要性排序
        categorized[category] = sorted(
            items_list,
            key=lambda x: x['importance_score'],
            reverse=True
        )

    # 提取各等级
    critical = []
    attention = []
    general = []

    for item in filtered:
        level = item.get('importance_level', '🟢')
        if level == '🔴':
            critical.append(item)
        elif level == '🟡':
            attention.append(item)
        else:
            general.append(item)

    # 生成Markdown报告
    today = datetime.now().strftime('%Y-%m-%d')

    report = f"""# 投资资讯摘要 {today}

## 🔴 重大资讯 ({len(critical)}条)

"""

    for item in critical[:5]:
        title = item.get('title', '无标题')
        source = item.get('source_name', '未知来源')
        link = item.get('link', '')
        desc = item.get('description', '')[:200]

        report += f"### {title}\n"
        report += f"- 来源: {source}\n"
        if link:
            report += f"- 链接: [查看]({link})\n"
        if desc:
            report += f"- 要点: {desc}\n"
        report += "\n"

    report += f"""
## 🟡 关注资讯 ({len(attention)}条)

"""

    for item in attention[:10]:
        title = item.get('title', '无标题')
        source = item.get('source_name', '未知来源')
        category = item.get('category', '其他')
        link = item.get('link', '')

        report += f"### [{category}] {title}\n"
        report += f"- 来源: {source}\n"
        if link:
            report += f"- 链接: [查看]({link})\n"
        report += "\n"

    # 统计
    report += f"""
## 📊 分类统计

| 分类 | 数量 |
|------|------|
"""

    for cat, count in sorted(categorized.items(), key=lambda x: len(x[1]), reverse=True):
        report += f"| {cat} | {len(categorized[cat])} |\n"

    # 来源统计
    sources_ok = raw_data.get('sources_ok', 0)
    sources_fail = raw_data.get('sources_fail', 0)

    report += f"""
## 📈 数据来源

- 成功: {sources_ok} 个源
- 失败: {sources_fail} 个源

---

*生成时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}*
"""

    return report, {
        'critical': critical[:5],
        'attention': attention[:10],
        'categorized': dict(categorized),
        'total_filtered': len(filtered),
    }


# ==================== 主函数 ====================

def main():
    print("=" * 50)
    print("📊 RSS投资资讯分析")
    print("=" * 50)

    ensure_dirs()

    # 获取数据
    raw_data = get_rss_data()
    if not raw_data:
        print("❌ 无法获取RSS数据")
        return 1

    # 生成摘要
    report, summary = generate_digest(raw_data)

    # 保存报告
    today = datetime.now().strftime('%Y-%m-%d')
    report_file = OUTPUT_DIR / f'digest_{today}.md'

    with open(report_file, 'w', encoding='utf-8') as f:
        f.write(report)

    print(f"\n✅ 分析完成")
    print(f"📊 原始: {raw_data.get('count', 0)} 条 → 过滤: {summary['total_filtered']} 条")
    print(f"🔴 重大: {len(summary['critical'])} 条")
    print(f"🟡 关注: {len(summary['attention'])} 条")
    print(f"💾 本地保存: {report_file}")

    # 打印摘要到控制台
    print("\n" + "=" * 50)
    print("📰 今日重点资讯")
    print("=" * 50)

    if summary['critical']:
        print("\n🔴 重大:")
        for item in summary['critical'][:3]:
            print(f"  • {item.get('title', '')[:60]}")

    if summary['attention']:
        print("\n🟡 关注:")
        for item in summary['attention'][:5]:
            print(f"  • {item.get('title', '')[:60]}")

    return 0


if __name__ == '__main__':
    exit(main())