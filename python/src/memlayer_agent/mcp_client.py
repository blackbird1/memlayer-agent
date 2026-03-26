import json
import os
from pathlib import Path
from typing import Any

import structlog
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamablehttp_client

logger = structlog.get_logger(__name__)

ToolExecutor = Any  # async callable (args: dict) -> str


def _resolve_bearer_token(cfg: dict) -> str:
    for key in ("bearerToken", "bearer_token"):
        val = cfg.get(key, "").strip()
        if val:
            return val
    for env_key in ("MEMLAYER_MCP_BEARER_TOKEN", "MCP_BEARER_TOKEN"):
        val = os.environ.get(env_key, "").strip()
        if val:
            return val
    return ""


def _authorization_header(token: str) -> str:
    token = token.strip()
    if not token:
        return ""
    if token.lower().startswith("bearer "):
        return token
    return f"Bearer {token}"


def _resolve_headers(cfg: dict) -> dict[str, str]:
    headers: dict[str, str] = {}
    for k, v in cfg.get("headers", {}).items():
        if v and v.strip():
            headers[k] = v
    headers["X-Accel-Buffering"] = "no"
    # Only add Authorization if not already present
    if not any(k.lower() == "authorization" for k in headers):
        token = _resolve_bearer_token(cfg)
        if token:
            auth = _authorization_header(token)
            headers["Authorization"] = auth
            logger.info("MCP Authorization header resolved", length=len(auth))
        else:
            logger.warning("No MCP bearer token found in config or environment")
    return headers


def _discover_settings() -> dict | None:
    paths = [
        os.environ.get("MCP_SETTINGS_PATH", ""),
        "mcp_settings.json",
        ".mcp_settings.json",
        ".gemini/settings.json",
    ]
    for p in paths:
        if not p:
            continue
        path = Path(p).resolve()
        if path.exists():
            logger.info("Loading MCP settings", path=str(path))
            return json.loads(path.read_text())
    return None


def _tool_to_openai(tool: Any) -> dict:
    schema = tool.inputSchema if hasattr(tool, "inputSchema") else {}
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description or "",
            "parameters": {
                "type": "object",
                "properties": schema.get("properties") or {},
                "required": schema.get("required") or [],
            },
        },
    }


class MCPServerManager:
    def __init__(self) -> None:
        self._sessions: dict[str, ClientSession] = {}
        self._context_managers: list = []

    async def load_and_connect(self) -> None:
        settings = _discover_settings()
        if settings is None:
            mcp_url = os.environ.get("MCP_URL", "").strip()
            if mcp_url:
                logger.info("Falling back to MCP_URL from environment")
                await self._connect_http("default", {"url": mcp_url, "type": "http"})
            return

        for server_name, cfg in settings.get("mcpServers", {}).items():
            try:
                if cfg.get("command"):
                    await self._connect_stdio(server_name, cfg)
                elif cfg.get("url"):
                    await self._connect_http(server_name, cfg)
                else:
                    logger.error("Invalid MCP config: missing command or url", server=server_name)
            except Exception as exc:
                logger.error("Failed to connect to MCP server", server=server_name, error=str(exc))

    async def _connect_http(self, server_name: str, cfg: dict) -> None:
        url = cfg["url"]
        headers = _resolve_headers(cfg)
        logger.info("Connecting to Streamable HTTP MCP server", server=server_name, url=url)
        ctx = streamablehttp_client(url, headers=headers)
        read, write, _ = await ctx.__aenter__()
        self._context_managers.append(ctx)
        session = ClientSession(read, write)
        await session.__aenter__()
        await session.initialize()
        self._sessions[server_name] = session
        logger.info("Successfully connected to MCP server", server=server_name)

    async def _connect_stdio(self, server_name: str, cfg: dict) -> None:
        env = dict(os.environ)
        env.update(cfg.get("env", {}))
        params = StdioServerParameters(
            command=cfg["command"],
            args=cfg.get("args", []),
            env=env,
        )
        logger.info("Connecting to stdio MCP server", server=server_name, command=cfg["command"])
        ctx = stdio_client(params)
        read, write = await ctx.__aenter__()
        self._context_managers.append(ctx)
        session = ClientSession(read, write)
        await session.__aenter__()
        await session.initialize()
        self._sessions[server_name] = session
        logger.info("Successfully connected to stdio MCP server", server=server_name)

    async def list_all_tools_async(self) -> tuple[list[dict], dict[str, ToolExecutor]]:
        tools: list[dict] = []
        executors: dict[str, ToolExecutor] = {}
        for server_name, session in self._sessions.items():
            try:
                result = await session.list_tools()
                for tool in result.tools:
                    tools.append(_tool_to_openai(tool))
                    executors[tool.name] = self._make_executor(session, tool.name)
                logger.info("MCP tools registered", server=server_name, count=len(result.tools))
            except Exception as exc:
                logger.error("Failed to list MCP tools", server=server_name, error=str(exc))
        return tools, executors

    def _make_executor(self, session: ClientSession, tool_name: str) -> ToolExecutor:
        async def executor(args: dict[str, Any]) -> str:
            result = await session.call_tool(tool_name, args)
            if result.isError:
                raise RuntimeError(f"tool {tool_name} returned error: {result.content}")
            parts = []
            for item in result.content:
                if hasattr(item, "text"):
                    parts.append(item.text)
                else:
                    parts.append(str(item))
            return "\n".join(parts)
        return executor

    async def close(self) -> None:
        for ctx in self._context_managers:
            try:
                await ctx.__aexit__(None, None, None)
            except Exception:
                pass
