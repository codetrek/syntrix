---
name: trim-cot-leakage
description: Use when auditing or editing repository prose for authoring-session residue, unresolved draft citations, review choreography, change narration, or unsupported planning hedges. Preserves durable rationale, factual clauses, and evidence provenance.
---

# Remove Authoring-Session Residue

Prose leaks an authoring session when it depends on artifacts only that session could see, narrates edits in a current-state document, or argues with a departed reviewer. Read [prose-standard](../prose-standard/SKILL.md) for the complete-proposition rule.

Ask whether a reader of the committed repository, without a session transcript, review thread, or uncommitted draft, can resolve every reference and assess every claim. If not, restate the surviving facts from the repository's perspective and remove the session-specific framing. Resolvable history can still belong in a decision note or commit message rather than a current-state passage.

## Patterns to assess

1. **Unresolved draft citations:** “decision 7,” “audit C2,” phase labels, or a section number with no durable owner. Link the committed owner by relative path when one exists; otherwise state the factual clause independently and remove the dead citation.
2. **Review and edit narration:** “this change adds,” “the previous version,” or “the reviewer confirmed.” State the delivered mechanism and its rationale. Keep actual deferred work in its active task, proposal, issue, or meaningful TODO.
3. **Historical contrast in current-state prose:** “used to,” “no longer,” or “this round.” State present behavior. A relevant defect rationale can be a present counterfactual: “Without the version check, two concurrent writes can both succeed.” Preserve legitimate historical decision context in its owning note.
4. **Reviewer-addressed justification:** “this is safe because it simply...” or “note that this is correct.” State the invariant or failure condition, or remove the comment when code already communicates it.
5. **Control-flow narration and obvious derivations:** Remove walkthroughs of evident branches; preserve non-obvious ordering, ownership, and invariants.
6. **Hedges and planning residue:** “probably fine for now” and “should be enough.” State the actual bound, record a real unresolved item, or remove an empty hedge. Do not promote uncertainty to a verified claim.
7. **Working-language slips:** New or rewritten documents, notes, comments, commit messages, and skills use English. Preserve technical names, quoted evidence, and the scope of the requested edit; user-facing discussion remains Chinese.

## Preserve durable content

Pattern matches alone are not findings. Keep:

- resolvable issue, merged-PR, and committed-document references, including stable section references;
- external standards and commit-pinned source citations;
- counterfactuals that explain a real current property;
- measurements with their material method, conditions, and honest provenance;
- runtime transitions, such as draining a previous connection before accepting a replacement;
- an Agent Note's `## Alternatives` and historical account of why the decision was made;
- the project's authorial voice, including “we” and “our.”

Never drop “measured,” “inferred,” or equivalent provenance when it distinguishes evidence from a claim. Never flip an obligation into a description, turn a proposal into shipped behavior, or delete a true fact with the surrounding narration.

## Respect document roles

Current-state passages in `docs/design/`, `docs/reference/`, READMEs, and comments describe supported behavior and retain concise Why alongside How. Proposed designs must remain explicitly proposed. Decision history and full alternatives belong in Agent Notes; commit messages may describe changes. Plans and tasks may describe future work and its actual status.

Active research and evidence documents remain synchronized with related work while preserving the conditions and provenance of their observations. Only explicitly archived historical documents are frozen. Archived Agent Notes are immutable: report a conflict through the active owning document, never edit the archived note.

## Workflow

1. Work within the user's stated scope. Do not broaden a local edit into a repository-wide audit or request confirmation of scope already given.
2. Audit before editing. Search with `--hidden` where `.agents/` is included, judge every hit semantically, and read dense passages that searches miss.
3. Enumerate the relevant propositions before removing prose. Check the preservation rules above.
4. Edit authorized active documents coherently and synchronize their owners and dependents. Report material changes and unresolved decisions.

Example probes, limited to the requested paths:

```sh
rg --hidden -n '§[0-9]|\(decision [0-9]|audit [A-Z][0-9]|see the (design|plan|review)' <paths>
rg --hidden -n 'used to|no longer|previously|this round|this change adds' <paths>
rg --hidden -n 'reviewer|rejected in review|v[0-9]+ of this' <paths>
rg --hidden -n 'probably|should be enough|for now|first we|then we' <paths>
```

These are recall probes, not a definition of residue or an instruction to delete every match.
