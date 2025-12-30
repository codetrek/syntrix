---
applyTo: "**"
---

# AI AGENT INSTRUCTIONS

- always disucss in "中文" with user and write in English.
- always run testing to ensure code quality.
- always run `make coverage` to ensure test coverage.
- always write unit tests for newly added code and use "github.com/stretchr/testify" for unit testing.
- always ask "should I add more testing" and make robust but not over-engineering testing.
- always document the "Why" (reasoning/analysis) alongside the "How" (decision/implementation) in design discussion documents.
- all documentation and code must be written in English.
- frontend engineering uses `bun` for package scripts and tests unless explicitly overridden.
- when a task starts or completes, update its Status in the task doc and the task index.

## DOCUMENTATION

### directory `docs/design`

Contains the architecture and design details of the service, maybe implemented, maybe not, it's the single souce of truth to the system.
When creating design or implementation documentation, follow this structure:

- `000.requirements.md`: Describe specific requirements and constraints.
- `001.architecture.md`: Record the overall architecture, including module diagrams (ASCII art) and UI layout diagrams (ASCII art).
- `002.xxx.md`: Specific module details, numbered sequentially.

### directory `tasks`

It's the guidence of implement, contains task breakdowns to implement specific features in docs/design, A refer link to target design doc should be exists. Any details should be noted here:

- Execution steps.
- How to implement new design.
- How to migrate current implementation to new design if already implemented in different ways.
- Detail implement decisions compares to current code.
- Guidence of comprehensive unittests.

## 🚨 STOP CONDITIONS

IMMEDIATELY STOP and ask user when:

- Authentication/permission errors
- Need to add new dependencies
- Creating new architectural patterns

## 🚫 FORBIDDEN PATTERNS

- Using unverified parameters from external interfaces (Strict validation required)
- **Integration Tests**: Direct calls to internal service components (e.g., `query.Engine`, `storage.Backend`) are FORBIDDEN in `tests/integration`. Tests must treat the service as a black box and interact ONLY via public interfaces (HTTP API, etc.).
- **File Editing**: Using `cat` or `echo` to write or append to files in the terminal is FORBIDDEN. ALWAYS use the `create_file` or `replace_string_in_file` tools.

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
