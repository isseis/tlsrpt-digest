Read the following documents before starting.

**Task documents** (in the current task directory under `docs/tasks/`):
- `01_requirements.md` — requirements and acceptance criteria
- `02_architecture.md` — high-level design

**Project guides** (read these in full; the plan must conform to them):
- `docs/dev/developer_guide/requirements_process.md` — process for AC traceability and implementation plans
- `docs/dev/developer_guide/test_organization.md` — test helper file placement, naming, build tags, and package naming rules

Based on these, create `03_implementation_plan.md`.
- Break tasks into small steps with checkboxes
- Explicitly document traceability between each acceptance criterion (AC) and the corresponding tasks
- Include a test task for each AC

After creating the file, review the entire document from the following perspectives and fix any issues found.

- [ ] AC coverage: every acceptance criterion in `01_requirements.md` is addressed by at least one task in the plan
- [ ] Test sufficiency: test tasks exist for non-trivial logic, error paths, and boundary values
- [ ] No duplicate tests: no tasks re-test what existing tests already cover
- [ ] Reuse existing implementations: no tasks implement logic from scratch when an existing function in the codebase can be used
- [ ] No Japanese in code: no tasks that would result in Japanese text in Go source file comments, identifiers, or string literals
- [ ] Test file placement: each test helper file is placed in `testutil/` (Classification A) or as `test_helpers.go` (Classification B) per test_organization.md, with `//go:build test` and correct package name

When done, commit. (No need to wait for user confirmation.)
