---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant: a change to the emulator's role (ADR 0001), the
VDA 5050 subset (ADR 0002), telegram protocol v1 (ADR 0003, asrs-control conforms to it), simulated
time, faults or the admin API (ADR 0004), a reversal of a decision, or anything a future reader would
otherwise reverse-engineer from the diff. A bug fix inside an already-decided architecture needs none.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four digits, the next integer after the highest existing one
(`git ls-tree --name-only origin/develop -- docs/adr/`); never reuse a number. Add a row to
`docs/adr/index.md` and a link to the "Architecture Decision Records" category of
`docs/sidebars.ts`. The ADRs stay in `docs/adr/` (other repositories cite those paths); the
Docusaurus site serves them in place under `/docs/adr` as plain Markdown, so no front matter is
needed.

## Format

The existing ADRs' shape:

```markdown
# 4. Title (a short noun phrase)

Status: Accepted (YYYY-MM-DD). One line on what it implements or amends.

## Context
The forces and the alternatives weighed.

## Decision
What is decided, specific enough to implement from scratch.

## Consequences
What becomes easier, what becomes harder, the follow-ups.
```

ADR 0002 is the model for a Decision section: numbered decisions, each with its mechanism, and an
honest "Known limits" section.

## ADR bodies are immutable

Never edit an accepted ADR's decision. A changed decision is a NEW ADR that names the one it amends
or supersedes; the old one may only gain a dated status note ("Superseded by ADR 0004",
"Implemented (date): PR #n").

## Cross-repo decisions: companion ADRs

A decision that spans two repositories (a new telegram protocol version with equipment-emulator, a
new consumer in material-flow-control) gets one ADR per repository, each naming the other as its
companion and stating the split of responsibility.

## After writing: build the site

```bash
cd docs && npm ci && npm run build
```

`onBrokenLinks` is `throw` for every page; ADR sources are parsed as CommonMark and their own
relative links are tolerated, but a link from a site page to a wrong ADR route fails the build.
