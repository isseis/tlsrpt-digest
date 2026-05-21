Your goal is to implement one task under `docs/tasks/` by following its `03_implementation_plan.md`.

Work in the following order.

1. Identify the target task directory by following the rules in `docs/dev/developer_guide/task_identification.md`.

2. Read the required input documents.
- `01_requirements.md` in the target task directory
- `02_architecture.md` in the target task directory
- `03_implementation_plan.md` in the target task directory
- `docs/dev/developer_guide/test_organization.md`

3. Verify that implementation is allowed.
- Check the document status in `03_implementation_plan.md`.
- If the status is not `approved`, do not begin implementing.
- In that case, stop and report that implementation cannot begin until `03_implementation_plan.md` is `approved`.

4. Identify the next phase group to implement.
- Read the checkboxes in `03_implementation_plan.md`. Legend: `[ ]` not started, `[x]` done, `[-]` skipped.
- If all phases are complete, proceed directly to the final review in step 7.
- Otherwise, select a phase group: normally one phase at a time.
  - Exception: if the next phase cannot reach a passing `make test` on its own (e.g. it only adds stub code to unblock compilation, or it is so tightly coupled to the following phase that the build cannot pass without both), group it together with the following phase. If two phases are still not enough, continue adding phases until the group can pass `make test`.
  - In that case, briefly note why the phases are grouped before starting work.

5. Implement the selected phase group.
- Follow the design in `02_architecture.md` when implementing.
- Apply test helper rules from `docs/dev/developer_guide/test_organization.md`:
  - If new cross-package helpers or mocks are needed, place them under `testutil/` with the correct file naming and package naming.
  - If package-internal helpers are needed, place them in `test_helpers.go` or `test_helpers_<category>.go` with `//go:build test`.
- After each code change involving Go files: run `make fmt`, then `make test`, then `make lint`. Fix any errors before continuing.
  - Exception: when implementing a phase group, test failures caused by the incomplete state of the group (e.g. missing implementations that stubs depend on) are expected and need not be fixed until the group is complete; fix only errors unrelated to the in-progress group.
- When all items in the phase group are complete, update the plan's checkboxes (`[x]` for done, `[-]` for skipped with a note) and commit.

6. Review the phase group.
- Run `make deadcode`. For each reported function, determine whether it was made unreachable by this phase group (i.e. it existed before and is no longer called as a result of this change) — if so, remove it. Do not remove code that was intentionally introduced in this phase group as scaffolding for a later phase or task; such dead code is expected and will be activated by future work. If any removals were made, run `make fmt && make test && make lint`, then commit.
- Review the diff introduced by the phase group against the checklist below.
  - Skip checklist items that are intentionally deferred to a later phase (e.g. "all AC satisfied" is not expected after a stub-only phase); note the reason for skipping.
- For each issue found: fix it, run `make fmt && make test && make lint`, commit, and re-run the checklist until all applicable items pass.

Phase-group review checklist:
- [ ] Implementation is consistent with the design in `02_architecture.md`.
- [ ] Every AC that the implementation plan assigns to this phase group has at least one test that verifies it.
- [ ] Test coverage is sufficient: non-trivial logic, error paths, and boundary values are covered.
- [ ] No tests duplicate existing test coverage without good reason.
- [ ] No tests are so trivial that they add no verification value.
- [ ] No logic is reimplemented from scratch when an existing function in the codebase can be used.
- [ ] All source comments and identifiers are in English.
- [ ] No planning document references (e.g. `AC-01`, `F-001`) remain in source comments or string literals.
- [ ] `make fmt` produces no diff.
- [ ] `make lint` passes with no errors.
- [ ] `make test` passes with no errors.

7. Decide whether to continue or finish.
- If a phase group was just implemented (i.e. steps 5 and 6 were executed in this iteration):
  - Provide a concise summary of what was implemented in this phase group and which acceptance criteria were verified.
  - Note any assumptions made or items intentionally deferred.
- Check whether all phases in `03_implementation_plan.md` are now complete.
  - If **not all phases are complete**: ask the user "Shall I continue with the next phase group?" and return to step 4 if they agree; otherwise provide a status report and stop.
  - If **all phases are complete**: perform the final review without asking:
    - Verify that every acceptance criterion in `01_requirements.md` is satisfied by the implementation.
    - Verify that every AC has at least one test.
    - Report the final status and any remaining gaps.
