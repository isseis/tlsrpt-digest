# Project Context for Claude Commands

This file is the single source of truth for every project-specific value the
commands in this directory depend on. Commands reference the **names** defined
here (e.g. "the task root", "the build checks") instead of hard-coding paths.

**To port these commands to another project, edit only this file** (plus the
guide documents it points to). The command bodies should not need changes.

The values are grouped by the layer they belong to, so you can see at a glance
what changes under which condition:

- **Process convention** — changes only if the new project adopts a different
  requirements → architecture → plan → PR workflow.
- **Tech-stack convention** — changes only if the new project is not Go, or uses
  different build tooling / source layout.
- **Domain-specific** — always changes; specific to tlsrpt-digest.

---

## Process convention

| Name | Value |
|---|---|
| Task root | `docs/tasks/` |
| Task identification guide | `docs/dev/developer_guide/task_identification.md` |
| Requirements process guide | `docs/dev/developer_guide/requirements_process.md` |
| Test organization guide | `docs/dev/developer_guide/test_organization.md` |
| Mermaid reference guide | `docs/dev/developer_guide/mermaid_reference.md` |
| Package reference guide | `docs/dev/developer_guide/package_reference.md` |
| Requirements document | `01_requirements.md` |
| Architecture document | `02_architecture.md` |
| Implementation plan document | `03_implementation_plan.md` |
| Document status values | `draft` → `approved` |
| Document language | Japanese |
| Translation glossary | `docs/translation_glossary.md` |
| Default translation direction | Japanese (primary) ⇄ English |

### PR marker conventions

PR boundary markers embedded in the implementation plan use these labels:

- Section heading: `### PR-N 作成ポイント: <scope label in English>`
- Fields: `**対象ステップ**`, `**推奨タイトル**`, `**レビュー観点**`
- Conventional commit scope format: `feat(<task-id>): <concise English title>`

---

## Tech-stack convention

| Name | Value |
|---|---|
| Build checks (run after edits) | `make fmt` (Go only) → `make test` → `make lint` |
| Dead-code check | `make deadcode` |
| Green gate (must pass before PR) | `make test && make lint` |
| Source layout | `cmd/`, `internal/` |
| Cross-package test helpers | `testutil/` |
| Package-internal test helpers | `test_helpers.go` / `test_helpers_<category>.go` with `//go:build test` |
| Source-language rule | Go comments, identifiers, and string literals must be English |

---

## Domain-specific (tlsrpt-digest only — replace wholesale when porting)

| Name | Value |
|---|---|
| Conditional security guide | `docs/dev/developer_guide/notification_security.md` |
| Conditional-guide trigger | the feature sends notifications or handles notification destinations |

### Domain examples referenced by commands

The methodology commands (`mkplan`, `runplan`) include illustrative examples
drawn from this project's domain (TLSRPT report parsing, IMAP mailbox handling,
the reset/recover flow). When porting, these examples no longer apply — replace
them with examples from the new project's domain, or drop them. They appear in:

- `runplan.md` step 5 ("State invariants before coding"): the ULID test-ID,
  `--dry-run` side-effect, and IMAP CLOSE/SELECT teardown examples.
- `mkplan.md` step 5 and its checklists: the `TestBootstrap_PendingReset`,
  `resetPhase`, `systemErrorHint`, mailbox 32-char-name, and `recover --mode`
  examples.
