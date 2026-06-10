# tools — 工具定义与调用（time / weather / search）
import logging
import time
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional

logger = logging.getLogger(__name__)


@dataclass
class Tool:
    name: str
    description: str
    params: List[Dict[str, str]]
    func: Callable
    is_mcp: bool = False


@dataclass
class CallResult:
    success: bool
    content: str
    error: Optional[str] = None


# ─────────────────────────────── 内置工具 ────────────────────────────────

def get_time(_args: Dict[str, str]) -> str:
    return time.strftime("%Y-%m-%d %H:%M:%S", time.localtime())


def get_weather(args: Dict[str, str]) -> str:
    city = args.get("city", "北京")
    return f"{city} 今天天气晴朗，温度 25°C，风力 3 级。"


def search_web(args: Dict[str, str]) -> str:
    query = args.get("query", "")
    if not query:
        return "请提供搜索关键词"
    return f"搜索结果：关于 '{query}' 的相关信息...（模拟搜索）"


def rag_search(args: Dict[str, str]) -> str:
    query = args.get("query", "")
    return f"知识库检索结果：关于 '{query}' 的相关知识...（需要连接 RAG 引擎）"


# ─────────────────────────────── 工具列表 ────────────────────────────────

def default_tools() -> List[Tool]:
    return [
        Tool(name="get_time", description="获取当前系统时间", params=[], func=get_time),
        Tool(
            name="get_weather",
            description="获取指定城市的天气信息",
            params=[{"name": "city", "type": "string", "description": "城市名称"}],
            func=get_weather,
        ),
        Tool(
            name="search_web",
            description="执行网络搜索",
            params=[{"name": "query", "type": "string", "description": "搜索关键词"}],
            func=search_web,
        ),
        Tool(
            name="rag_search",
            description="在知识库中检索相关信息",
            params=[{"name": "query", "type": "string", "description": "检索关键词"}],
            func=rag_search,
        ),
    ]


# ─────────────────────────────── 工具调用器 ──────────────────────────────

class ToolExecutor:
    def __init__(self, tools: Optional[List[Tool]] = None):
        self.tools = tools if tools else default_tools()
        self._tool_map = {t.name: t for t in self.tools}

    def call(self, tool_name: str, args: Dict[str, str]) -> CallResult:
        if tool_name not in self._tool_map:
            return CallResult(success=False, content="", error=f"工具 {tool_name} 不存在")

        tool = self._tool_map[tool_name]
        try:
            result = tool.func(args)
            return CallResult(success=True, content=str(result))
        except Exception as e:
            logger.error("工具调用失败: %s", e)
            return CallResult(success=False, content="", error=str(e))

    def get_tool_descriptions(self) -> List[Dict[str, Any]]:
        descriptions = []
        for tool in self.tools:
            descriptions.append(
                {
                    "name": tool.name,
                    "description": tool.description,
                    "parameters": [
                        {
                            "name": p["name"],
                            "type": p["type"],
                            "description": p.get("description", ""),
                        }
                        for p in tool.params
                    ],
                    "is_mcp": tool.is_mcp,
                }
            )
        return descriptions

    def add_tool(self, tool: Tool):
        self.tools.append(tool)
        self._tool_map[tool.name] = tool


# ─────────────────────────────── MCP 工具 ────────────────────────────────

def new_mcp_tool(name: str, description: str, params: List[Dict[str, str]], func: Callable) -> Tool:
    return Tool(name=name, description=description, params=params, func=func, is_mcp=True)
