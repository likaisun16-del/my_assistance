# Final Stage — 全阶段整合 AI 助手（Python 版）
#
# 这是当前 python 分支的启动入口：
# - 读取配置
# - 初始化基础设施
# - 构建统一智能体
# - 注册 HTTP 路由
# - 启动 FastAPI 服务
import logging
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from config.config import default_config
from internal.agent.agent import UnifiedAgent
from internal.handler.handler import setup_routes
from internal.infra.infra import Infrastructure

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
)
logger = logging.getLogger(__name__)


def main():
    cfg = default_config()

    logger.info("🔧 正在连接基础设施...")
    inf = Infrastructure(cfg)

    try:
        agent = UnifiedAgent(cfg, inf)
        app = setup_routes(agent, inf, cfg)
        print_banner(cfg, inf)

        import uvicorn

        uvicorn.run(app, host="0.0.0.0", port=int(cfg.server_port))
    finally:
        inf.close()


def print_banner(cfg, inf):
    addr = f":{cfg.server_port}"
    print("========================================")
    print("Final Stage · AGI 智能助手启动成功")
    print("========================================")
    print(f"[INFO] Service       http://localhost{addr}")
    print(f"[INFO] 通用模型           {cfg.llm_model}")
    print(f"[INFO] Embedding     {cfg.embedding_model}")
    print("----------------------------------------")
    print(f"[INFO] Milvus        {inf.ready.milvus}")
    print(f"[INFO] PostgreSQL    {cfg.pg_host}:{cfg.pg_port}")
    print(f"[INFO] ElasticSearch {inf.ready.elasticsearch}")
    print(f"[INFO] Kafka         {inf.ready.kafka}")
    print("----------------------------------------")
    print("[READY] 道阻且长，行则将至。")
    print("========================================")


if __name__ == "__main__":
    main()
