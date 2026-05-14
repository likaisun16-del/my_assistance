# handler — HTTP API 路由处理
import json
import logging
from typing import Dict, Any

from fastapi import FastAPI, HTTPException, UploadFile, File
from fastapi.staticfiles import StaticFiles

from config.config import APIConfig
from internal.agent.agent import UnifiedAgent
from internal.infra.infra import Infrastructure

logger = logging.getLogger(__name__)


def setup_routes(agent: UnifiedAgent, inf: Infrastructure, cfg: APIConfig) -> FastAPI:
    """设置 HTTP 路由"""
    app = FastAPI(title="AGI Assistant", version="1.0")

    # ─────────────────────────────── 聊天接口 ────────────────────────────

    @app.post("/api/chat")
    async def chat(request: Dict[str, Any]):
        """聊天接口"""
        try:
            message = request.get("message", "")
            if not message:
                raise HTTPException(status_code=400, detail="缺少 message 参数")

            # 路由到 agent
            response = agent.route(message)

            return {"answer": response, "success": True}

        except Exception as e:
            logger.error("聊天接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    # ─────────────────────────────── 文档上传 ────────────────────────────

    @app.post("/api/upload")
    async def upload(file: UploadFile = File(...)):
        """上传文档到知识库"""
        try:
            content = await file.read()
            text = content.decode("utf-8")

            # 向 RAG 引擎添加文档
            chunk_count = agent.rag_ingest(text)

            return {"chunk_count": chunk_count, "success": True}

        except Exception as e:
            logger.error("上传接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    # ─────────────────────────────── RAG 查询 ────────────────────────────

    @app.post("/api/rag/query")
    async def rag_query(request: Dict[str, Any]):
        """RAG 知识库查询"""
        try:
            question = request.get("question", "")
            if not question:
                raise HTTPException(status_code=400, detail="缺少 question 参数")

            answer, results = agent.rag_query(question)

            return {
                "answer": answer,
                "results": [{"content": r.chunk.content, "similarity": r.similarity} for r in results],
                "success": True
            }

        except Exception as e:
            logger.error("RAG 查询接口错误: %s", e)
            raise HTTPException(status_code=500, detail=str(e))

    # ─────────────────────────────── 服务状态 ────────────────────────────

    @app.get("/api/status")
    async def status():
        """获取服务状态"""
        return {
            "status": "running",
            "milvus": inf.ready.milvus,
            "postgresql": inf.ready.postgresql,
            "elasticsearch": inf.ready.elasticsearch,
            "kafka": inf.ready.kafka,
            "llm": cfg.llm_model,
            "embedding": cfg.embedding_model,
        }

    # ─────────────────────────────── 工具列表 ────────────────────────────

    @app.get("/api/tools")
    async def tools():
        """获取可用工具列表"""
        return {"tools": agent.get_tools()}

    # ─────────────────────────────── 记忆系统状态 ────────────────────────

    @app.get("/api/memory")
    async def memory():
        """获取记忆系统状态"""
        return {
            "short_term_turns": agent.stm.count(),
            "long_term_count": len(agent.ltm.items),
            "preferences": agent.preference.get_all(),
        }

    # ─────────────────────────────── 挂载前端静态资源 ────────────────────

    app.mount("/", StaticFiles(directory="frontend", html=True), name="frontend")

    return app
