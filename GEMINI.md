# Project Instructions

- Stephen prefers a Go architecture of router -> service -> store -> sql, with the transaction layer managed at the service level to combine multiple stores.
- Compiled binaries in this project always go inside a bin directory.
- The user plans to use FastStream for background summarization work in the future to avoid blocking the main embedding service.
- Always build Go binaries in the 'bin' directory.
- Migrated MemLayer tests to use async PostgresBackend by default. Fixed SQL type casts, async generator handling in PostgresBackend, implemented close() for resource cleanup, and updated tests to use valid UUIDs.
- Always use the project's virtual environment (.venv) when running commands locally.
- Refactored PostgresBackend to use a repository layer (PostgresRepository) for encapsulating aiosql queries and converting records to Pydantic schemas. This also fixed an issue where aiosql returned async generators that were not properly awaited.
- Always use aiosql + repository pattern for database interactions in the MemLayer project.
- Successfully migrated MemLayer to Go with a strict multi-tenant context bridge in the MCP server. Fixed auth-service token binding to support both JSON and Form requests. Deployment to hetzner-va verified with log-based confirmation of orgID propagation and token verification. Strict nil-UUID checks in tools prevent FK violations during ingestion.
- NEVER modify .env.local or any specific environment files directly. Environment configuration must be managed EXCLUSIVELY by updating the .env symbolic link to point to the desired template (e.g., .env.local, .env.hetzner).
- Always use 'gopls' to determine type inference and symbol definitions in Go projects.
- Always use the gateway (port 80/443) for external callbacks and API endpoints in the MemLayer project, even on localhost.
- The default ProcIQ memory scope for the MemLayer project is 'default'.
