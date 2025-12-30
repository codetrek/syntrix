---
applyTo: "**"
---

# AI AGENT INSTRUCTIONS

- always disucss in "中文" with user and write document and code in English.
- always ask "should I add more testing" and make robust but not over-engineering testing.
- always document the "Why" (reasoning/analysis) alongside the "How" (decision/implementation) in design discussion documents.
- frontend engineering uses `bun` for package scripts and tests unless explicitly overridden.
- when a task starts or completes, update its Status in the task doc and the task index.

## 🚨 STOP CONDITIONS

IMMEDIATELY STOP and ask user when:

- Authentication/permission errors
- Need to add new dependencies
- Creating new architectural patterns

## 🚫 FORBIDDEN PATTERNS

- Using unverified parameters from external interfaces (Strict validation required)
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
