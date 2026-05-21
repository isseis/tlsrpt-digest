---
name: runplan
description: Use when the user asks to implement a task under docs/tasks by following its 03_implementation_plan.md phase by phase.
---

# Run Plan

Your goal is to implement one task under `docs/tasks/` by following its
`03_implementation_plan.md`.

1. Identify the target task per
   `docs/dev/developer_guide/task_identification.md`.

2. Read the target task's `01_requirements.md`, `02_architecture.md`,
   `03_implementation_plan.md`, and
   `docs/dev/developer_guide/test_organization.md`.

3. Check the document status in `03_implementation_plan.md`. If not
   `approved`, stop and report.

4. Select the next phase group from `03_implementation_plan.md` checkboxes:
   `[ ]` not started, `[x]` done, `[-]` skipped.
   - If all phases are complete, go to final review.
   - Otherwise, use one phase unless it cannot pass `make test` alone.
     If needed, extend the group until it can pass and briefly note why.

5. Implement the selected phase group.
   - Follow `02_architecture.md`.
   - Place test helpers per
     `docs/dev/developer_guide/test_organization.md`.
   - After each Go file change, run
     `make fmt && make test && make lint`.
   - Fix errors before continuing, except test failures caused by the
     phase group's incomplete state.
   - When complete, update checkboxes (`[x]` done, `[-]` skipped with a note),
     then commit using the `git-commit` skill guidelines: inspect the staged
     diff, draft the commit message, ask for confirmation, and only commit
     after approval.

6. Review the phase group.
   - Run `make deadcode`.
   - Remove functions made unreachable by this phase group.
   - Keep intentional scaffolding for future phases or tasks.
   - Review only the diff introduced by this phase group.
   - Fix issues, run `make fmt && make test && make lint`, then commit using
     the `git-commit` skill guidelines. Repeat until applicable checklist
     items pass.

7. Phase-group checklist:
   - Consistent with `02_architecture.md`.
   - Every AC assigned to this phase group has at least one test.
   - Covers non-trivial logic, error paths, and boundary values.
   - No duplicate or trivial tests.
   - No reimplementation when existing code can be used.
   - Comments and identifiers are English.
   - No planning references such as `AC-01` remain in source comments or
     string literals.
   - `make fmt`, `make lint`, and `make test` pass.

8. Decide whether to continue or finish.
   - If implementation ran this iteration, summarize implementation,
     verified ACs, assumptions, and deferred items.
   - If phases remain, ask whether to continue with the next phase group.
   - If all phases are complete, verify every AC in `01_requirements.md`
     is satisfied and has at least one test, then report final status.
