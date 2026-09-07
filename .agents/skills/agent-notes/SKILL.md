---
name: agent-notes
description: Use when adding, reviewing, moving, superseding, or pruning decision notes under .agents/notes.
---

# Working with Agent Notes

An Agent Note records a decision or proposal: why it was made, what alternatives were considered, and what was given up. Read the [notes README](../../notes/README.md) first; it owns the format, lifecycle, and classification rules.

## Find the owner before writing

Every new note requires a supersession check. Search existing notes for the decision or mechanism:

```sh
rg --hidden -l '<mechanism>' .agents/notes/
```

- If an active note already owns the decision, update it rather than creating a competing account.
- If the decision changes, write a new note. Never rewrite the old decision into its opposite. Cross-link active notes so their ownership is clear.
- For partial supersession, keep both notes, cross-link them, and synchronize statements about current facts in the active implemented note. Preserve the historical decision and its premises.
- Archived notes are immutable. A new note may link to an archived predecessor; never edit the archive to add a backlink.

Use relative Markdown links. Repair affected links when moving or deleting active notes. Preserve the original topic date when moving a note between lifecycle directories.

## Write the decision

Follow the README skeleton and use English headings. `## Problem` must stand independently of the proposed solution: a reader should be able to agree with the problem and reject the proposal.

`## Alternatives` is mandatory:

- Record alternatives that were actually considered. If there were none, say so; do not invent comparisons.
- Give each alternative a bold-led paragraph explaining what it is and the concrete cost or failure that ruled it out. “Simpler” and “cleaner” alone are not reasons.
- If an alternative remains open, state what evidence or condition would decide it.

Record mechanisms, unique rationale, consequences, and named gaps. Design documents still need a concise Why alongside the How; link them to the note for the full decision record.

## Change lifecycle deliberately

Notes use `{lifecycle}/{class}/yyyy-mm-dd-topic-title.md`. The lifecycle is `proposed`, `implemented`, `rejected`, or `archived`; the class is `feature`, `bug-fix`, `simplification`, `architecture`, `process`, or `testing`.

Decisions delivered by the current work go directly into `implemented/`. Use `proposed/` for substantive future work that is deferred.

For `proposed/` to `implemented/`, move the file and update its status and structure together:

- Change `Status: proposed` to `Status: implemented`.
- Rewrite `## Proposal` as a present-tense `## Decision` describing the delivered behavior.
- Fold `## Acceptance Criteria` and `## Risks` into `## Consequences`, preserving both benefits and costs.
- Remove proposal-only plans and migration steps. Describe what shipped directly; record remaining substantive gaps in the appropriate active proposal or task without leaving planning residue in the implemented note.

For `proposed/` to `rejected/`, move the file, set `Status: rejected - <reason>`, and preserve the proposal body as the record of what was declined.

Archive only an implemented decision whose rationale is unlikely to guide future work. Keep `Status: implemented` when moving it to `archived/`; archival is the exception to status matching its directory. Once archived, never edit, reorder, update, move, or delete the note. Treat it as historical evidence, not current behavior. Never archive a proposal: reject an obsolete proposal with its reason.

## Maintain facts without rewriting history

Update an active implemented note in the same change when current facts change, including paths, package names, defaults, and behavior. A sentence about the system today must stay current. A sentence about the decision's original premises, alternatives, delivery, or costs belongs to that historical moment and must retain its meaning. Link a later decision when it changes the conclusion.

Keep notes active while their alternatives, ownership, negative guarantees, durability or production semantics, security rules, or reconsideration conditions still guide work.

## Prune without losing rationale

A rejected note earns its place while its rationale prevents a tempting, meaningful mistake. Delete it when that rationale no longer serves this purpose, repairing active inbound links.

Delete an active implemented note only when it is fully superseded: the owning note must first absorb every unique rationale, alternative, consequence, and named gap, and every active inbound link must be repaired. Partial supersession requires retaining both notes. Git history alone is never a reason to delete rationale. Archived notes are exempt from pruning.

## Checks

Run recursive searches across the lifecycle/class layout:

```sh
# Inspect status against lifecycle; archived notes retain implemented status.
rg --hidden -n '^Status: ' .agents/notes/ -g '*.md' -g '!README.md' -g '!AGENTS.md'

# Find relative Markdown links, then resolve them from each containing file.
rg --hidden -n '\]\(\.{1,2}/[^)]*\)' .agents/notes/ -g '*.md'

# Find notes missing the mandatory Alternatives section.
rg --hidden --files-without-match '^## Alternatives$' .agents/notes/ -g '*.md' -g '!README.md' -g '!AGENTS.md'
```

These searches identify candidates; they do not prove lifecycle consistency or link validity. Check findings against the README before changing a note, and leave archived files untouched.
