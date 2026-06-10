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
from internal.tools.tools import Tool, ToolExecutor, default_tools, new_mcp_tool

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


# 工具关键字触发表（与 main 分支保持一致）
_TOOL_TRIGGERS = [
    ("时间", "get_time"),
    ("几点", "get_time"),
    ("现在", "get_time"),
    ("天气", "get_weather"),
    ("搜索", "search_web"),
    ("查找", "search_web"),
    ("知识", "rag_search"),
    ("文档", "rag_search"),
]


class UnifiedAgent:
    def __init__(self, cfg: APIConfig, inf: Infrastructure):
        self.cfg = cfg
        self.inf = inf
        self.llm = LLMClient(cfg)
        self.stm = ShortTerm(cfg.short_term_max_turns)
        self.ltm = LongTerm(cfg, inf)
        self.preference = Preference("default_user", inf)
        self.rag = RAGEngine(cfg, inf, self.llm)
        self.tool_executor = ToolExecutor(default_tools())
        self.max_iterations = cfg.max_iterations
        self.max_retries = cfg.max_retries
        self._cancel_event = threading.Event()
        self._memory_lock = threading.Lock()

        self.ltm.set_embed_fn(self.llm.embed)
        self.rag.set_generate_fn(self._llm_generate)
        self.ltm.load_from_storage()

        logger.info("✅ UnifiedAgent 初始化完成")

    def _llm_generate(self, system_prompt: str, user_msg: str) -> str:
        return self.llm.chat([Message(role="user", content=user_msg)], system_prompt=system_prompt)

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

        # 偏好提取统一在异步线程做（避免主线程阻塞 + 重复提取）
        self._async_update_memory(query, resp)

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

        if self._cancel_event.is_set():
            resp.interrupted = True

        self.stm.add("assistant", resp.answer)
        self.inf.save_chat_history("assistant", resp.answer)
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

    # ── Memory / Prompt 拼装 ────────────────────────────────────────────────

    def _build_memory_system_prefix(self, query: str = "") -> str:
        parts: List[str] = []
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
        return self.llm.chat(hist_msgs, system_prompt=system_prompt)

    # ── 路由判断 ────────────────────────────────────────────────────────────

    def _need_tool(self, query: str) -> bool:
        q = query.lower()
        return any(trigger in q for trigger, _ in _TOOL_TRIGGERS)

    def _need_rag(self, query: str) -> bool:
        return self.rag.loaded and not self._need_tool(query) and not self._need_react(query)

    def _need_react(self, query: str) -> bool:
        q = query.lower()
        seen = set()
        for trigger, tool_name in _TOOL_TRIGGERS:
            if trigger in q:
                seen.add(tool_name)
        # 命中两类及以上工具或显式包含"总结/汇总"等汇总语义时进 ReAct
        return len(seen) >= 2 or any(k in q for k in ["总结", "汇总", "分析"]) and seen

    def _need_react_from_tools(self, tools_map: Dict[str, Tool]) -> bool:
        return len(tools_map) > 1

    def _filter_tools(self, names: List[str]) -> Dict[str, Tool]:
        return {n: self.tool_executor._tool_map[n] for n in names if n in self.tool_executor._tool_map}

    # ── 工具调用 ────────────────────────────────────────────────────────────

    def _parse_tool_params(self, tool_name: str, user_input: str) -> Dict[str, str]:
        params: Dict[str, str] = {}
        if tool_name == "get_weather":
            match = re.search(r"(天气|温度)\s*([^\s,，。？?!！]+)", user_input)
            if match:
                params["city"] = match.group(2)
        elif tool_name in {"search_web", "rag_search"}:
            match = re.search(r"(搜索|查找|知识|文档)\s*(.*)", user_input)
            if match and match.group(2).strip():
                params["query"] = match.group(2).strip()
            else:
                params["query"] = user_input
        return params

    def _run_tool_from_set(self, query: str, tools_map: Dict[str, Tool], mem_prefix: str, hist_msgs: List[Message]):
        tool_name = self._detect_tool(query, tools_map)
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
        for trigger, tool_name in _TOOL_TRIGGERS:
            if trigger in q and tool_name in tools_map:
                return tool_name
        return None

    # ── ReAct ───────────────────────────────────────────────────────────────

    def _run_react_with_tools(self, query: str, tools_map: Dict[str, Tool], mem_prefix: str, hist_msgs: List[Message]):
        steps: List[ReActStep] = []
        task = {"task_id": f"task_{int(time.time())}", "query": query, "status": "running", "steps": []}
        for _ in range(self.max_iterations):
            if self._cancel_event.is_set():
                task["status"] = "interrupted"
                return "[已中断]", steps, task
            thought = self._generate_thought(query, steps, mem_prefix, tools_map)
            steps.append(ReActStep(type=StepType.THOUGHT, content=thought))
            if self._is_complete(thought):
                final_answer = self._generate_final_answer(query, steps, mem_prefix)
                steps.append(ReActStep(type=StepType.FINAL_ANSWER, content=final_answer))
                break
            action, tool_name, params = self._parse_action(thought, tools_map)
            if not tool_name:
                final_answer = self._generate_final_answer(query, steps, mem_prefix)
                steps.append(ReActStep(type=StepType.FINAL_ANSWER, content=final_answer))
                break
            if tool_name not in tools_map:
                steps.append(ReActStep(type=StepType.OBSERVATION, content=f"工具 {tool_name} 不可用"))
                break
            # 工具调用带重试
            result = self._call_tool_with_retry(tool_name, params)
            steps.append(ReActStep(type=StepType.ACTION, content=action, tool=tool_name, params=params))
            steps.append(ReActStep(type=StepType.OBSERVATION, content=result.content if result.success else f"失败: {result.error}"))
            self._save_snapshot(task["task_id"], steps)
        else:
            # 达到最大迭代后强制总结
            final_answer = self._generate_final_answer(query, steps, mem_prefix)
            steps.append(ReActStep(type=StepType.FINAL_ANSWER, content=final_answer))

        answer = self._format_react_response(steps)
        task["status"] = "completed"
        task["steps"] = [{"type": s.type, "content": s.content, "tool": s.tool, "params": s.params} for s in steps]
        return answer, steps, task

    def _call_tool_with_retry(self, tool_name: str, params: Dict[str, str]):
        last = None
        for attempt in range(max(1, self.max_retries)):
            last = self.tool_executor.call(tool_name, params)
            if last.success:
                return last
            time.sleep(self.cfg.retry_delay_ms / 1000.0)
        return last

    def _generate_thought(self, query: str, steps: List[ReActStep], mem_prefix: str, tools_map: Dict[str, Tool]) -> str:
        steps_str = "\n".join(f"{s.type}: {s.content}" for s in steps) or "（暂无）"
        tools_desc = "\n".join(
            f"- {name}: {tool.description}; 参数: {[p['name'] for p in tool.params]}"
            for name, tool in tools_map.items()
        )
        prompt = f"""你是一个 ReAct 推理助手。请基于任务和已有步骤决定下一步。

可用工具：
{tools_desc}

任务: {query}

记忆上下文:
{mem_prefix or '（无）'}

历史步骤:
{steps_str}

请输出一条决策，必须严格满足以下两种格式之一：
1) 需要调用工具：    Action: tool_name(key1="value1", key2="value2")
2) 已可给出答案：    Final: <最终结论>

不要输出额外内容。
"""
        messages = [Message(role="user", content=prompt)]
        return self.llm.chat(messages, system_prompt="你是一个擅长推理的助手，按要求格式输出决策。")

    def _is_complete(self, thought: str) -> bool:
        return bool(re.search(r"^\s*Final\s*[:：]", thought, re.MULTILINE))

    def _parse_action(self, thought: str, tools_map: Dict[str, Tool]):
        # 匹配 Action: tool_name(k=v, k="v")
        m = re.search(r"Action\s*[:：]\s*([a-zA-Z_][\w]*)\s*\((.*?)\)", thought, re.DOTALL)
        if not m:
            return "", "", {}
        tool_name = m.group(1)
        if tool_name not in tools_map:
            return "", "", {}
        params: Dict[str, str] = {}
        raw = m.group(2).strip()
        if raw:
            for pair in re.split(r",(?![^\"]*\")", raw):
                if "=" not in pair:
                    continue
                k, v = pair.split("=", 1)
                v = v.strip().strip('"').strip("'")
                params[k.strip()] = v
        return f"调用工具 {tool_name}", tool_name, params

    def _generate_final_answer(self, query: str, steps: List[ReActStep], mem_prefix: str) -> str:
        steps_str = "\n".join(f"{s.type}: {s.content}" for s in steps)
        prompt = f"""基于以下推理过程，给出最终答案。

任务: {query}

记忆上下文:
{mem_prefix or '（无）'}

推理过程:
{steps_str}

请用自然语言总结最终答案，不要包含 Action/Final 等关键字。
"""
        messages = [Message(role="user", content=prompt)]
        return self.llm.chat(messages, system_prompt="你是一个总结助手，能够基于推理过程给出简洁的最终答案。")

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

    # ── 异步记忆维护 ────────────────────────────────────────────────────────

    def _async_update_memory(self, user_input: str, resp: Response):
        """单一入口：抽偏好 + 写长期记忆。结果通过 resp.extracted_info 反馈。"""
        # 同步规则提取（用于即时反馈）
        from internal.llm.llm import _extract_rule_based
        with self._memory_lock:
            quick = _extract_rule_based(user_input)
            if quick:
                self.preference.save_batch(quick)
                resp.extracted_info = "已记住：" + ", ".join(f"{k}={v}" for k, v in quick.items())

        def update():
            try:
                with self._memory_lock:
                    extracted = self.llm.extract_preferences(user_input)
                    if extracted:
                        self.preference.save_batch(extracted)
                self.ltm.add(user_input)
            except Exception as e:
                logger.warning("异步更新记忆失败: %s", e)

        threading.Thread(target=update, daemon=True).start()

    def _maybe_consolidate_memory(self):
        try:
            with self._memory_lock:
                if self.ltm.need_consolidation():
                    self.ltm.consolidate()
        except Exception as e:
            logger.warning("记忆合并失败: %s", e)
