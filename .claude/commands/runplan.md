Your goal is to implement one task under `docs/tasks/` by following its `03_implementation_plan.md`.

Work in order.

1. Identify the target task per `docs/dev/developer_guide/task_identification.md`.

2. Read `03_implementation_plan.md`. If the document status is not `approved`, stop and report.

3. Read `01_requirements.md`, `02_architecture.md` (both in the target task directory), and `docs/dev/developer_guide/test_organization.md`.

4. Select the next phase group from `03_implementation_plan.md` checkboxes (`[ ]` not started, `[x]` done, `[-]` skipped).
- If all phases are complete, skip to step 8 and follow the "If all phases are complete" bullet.
- Otherwise, use one phase unless it cannot pass `make test` alone (e.g. stub-only or tightly coupled); then extend the group until it can pass. Briefly note the reason for grouping before starting work.

5. Implement the selected phase group.
- Follow the design in `02_architecture.md`.
- Place test helpers per `docs/dev/developer_guide/test_organization.md`: cross-package helpers under `testutil/`; package-internal helpers in `test_helpers.go` (or `test_helpers_<category>.go`) with `//go:build test`.
- After each Go file change, run `make fmt && make test && make lint`; fix errors before continuing. Exception: errors caused by the phase group's incomplete state (e.g. build or test failures from missing implementations that stubs depend on) need not be fixed until the group is complete; fix only errors unrelated to the in-progress group.
- When complete, update checkboxes (`[x]` done, `[-]` skipped with a note) and commit.

6. Run `make deadcode`. Remove functions made unreachable by this phase group; keep intentional scaffolding for future phases or tasks. If changes were made, run `make fmt && make test && make lint` and commit.

7. Spawn a review subagent using the Agent tool to critically evaluate this phase group's changes.
   Construct a self-contained prompt that includes all of the following:
   - **Persona**: act as an experienced senior Go engineer and senior SRE whose job is to find real problems — not to approve. Be thorough and unsparing. Surface bugs, missing test coverage, architecture drift, and unclear code. Do not soften findings.
   - **Context**: the task directory path; instruct the subagent to read `02_architecture.md` and `03_implementation_plan.md` in full before evaluating the code.
   - **Files changed**: list the source files added or modified in this phase group and instruct the subagent to read them in full. Also provide the specific commit range for this phase group (e.g., `HEAD~N..HEAD`) and instruct the subagent to run `git diff <range>` to see exactly what changed.
   - **Evaluation criteria**: every item from the phase-group review checklist below, copied verbatim.
   - **Output format**: for each issue found, report Severity (Critical / Major / Minor), File and line, Problem, and Suggestion. If a checklist item has no issues, state that explicitly.

   After receiving findings:
   - Fix all Critical and Major issues, then run `make fmt && make test && make lint` and commit.
   - Apply Minor fixes at your discretion.
   - If any Critical or Major issue required a fix, spawn a second review subagent to verify the fixes. Repeat, subject to the three-pass limit below, until the subagent reports no Critical or Major issues.
   - After three review passes, continue only if the remaining Critical or Major issues are concrete, scoped to this phase group, and clearly fixable without expanding the phase scope. Otherwise, stop and report the remaining issues instead of continuing automatically.

Phase-group review checklist (use verbatim as evaluation criteria in the subagent prompt above):
- [ ] Implementation is consistent with `02_architecture.md`.
- [ ] Every AC assigned to this phase group by the implementation plan has at least one test.
- [ ] Test coverage is sufficient: non-trivial logic, error paths, and boundary values are covered.
- [ ] No tests duplicate existing coverage without good reason.
- [ ] No tests are so trivial that they add no verification value.
- [ ] No logic is reimplemented when an existing function in the codebase can be used.
- [ ] All source comments and identifiers are in English.
- [ ] No planning document references (e.g. `AC-01`, `F-001`) remain in source comments or string literals.
- [ ] `make fmt` produces no diff.
- [ ] `make lint` passes with no errors.
- [ ] `make test` passes with no errors.

8. Decide whether to continue or finish.
- If implementation ran this iteration: summarize implementation, verified ACs, assumptions, and deferred items.
- If phases remain: ask "Shall I continue with the next phase group?" — return to step 4 if agreed, otherwise report status and stop.
- If all phases are complete: verify every AC in `01_requirements.md` is satisfied by the implementation and has at least one test. Report the final status and any gaps.
