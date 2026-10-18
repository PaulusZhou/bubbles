# -*- coding: utf-8 -*-
"""Tavily 搜索引擎实现.

使用 tavily-python SDK 调用 Tavily Search API。
免费额度: 1000 credits/月（basic search 消耗 1 credit/次）。
"""
import asyncio
from typing import List

from scripts.providers.base import (
    AuthenticationError,
    BaseSearchProvider,
    QuotaExhaustedError,
    RateLimitError,
    SearchResult,
)

try:
    from tavily import TavilyClient
    HAS_TAVILY = True
except ImportError:
    HAS_TAVILY = False


class TavilySearchProvider(BaseSearchProvider):  # pylint: disable=too-few-public-methods
    """Tavily 搜索引擎提供商.

    使用 Tavily Python SDK 进行搜索。
    Basic Search 每次消耗 1 credit，Advanced Search 消耗 2 credits。
    """

    provider_name = "tavily"

    def __init__(self, api_key: str, timeout: int = 30) -> None:
        """初始化 Tavily 提供商.

        :param api_key: Tavily API key
        :param timeout: 请求超时时间
        :raises ImportError: tavily-python 未安装
        """
        super().__init__(api_key, timeout)
        if not HAS_TAVILY:
            raise ImportError(
                "tavily-python 未安装，请运行: pip install tavily-python"
            )
        self._client = TavilyClient(api_key=api_key)

    async def _do_search(
        self,
        query: str,
        max_results: int = 5,
    ) -> List[SearchResult]:
        """执行 Tavily 搜索.

        :param query: 搜索关键词
        :param max_results: 最大返回结果数
        :return: 搜索结果列表
        """
        try:
            loop = asyncio.get_event_loop()
            response = await loop.run_in_executor(
                None,
                lambda: self._client.search(
                    query=query,
                    max_results=max_results,
                    search_depth="basic",
                ),
            )
        except Exception as exc:
            error_msg = str(exc).lower()
            if "unauthorized" in error_msg or "invalid api key" in error_msg:
                raise AuthenticationError(
                    f"Tavily API key 无效: {exc}"
                ) from exc
            if "rate limit" in error_msg or "429" in error_msg:
                raise RateLimitError(
                    f"Tavily 速率限制: {exc}"
                ) from exc
            if "quota" in error_msg or "credit" in error_msg or "usage limit" in error_msg:
                raise QuotaExhaustedError(
                    f"Tavily 额度耗尽: {exc}"
                ) from exc
            raise

        results = []
        for item in response.get("results", []):
            results.append(SearchResult(
                title=item.get("title", ""),
                url=item.get("url", ""),
                snippet=item.get("content", "")[:500],
                content=item.get("content", ""),
                score=item.get("score", 0.0),
                source="tavily",
            ))

        return results
