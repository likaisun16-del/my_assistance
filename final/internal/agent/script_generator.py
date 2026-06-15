# script_generator — 视频口播脚本生成器（结构化 JSON 输出）
import json
import re
from dataclasses import dataclass, field
from typing import List, Protocol


SCRIPT_PROMPT = """你是资深短视频脚本写手。请根据以下输入生成一条口播脚本。

要求：
1. 风格：{style}
2. 时长目标：{duration} 秒
3. 主题：{topic}

输出严格 JSON（不要 markdown 代码块、不要任何前后缀），结构如下：
{{
  "hook": "开头 5 秒钩子，必须制造冲突或反常识",
  "body": ["主体段落 1", "主体段落 2", "..."],
  "cta": "结尾呼吁",
  "duration_estimate": <数字，预估秒数>
}}
"""


class LLMLike(Protocol):
    def chat(self, messages, system_prompt: str = "") -> str: ...


@dataclass
class ScriptRequest:
    topic: str
    duration: int = 120
    style: str = "口播"


@dataclass
class ScriptResponse:
    hook: str = ""
    body: List[str] = field(default_factory=list)
    cta: str = ""
    duration_estimate: int = 0


def _strip_codeblock(s: str) -> str:
    s = s.strip()
    if s.startswith("```"):
        s = re.sub(r"^```[a-zA-Z]*\n?", "", s)
        s = re.sub(r"\n?```$", "", s)
    return s.strip()


class ScriptGenerator:
    def __init__(self, llm: LLMLike):
        self.llm = llm

    def generate(self, req: ScriptRequest) -> ScriptResponse:
        prompt = SCRIPT_PROMPT.format(style=req.style, duration=req.duration, topic=req.topic)
        try:
            from internal.llm.llm import Message
            raw = self.llm.chat([Message(role="user", content=prompt)])
        except ImportError:
            raw = self.llm.chat(prompt)
        cleaned = _strip_codeblock(raw)
        try:
            data = json.loads(cleaned)
        except json.JSONDecodeError as e:
            raise ValueError(f"invalid script json: {e}; raw={raw[:200]}")
        return ScriptResponse(
            hook=data.get("hook", ""),
            body=list(data.get("body", []) or []),
            cta=data.get("cta", ""),
            duration_estimate=int(data.get("duration_estimate", 0) or 0),
        )
