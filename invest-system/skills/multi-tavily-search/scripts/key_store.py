# -*- coding: utf-8 -*-
"""API Key 存储与月度额度追踪.

使用本地 JSON 文件记录每个 key 的月度使用量，
支持月度自动重置和多 key 轮换。
"""
import json
import os
import threading
from datetime import datetime
from pathlib import Path
from typing import Dict, Optional


class KeyStore:
    """管理 API key 的使用量追踪和月度重置.

    使用 JSON 文件持久化存储每个 key 的使用次数，
    当检测到新月份时自动重置所有计数器。
    """

    def __init__(self, usage_file: str) -> None:
        """初始化 KeyStore.

        :param usage_file: 使用量记录文件路径
        """
        self._usage_file = usage_file
        self._lock = threading.Lock()
        self._usage_data: Dict = {}
        self._load()

    def _load(self) -> None:
        """从文件加载使用量数据."""
        if Path(self._usage_file).exists():
            try:
                with open(self._usage_file, "r", encoding="utf-8") as usage_file:
                    self._usage_data = json.load(usage_file)
            except (json.JSONDecodeError, OSError):
                self._usage_data = {}

        self._check_month_reset()

    def _save(self) -> None:
        """持久化使用量数据到文件."""
        parent_dir = os.path.dirname(self._usage_file)
        if parent_dir:
            os.makedirs(parent_dir, exist_ok=True)

        with open(self._usage_file, "w", encoding="utf-8") as usage_file:
            json.dump(self._usage_data, usage_file, indent=2, ensure_ascii=False)

    def _check_month_reset(self) -> None:
        """检查是否需要月度重置.

        比较存储的月份标记与当前月份，如果不同则重置所有计数。
        """
        current_month = datetime.now().strftime("%Y-%m")
        stored_month = self._usage_data.get("month", "")

        if current_month != stored_month:
            self._usage_data = {
                "month": current_month,
                "providers": {},
            }
            self._save()

    def _ensure_provider_structure(self, provider: str, key: str) -> None:
        """确保 provider 和 key 的数据结构存在.

        :param provider: 引擎名称
        :param key: API key
        """
        if "providers" not in self._usage_data:
            self._usage_data["providers"] = {}

        if provider not in self._usage_data["providers"]:
            self._usage_data["providers"][provider] = {}

        if key not in self._usage_data["providers"][provider]:
            self._usage_data["providers"][provider][key] = {
                "used": 0,
                "invalid": False,
            }

    def get_usage(self, provider: str, key: str) -> int:
        """获取指定 key 的本月使用量.

        :param provider: 引擎名称
        :param key: API key
        :return: 已使用次数
        """
        with self._lock:
            self._check_month_reset()
            self._ensure_provider_structure(provider, key)
            return self._usage_data["providers"][provider][key]["used"]

    def increment_usage(self, provider: str, key: str, count: int = 1) -> int:
        """增加指定 key 的使用量.

        :param provider: 引擎名称
        :param key: API key
        :param count: 增加的使用次数
        :return: 更新后的使用量
        """
        with self._lock:
            self._check_month_reset()
            self._ensure_provider_structure(provider, key)
            self._usage_data["providers"][provider][key]["used"] += count
            new_usage = self._usage_data["providers"][provider][key]["used"]
            self._save()
            return new_usage

    def is_key_available(self, provider: str, key: str, quota: int) -> bool:
        """检查 key 是否仍有可用额度.

        :param provider: 引擎名称
        :param key: API key
        :param quota: 月度额度上限
        :return: True 表示仍有额度可用
        """
        with self._lock:
            self._check_month_reset()
            self._ensure_provider_structure(provider, key)
            key_data = self._usage_data["providers"][provider][key]
            if key_data.get("invalid", False):
                return False
            return key_data["used"] < quota

    def mark_key_invalid(self, provider: str, key: str) -> None:
        """将 key 标记为无效（认证失败等情况）.

        :param provider: 引擎名称
        :param key: API key
        """
        with self._lock:
            self._ensure_provider_structure(provider, key)
            self._usage_data["providers"][provider][key]["invalid"] = True
            self._save()

    def get_available_key(self, provider: str, keys: list, quota: int) -> Optional[str]:
        """从 key 列表中获取第一个可用的 key.

        :param provider: 引擎名称
        :param keys: API key 列表
        :param quota: 月度额度上限
        :return: 可用的 key，如果全部耗尽则返回 None
        """
        for key in keys:
            if self.is_key_available(provider, key, quota):
                return key
        return None

    def get_remaining_quota(self, provider: str, key: str, quota: int) -> int:
        """获取指定 key 的剩余额度.

        :param provider: 引擎名称
        :param key: API key
        :param quota: 月度额度上限
        :return: 剩余额度数
        """
        used = self.get_usage(provider, key)
        return max(0, quota - used)

    def get_status_summary(self, providers_config: dict) -> dict:
        """获取所有引擎和 key 的使用状态摘要.

        :param providers_config: 各引擎配置，格式为 {provider_name: ProviderConfig}
        :return: 状态摘要字典
        """
        with self._lock:
            self._check_month_reset()

        summary = {
            "month": self._usage_data.get("month", ""),
            "providers": {},
        }

        for name, provider_cfg in providers_config.items():
            provider_summary = {
                "keys": [],
                "total_remaining": 0,
            }

            for key in provider_cfg.keys:
                used = self.get_usage(name, key)
                remaining = max(0, provider_cfg.monthly_quota - used)
                masked_key = _mask_key(key)
                provider_summary["keys"].append({
                    "key": masked_key,
                    "used": used,
                    "quota": provider_cfg.monthly_quota,
                    "remaining": remaining,
                    "available": self.is_key_available(name, key, provider_cfg.monthly_quota),
                })
                provider_summary["total_remaining"] += remaining

            summary["providers"][name] = provider_summary

        return summary


def _mask_key(key: str) -> str:
    """遮蔽 API key 只显示前4位和后4位.

    :param key: 原始 key
    :return: 遮蔽后的 key
    """
    if len(key) <= 8:
        return key[:2] + "***" + key[-2:]
    return key[:4] + "***" + key[-4:]
