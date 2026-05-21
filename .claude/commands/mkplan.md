Your goal is to create `03_implementation_plan.md` for one task under `docs/tasks/`.

Work in the following order.

1. Identify the target task directory by following the rules in `docs/dev/developer_guide/task_identification.md`.

2. Read `02_architecture.md` in the target task directory.

3. Verify that implementation planning is allowed.
- Check the document status in `02_architecture.md`.
- If the status is not `approved`, do not create `03_implementation_plan.md`.
- In that case, stop and report that implementation planning cannot begin until `02_architecture.md` is `approved`.

4. Read the remaining required input documents.
- `01_requirements.md` in the target task directory
- `docs/dev/developer_guide/requirements_process.md`
- `docs/dev/developer_guide/test_organization.md`

5. Inspect the current codebase before writing the plan.
- Check the relevant packages, tests, and test helpers under `cmd/` and `internal/`.
- Identify existing functions, tests, and helper utilities that should be reused.
- Do not plan to re-implement logic or add duplicate tests when the repository already has suitable coverage or reusable helpers.

6. Create `03_implementation_plan.md` in the same task directory.
- Write in Japanese.
- Set the document status to `draft`.
- Include all required sections defined in `docs/dev/developer_guide/requirements_process.md`.
- Organize work into small, phase-based steps with checkboxes.
- Explicitly map each acceptance criterion to the tasks and tests that will verify it.
- Include at least one concrete test task for each acceptance criterion.
- Reference the architecture document instead of duplicating design details.
- Include specific file paths to modify where they can be identified confidently.
- Keep tasks actionable, observable, and small enough to complete and verify.

7. Apply test helper planning rules from `docs/dev/developer_guide/test_organization.md`.
- If new cross-package helpers or mocks are needed, plan them under `testutil/` with the correct file naming and package naming rules.
- If package-internal helpers are needed, plan them as `test_helpers.go` or `test_helpers_<category>.go` with `//go:build test`.
- Do not add helper files in the plan unless they are actually needed.

8. Spawn a review subagent using the Agent tool to critically evaluate the created document.
   Construct a self-contained prompt that includes all of the following:
   - **Persona**: act as an experienced senior engineer and senior SRE whose job is to find real problems — not to approve. Be thorough and unsparing. Surface gaps, missing test coverage, vague task descriptions, and AC traceability holes. Do not soften findings.
   - **Files to read**: embed the resolved absolute paths of `03_implementation_plan.md`, `02_architecture.md`, `01_requirements.md`, `docs/dev/developer_guide/requirements_process.md`, and `docs/dev/developer_guide/test_organization.md` as literal strings in the prompt so the subagent can read them without relying on your context.
   - **Evaluation criteria**: every item from the Technical correctness checklist and the Readability and consistency checklist below, copied verbatim.
   - **Output format**: for each issue found, report Severity (Critical / Major / Minor), Location (section name or checklist item), Problem (what is wrong or missing), and Suggestion (concrete fix). If a checklist category has no issues, state that explicitly.

   After receiving findings:
   - Fix all Critical and Major issues.
   - Apply Minor fixes at your discretion.
   - If more than one Critical or Major issue required a fix, spawn a second review subagent to verify the fixes. Repeat until the subagent reports no Critical or Major issues, up to a maximum of three passes.
   - Commit only after all review passes are complete and all Critical and Major issues are resolved.

**Technical correctness checklist (use verbatim as evaluation criteria in the subagent prompt above):**
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

**Readability and consistency checklist (use verbatim as evaluation criteria in the subagent prompt above):**
- [ ] Terminology is consistent with `02_architecture.md`; the same concept always uses the same Japanese term.
- [ ] Task descriptions are phrased as clear, actionable instructions. Vague verbs (e.g., "対応する", "実装する") are replaced with specific actions where possible.
- [ ] Redundant or repetitive content is removed; design details already in the architecture document are referenced rather than restated.
- [ ] Ambiguous or overly terse expressions are rewritten in direct, plain Japanese so readers do not need prior context to understand what is expected.

When finished, provide a concise summary of what you created and any assumptions you had to make.
