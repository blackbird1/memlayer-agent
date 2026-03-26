import json
import os
from dataclasses import dataclass, field
from typing import Any

import redis.asyncio as aioredis
import structlog
from openai import AsyncOpenAI

from .mcp_client import MCPServerManager
from .session import load_history, save_history

logger = structlog.get_logger(__name__)

ASSISTANT_PROMPT = """You are a concise assistant augmented with a self-learning memory layer via ProcIQ MCP tools.

## Memory Cycle (Retrieve -> Act -> Log)

For every non-trivial task (coding, debugging, research, architecture):

1. RETRIEVE first: call prociq_retrieve_context with a clear task description before acting.
   - If the result contains Skills or Patterns, treat those as mandatory procedural guidance.
   - After the first retrieval, call prociq_list_scopes to resolve the default scope for this session.
   - If only one scope is available, use it. If multiple, pick the most relevant or ask the user once.

2. ACT: perform the task informed by retrieved context.
   - On any error: stop, call prociq_log_episode with outcome=failure, then call prociq_retrieve_context
     describing the error before retrying.
   - For static facts worth preserving, call prociq_log_note.

3. LOG: you MUST call prociq_log_episode as a tool call BEFORE giving your final text response.
   - This is a required tool call, not optional. Do not skip it.
   - Required fields: task_goal, approach_taken, outcome (success/partial/failure), scope.
   - Skip only for trivial or purely conversational exchanges (e.g. "hello", "thanks").

## General Behaviour
Use available MCP tools when they help answer the user's question.
Keep responses short, structured, and grounded in tool results when tools are used.
Always produce a final text response to the user after completing tool calls — never end a turn with only tool calls and no message."""


def _resolve_client() -> tuple[AsyncOpenAI, str]:
    openai_key = os.environ.get("OPENAI_API_KEY", "").strip()
    google_key = (os.environ.get("GOOGLE_API_KEY") or os.environ.get("GEMINI_API_KEY") or "").strip()
    base_url = os.environ.get("OPENAI_BASE_URL", "").strip()
    model = os.environ.get("MODEL", "").strip()

    if openai_key:
        api_key = openai_key
        if not model:
            model = "gpt-4o-mini"
    elif google_key:
        api_key = google_key
        if not base_url:
            base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
        if not model:
            model = "gemini-2.0-flash"
    else:
        raise ValueError("OPENAI_API_KEY or GOOGLE_API_KEY is required")

    kwargs: dict[str, Any] = {"api_key": api_key}
    if base_url:
        kwargs["base_url"] = base_url
    return AsyncOpenAI(**kwargs), model


def _normalize_tool_name(name: str) -> str:
    name = name.strip()
    if ":" in name:
        return name.split(":")[-1]
    return name


@dataclass
class ChatStep:
    type: str  # "tool_call" or "tool_result"
    name: str = ""
    args: dict[str, Any] = field(default_factory=dict)
    result: str = ""


async def handle_chat(
    session_id: str,
    message: str,
    redis_client: aioredis.Redis,
    mcp_manager: MCPServerManager,
) -> tuple[list[ChatStep], str]:
    client, model = _resolve_client()

    tools: list[dict] = []
    tool_executors: dict[str, Any] = {}

    # Register MCP tools
    try:
        mcp_tools, mcp_executors = await mcp_manager.list_all_tools_async()
        tools.extend(mcp_tools)
        tool_executors.update(mcp_executors)
        logger.info("MCP tools registered", session_id=session_id, count=len(mcp_tools))
    except Exception as exc:
        logger.error("Failed to list MCP tools", session_id=session_id, error=str(exc))

    # Register Finnhub example tools when FINNHUB_API_KEY is set.
    # See finnhub_tools.py for the implementation and as a pattern for adding
    # your own local tool integrations.
    if os.environ.get("FINNHUB_API_KEY"):
        from .finnhub_tools import TOOL_DECLARATIONS as FINNHUB_DECLS, TOOL_EXECUTORS as FINNHUB_EXECUTORS
        tools.extend(FINNHUB_DECLS)
        tool_executors.update(FINNHUB_EXECUTORS)
        logger.info("Finnhub example tools registered", session_id=session_id, count=len(FINNHUB_DECLS))

    history = await load_history(redis_client, session_id)
    logger.info("Chat history loaded", session_id=session_id, history_items=len(history))

    messages: list[dict] = [{"role": "system", "content": ASSISTANT_PROMPT}]
    messages.extend(history)
    messages.append({"role": "user", "content": message})

    steps: list[ChatStep] = []
    final_text = ""

    # Tool call loop
    while True:
        kwargs: dict[str, Any] = {"model": model, "messages": messages}
        if tools:
            kwargs["tools"] = tools

        resp = await client.chat.completions.create(**kwargs)
        if not resp.choices:
            break

        choice = resp.choices[0]
        msg = choice.message
        messages.append(msg.model_dump(exclude_unset=False, exclude_none=True))

        if not msg.tool_calls:
            final_text = msg.content or ""
            break

        for tc in msg.tool_calls:
            tool_name = _normalize_tool_name(tc.function.name)
            try:
                args = json.loads(tc.function.arguments)
            except (json.JSONDecodeError, ValueError):
                args = {}

            logger.info("Tool call received", tool=tool_name, args=args)
            steps.append(ChatStep(type="tool_call", name=tool_name, args=args))

            executor = tool_executors.get(tool_name)
            if executor is None:
                error_msg = f"tool {tool_name!r} is not available"
                result_str = json.dumps({"error": error_msg})
                steps.append(ChatStep(type="tool_result", name=tool_name, result=f"Error: {error_msg}"))
            else:
                try:
                    result_str = await executor(args)
                    logger.info("Tool executed successfully", tool=tool_name)
                    steps.append(ChatStep(type="tool_result", name=tool_name, result=result_str))
                except Exception as exc:
                    logger.error("Tool execution failed", tool=tool_name, error=str(exc))
                    result_str = json.dumps({"error": str(exc)})
                    steps.append(ChatStep(type="tool_result", name=tool_name, result=f"Error: {exc}"))

            messages.append({
                "role": "tool",
                "tool_call_id": tc.id,
                "content": result_str,
            })

    # Save history (skip system message)
    await save_history(redis_client, session_id, messages[1:])
    logger.info(
        "handleChat completed",
        session_id=session_id,
        steps=len(steps),
        response_chars=len(final_text.strip()),
    )
    return steps, final_text
