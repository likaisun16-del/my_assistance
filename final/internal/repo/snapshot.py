# snapshot — 任务快照仓储（Postgres 实现）。
import logging

from internal.platform.postgres import PostgresClient

logger = logging.getLogger(__name__)


class PGRepo:
    """Postgres 实现；client 不可用时降级为空操作。"""

    def __init__(self, client: PostgresClient):
        self.client = client

    # upsert 任务快照（同一 task_id 多次保存覆盖最新状态）
    def save(self, task_id: str, state_json: bytes) -> None:
        if self.client is None or not self.client.is_real():
            return
        # JSONB 字段接受 bytes / str
        state_param = state_json
        if isinstance(state_param, (bytes, bytearray)):
            try:
                state_param = bytes(state_param).decode("utf-8")
            except Exception:
                state_param = "{}"
        try:
            self.client.exec(
                "INSERT INTO task_snapshots (task_id, state) VALUES (%s, %s) "
                "ON CONFLICT (task_id) DO UPDATE SET state = %s, created_at = NOW()",
                (task_id, state_param, state_param),
            )
        except Exception as e:
            logger.warning("⚠️  快照保存到 PG 失败: %s", e)
