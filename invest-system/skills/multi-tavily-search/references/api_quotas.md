# Tavily API 额度与限制

## 免费额度

- **月度额度**: 1000 API credits/月
- **重置时间**: 每月1号自动重置
- **注册要求**: 无需信用卡

## Credit 消耗规则

| 操作 | 消耗 |
|------|------|
| Basic Search | 1 credit/次 |
| Advanced Search | 2 credits/次 |
| Basic Extract | 1 credit/5个URL |
| Advanced Extract | 2 credits/5个URL |

本 skill 使用 Basic Search，每次搜索消耗 1 credit。

## 速率限制

- 开发版: 100 requests/分钟

本仓库安装、密钥与调用方式见 `references/usage.md` 与上级 `SKILL.md`；此处仅汇总官方额度与速率。

## 获取免费 Key

前往 https://app.tavily.com 注册账号即可获得免费 key。
