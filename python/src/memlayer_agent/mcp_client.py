import json
import os
from pathlib import Path
from typing import Any

import structlog
from google.genai import types as genai_types
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


def _mcp_type_to_genai(mcp_type: str) -> genai_types.Type:
    mapping = {
        "string": genai_types.Type.STRING,
        "number": genai_types.Type.NUMBER,
        "integer": genai_types.Type.INTEGER,
        "boolean": genai_types.Type.BOOLEAN,
        "array": genai_types.Type.ARRAY,
        "object": genai_types.Type.OBJECT,
    }
    return mapping.get(mcp_type.lower(), genai_types.Type.STRING)


def _convert_schema(prop_map: dict) -> genai_types.Schema:
    schema = genai_types.Schema(type=_mcp_type_to_genai(prop_map.get("type", "string")))
    if desc := prop_map.get("description"):
        schema.description = desc
    if schema.type == genai_types.Type.OBJECT:
        if props := prop_map.get("properties"):
            schema.properties = {k: _convert_schema(v) for k, v in props.items()}
        if req := prop_map.get("required"):
            schema.required = req
    if schema.type == genai_types.Type.ARRAY:
        items = prop_map.get("items")
        schema.items = _convert_schema(items) if items else genai_types.Schema(type=genai_types.Type.STRING)
    return schema


def _tool_to_declaration(tool: Any) -> genai_types.FunctionDeclaration:
    schema = tool.inputSchema if hasattr(tool, "inputSchema") else {}
    props = schema.get("properties") or {}
    required = schema.get("required") or []
    parameters = genai_types.Schema(
        type=genai_types.Type.OBJECT,
        properties={k: _convert_schema(v) for k, v in props.items()},
        required=required,
    )
    return genai_types.FunctionDeclaration(
        name=tool.name,
        description=tool.description or "",
        parameters=parameters,
    )


class MCPServerManager:
    def __init__(self) -> None:
        self._sessions: dict[str, ClientSession] = {}
        self._tool_to_session: dict[str, ClientSession] = {}
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

    def list_all_tools(self) -> tuple[list[genai_types.FunctionDeclaration], dict[str, ToolExecutor]]:
        return [], {}

    async def list_all_tools_async(self) -> tuple[list[genai_types.FunctionDeclaration], dict[str, ToolExecutor]]:
        decls: list[genai_types.FunctionDeclaration] = []
        executors: dict[str, ToolExecutor] = {}
        for server_name, session in self._sessions.items():
            try:
                result = await session.list_tools()
                for tool in result.tools:
                    decl = _tool_to_declaration(tool)
                    decls.append(decl)
                    executors[tool.name] = self._make_executor(session, tool.name)
                logger.info("MCP tools registered", server=server_name, count=len(result.tools))
            except Exception as exc:
                logger.error("Failed to list MCP tools", server=server_name, error=str(exc))
        return decls, executors

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
