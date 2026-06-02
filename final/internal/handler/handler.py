# handler — HTTP API 路由处理
import logging
from io import BytesIO
from typing import Any, Dict, List

from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.staticfiles import StaticFiles

from config.config import APIConfig
from internal.agent.agent import ChatOptions, Response, UnifiedAgent
from internal.infra.infra import Infrastructure

logger = logging.getLogger(__name__)

try:
    from PyPDF2 import PdfReader
    _HAS_PDF = True
except ImportError:
    _HAS_PDF = False
    logger.warning("⚠️  PyPDF2 未安装，PDF 解析不可用")


def extract_text_from_file(file: UploadFile, content: bytes) -> str:
    filename = file.filename or ""
    if filename.lower().endswith(".pdf"):
        if not _HAS_PDF:
            raise HTTPException(status_code=500, detail="PyPDF2 未安装，无法解析 PDF")
        try:
            reader = PdfReader(BytesIO(content))
            text = ""
            for page in reader.pages:
                text += page.extract_text() or ""
            return text
        except Exception as e:
            logger.error("PDF 解析失败: %s", e)
            raise HTTPException(status_code=500, detail=f"PDF 解析失败: {str(e)}")
    try:
        return content.decode("utf-8")
    except UnicodeDecodeError:
        return content.decode("gbk", errors="ignore")


def _response_to_dict(resp: Response) -> Dict[str, Any]:
    return {
        "query": resp.query,
        "answer": resp.answer,
        "mode": resp.mode,
        "steps": [
            {
                "type": s.type,
                "content": s.content,
                "tool": s.tool,
                "params": s.params,
            }
            for s in resp.steps
        ],
        "tool_call": resp.tool_call,
        "search_results": resp.search_results,
        "task": resp.task,
        "extracted_info": resp.extracted_info,
        "short_term_count": resp.short_term_count,
        "long_term_count": resp.long_term_count,
        "preferences": resp.preferences,
        "interrupted": resp.interrupted,
        "success": True,
    }


def setup_routes(agent: UnifiedAgent, inf: Infrastructure, cfg: APIConfig) -> FastAPI:
    app = FastAPI(title="AGI Assistant", version="1.0")

    @app.post("/api/chat")
    async def chat(request: Dict[str, Any]):
        try:
            message = request.get("message", "")
            if not message:
                raise HTTPException(status_code=400, detail="缺少 message 参数")

            opts = ChatOptions(
                use_rag=bool(request.get("use_rag", False)),
                selected_tools=request.get("selected_tools"),
                explicit=bool(request.get("explicit", False)),
            )
            resp = agent.process_with_options(message, opts)
            return _response_to_dict(resp)
        except HTTPException:
            raise
        except Exception as e:
            logger.error("聊天接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    @app.post("/api/upload")
    async def upload(file: UploadFile = File(...)):
        try:
            content = await file.read()
            text = extract_text_from_file(file, content)
            if not text.strip():
                return {"chunk_count": 0, "success": False, "message": "文件内容为空"}
            chunk_count = agent.rag_ingest(text)
            return {"chunk_count": chunk_count, "success": True}
        except HTTPException:
            raise
        except Exception as e:
            logger.error("上传接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    @app.post("/api/rag/query")
    async def rag_query(request: Dict[str, Any]):
        try:
            question = request.get("question", "")
            if not question:
                raise HTTPException(status_code=400, detail="缺少 question 参数")
            answer, results = agent.rag_query(question)
            return {
                "answer": answer,
                "results": [
                    {"content": r["content"], "score": r["score"], "source": r.get("source", "unknown")}
                    for r in results
                ],
                "success": True,
            }
        except HTTPException:
            raise
        except Exception as e:
            logger.error("RAG 查询接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    @app.post("/api/tools/mcp")
    async def register_mcp_tool(request: Dict[str, Any]):
        try:
            name = request.get("name", "").strip()
            description = request.get("description", "").strip()
            endpoint = request.get("endpoint", "").strip()
            params = request.get("params", []) or []
            if not name or not endpoint:
                raise HTTPException(status_code=400, detail="缺少 name 或 endpoint 参数")

            def _mcp_func(args: Dict[str, str]) -> str:
                return f"MCP 工具 {name} 已注册，端点: {endpoint}，参数: {args}"

            agent.register_mcp_tool(name, description, params, _mcp_func)
            return {"success": True, "ok": True}
        except HTTPException:
            raise
        except Exception as e:
            logger.error("注册 MCP 工具失败: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    @app.get("/api/status")
    async def status():
        return {
            "status": "running",
            "milvus": inf.ready.milvus,
            "postgresql": inf.ready.postgresql,
            "elasticsearch": inf.ready.elasticsearch,
            "kafka": inf.ready.kafka,
            "llm": cfg.llm_model,
            "embedding": cfg.embedding_model,
        }

    @app.get("/api/tools")
    async def tools():
        return {"tools": agent.get_tools()}

    @app.get("/api/memory")
    async def memory():
        return {
            "short_term_turns": agent.stm.count(),
            "long_term_count": len(agent.ltm.items),
            "preferences": agent.preference.get_all(),
        }

    app.mount("/", StaticFiles(directory="frontend", html=True), name="frontend")
    return app
