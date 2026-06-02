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

**[Always required] structural and traceability rules — apply to every plan:**
- Write in Japanese; set the document status to `draft`.
- Include all required sections from `docs/dev/developer_guide/requirements_process.md`, plus explicit top-level sections for: Implementation Order and Milestones, Test Strategy, Implementation Checklist, Acceptance Criteria Verification. Do not let phase task lists implicitly substitute for these.
- Add a `既存コード調査結果` subsection under the implementation overview (§1) with the findings from step 5. If none, state explicitly that no existing code changes are required.
- Organize work into small, phase-based steps with checkboxes. When a step deletes multiple distinct, separately-named entities (e.g. several test functions), give each its own checkbox rather than a batch instruction (`Test*_全件削除`). (Uniform edits across many call sites may still be described by one pattern — see "change sites" below.)
- Map each acceptance criterion to the tasks and tests that verify it. Every AC row in the Acceptance Criteria Verification section must name either an exact test location in `path::TestName` format, or an explicit static verification command with its expected result. Do not use vague labels ("compile passes", "document review", "grep check", "none"); spell out the exact `rg` command and what counts as success.
- Label each AC verification as `test` (executable, fails on wrong behavior), `static` (rg/grep/compile), or `manual` (PR observation, deploy check). A static `rg` check is valid only when the AC is purely about textual presence/absence; behavior encoded in scripts, workflow YAML, env handling, or command routing needs an executable test.
- Keep tasks actionable, observable, and small enough to complete and verify. Include specific file paths where confidently known.
- Add a cross-search checklist for removed or redefined concepts, with explicit `rg` commands/patterns and expected results for code, tests, docs, and translation/glossary files in scope.
- Any planned Go source comment, identifier, or string literal must be written in English (the plan prose itself may be Japanese).

**[When applicable] apply only when the trigger matches:**
- Documentation-only ACs: create concrete verification tasks; include every touched doc (glossaries, translations) in the AC table or cross-search checklist. Example: `rg -n -e "old term" docs/file.md` expected: no matches except allowed historical notes.
- New authored content (prose, tables, command examples, runbooks, translations): verify against ground truth, not only absence-search — run the documented command and confirm exit code/output, cite the source implementation the text describes, or diff a translation against its source. When a table groups several cases under one entry, confirm their behavior/remediation is truly identical, else split the entry.
- Text edits to a string literal, error message, or source comment: state the complete before/after string, not just the substring to change. This prevents dropped prefixes, dangling `%w`, or stray trailing spaces.
  - Bad:  "Remove `(or --abort-reset --yes)` from the `systemErrorHint` return value."
  - Good: "Change the `systemErrorHint` return value from `"Run: tlsrpt-digest recover --mode discard-old --yes (or --abort-reset --yes)"` to `"Run: tlsrpt-digest recover --mode discard-old --yes"`."
- Change sites spread across many locations: describe them by grep pattern (e.g. "all `_ = notifyXxx(...)` call sites"), not line numbers, which go stale. Use line numbers only when the pattern alone cannot locate the spot.

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
- [ ] Creation tasks (new prose, tables, command examples, runbooks, translations) each have a verification task that checks the content against ground truth, not only an absence-search.
- [ ] The plan does not duplicate tests or re-test behavior that existing tests already cover without reason.
- [ ] The plan reuses existing implementations, tests, and helper utilities where appropriate.
- [ ] The plan references architecture sections instead of duplicating design details.
- [ ] The plan does not imply Japanese text in Go source comments, identifiers, or string literals.
- [ ] Any planned test helper files follow `docs/dev/developer_guide/test_organization.md`.
- [ ] Planned file paths are specific where known and do not conflict with existing package responsibilities.

**Conditional checks (apply each only when its trigger matches; mark N/A otherwise — N/A is not a finding):**
- [ ] Integration tests touch external/long-lived state (containers, DBs, mailboxes, queues, files outside `t.TempDir`) → rerun isolation is planned: identifiers unique per run (not just per test name), and tests locate their own artifacts by a stable marker instead of assuming result length/ordering.
- [ ] Assertions on values returned by external systems (message IDs, headers, URLs) → a normalization helper compares normalized expected vs normalized actual, not exact string equality against the injected value.
- [ ] A security-linter-flagged construct is introduced (`InsecureSkipVerify`, `sql.Open` with interpolation, `reflect.DeepEqual` on interfaces, etc.) → a narrow `//nolint:<linter>` task scoped to the minimum block, with a rationale comment, is planned — never file- or package-wide.
- [ ] CI/workflow or change-detection logic has two or more conditions → it is extracted to a standalone script with a test feeding representative inputs and asserting outputs. `actionlint` covers syntax only, not behavior.
- [ ] Environment-variable skip logic exists → detection is a pure `missing...Env(getenv func(string) string) []string` helper with a thin `t.Skip` wrapper, unit-tested for missing required vars, invalid values (e.g. non-integer port), and successful propagation.
- [ ] Phase names/order match the approved architecture's implementation priorities. A prerequisite for a later phase is stated within that phase, not by renumbering; if the order must change, the architecture is revised first.
- [ ] Every cleanup/close/logout call in the plan (`store.Close()`, `conn.Logout()`, `t.Cleanup`, …) names a real API confirmed to exist, or an explicit task to add it — no invented lifecycle methods.

**Readability and consistency checklist (use verbatim as evaluation criteria in the subagent prompt above):**
- [ ] Terminology is consistent with `02_architecture.md`; the same concept always uses the same Japanese term.
- [ ] Task descriptions are phrased as clear, actionable instructions. Vague verbs (e.g., "対応する", "実装する") are replaced with specific actions where possible.
- [ ] Redundant or repetitive content is removed; design details already in the architecture document are referenced rather than restated.
- [ ] Ambiguous or overly terse expressions are rewritten in direct, plain Japanese so readers do not need prior context to understand what is expected.

When finished, provide a concise summary of what you created and any assumptions you had to make.
