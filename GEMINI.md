# Workspace Instructions

- This repository is split into separate `go/` and `python/` implementation roots.
- The shared web UI lives at `web/`.
- The env template stays at the repository root as `.env.example`.

## Go Implementation

- Build artifacts should go in `go/bin`.
- Run Go commands from `go/`.
- Keep Go-specific changes inside `go/` unless the task explicitly requires workspace-level edits.

## Python Implementation

- Keep Python-specific changes inside `python/`.
- Use `python/.venv` for local Python development.
- Keep Python package code under `python/src/memlayer_agent/`.

## Shared Rules

- Do not duplicate instruction content across subdirectories.
- Prefer the root-level docs for workspace-wide guidance and use subproject READMEs for command details.
