# -*- coding: utf-8 -*-
"""搜索引擎提供商基类.

定义所有搜索引擎统一的接口和数据结构。
"""
import abc
from dataclasses import dataclass, field
from typing import List, Optional


@dataclass
class SearchResult:
    """统一的搜索结果数据结构."""

    title: str = ""
    url: str = ""
    snippet: str = ""
    content: str = ""
    score: float = 0.0
    source: str = ""


@dataclass
class SearchResponse:
    """搜索响应，包含结果列表和元信息."""

    results: List[SearchResult] = field(default_factory=list)
    query: str = ""
    provider: str = ""
    key_used: str = ""
    error: Optional[str] = None


class SearchProviderError(Exception):
    """搜索引擎错误基类."""


class QuotaExhaustedError(SearchProviderError):
    """额度耗尽错误."""


class AuthenticationError(SearchProviderError):
    """认证失败错误."""


class RateLimitError(SearchProviderError):
    """速率限制错误."""


class AllProvidersExhaustedError(SearchProviderError):
    """所有引擎均不可用."""


class BaseSearchProvider(abc.ABC):  # pylint: disable=too-few-public-methods
    """搜索引擎提供商基类.

    所有具体的搜索引擎实现都需要继承此类，
    并实现 `_do_search` 方法。
    """

    provider_name: str = ""

    def __init__(self, api_key: str, timeout: int = 30) -> None:
        """初始化提供商.

        :param api_key: API key
        :param timeout: 请求超时时间（秒）
        """
        self._api_key = api_key
        self._timeout = timeout

    @abc.abstractmethod
    async def _do_search(
        self,
        query: str,
        max_results: int = 5,
    ) -> List[SearchResult]:
        """执行实际搜索（由子类实现）.

        :param query: 搜索关键词
        :param max_results: 最大返回结果数
        :return: 搜索结果列表
        :raises SearchProviderError: 搜索过程中出错
        """

    async def search(
        self,
        query: str,
        max_results: int = 5,
    ) -> SearchResponse:
        """执行搜索并返回统一格式的响应.

        :param query: 搜索关键词
        :param max_results: 最大返回结果数
        :return: 搜索响应对象
        """
        try:
            results = await self._do_search(query, max_results)
            return SearchResponse(
                results=results,
                query=query,
                provider=self.provider_name,
                key_used=_mask_key(self._api_key),
            )
        except SearchProviderError:
            raise
        except Exception as exc:
            raise SearchProviderError(
                f"{self.provider_name} 搜索失败: {exc}"
            ) from exc


def _mask_key(key: str) -> str:
    """遮蔽 API key.

    :param key: 原始 key
    :return: 遮蔽后的字符串
    """
    if len(key) <= 8:
        return key[:2] + "***" + key[-2:]
    return key[:4] + "***" + key[-4:]
