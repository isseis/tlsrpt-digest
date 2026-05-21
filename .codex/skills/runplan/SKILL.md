---
name: runplan
description: Use when the user asks to implement a task under docs/tasks by following its 03_implementation_plan.md phase by phase.
---

# Run Plan

Your goal is to implement one task under `docs/tasks/` by following its
`03_implementation_plan.md`.

1. Identify the target task per
   `docs/dev/developer_guide/task_identification.md`.

2. Read the target task's `03_implementation_plan.md`.

3. Check the document status in `03_implementation_plan.md`. If not
   `approved`, stop and report.

4. Read the remaining required documents: `01_requirements.md`,
   `02_architecture.md`, and
   `docs/dev/developer_guide/test_organization.md`.

5. Select the next phase group from `03_implementation_plan.md` checkboxes:
   `[ ]` not started, `[x]` done, `[-]` skipped.
   - If all phases are complete, go to final review.
   - Otherwise, use one phase unless it cannot pass `make test` alone.
     If needed, extend the group until it can pass and briefly note why.

6. Implement the selected phase group.
   - Follow `02_architecture.md`.
   - Place test helpers per
     `docs/dev/developer_guide/test_organization.md`.
   - After each Go file change, run
     `make fmt && make test && make lint`.
   - Fix errors before continuing, except test failures caused by the
     phase group's incomplete state.
   - When complete, update checkboxes (`[x]` done, `[-]` skipped with a note),
     then commit using the `git-commit` skill guidelines.

7. Run `make deadcode`. Remove functions made unreachable by this phase
   group; keep intentional scaffolding for future phases or tasks.
   If changes were made, run `make fmt && make test && make lint` and commit
   using the `git-commit` skill guidelines.

8. Spawn a review subagent using the Agent tool to critically evaluate this
   phase group's changes.
   Construct a self-contained prompt that includes all of the following:
   - **Persona**: act as an experienced senior Go engineer and senior SRE
     whose job is to find real problems — not to approve. Be thorough and
     unsparing. Surface bugs, missing test coverage, architecture drift, and
     unclear code. Do not soften findings.
   - **Context**: the task directory path, so the subagent can read
     `02_architecture.md` and `03_implementation_plan.md` as the authoritative
     design and plan references.
   - **Files changed**: list the source files added or modified in this phase
     group and instruct the subagent to read them in full. Also instruct the
     subagent to run `git diff` to see exactly what changed.
   - **Evaluation criteria**: every item from the phase-group checklist below,
     copied verbatim.
   - **Output format**: for each issue found, report Severity (Critical /
     Major / Minor), File and line, Problem, and Suggestion. If a checklist
     item has no issues, state that explicitly.

   After receiving findings:
   - Fix all Critical and Major issues, then run
     `make fmt && make test && make lint` and commit using the `git-commit`
     skill guidelines.
   - Apply Minor fixes at your discretion.
   - If significant changes were made, spawn a second review subagent to
     verify the fixes.

9. Phase-group checklist (use verbatim as evaluation criteria in the subagent prompt above):
   - Consistent with `02_architecture.md`.
   - Every AC assigned to this phase group has at least one test.
   - Covers non-trivial logic, error paths, and boundary values.
   - No duplicate or trivial tests.
   - No reimplementation when existing code can be used.
   - Comments and identifiers are English.
   - No planning references such as `AC-01` remain in source comments or
     string literals.
   - `make fmt`, `make lint`, and `make test` pass.

10. Decide whether to continue or finish.
   - If implementation ran this iteration, summarize implementation,
     verified ACs, assumptions, and deferred items.
   - If phases remain, ask whether to continue with the next phase group.
   - If all phases are complete, verify every AC in `01_requirements.md`
     is satisfied and has at least one test, then report final status.
