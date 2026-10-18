# -*- coding: utf-8 -*-
"""Tavily 搜索管理器.

按顺序尝试多个 API key，自动处理额度耗尽和 key 失效的情况。
"""
import logging
from typing import Optional

from scripts.config import SearchConfig, load_config
from scripts.key_store import KeyStore
from scripts.providers.base import (
    AllProvidersExhaustedError,
    AuthenticationError,
    QuotaExhaustedError,
    RateLimitError,
    SearchProviderError,
    SearchResponse,
)
from scripts.providers.tavily_provider import TavilySearchProvider

logger = logging.getLogger(__name__)

PROVIDER_NAME = "tavily"


class SearchManager:
    """Tavily 搜索管理器.

    持有多个 API key，当当前 key 的月度额度耗尽或失效时，
    自动切换到下一个可用 key。
    """

    def __init__(
        self,
        config: Optional[SearchConfig] = None,
        config_path: Optional[str] = None,
    ) -> None:
        """初始化搜索管理器.

        :param config: 搜索配置对象，为 None 时自动加载
        :param config_path: 配置文件路径
        """
        self._config = config or load_config(config_path)
        self._key_store = KeyStore(self._config.usage_file)

    async def search(
        self,
        query: str,
        max_results: Optional[int] = None,
    ) -> SearchResponse:
        """执行搜索，自动轮换 key.

        :param query: 搜索关键词
        :param max_results: 最大返回结果数
        :return: 搜索响应
        :raises AllProvidersExhaustedError: 所有 key 均不可用
        """
        if not self._config.keys:
            raise AllProvidersExhaustedError(
                "未配置任何 Tavily API key，请检查配置文件或环境变量 TAVILY_API_KEYS"
            )

        count = max_results or self._config.max_results
        errors = []

        for key in self._config.keys:
            if not self._key_store.is_key_available(
                PROVIDER_NAME, key, self._config.monthly_quota,
            ):
                logger.debug("key %s...%s 额度已用尽，跳过", key[:4], key[-4:])
                continue

            result = await self._try_key(key, query, count, errors)
            if result is not None:
                return result

        raise AllProvidersExhaustedError(
            f"所有 Tavily key 均不可用。错误: {'; '.join(errors)}"
        )

    async def _try_key(
        self,
        key: str,
        query: str,
        max_results: int,
        errors: list,
    ) -> Optional[SearchResponse]:
        """使用单个 key 尝试搜索.

        :param key: API key
        :param query: 搜索关键词
        :param max_results: 最大结果数
        :param errors: 错误收集列表
        :return: 搜索响应，失败返回 None
        """
        try:
            provider = TavilySearchProvider(
                api_key=key,
                timeout=self._config.timeout,
            )
            response = await provider.search(query, max_results)
            self._key_store.increment_usage(PROVIDER_NAME, key)
            logger.info("搜索成功: key=%s...%s, query=%s", key[:4], key[-4:], query[:50])
            return response

        except AuthenticationError as exc:
            self._key_store.mark_key_invalid(PROVIDER_NAME, key)
            errors.append(f"key {key[:4]}...(auth): {exc}")
            logger.warning("认证失败，标记 key 无效: %s", exc)

        except (QuotaExhaustedError, RateLimitError) as exc:
            self._key_store.increment_usage(
                PROVIDER_NAME, key, self._config.monthly_quota,
            )
            errors.append(f"key {key[:4]}...(quota/rate): {exc}")
            logger.warning("额度/限流: %s", exc)

        except SearchProviderError as exc:
            errors.append(f"key {key[:4]}...: {exc}")
            logger.warning("搜索异常: %s", exc)

        return None

    def get_status(self) -> dict:
        """获取所有 key 的使用状态.

        :return: 状态摘要字典
        """
        status = {
            "month": "",
            "keys": [],
            "total_remaining": 0,
        }

        for key in self._config.keys:
            used = self._key_store.get_usage(PROVIDER_NAME, key)
            remaining = max(0, self._config.monthly_quota - used)
            available = self._key_store.is_key_available(
                PROVIDER_NAME, key, self._config.monthly_quota,
            )
            masked = key[:4] + "***" + key[-4:]
            status["keys"].append({
                "key": masked,
                "used": used,
                "quota": self._config.monthly_quota,
                "remaining": remaining,
                "available": available,
            })
            status["total_remaining"] += remaining

        return status
