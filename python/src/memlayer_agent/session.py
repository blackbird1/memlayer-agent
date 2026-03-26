import json
from typing import Optional

import redis.asyncio as aioredis
import structlog
from google.genai import types as genai_types

logger = structlog.get_logger(__name__)

SESSION_TTL = 30 * 60  # 30 minutes in seconds


def _content_to_dict(content: genai_types.Content) -> dict:
    parts = []
    for part in content.parts or []:
        if part.text:
            parts.append({"type": "text", "text": part.text})
        elif part.function_call:
            parts.append({
                "type": "function_call",
                "function_call": {
                    "name": part.function_call.name,
                    "args": dict(part.function_call.args or {}),
                },
            })
        elif part.function_response:
            parts.append({
                "type": "function_response",
                "function_response": {
                    "name": part.function_response.name,
                    "response": dict(part.function_response.response or {}),
                },
            })
    return {"role": content.role or "user", "parts": parts}


def _dict_to_content(d: dict) -> genai_types.Content:
    parts = []
    for p in d.get("parts", []):
        t = p.get("type")
        if t == "text":
            parts.append(genai_types.Part(text=p["text"]))
        elif t == "function_call" and p.get("function_call"):
            fc = p["function_call"]
            parts.append(genai_types.Part(
                function_call=genai_types.FunctionCall(
                    name=fc["name"],
                    args=fc.get("args", {}),
                )
            ))
        elif t == "function_response" and p.get("function_response"):
            fr = p["function_response"]
            parts.append(genai_types.Part(
                function_response=genai_types.FunctionResponse(
                    name=fr["name"],
                    response=fr.get("response", {}),
                )
            ))
    return genai_types.Content(role=d.get("role", "user"), parts=parts)


async def load_history(
    redis_client: aioredis.Redis, session_id: str
) -> list[genai_types.Content]:
    try:
        data = await redis_client.get(f"session:{session_id}")
        if data is None:
            return []
        items = json.loads(data)
        history = [_dict_to_content(item) for item in items]
        logger.info("history loaded", session_id=session_id, items=len(history))
        return history
    except Exception as exc:
        logger.error("failed to load history", session_id=session_id, error=str(exc))
        return []


async def save_history(
    redis_client: aioredis.Redis,
    session_id: str,
    history: list[genai_types.Content],
) -> None:
    try:
        data = json.dumps([_content_to_dict(c) for c in history])
        await redis_client.set(f"session:{session_id}", data, ex=SESSION_TTL)
        logger.info("history saved", session_id=session_id, items=len(history))
    except Exception as exc:
        logger.error("failed to save history", session_id=session_id, error=str(exc))
