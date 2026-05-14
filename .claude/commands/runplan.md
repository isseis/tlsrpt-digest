Your goal is to implement one task under `docs/tasks/` by following its `03_implementation_plan.md`.

Work in the following order.

1. Identify the target task directory.
- If the target task directory is already clear from the current context, use it.
- If multiple task directories are plausible and the target cannot be determined confidently, stop and report the candidate directories instead of guessing.

2. Read the required input documents.
- `01_requirements.md` in the target task directory
- `02_architecture.md` in the target task directory
- `03_implementation_plan.md` in the target task directory
- `docs/dev/developer_guide/test_organization.md`

3. Verify that implementation is allowed.
- Check the document status in `03_implementation_plan.md`.
- If the status is not `approved`, do not begin implementing.
- In that case, stop and report that implementation cannot begin until `03_implementation_plan.md` is `approved`.

4. Identify the next incomplete phase.
- Read the checkboxes in `03_implementation_plan.md`. Legend: `[ ]` not started, `[x]` done, `[-]` skipped.
- Work on the next phase that contains at least one unchecked `[ ]` item.
- If a phase has sub-sections, complete the entire phase before moving to the next.
- If all phases are complete, proceed directly to the review step (step 6).

5. Implement the current phase.
- Follow the design in `02_architecture.md` when implementing.
- Apply test helper rules from `docs/dev/developer_guide/test_organization.md`:
  - If new cross-package helpers or mocks are needed, place them under `testutil/` with the correct file naming and package naming.
  - If package-internal helpers are needed, place them in `test_helpers.go` or `test_helpers_<category>.go` with `//go:build test`.
- After each code change, run `make lint` and `make test` and fix any errors before continuing.
- When all items in the phase are complete, update the plan's checkboxes (`[x]` for done, `[-]` for skipped with a note) and commit.
- Return to step 4.

6. Review the implementation.
- Run `make deadcode` and remove any dead code made obsolete by this change. Commit if changes are made.
- Review the diff between the current branch and its base branch against the checklist below.
- Fix any issues found, commit, and re-run the checklist until all items pass.

Review checklist:
- [ ] All acceptance criteria in `01_requirements.md` are satisfied by the implementation.
- [ ] Implementation is consistent with the design in `02_architecture.md`.
- [ ] Every acceptance criterion in `01_requirements.md` has at least one test that verifies it.
- [ ] Test coverage is sufficient: non-trivial logic, error paths, and boundary values are covered.
- [ ] No tests duplicate existing test coverage without good reason.
- [ ] No tests are so trivial that they add no verification value.
- [ ] No logic is reimplemented from scratch when an existing function in the codebase can be used.
- [ ] All source comments and identifiers are in English.
- [ ] No planning document references (e.g. `AC-01`, `F-001`) remain in source comments or string literals.
- [ ] `make lint` passes with no errors.
- [ ] `make test` passes with no errors.

7. Commit the final state.

When finished, provide a concise summary of what you created and any assumptions you had to make. If your runtime instructions allow committing at this stage, commit with an English commit message after the review is complete.
