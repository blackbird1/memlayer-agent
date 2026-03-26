import json
import os
from dataclasses import dataclass, field
from typing import Any

import redis.asyncio as aioredis
import structlog
from google import genai
from google.genai import types as genai_types

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


@dataclass
class ChatStep:
    type: str  # "tool_call" or "tool_result"
    name: str = ""
    args: dict[str, Any] = field(default_factory=dict)
    result: str = ""


def _tool_result_payload(result: str) -> dict[str, Any]:
    try:
        parsed = json.loads(result)
        if isinstance(parsed, dict):
            return parsed
    except (json.JSONDecodeError, ValueError):
        pass
    return {"result": result}


def _normalize_tool_name(name: str) -> str:
    name = name.strip()
    if ":" in name:
        return name.split(":")[-1]
    return name


async def handle_chat(
    session_id: str,
    message: str,
    redis_client: aioredis.Redis,
    mcp_manager: MCPServerManager,
) -> tuple[list[ChatStep], str]:
    api_key = os.environ.get("GEMINI_API_KEY") or os.environ.get("GOOGLE_API_KEY", "")
    if not api_key:
        raise ValueError("GEMINI_API_KEY or GOOGLE_API_KEY is required")

    model_name = os.environ.get("MODEL", "gemini-2.0-flash").strip() or "gemini-2.0-flash"

    client = genai.Client(api_key=api_key)

    func_decls: list[genai_types.FunctionDeclaration] = []
    tool_executors: dict[str, Any] = {}

    # Register MCP tools
    try:
        mcp_decls, mcp_executors = await mcp_manager.list_all_tools_async()
        func_decls.extend(mcp_decls)
        tool_executors.update(mcp_executors)
        logger.info("MCP tools registered", session_id=session_id, count=len(mcp_decls))
    except Exception as exc:
        logger.error("Failed to list MCP tools", session_id=session_id, error=str(exc))

    # Register Finnhub example tools when FINNHUB_API_KEY is set.
    # See finnhub_tools.py for the implementation and as a pattern for adding
    # your own local tool integrations.
    if os.environ.get("FINNHUB_API_KEY"):
        from .finnhub_tools import TOOL_DECLARATIONS as FINNHUB_DECLS, TOOL_EXECUTORS as FINNHUB_EXECUTORS
        func_decls.extend(FINNHUB_DECLS)
        tool_executors.update(FINNHUB_EXECUTORS)
        logger.info("Finnhub example tools registered", session_id=session_id, count=len(FINNHUB_DECLS))

    config = genai_types.GenerateContentConfig(
        system_instruction=ASSISTANT_PROMPT,
        tools=[genai_types.Tool(function_declarations=func_decls)] if func_decls else None,
    )

    history = await load_history(redis_client, session_id)
    logger.info("Chat history loaded", session_id=session_id, history_items=len(history))

    chat = client.aio.chats.create(model=model_name, config=config, history=history)
    resp = await chat.send_message(message)

    steps, full_response = await _process_response(resp, chat, tool_executors)

    await save_history(redis_client, session_id, chat.get_history())
    logger.info(
        "handleChat completed",
        session_id=session_id,
        steps=len(steps),
        response_chars=len(full_response.strip()),
    )
    return steps, full_response


async def _process_response(
    resp: genai_types.GenerateContentResponse,
    chat: Any,
    tool_executors: dict[str, Any],
) -> tuple[list[ChatStep], str]:
    steps: list[ChatStep] = []
    full_response_parts: list[str] = []
    tool_round_trips = 0

    while True:
        next_resp, handled = await _process_model_turn(
            resp, chat, tool_executors, steps, full_response_parts
        )
        if not handled:
            break
        tool_round_trips += 1
        resp = next_resp

    logger.info("processResponse completed", steps=len(steps), tool_round_trips=tool_round_trips)
    return steps, "".join(full_response_parts)


async def _process_model_turn(
    resp: genai_types.GenerateContentResponse,
    chat: Any,
    tool_executors: dict[str, Any],
    steps: list[ChatStep],
    full_response_parts: list[str],
) -> tuple[Any, bool]:
    for candidate in resp.candidates or []:
        if not candidate.content:
            continue
        for part in candidate.content.parts or []:
            if part.text:
                full_response_parts.append(part.text)
            if part.function_call:
                next_resp = await _handle_function_call(
                    part.function_call, chat, tool_executors, steps
                )
                return next_resp, True
    return None, False


async def _handle_function_call(
    fc: genai_types.FunctionCall,
    chat: Any,
    tool_executors: dict[str, Any],
    steps: list[ChatStep],
) -> genai_types.GenerateContentResponse:
    tool_name = _normalize_tool_name(fc.name)
    tool_args = dict(fc.args or {})
    logger.info("Tool call received", tool=fc.name, tool_args=tool_args)

    steps.append(ChatStep(type="tool_call", name=tool_name, args=tool_args))

    executor = tool_executors.get(tool_name)
    if executor is None:
        error_msg = f"tool {tool_name!r} is not available"
        steps.append(ChatStep(type="tool_result", name=tool_name, result=f"Error: {error_msg}"))
        return await _send_tool_response(chat, fc.name, {"error": error_msg})

    try:
        result = await executor(tool_args)
        logger.info("Tool executed successfully", tool=tool_name)
        steps.append(ChatStep(type="tool_result", name=tool_name, result=result))
        return await _send_tool_response(chat, fc.name, _tool_result_payload(result))
    except Exception as exc:
        logger.error("Tool execution failed", tool=tool_name, error=str(exc))
        steps.append(ChatStep(type="tool_result", name=tool_name, result=f"Error: {exc}"))
        return await _send_tool_response(chat, fc.name, {"error": str(exc)})


async def _send_tool_response(
    chat: Any,
    function_name: str,
    payload: dict[str, Any],
) -> genai_types.GenerateContentResponse:
    return await chat.send_message(
        genai_types.Part(
            function_response=genai_types.FunctionResponse(
                name=function_name,
                response=payload,
            )
        )
    )
