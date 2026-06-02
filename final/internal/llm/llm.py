# llm — LLM 客户端（真实 API + Mock 降级）
import logging
import re
from dataclasses import dataclass
from typing import Dict, List

import requests

from config.config import APIConfig

logger = logging.getLogger(__name__)


@dataclass
class Message:
    role: str
    content: str


class Client:
    """LLM 客户端封装，支持真实 API 和 Mock 模式。"""

    def __init__(self, cfg: APIConfig):
        self.cfg = cfg
        self._mock_responses = {
            "hello": "你好！我是一个智能助手，很高兴为你服务。",
            "hi": "嗨！有什么我可以帮助你的吗？",
            "name": "我是 AGI 智能助手，是一个基于大语言模型的 AI 助手。",
            "time": "当前时间是 2024 年。",
            "weather": "今天天气晴朗，温度适中。",
        }

    def chat(self, messages: List[Message]) -> str:
        if not self.cfg.is_real_llm():
            return self._mock_chat(messages)

        try:
            payload = {
                "model": self.cfg.llm_model,
                "messages": [{"role": m.role, "content": m.content} for m in messages],
                "temperature": self.cfg.temperature,
            }
            headers = {
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.cfg.llm_api_key}",
            }
            response = requests.post(
                self.cfg.llm_api_url,
                headers=headers,
                json=payload,
                timeout=60,
            )
            response.raise_for_status()
            result = response.json()
            if "choices" in result and result["choices"]:
                return result["choices"][0]["message"]["content"]
            return "API 返回格式异常"
        except Exception as e:
            logger.error("LLM API 调用失败: %s", e)
            return f"抱歉，暂时无法连接到语言模型服务。错误: {str(e)}"

    def chat_context(self, ctx, system_prompt: str, messages: List[Message]) -> str:
        """带上下文的对话接口，兼容主分支 Go 版 ChatContext。"""
        if getattr(ctx, "cancelled", False):
            return "[已中断] 请求被取消"
        return self.chat([Message(role="system", content=system_prompt)] + messages)

    def _mock_chat(self, messages: List[Message]) -> str:
        if not messages:
            return "你好！"
        last_msg = messages[-1].content.lower()
        for key, response in self._mock_responses.items():
            if key in last_msg:
                return response
        return f"这是一个模拟回复。你的问题是: {last_msg}"

    def embed(self, text: str) -> List[float]:
        if not self.cfg.is_real_embedding():
            return self._mock_embed(text)

        try:
            payload = {
                "model": self.cfg.embedding_model,
                "input": text,
            }
            headers = {
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.cfg.embedding_api_key}",
            }
            response = requests.post(
                self.cfg.embedding_api_url,
                headers=headers,
                json=payload,
                timeout=60,
            )
            response.raise_for_status()
            result = response.json()
            if "data" in result and result["data"]:
                return result["data"][0]["embedding"]
            return []
        except Exception as e:
            logger.error("Embedding API 调用失败: %s", e)
            return self._mock_embed(text)

    def _mock_embed(self, text: str) -> List[float]:
        hash_val = hash(text)
        return [((hash_val * (i + 1)) % 1000) / 1000.0 for i in range(768)]

    def extract_preferences(self, text: str) -> Dict[str, str]:
        """从文本中提取偏好信息，供主分支逻辑使用。"""
        preferences: Dict[str, str] = {}
        if not text:
            return preferences

        if "我叫" in text or "我是" in text:
            match = re.search(r"(我叫|我是)\s*(\S+)", text)
            if match:
                preferences["name"] = match.group(2)

        if "喜欢" in text:
            match = re.search(r"喜欢\s*(\S+)", text)
            if match:
                preferences["like"] = match.group(1)

        if "讨厌" in text or "不喜欢" in text:
            match = re.search(r"(讨厌|不喜欢)\s*(\S+)", text)
            if match:
                preferences["dislike"] = match.group(2)

        return preferences

    def summarize(self, text: str, max_length: int = 100) -> str:
        if not self.cfg.is_real_llm():
            return text[:max_length] + "..." if len(text) > max_length else text

        try:
            messages = [
                Message(role="system", content="请将以下文本进行简要总结，控制在指定长度内。"),
                Message(role="user", content=f"文本: {text}\n\n最大长度: {max_length}"),
            ]
            return self.chat(messages)
        except Exception as e:
            logger.error("Summarize API 调用失败: %s", e)
            return text[:max_length] + "..." if len(text) > max_length else text
