Your goal is to create `03_implementation_plan.md` for one task under `docs/tasks/`.

Work in the following order.

1. Identify the target task directory.
- If the target task directory is already clear from the current context, use it.
- If multiple task directories are plausible and the target cannot be determined confidently, stop and report the candidate directories instead of guessing.

2. Read the required input documents.
- `01_requirements.md` in the target task directory
- `02_architecture.md` in the target task directory
- `docs/dev/developer_guide/requirements_process.md`
- `docs/dev/developer_guide/test_organization.md`

3. Verify that implementation planning is allowed.
- Check the document status in `02_architecture.md`.
- If the status is not `approved`, do not create `03_implementation_plan.md`.
- In that case, stop and report that implementation planning cannot begin until `02_architecture.md` is `approved`.

4. Inspect the current codebase before writing the plan.
- Check the relevant packages, tests, and test helpers under `cmd/` and `internal/`.
- Identify existing functions, tests, and helper utilities that should be reused.
- Do not plan to re-implement logic or add duplicate tests when the repository already has suitable coverage or reusable helpers.

5. Create `03_implementation_plan.md` in the same task directory.
- Write in Japanese.
- Set the document status to `draft`.
- Include all required sections defined in `docs/dev/developer_guide/requirements_process.md`.
- Organize work into small, phase-based steps with checkboxes.
- Explicitly map each acceptance criterion to the tasks and tests that will verify it.
- Include at least one concrete test task for each acceptance criterion.
- Reference the architecture document instead of duplicating design details.
- Include specific file paths to modify where they can be identified confidently.
- Keep tasks actionable, observable, and small enough to complete and verify.

6. Apply test helper planning rules from `docs/dev/developer_guide/test_organization.md`.
- If new cross-package helpers or mocks are needed, plan them under `testutil/` with the correct file naming and package naming rules.
- If package-internal helpers are needed, plan them as `test_helpers.go` or `test_helpers_<category>.go` with `//go:build test`.
- Do not add helper files in the plan unless they are actually needed.

7. Review the document end to end and fix any issues you find before finishing.

Review checklist:
- [ ] `02_architecture.md` is `approved`.
- [ ] `03_implementation_plan.md` is written in Japanese and its status is `draft`.
- [ ] All required sections from the requirements process guide are present.
- [ ] Every acceptance criterion in `01_requirements.md` is addressed by at least one implementation task.
- [ ] The plan includes an explicit acceptance criteria verification section.
- [ ] Every acceptance criterion has at least one concrete test task.
- [ ] Test tasks cover non-trivial logic, error paths, and boundary values where applicable.
- [ ] The plan does not duplicate tests or re-test behavior that existing tests already cover without reason.
- [ ] The plan reuses existing implementations, tests, and helper utilities where appropriate.
- [ ] The plan references architecture sections instead of duplicating design details.
- [ ] The plan does not imply Japanese text in Go source comments, identifiers, or string literals.
- [ ] Any planned test helper files follow `docs/dev/developer_guide/test_organization.md`.
- [ ] Planned file paths are specific where known and do not conflict with existing package responsibilities.

When finished, provide a concise summary of what you created and any assumptions you had to make. If your runtime instructions allow committing at this stage, commit with an English commit message after the review is complete.
