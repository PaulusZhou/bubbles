---
name: multi-tavily-search
description: 通过 tavily-python 在本仓库运行 Tavily 联网搜索，支持多 API key 轮换、本地月度计数及认证失败或限流时的故障切换。
---

# Multi Tavily Search

## Quick Reference

| 主题 | 文件 |
|------|------|
| 安装、配置、完整示例、异常与模块 | [references/usage.md](references/usage.md) |
| 官方额度与速率 | [references/api_quotas.md](references/api_quotas.md) |

## When to Use

- 在本仓库用 Tavily 做程序化联网搜索或接入 `SearchManager`。
- 需要多 API key 轮换、本地月度计数，或单 key 认证失败/限流时自动换 key。
- 需查安装、密钥格式、`get_status()`、模块结构或 Tavily 官方额度。

## Core Rules

1. **密钥**：仅从 `~/.web_search_keys.json` 或环境变量 `TAVILY_API_KEYS` 读取；禁止硬编码。
2. **环境**：`pip install tavily-python`；将本 skill **根目录**加入 `PYTHONPATH`。
3. **入口**：`from scripts.search_manager import SearchManager`；`search` 为异步，使用 `asyncio.run` 或事件循环内 `await`。
4. **深读**：行为细节、异常与模块表见 [references/usage.md](references/usage.md)；额度见 [references/api_quotas.md](references/api_quotas.md)。

## 最小调用

```python
import asyncio
from scripts.search_manager import SearchManager

manager = SearchManager()
response = asyncio.run(manager.search("查询语句"))
```
