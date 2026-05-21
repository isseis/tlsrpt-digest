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
   - If all phases are complete, skip to step 10 and follow the "If all phases are complete" bullet.
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
   - When complete, update checkboxes (`[x]` done, `[-]` skipped with a note).
     Do not commit yet; the phase-group commit is created once at the end of
     step 8 after the review loop has no Critical or Major issues.

7. Run `make deadcode`. Remove functions made unreachable by this phase
   group; keep intentional scaffolding for future phases or tasks.
   If changes were made, run `make fmt && make test && make lint`. Do not
   commit yet; include these changes in the single phase-group commit at the
   end of step 8.

8. Spawn a review subagent using the Agent tool to critically evaluate this
   phase group's changes.
   Construct a self-contained prompt that includes all of the following:
   - **Persona**: act as an experienced senior Go engineer and senior SRE
     whose job is to find real problems — not to approve. Be thorough and
     unsparing. Surface bugs, missing test coverage, architecture drift, and
     unclear code. Do not soften findings.
   - **Context**: the task directory path; instruct the subagent to read
     `02_architecture.md` and `03_implementation_plan.md` in full before
     evaluating the code.
   - **Files changed**: list the source files added or modified in this phase
     group and instruct the subagent to read them in full. Instruct the
     subagent to run both `git diff HEAD -- <files>` and
     `git diff --staged -- <files>` to see exactly what changed in the
     uncommitted phase-group diff.
   - **Evaluation criteria**: every item from the phase-group checklist below,
     copied verbatim.
   - **Output format**: for each issue found, report Severity (Critical /
     Major / Minor), File and line, Problem, and Suggestion. If a checklist
     item has no issues, state that explicitly.

   After receiving findings:
   - Fix all Critical and Major issues, then run
     `make fmt && make test && make lint`.
   - Apply Minor fixes at your discretion.
   - If any Critical or Major issue required a fix, spawn a second
     review subagent to verify the fixes. Repeat until the subagent reports
     no Critical or Major issues, up to a maximum of three passes.
   - Once the review loop ends with no Critical or Major issues, commit the
     entire phase group once using the `git-commit` skill guidelines.
     If the maximum review pass is reached with remaining Critical or Major
     issues, stop and report instead of committing.

9. Phase-group checklist (use verbatim as evaluation criteria in the subagent prompt above):
   - Consistent with `02_architecture.md`.
   - Every AC assigned to this phase group has at least one test.
   - Test coverage is sufficient: non-trivial logic, error paths, and boundary values are covered.
   - No tests duplicate existing coverage without good reason.
   - No tests are so trivial that they add no verification value.
   - No logic is reimplemented when an existing function in the codebase can be used.
   - All source comments and identifiers are in English.
   - No planning document references (e.g. `AC-01`, `F-001`) remain in source comments or string literals.
   - `make fmt` produces no diff.
   - `make lint` passes with no errors.
   - `make test` passes with no errors.

10. Decide whether to continue or finish.
   - If implementation ran this iteration, summarize implementation,
     verified ACs, assumptions, and deferred items.
   - If phases remain, ask whether to continue with the next phase group.
   - If all phases are complete, verify every AC in `01_requirements.md`
     is satisfied and has at least one test, then report final status.
