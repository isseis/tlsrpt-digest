Your goal is to implement one task under `docs/tasks/` by following its `03_implementation_plan.md`.

Work in the following order.

1. Identify the target task directory per `docs/dev/developer_guide/task_identification.md`.

2. Read the required input documents: `01_requirements.md`, `02_architecture.md`, `03_implementation_plan.md` in the target task directory, and `docs/dev/developer_guide/test_organization.md`.

3. Check the document status in `03_implementation_plan.md`. If not `approved`, stop and report.

4. Identify the next phase group to implement.
- Read the checkboxes in `03_implementation_plan.md`. Legend: `[ ]` not started, `[x]` done, `[-]` skipped.
- If all phases are complete, proceed directly to the final review in step 7.
- Otherwise, select a phase group: normally one phase at a time.
  - Exception: if a phase cannot pass `make test` on its own (e.g. stub-only or tightly coupled to the next phase), extend the group to include the next phase. Repeat until the group can pass `make test`. Briefly note the reason for grouping before starting work.

5. Implement the selected phase group.
- Follow the design in `02_architecture.md`.
- Place test helpers per `docs/dev/developer_guide/test_organization.md`: cross-package helpers under `testutil/`; package-internal helpers in `test_helpers.go` (or `test_helpers_<category>.go`) with `//go:build test`.
- After each Go file change: run `make fmt && make test && make lint`. Fix errors before continuing.
  - Exception: within a phase group, test failures caused by the group's incomplete state are expected; fix only errors unrelated to the in-progress group.
- When the phase group is complete, update checkboxes (`[x]` done, `[-]` skipped with a note) and commit.

6. Review the phase group.
- Run `make deadcode`. Remove functions made unreachable by this phase group; keep intentional scaffolding for future phases or tasks. If changes were made, run `make fmt && make test && make lint` and commit.
- Review the diff introduced by this phase group against the checklist below. Skip items intentionally deferred to a later phase (note the reason).
- For each issue found: fix, run `make fmt && make test && make lint`, commit, and re-run the checklist.

Phase-group review checklist:
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

7. Decide whether to continue or finish.
- If steps 5 and 6 were executed this iteration: summarize what was implemented and which ACs were verified; note assumptions and deferred items.
- If phases remain: ask "Shall I continue with the next phase group?" — return to step 4 if agreed, otherwise report status and stop.
- If all phases are complete: verify every AC in `01_requirements.md` is satisfied by the implementation and has at least one test. Report the final status and any gaps.
