---
applyTo: "**"
---

# AI AGENT INSTRUCTIONS
- always disucss in "中文" with user and write in English.
- always run testing to ensure code quality
- always write unit tests for newly added code and use "github.com/stretchr/testify" for unit testing
- always ask "should I add more testing" and make robust but not over-engineering testing
- always document the "Why" (reasoning/analysis) alongside the "How" (decision/implementation) in design discussion documents
- all documentation and code must be written in English

## � DOCUMENTATION STRUCTURE
When creating design or implementation documentation, follow this structure:
- `000_requirements.md`: Describe specific requirements and constraints.
- `001_architecture.md`: Record the overall architecture, including module diagrams (ASCII art) and UI layout diagrams (ASCII art).
- `002_xxx.md`: Specific module details, numbered sequentially.

## �🚨 STOP CONDITIONS
IMMEDIATELY STOP and ask user when:
- Authentication/permission errors
- Need to add new dependencies
- Creating new architectural patterns

## 🚫 FORBIDDEN PATTERNS
- Using unverified parameters from external interfaces (Strict validation required)
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
