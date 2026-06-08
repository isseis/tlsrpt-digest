# Reusing the Claude Commands in a New Project

This guide explains how the Claude Code commands under `.claude/commands/` are
structured for reuse, and exactly which files to copy and edit when you start a
new project. It is written for someone bootstrapping a fresh repository who wants
the same requirements → architecture → plan → PR → implementation workflow.

## The three layers

Every piece of content in the commands belongs to one of three layers. The
structure exists so that porting touches as few files as possible.

| Layer | Meaning | Changes when… |
|---|---|---|
| 1. Project-independent | Works in any project unchanged | never |
| 2a. Process convention | The `docs/tasks/` methodology itself | the new project adopts a different requirements/design/plan/PR workflow |
| 2b. Tech-stack convention | Go + Makefile + `cmd/`/`internal/` assumptions | the new project is not Go, or uses different build tooling / layout |
| 3. Domain-specific | tlsrpt-digest domain knowledge and examples | always |

The key design move: **all layer-2 and layer-3 values are concentrated in one
file, `.claude/commands/_context.md`.** The command bodies refer to those values
by name instead of hard-coding them, so porting is mostly a matter of rewriting
`_context.md` and the guide documents it points to.

## File map

```
.claude/commands/
  _context.md                       # Layer 2 + 3: all project-specific values (single source of truth)
  _lib/
    review-subagent-pattern.md      # Layer 1: shared critical-review procedure used by 5 commands
  fixpr.md                          # Layer 1: GitHub PR review-thread triage (gh CLI / GraphQL)
  mktrans.md                        # Layer 1: JA⇄EN document translation workflow
  mkarch.md                         # Layer 2: create 02_architecture.md
  mkplan.md                         # Layer 2: create 03_implementation_plan.md
  mkplan2.md                        # Layer 2: design PR boundaries inside the plan
  runplan.md                        # Layer 2: implement the plan phase by phase
```

`_context.md` and `_lib/*.md` are not invocable commands; they are data and
library files that the commands read at runtime. The leading underscore marks
them as non-command helpers.

### How the indirection works

Claude Code commands have no native include mechanism. Instead, each command
begins with a directive to read the helper files, for example:

> Read `.claude/commands/_context.md`. … Where this command names such a path or
> value, treat the entry in `_context.md` as canonical.

The agent reads those files at the start of the run and substitutes the values.
This is why the command bodies can stay generic while the project specifics live
in one place.

## What each layer contains

**Layer 1 (copy as-is):**
- `fixpr.md` — entire PR review-thread workflow. Only the build-check command
  names (`make lint`, `make test`) come from `_context.md`.
- `mktrans.md` — entire translation workflow. Only the glossary path comes from
  `_context.md`. Translation direction is determined by the source file extension
  (`.ja.md` → EN output, `.md` → JA output); `_context.md` records the project's
  language-pair convention for reference but `mktrans.md` does not read it at
  runtime.
- `_lib/review-subagent-pattern.md` — the persona / files / criteria / output /
  three-pass review loop shared by `mkarch`, `mkplan`, `mkplan2`, `mktrans`, and
  `runplan`.

**Layer 2 (keep the command, rewrite `_context.md`):**
- `mkarch.md`, `mkplan.md`, `mkplan2.md`, `runplan.md` — the methodology
  pipeline. Their structure is generic; the task root, document names, status
  values, guide paths, build checks, source layout, and PR-marker labels all come
  from `_context.md`.

**Layer 3 (rewrite or drop):**
- The conditional security guide reference and the domain examples embedded in
  `mkplan.md` / `runplan.md` (ULID test IDs, `--dry-run` side effects, IMAP
  teardown, `recover --mode`, etc.). `_context.md` lists where these appear so
  you can find and replace them.

## Porting checklist

Follow these steps to reuse the commands in a new repository.

1. **Copy the files.** Copy the entire `.claude/commands/` directory into the new
   repository.

2. **Decide your workflow (Layer 2a).** If the new project uses the same
   requirements → architecture → plan → PR pipeline, keep the methodology
   commands as-is. If not, adjust them, but most teams keep the pipeline and only
   change values.

3. **Rewrite `_context.md` and remove domain examples from commands.**
   - **Process convention**: set the task root, guide paths, document names,
     status values, document language, glossary path, and PR-marker conventions.
   - **Tech-stack convention**: set the build checks, source layout, and
     test-helper placement. If the new project is not Go, replace the Go-specific
     entries (e.g. `make` targets, `//go:build test`, `cmd/`/`internal/`) with the
     new stack's equivalents.
   - **Domain-specific in `_context.md`**: replace the conditional-guide entry
     with one from the new project's domain, or remove it.
   - **Domain-specific in command bodies**: the illustrative examples embedded in
     `mkplan.md` and `runplan.md` (ULID test IDs, `--dry-run` side effects, IMAP
     teardown, `recover --mode`, etc.) are specific to tlsrpt-digest and must be
     replaced or removed. `_context.md` lists exactly where each example lives.

4. **Create the guide documents the commands reference.** `_context.md` points to
   guides such as the requirements process guide, task identification guide, test
   organization guide, and Mermaid reference. Author these for the new project (or
   copy and adapt them). The methodology commands depend on them existing.

5. **Create the supporting files.** If you keep `mktrans.md`, create the
   translation glossary at the path you set in `_context.md`. If you keep the
   `docs/tasks/` pipeline, create the task template directory.

6. **Verify the PR-boundary skill name.** `runplan.md` invokes `/mkplan2` to
   design PR boundaries. If the new project uses a different command name for this
   step, update the reference in `runplan.md` step 2.5 accordingly.

7. **Smoke-test one command.** Run a single command (e.g. `/mkarch`) on a trial
   task and confirm it reads `_context.md`, resolves the paths, and behaves as
   expected.

## Summary

After this restructuring, the per-project porting cost is concentrated in:

- `_context.md` — one file holding every project-specific value.
- The guide documents under the developer guide directory — the project's
  knowledge base, which the commands reference rather than embed.

Layer-1 files (`fixpr.md`, `mktrans.md`, `_lib/review-subagent-pattern.md`) carry
over with no edits.
