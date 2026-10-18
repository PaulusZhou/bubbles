# Multi Tavily Search — 使用说明

## 外部依赖与网络

- Tavily Search API：由 `tavily-python` 的 `TavilyClient` 发起请求（具体端点以 SDK 为准）。
- 运行环境需能访问公网 Tavily 服务。

## 功能

- **多 Key 轮换**：按配置顺序使用 Tavily API key；本月本地计为已耗尽的 key 会跳过；遇认证失败、额度或限流则自动换下一个。
- **用量记录**：在 `~/.web_search_usage.json` 中按 key 累计本月用量（新月重置逻辑见 `scripts/key_store.py`）；可通过 `SearchManager.get_status()` 查看摘要。
- **搜索能力**：基于 `tavily-python`，当前实现为 **Basic Search**（`search_depth="basic"`），返回标题、URL、摘要等；单次请求条数受 `SearchConfig.max_results` 等约束（默认见 `scripts/config.py`）。
- **失败语义**：全部 key 均不可用时抛出 `AllProvidersExhaustedError`（定义于 `scripts/providers/base.py`）。

## 使用方法

1. **安装依赖**：`pip install tavily-python`。
2. **配置密钥**（二选一，**JSON 优先于环境变量**）：
   - 文件 `~/.web_search_keys.json` 示例：

```json
{ "keys": ["tvly-a", "tvly-b"], "monthly_quota": 1000 }
```

   - 或环境变量：`export TAVILY_API_KEYS="tvly-a,tvly-b"`（逗号分隔多个 key）。
3. **运行路径**：以本 skill 的目录为 Python 包根，需把 **skill 根目录**（仓库内 `.cursor/skills/multi-tavily-search/`）加入 `PYTHONPATH`，再执行你的脚本或 REPL。
4. **调用示例**（`search` 为异步方法，需 `asyncio.run` 或事件循环内 `await`）：

```python
import asyncio
from scripts.search_manager import SearchManager

manager = SearchManager()
response = asyncio.run(manager.search("你的查询"))
for r in response.results:
    print(r.title, r.url)

print(manager.get_status())
```

## 注意事项

- **密钥安全**：密钥只从 `~/.web_search_keys.json` 或 `TAVILY_API_KEYS` 读取，**禁止**在代码、仓库或日志中硬编码；`get_status()` 返回的 key 为掩码形式。
- **本地配额与官方账单**：`monthly_quota` 与本地计数用于轮换决策，可能与 Tavily 控制台实时计费不完全一致；调参与官方限制见 [api_quotas.md](api_quotas.md)。
- **无 key 或全失败**：未配置 key 或所有 key 不可用时会抛错，调用方应捕获 `AllProvidersExhaustedError` 并做降级或提示。
- **依赖**：未安装 `tavily-python` 时 Provider 初始化会失败（网络要求见上文「外部依赖与网络」）。

## 模块一览

| 路径 | 说明 |
|------|------|
| `scripts/search_manager.py` | 对外入口 `SearchManager` |
| `scripts/config.py` | 加载 `SearchConfig` 与 key 列表 |
| `scripts/key_store.py` | 月度用量与 key 状态 |
| `scripts/providers/tavily_provider.py` | Tavily 调用与 Basic Search |
