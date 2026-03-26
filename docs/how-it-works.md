# How It Works

MemLayer Agent is a self-learning AI agent that gets smarter over time. The key idea is simple: before acting, the agent retrieves what it already knows; after acting, it logs what it learned. This **Retrieve → Act → Log** cycle is what makes it a *memory-augmented* agent rather than a stateless one.

## The memory cycle

```
User message
     │
     ▼
┌─────────────────────────────────────────────────────┐
│                   RETRIEVE                          │
│  prociq_retrieve_context(task_description=...)      │
│  → past episodes, patterns, skills surfaced         │
│  → agent reads these before doing anything          │
└──────────────────────────┬──────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────┐
│                     ACT                             │
│  Agent calls tools, reasons, produces a response    │
│                                                     │
│  On error → log failure, retrieve known fixes,      │
│             then retry with retrieved solution      │
└──────────────────────────┬──────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────┐
│                     LOG                             │
│  prociq_log_episode(task_goal, approach, outcome)   │
│  → stored in ProcIQ, available in future sessions   │
└──────────────────────────┬──────────────────────────┘
                           │
                           ▼
                    Final response
```

Each logged episode becomes retrievable context for future conversations. Over time the agent accumulates a knowledge base of what worked, what failed, and how to approach recurring tasks.

## Request flow

```
Browser / curl
     │  POST /api/chat  { message, sessionId }
     ▼
  Nginx (adk-web :8080)
     │  proxy /api/* → backend
     ▼
  API server  (Go :9090  or  Python :9091)
     │
     ├─ Load chat history from Redis (keyed by sessionId)
     │
     ├─ Register tools:
     │    ├─ ProcIQ MCP tools  (always — memory layer)
     │    └─ Local tools       (if env key present, e.g. FINNHUB_API_KEY)
     │
     ├─ Send message + history → Gemini
     │
     └─ Tool call loop:
          ├─ Model returns function_call → execute tool → send result back
          └─ Repeat until model returns plain text
     │
     ├─ Save updated history to Redis
     │
     └─ Return { response, steps[] }
```

## Components

| Component | Role |
|-----------|------|
| **Gemini** | Language model — reasons, calls tools, generates responses |
| **ProcIQ MCP** | Memory layer — stores and retrieves episodes, notes, patterns, skills |
| **Redis** | Short-term session store — chat history per `sessionId` (30-min TTL) |
| **Nginx** | Reverse proxy — serves the static UI and routes `/api/*` to the backend |

## Two kinds of tools

### MCP tools (remote, via ProcIQ)

Connected at startup over Streamable HTTP. These are the memory tools:

- `prociq_retrieve_context` — surface relevant past context before acting
- `prociq_log_episode` — record what happened and what was learned
- `prociq_log_note` — store a static fact
- `prociq_search_patterns` / `prociq_list_skills` — query accumulated knowledge

The agent is instructed to treat retrieved Skills and Patterns as **mandatory procedural guidance**, not optional suggestions.

### Local tools (in-process)

Implemented directly in the server binary. Loaded only when a relevant environment variable is set. The Finnhub integration (`finnhub_tools.go` / `finnhub_tools.py`) is the reference example — it shows the full pattern:

1. Define `FunctionDeclaration` schemas (what the model sees)
2. Implement executor functions (what actually runs)
3. Export a registry and register it behind an env var check

See [`CONTRIBUTING.md`](../CONTRIBUTING.md) for how to add your own.

## Memory types

ProcIQ stores several kinds of memory:

| Type | What it is | Example |
|------|-----------|---------|
| **Episode** | A completed task with goal, approach, and outcome | "Fixed CORS 403 by setting Allow-Origin header" |
| **Note** | A static fact to remember | "User prefers Go over Python for new services" |
| **Pattern** | A recurring successful strategy | "Always run go vet before committing Go code" |
| **Skill** | A reusable procedural guide | "How to add a new MCP tool integration" |

Episodes accumulate automatically as the agent works. Patterns and skills are promoted from episodes when a strategy proves consistently effective.

## Session vs. long-term memory

| | Redis (session) | ProcIQ (long-term) |
|---|---|---|
| **Scope** | One conversation | Across all conversations |
| **Content** | Full message history | Distilled knowledge |
| **TTL** | 30 minutes | Indefinite |
| **Purpose** | Keep conversation coherent | Accumulate expertise |

Redis lets the agent remember what was said earlier in the same chat. ProcIQ lets it remember what was learned across many different sessions.
