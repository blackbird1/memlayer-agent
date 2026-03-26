# Contributing to MemLayer Agent

Thanks for your interest in contributing! This project is a showcase for [ProcIQ](https://prociq.ai) persistent memory — we welcome bug fixes, new tools, documentation improvements, and backend enhancements.

## Prerequisites

You will need accounts and API keys from the following services. **Never commit API keys or secrets to the repository.**

### Required

| Service | What you need | Where to get it |
|---------|--------------|-----------------|
| **Google Gemini** | `GOOGLE_API_KEY` | [Google AI Studio](https://aistudio.google.com/apikey) — create a project and generate an API key |
| **ProcIQ** | `MEMLAYER_MCP_BEARER_TOKEN` | [prociq.ai](https://prociq.ai) — sign up, create an organization, and generate an MCP bearer token |

### Optional: example tool integration

| Service | What you need | Where to get it |
|---------|--------------|-----------------|
| **Finnhub** | `FINNHUB_API_KEY` | [finnhub.io](https://finnhub.io) — free tier available, sign up and copy your API key |

Finnhub is included as a **reference implementation** showing how to add local tool integrations. When `FINNHUB_API_KEY` is set, stock market tools (quotes, news, earnings, analyst data) are enabled automatically. The agent runs fully without it.

The Finnhub implementation lives in:
- **Go**: `go/cmd/adk-server/finnhub_tools.go`
- **Python**: `python/src/memlayer_agent/finnhub_tools.py`

Use either file as a template to integrate your own APIs.

## Getting started

1. **Fork and clone** the repository.

2. **Create your env file** from the template:

   ```bash
   cp .env.example .env
   ```

3. **Fill in your API keys** in `.env` (see [Prerequisites](#prerequisites) above).

4. **Start the stack** with Docker Compose:

   ```bash
   # Go backend (default)
   docker compose --profile go up --build

   # Python backend
   API_BACKEND=adk-server-python:9090 docker compose --profile python up --build
   ```

5. **Open the UI** at `http://localhost:8080` and send a test message.

See [`go/README.md`](./go/README.md) for local development without Docker and troubleshooting tips.

## Project structure

```
go/          Go backend (adk-server)
python/      Python backend (adk-server-python)
web/         Nginx config + chat UI
```

Both backends implement the same API. Pick whichever language you prefer to work in.

## How to contribute

### Reporting bugs

Open an issue with:
- Steps to reproduce
- Expected vs actual behavior
- Which backend (Go or Python) you were using

### Adding a new tool

Tools are one of the easiest ways to contribute. The Finnhub integration is a purpose-built reference:
- **Go** — `go/cmd/adk-server/finnhub_tools.go`
- **Python** — `python/src/memlayer_agent/finnhub_tools.py`

Both files follow the same pattern: define declarations (schema), implement executors, and export a registry. The tool registration in `main.go` / `chat.py` guards loading behind the relevant env var.

To add a new tool integration:
1. Copy the Finnhub file and rename it (e.g., `weather_tools.go`)
2. Implement your tool declarations and executors
3. Add a guard in `main.go` or `chat.py` to load your tools when a relevant env var is set
4. Test end-to-end via the chat UI
5. Submit a PR with a brief description of the integration

### General guidelines

- **One concern per PR** — keep pull requests focused
- **Test your changes** — run the stack locally and verify via the chat UI before submitting
- **Follow existing patterns** — match the style and structure of the code around your changes
- **No secrets in commits** — `.env` files are gitignored for a reason

### Commit messages

Follow the existing convention:

```
feat: add weather tool with OpenWeatherMap integration
fix: handle empty response from Finnhub earnings endpoint
docs: add architecture diagram to README
chore: update Go dependencies
```

## Code of conduct

Be respectful and constructive. We're all here to build something useful.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](./LICENSE).
