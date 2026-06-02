# agent — Python 版统一智能体：对齐主分支 Go 版的路由与响应结构
import json
import logging
import re
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional

from config.config import APIConfig
from internal.infra.infra import Infrastructure
from internal.llm.llm import Client as LLMClient, Message
from internal.memory.memory import LongTerm, Preference, ShortTerm
from internal.rag.rag import Engine as RAGEngine
from internal.tools.tools import CallResult, Tool, ToolExecutor, default_tools, new_mcp_tool

logger = logging.getLogger(__name__)


class StepType:
    THOUGHT = "Thought"
    ACTION = "Action"
    OBSERVATION = "Observation"
    FINAL_ANSWER = "Final Answer"


@dataclass
class ReActStep:
    type: str
    content: str
    tool: str = ""
    params: Optional[Dict[str, str]] = None


@dataclass
class ChatOptions:
    use_rag: bool = False
    selected_tools: Optional[List[str]] = None
    explicit: bool = False


@dataclass
class Response:
    query: str
    answer: str = ""
    mode: str = "chat"
    steps: List[ReActStep] = field(default_factory=list)
    tool_call: Optional[Dict[str, Any]] = None
    search_results: List[dict] = field(default_factory=list)
    task: Optional[dict] = None
    extracted_info: str = ""
    short_term_count: int = 0
    long_term_count: int = 0
    preferences: Dict[str, str] = field(default_factory=dict)
    interrupted: bool = False


