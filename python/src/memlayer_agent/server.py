import os
from contextlib import asynccontextmanager
from typing import Any

import redis.asyncio as aioredis
import structlog
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

from .chat import handle_chat, ChatStep
from .mcp_client import MCPServerManager

logger = structlog.get_logger(__name__)


class ChatRequest(BaseModel):
    message: str
    sessionId: str = "default"


class ChatResponse(BaseModel):
    response: str = ""
    steps: list[dict[str, Any]] = []
    error: str = ""


_redis_client: aioredis.Redis | None = None
_mcp_manager: MCPServerManager | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _redis_client, _mcp_manager

    redis_addr = os.environ.get("REDIS_ADDR", "localhost:6379")
    _redis_client = aioredis.from_url(f"redis://{redis_addr}")
    try:
        await _redis_client.ping()
        logger.info("Redis connected", addr=redis_addr)
    except Exception as exc:
        logger.error("Failed to connect to Redis", error=str(exc))

    _mcp_manager = MCPServerManager()
    try:
        await _mcp_manager.load_and_connect()
    except Exception as exc:
        logger.error("Failed to initialize MCP manager", error=str(exc))

    yield

    if _mcp_manager:
        await _mcp_manager.close()
    if _redis_client:
        await _redis_client.aclose()


app = FastAPI(lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["POST", "OPTIONS"],
    allow_headers=["Content-Type"],
)


@app.post("/api/chat", response_model=ChatResponse)
async def chat(req: ChatRequest) -> ChatResponse:
    session_id = req.sessionId or "default"
    logger.info("Received chat request", session_id=session_id)

    try:
        steps, response = await handle_chat(
            session_id=session_id,
            message=req.message,
            redis_client=_redis_client,
            mcp_manager=_mcp_manager,
        )
        return ChatResponse(
            response=response,
            steps=[
                {
                    "type": s.type,
                    "name": s.name,
                    "args": s.args,
                    "result": s.result,
                }
                for s in steps
            ],
        )
    except Exception as exc:
        logger.error("Error handling chat", session_id=session_id, error=str(exc))
        return ChatResponse(error=str(exc))
