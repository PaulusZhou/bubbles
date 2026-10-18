# 数据源与降级顺序（holding-decision-review 配套）

| 数据 | 首选 | 降级（免密钥） | 说明 |
|---|---|---|---|
| 港美股行情 | futuapi（OpenD 127.0.0.1:11111） | 腾讯行情 `curl "http://qt.gtimg.cn/q=hk01810,hk01211"` | 返回 GBK，需 `iconv -f gbk -t utf-8` |
| A 股行情/财务 | mx-data（MX_APIKEY） | 同上（`sh603662` / `sz002281`） | 字段：3=现价 4=昨收 32=涨跌% 39=PE |
| 个股新闻 | multi-tavily-search | 东方财富搜索 API | 见下 |
| 宏观/政策 | multi-tavily-search | 东方财富搜索 API（关键词："美联储" "央行" "五中全会"等） | 权威源优先：政府网 > 财联社 > 社区 |

## 腾讯行情字段（A 股/港股通用位置）
```
f[1]=名称 f[3]=现价 f[4]=昨收 f[5]=今开 f[31]=时间 f[32]=涨跌额 f[33]=涨跌%
f[34]=最高 f[35]=最低 f[38]=换手 f[39]=PE(TTM) f[43]=总市值(亿) f[44]=流通市值
```
港股示例（hk01810）：`~` 分隔，3=现价 39=成交额(万) 45=PE 47/48=市值(亿)。

## 东方财富新闻搜索（免密钥）
```bash
python3 - <<'EOF'
import json, re, subprocess, urllib.parse
kw = "小米集团"  # 关键词
p = {"uid": "", "keyword": kw, "type": ["cmsArticleWebOld"], "client": "web",
     "clientType": "web", "clientVersion": "curr",
     "param": {"cmsArticleWebOld": {"searchScope": "default", "sort": "default",
                                     "pageIndex": 1, "pageSize": 10}}}
url = "https://search-api-web.eastmoney.com/search/jsonp?cb=x&param=" + urllib.parse.quote(json.dumps(p, ensure_ascii=False))
raw = subprocess.run(["curl", "-sL", "-A", "Mozilla/5.0", url], capture_output=True, text=True).stdout
d = json.loads(re.search(r'x\((.*)\)', raw, re.S).group(1))
for it in d["result"]["cmsArticleWebOld"]:
    print(re.sub(r"<[^>]+>", "", it["title"]), "|", it["date"], "|", it["url"])
EOF
```
注意：`sort` 必须用 `"default"`（用 `"time"` 会返回无关的全站新闻）。

## 时效性纪律
- 行情注明"YYYY-MM-DD 收盘"；新闻逐条注明发布日期
- 搜索结果与记忆冲突时以搜索为准；无法确认最新时标注"截至 X 年 X 月"
