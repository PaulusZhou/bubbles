# -*- coding: utf-8 -*-
"""搜索引擎配置管理.

支持从 JSON 文件或环境变量加载 Tavily API key 列表。
JSON 文件优先级高于环境变量。

JSON 文件示例（~/.web_search_keys.json）:
{
  "keys": ["tvly-key1", "tvly-key2", "tvly-key3"],
  "monthly_quota": 1000
}

环境变量示例（多个 key 用逗号分隔）:
  TAVILY_API_KEYS="tvly-key1,tvly-key2,tvly-key3"
"""
import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import List, Optional


DEFAULT_CONFIG_PATH = os.path.expanduser("~/.web_search_keys.json")
DEFAULT_MONTHLY_QUOTA = 1000
ENV_VAR_KEYS = "TAVILY_API_KEYS"


@dataclass
class SearchConfig:
    """搜索配置."""

    keys: List[str] = field(default_factory=list)
    monthly_quota: int = DEFAULT_MONTHLY_QUOTA
    usage_file: str = os.path.expanduser("~/.web_search_usage.json")
    timeout: int = 30
    max_results: int = 5


def load_config(config_path: Optional[str] = None) -> SearchConfig:
    """加载搜索配置.

    优先从 JSON 文件加载，不存在则从环境变量读取。

    :param config_path: JSON 配置文件路径，为 None 时使用默认路径
    :return: 搜索配置对象
    """
    file_path = config_path or DEFAULT_CONFIG_PATH

    if Path(file_path).exists():
        return _load_from_file(file_path)

    return _load_from_env()


def _load_from_file(file_path: str) -> SearchConfig:
    """从 JSON 文件加载配置.

    :param file_path: 配置文件路径
    :return: 搜索配置对象
    """
    with open(file_path, "r", encoding="utf-8") as config_file:
        data = json.load(config_file)

    config = SearchConfig()
    config.keys = [k for k in data.get("keys", []) if k]
    config.monthly_quota = data.get("monthly_quota", DEFAULT_MONTHLY_QUOTA)

    if "usage_file" in data:
        config.usage_file = data["usage_file"]
    if "timeout" in data:
        config.timeout = data["timeout"]
    if "max_results" in data:
        config.max_results = data["max_results"]

    return config


def _load_from_env() -> SearchConfig:
    """从环境变量加载配置.

    多个 key 用逗号分隔，例如: TAVILY_API_KEYS="tvly-key1,tvly-key2"

    :return: 搜索配置对象
    """
    config = SearchConfig()
    keys_str = os.environ.get(ENV_VAR_KEYS, "")
    config.keys = [k.strip() for k in keys_str.split(",") if k.strip()]
    return config