class UnifiedAgent:
    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.inf = inf
        self.llm = LLMClient(cfg)
        self.stm = ShortTerm(cfg.short_term_max_turns)
        self.ltm = LongTerm(cfg, inf)
        self.preference = Preference("default_user", inf)
        self.rag = RAGEngine(cfg, inf)
        self.tool_executor = ToolExecutor(default_tools())
        self.max_iterations = cfg.max_iterations
        self.max_retries = cfg.max_retries
        self._cancel_event = threading.Event()

        self.ltm.set_embed_fn(self.llm.embed)
        self.rag.set_generate_fn(self._llm_generate)
        self.ltm.load_from_storage()

        logger.info("✅ UnifiedAgent 初始化完成")

    def _llm_generate(self, system_prompt: str, user_msg: str) -> str:
        messages = [
            Message(role="system", content=system_prompt),
            Message(role="user", content=user_msg),
        ]
        return self.llm.chat(messages)

    def cancel(self):
        self._cancel_event.set()

    def _reset_cancel(self):
        self._cancel_event.clear()

    def process(self, query: str) -> Response:
        return self.process_with_options(query, ChatOptions(explicit=False))

    def process_with_options(self, query: str, opts: ChatOptions) -> Response:
        self._reset_cancel()
        resp = Response(query=query)
        self.stm.add("user", query)
        self.inf.save_chat_history("user", query)

        extracted = self.preference.update_from_messages([{ "role": "user", "content": query }])
        if extracted:
            resp.extracted_info = extracted
        self._async_update_memory(query)

        mem_prefix = self._build_memory_system_prefix(query)
        hist_msgs = self._build_history_messages(query)

        if self._cancel_event.is_set():
            resp.interrupted = True
            resp.answer = "[已中断] 请求在开始前被取消"
            return resp

        if opts.explicit:
            if opts.selected_tools:
                filtered = self._filter_tools(opts.selected_tools)
                if self._need_react_from_tools(filtered):
                    resp.mode = "react"
                    resp.answer, resp.steps, resp.task = self._run_react_with_tools(query, filtered, mem_prefix, hist_msgs)
                else:
                    resp.mode = "tool"
                    resp.answer, resp.tool_call = self._run_tool_from_set(query, filtered, mem_prefix, hist_msgs)
            elif opts.use_rag and self.rag.loaded:
                resp.mode = "rag"
                resp.answer, resp.search_results = self.rag.query(query)
            else:
                resp.mode = "chat"
                resp.answer = self._chat_response(mem_prefix, hist_msgs)
        else:
            if self._need_react(query):
                resp.mode = "react"
                resp.answer, resp.steps, resp.task = self._run_react_with_tools(query, self.tool_executor._tool_map, mem_prefix, hist_msgs)
            elif self._need_tool(query):
                resp.mode = "tool"
                resp.answer, resp.tool_call = self._run_tool_from_set(query, self.tool_executor._tool_map, mem_prefix, hist_msgs)
            elif self._need_rag(query):
                resp.mode = "rag"
                resp.answer, resp.search_results = self.rag.query(query)
            else:
                resp.mode = "chat"
                resp.answer = self._chat_response(mem_prefix, hist_msgs)

        if resp.tool_call and not getattr(resp.tool_call, "tool_name", None):
            resp.tool_call.tool_name = self._detect_tool(query, self.tool_executor._tool_map) or ""
            resp.tool_call.tool_result = resp.tool_call.content
            resp.tool_call.params = self._parse_tool_params(resp.tool_call.tool_name, query)

        if self._cancel_event.is_set():
            resp.interrupted = True

        self.stm.add("assistant", resp.answer)
        self.inf.save_chat_history("assistant", resp.answer)
        threading.Thread(target=self._extract_memory_from_reply, args=(resp.answer,), daemon=True).start()
        threading.Thread(target=self._maybe_consolidate_memory, daemon=True).start()

        self.inf.publish_event("agent.chat", json.dumps({"query": query, "mode": resp.mode}, ensure_ascii=False))
        resp.short_term_count = self.stm.count()
        resp.long_term_count = len(self.ltm.items)
        resp.preferences = self.preference.get_all()
        return resp

    def route(self, user_input: str, use_rag: bool = False) -> str:
        return self.process_with_options(user_input, ChatOptions(use_rag=use_rag, explicit=False)).answer

    def get_tools(self) -> List[Dict[str, Any]]:
        return self.tool_executor.get_tool_descriptions()

    def add_tool(self, tool: Tool):
        self.tool_executor.add_tool(tool)

    def register_mcp_tool(self, name: str, description: str, params: List[Dict[str, str]], func):
        self.add_tool(new_mcp_tool(name, description, params, func))

    def rag_ingest(self, document: str) -> int:
        return self.rag.ingest(document)

    def rag_query(self, question: str) -> tuple:
        return self.rag.query(question)

    def _build_memory_system_prefix(self, query: str = "") -> str:
        parts = []
        prefs = self.preference.get_all()
        if prefs:
            parts.append(f"用户偏好: {json.dumps(prefs, ensure_ascii=False)}")
        memories = self.ltm.recall(query, self.cfg.long_term_top_k) if query else []
        if memories:
            parts.append("相关记忆:\n" + "\n".join(f"- {m.content}" for m in memories))
        return "\n".join(parts)

    def _build_history_messages(self, query: str) -> List[Message]:
        msgs = [Message(role=m["role"], content=m["content"]) for m in self.stm.get()]
        if not msgs or msgs[-1].content != query:
            msgs.append(Message(role="user", content=query))
        return msgs

    def _chat_response(self, mem_prefix: str, hist_msgs: List[Message]) -> str:
        system_prompt = "你是一个简洁的AI助手。结合你掌握的用户信息，使回答更个性化。"
        if mem_prefix:
            system_prompt = mem_prefix + "\n\n" + system_prompt
        return self.llm.chat([Message(role="system", content=system_prompt)] + hist_msgs)

    def _need_tool(self, query: str) -> bool:
        q = query.lower()
        return any(k in q for k in ["几点", "时间", "天气", "查", "搜索", "是什么"])

    def _need_rag(self, query: str) -> bool:
        return self.rag.loaded and not self._need_tool(query) and not self._need_react(query)

    def _need_react(self, query: str) -> bool:
        q = query.lower()
        count = 0
        for k in ["时间", "几点", "天气", "总结", "汇总", "查", "搜索"]:
            if k in q:
                count += 1
        return count >= 2

    def _need_react_from_tools(self, tools_map: Dict[str, Tool]) -> bool:
        return len(tools_map) > 0

    def _filter_tools(self, names: List[str]) -> Dict[str, Tool]:
        result: Dict[str, Tool] = {}
        for name in names:
            if name in self.tool_executor._tool_map:
                result[name] = self.tool_executor._tool_map[name]
        return result

    def _parse_tool_params(self, tool_name: str, user_input: str) -> Dict[str, str]:
        params: Dict[str, str] = {}
        if tool_name == "get_weather":
            match = re.search(r"(天气|温度)\s*(\S+)", user_input)
            if match:
                params["city"] = match.group(2)
        elif tool_name in {"search_web", "rag_search"}:
            match = re.search(r"(搜索|查找|知识)\s*(.*)", user_input)
            if match:
                params["query"] = match.group(2)
        return params

    def _run_tool_from_set(self, query: str, tools_map: Dict[str, Tool], mem_prefix: str, hist_msgs: List[Message]):
        tool_name = self._detect_tool(query, tools_map)
        if not tool_name:
            tool_name = self._guess_tool(query, tools_map)
        if not tool_name:
            return self._chat_response(mem_prefix, hist_msgs), None
        params = self._parse_tool_params(tool_name, query)
        result = self.tool_executor.call(tool_name, params)
        answer = result.content if result.success else f"工具调用失败: {result.error}"
        tool_call = {
            "tool_name": tool_name,
            "params": params,
            "tool_result": result.content,
            "success": result.success,
            "error": result.error,
        }
        return answer, tool_call

    def _detect_tool(self, query: str, tools_map: Dict[str, Tool]) -> Optional[str]:
        q = query.lower()
        triggers = [
            ("时间", "get_time"),
            ("几点", "get_time"),
            ("天气", "get_weather"),
            ("搜索", "search_web"),
            ("查找", "search_web"),
            ("知识", "rag_search"),
            ("文档", "rag_search"),
        ]
        for trigger, tool_name in triggers:
            if trigger in q and tool_name in tools_map:
                return tool_name
        return None

    def _guess_tool(self, query: str, tools_map: Dict[str, Tool]) -> Optional[str]:
        for tool_name in tools_map:
            if tool_name in {"get_time", "get_weather", "search_web", "rag_search"}:
                return tool_name
        return None

    def _run_react_with_tools(self, query: str, tools_map: Dict[str, Tool], mem_prefix: str, hist_msgs: List[Message]):
        steps: List[ReActStep] = []
        task = {"task_id": f"task_{int(time.time())}", "query": query, "status": "running", "steps": []}
        for _ in range(self.max_iterations):
            if self._cancel_event.is_set():
                task["status"] = "interrupted"
                return "[已中断]", steps, task
            thought = self._generate_thought(query, steps, mem_prefix)
            steps.append(ReActStep(type=StepType.THOUGHT, content=thought))
            if self._is_complete(thought):
                final_answer = self._generate_final_answer(query, steps, mem_prefix)
                steps.append(ReActStep(type=StepType.FINAL_ANSWER, content=final_answer))
                break
            action, tool_name, params = self._parse_action(thought)
            if not tool_name:
                final_answer = self._generate_final_answer(query, steps, mem_prefix)
                steps.append(ReActStep(type=StepType.FINAL_ANSWER, content=final_answer))
                break
            if tool_name not in tools_map:
                steps.append(ReActStep(type=StepType.OBSERVATION, content=f"工具 {tool_name} 不可用"))
                break
            result = self.tool_executor.call(tool_name, params)
            steps.append(ReActStep(type=StepType.ACTION, content=action, tool=tool_name, params=params))
            steps.append(ReActStep(type=StepType.OBSERVATION, content=result.content if result.success else f"失败: {result.error}"))
            self._save_snapshot(task["task_id"], steps)
        answer = self._format_react_response(steps)
        task["status"] = "completed"
        task["steps"] = [{"type": s.type, "content": s.content, "tool": s.tool, "params": s.params} for s in steps]
        return answer, steps, task

    def _generate_thought(self, query: str, steps: List[ReActStep], mem_prefix: str) -> str:
        steps_str = "\n".join(f"{s.type}: {s.content}" for s in steps)
        prompt = f"""你是一个推理助手。基于以下对话历史和当前任务，给出下一步思考。

任务: {query}

记忆上下文:
{mem_prefix}

历史步骤:
{steps_str}

请输出你的思考，格式为: 思考内容
"""
        messages = [Message(role="system", content="你是一个擅长推理的助手，能够分析问题并制定执行计划。"), Message(role="user", content=prompt)]
        return self.llm.chat(messages)

    def _is_complete(self, thought: str) -> bool:
        return any(k in thought for k in ["完成", "结束", "总结", "答案是"])

    def _parse_action(self, thought: str):
        match = re.search(r"(get_time|get_weather|search_web|rag_search)\s*\((.*?)\)", thought)
        if not match:
            return "", "", {}
        tool_name = match.group(1)
        params: Dict[str, str] = {}
        for pair in match.group(2).split(","):
            if "=" in pair:
                k, v = pair.split("=", 1)
                params[k.strip()] = v.strip()
        return f"调用工具 {tool_name}", tool_name, params

    def _generate_final_answer(self, query: str, steps: List[ReActStep], mem_prefix: str) -> str:
        steps_str = "\n".join(f"{s.type}: {s.content}" for s in steps)
        prompt = f"""基于以下推理过程，给出最终答案。

任务: {query}

记忆上下文:
{mem_prefix}

推理过程:
{steps_str}

请用自然语言总结最终答案。
"""
        messages = [Message(role="system", content="你是一个总结助手，能够基于推理过程给出简洁的最终答案。"), Message(role="user", content=prompt)]
        return self.llm.chat(messages)

    def _format_react_response(self, steps: List[ReActStep]) -> str:
        lines = []
        for s in steps:
            if s.type == StepType.THOUGHT:
                lines.append(f"💭 {s.content}")
            elif s.type == StepType.ACTION:
                lines.append(f"⚡ {s.tool}({s.params})")
            elif s.type == StepType.OBSERVATION:
                lines.append(f"👁 {s.content}")
            elif s.type == StepType.FINAL_ANSWER:
                lines.append(f"\n📝 最终答案:\n{s.content}")
        return "\n".join(lines)

    def _save_snapshot(self, task_id: str, steps: List[ReActStep]):
        snapshot = {
            "task_id": task_id,
            "steps": [{"type": s.type, "content": s.content, "tool": s.tool, "params": s.params} for s in steps],
            "timestamp": time.time(),
        }
        self.inf.save_snapshot(task_id, json.dumps(snapshot, ensure_ascii=False))

    def _async_update_memory(self, user_input: str):
        def update():
            self.ltm.add(user_input)
            self.preference.update_from_messages([{ "role": "user", "content": user_input }])
            self.ltm.consolidate()
        threading.Thread(target=update, daemon=True).start()

    def _extract_memory_from_reply(self, reply: str):
        if not reply:
            return
        return

    def _maybe_consolidate_memory(self):
        trigger = getattr(self.cfg, "memory_consolidation_trigger", 5)
        if len(self.ltm.items) >= trigger:
            self.ltm.consolidate()
