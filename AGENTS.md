---
applyTo: "**"
---

# AI AGENT INSTRUCTIONS

- Always disucss in "中文" with user and write document and code in English.
- Always run testing to ensure code quality.
- Always run `make coverage` to evaluate test coverage and fix as needed.
- Always ask "should I add more testing" and make robust but not over-engineering testing.
- Always document the "Why" (reasoning/analysis) alongside the "How" (decision/implementation) in design discussion documents.
- Reminder: Fix everything in one pass—search globally first, then verify and echo back, so the user never has to repeat the same request.

## 🚨 STOP CONDITIONS

IMMEDIATELY STOP and ask user when:

- Authentication/permission errors
- Need to add new dependencies
- Creating new architectural patterns

## 🚫 FORBIDDEN PATTERNS

- Using unverified parameters from external interfaces (Strict validation required).
- `Skip` any unit test or integration testing.
- Using `cat` or `echo` to write or append to files in the terminal.
- **Integration Tests**: Direct calls to internal service components (e.g., `query.Engine`, `storage.Backend`) are FORBIDDEN in `tests/integration`. Tests must treat the service as a black box and interact ONLY via public interfaces (HTTP API, etc.).

## 🔄 DECISION TREE

Before ANY file creation:

1. Can I modify existing file? → Do that
2. Is there a similar file? → Copy and modify
3. Neither? → Ask user first

Before ANY change:

1. Will this need new imports? → Check if already available

## 📝 HIERARCHY RULES

- Check for AGENTS.md in current directory
- Subdirectory rules compliment root rules
- If conflict → subdirectory wins

## Preference

- Use "github.com/stretchr/testify" for Golang tests.
- Uses `bun` for frontend package scripts.
- Document design in `docs/design` folder, and follow current directory layout.
- Create tasks in `tasks` folder.
