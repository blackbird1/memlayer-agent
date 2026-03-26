import json

import redis.asyncio as aioredis
import structlog

logger = structlog.get_logger(__name__)

SESSION_TTL = 30 * 60  # 30 minutes in seconds


async def load_history(
    redis_client: aioredis.Redis, session_id: str
) -> list[dict]:
    try:
        data = await redis_client.get(f"session:{session_id}")
        if data is None:
            return []
        history = json.loads(data)
        logger.info("history loaded", session_id=session_id, items=len(history))
        return history
    except Exception as exc:
        logger.warning("failed to load history, starting fresh", session_id=session_id, error=str(exc))
        return []


async def save_history(
    redis_client: aioredis.Redis,
    session_id: str,
    history: list[dict],
) -> None:
    try:
        data = json.dumps(history)
        await redis_client.set(f"session:{session_id}", data, ex=SESSION_TTL)
        logger.info("history saved", session_id=session_id, items=len(history))
    except Exception as exc:
        logger.error("failed to save history", session_id=session_id, error=str(exc))
