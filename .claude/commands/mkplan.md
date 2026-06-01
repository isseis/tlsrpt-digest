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
- Identify existing functions, tests, and helper utilities that should be reused. Do not plan to re-implement logic or add duplicate tests when the repository already has suitable coverage or reusable helpers.
- For each relevant area, note: what already exists, what is missing, and what needs to change. Omit areas where the existing code requires no attention.
- **Verify symbols and enumerate all instances before writing.** Confirm every cited function, test, variable, or error name exists in the expected file; note which file when similar names appear in multiple locations. For any symbol or pattern the plan will change or delete, find ALL occurrences across code, comments, test names, error strings, docs, and translations. Cover all search variants: function/variable names, CLI flags, error messages, numeric values, old terminology, and comment phrases. Map each result to its enclosing function and planned action. Add explicit cleanup tasks for stale comments and documentation.
  - Example: `rg -n "TestBootstrap_PendingReset" cmd/` to confirm file; `rg -n "resetPhase\((2|3|5)\)" internal/store -g "*_test.go"` to map every legacy seed.
- **Trace behavioral impact and coverage gaps.** When a function's return values or error conditions change, enumerate its callers and assess whether their observable behavior also changes. Before deleting a test, confirm each non-trivial invariant it verifies is covered by a surviving test, or add an explicit replacement test to the plan.

6. Create `03_implementation_plan.md` in the same task directory.
- Write in Japanese.
- Set the document status to `draft`.
- Include all required sections defined in `docs/dev/developer_guide/requirements_process.md`.
- Include explicit top-level sections for:
  - Implementation Order and Milestones
  - Test Strategy
  - Implementation Checklist
  - Acceptance Criteria Verification
  Do not rely on phase task lists as an implicit substitute for these required sections.
- Add a `既存コード調査結果` subsection under the implementation overview (§1), incorporating the detailed findings from step 5. If no findings were identified in step 5, explicitly state that no existing code changes are required.
- Organize work into small, phase-based steps with checkboxes.
- Explicitly map each acceptance criterion to the tasks and tests that will verify it.
- Include at least one concrete test task for each acceptance criterion.
- In the Acceptance Criteria Verification section, every AC row must name either:
  - an exact test location in `path::TestName` format, or
  - an explicit static verification command with its expected result.
- Do not use vague verification labels such as "compile passes", "document review", "grep check", or "none". If static verification is intended, spell out the exact `rg` command and what counts as success.
- For documentation-only ACs, create concrete verification tasks. Include every documentation file touched by the plan, including glossaries and translation outputs, in the AC verification table or cross-search checklist.
  - Example: `rg -n -e "old term" docs/file.md` expected: no matches except explicitly allowed historical notes.
- Reference the architecture document instead of duplicating design details.
- Include specific file paths to modify where they can be identified confidently.
- Keep tasks actionable, observable, and small enough to complete and verify.
- The implementation plan itself may be Japanese, but any planned Go source comment, identifier, string literal, or test comment replacement must be written in English.
- When describing change sites, prefer pattern-based descriptions (e.g., "all `_ = notifyXxx(...)` call sites") over exact line numbers. Line numbers become stale on the first unrelated edit; grep patterns remain valid. Use line numbers only when the specific location is essential context that the pattern alone cannot convey.
- **Specify complete before/after strings for all text edits.** When a task modifies a string literal, error message, or source comment, state the full result string explicitly — not just the substring to remove. This prevents unintended side-effects such as dropped prefixes, dangling format verbs (`%w`), or trailing spaces left by a deleted parenthetical.
  - Bad:  "Remove `(or --abort-reset --yes)` from the `systemErrorHint` return value."
  - Good: "Change the `systemErrorHint` return value from `"Run: tlsrpt-digest recover --mode discard-old --yes (or --abort-reset --yes)"` to `"Run: tlsrpt-digest recover --mode discard-old --yes"`."
- Add a cross-search checklist for removed or redefined concepts. It must include explicit `rg` commands or pattern lists and expected results for code, tests, docs, and translation/glossary files when those files are in scope.

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
   - If any Critical or Major issue required a fix, spawn a second review subagent to verify the fixes. Repeat until no Critical or Major issues remain, up to three passes total.
   - After three passes, continue only if remaining Critical or Major issues are concrete, scoped to this document, and clearly fixable without expanding the planning scope. Otherwise, stop and report the remaining issues.
   - Commit `03_implementation_plan.md` only after all review passes are complete and all Critical and Major issues are resolved.

**Technical correctness checklist (use verbatim as evaluation criteria in the subagent prompt above):**
- [ ] `02_architecture.md` is `approved`.
- [ ] `03_implementation_plan.md` is written in Japanese and its status is `draft`.
- [ ] All required sections from the requirements process guide are present.
- [ ] Every acceptance criterion in `01_requirements.md` is addressed by at least one implementation task.
- [ ] For each acceptance criterion that covers an existing code pattern, the plan addresses ALL instances of that pattern in the codebase. Verify by searching the codebase for the pattern before assessing completeness.
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
