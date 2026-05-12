## Implementation

1. Read `03_implementation_plan.md` under `docs/tasks/` and identify which steps have been completed.
   Legend: `[ ]` not started, `[x]` done, `[-]` skipped.
   Work on the next incomplete phase. If a phase has sub-sections (e.g. 2.1, 2.2), complete the entire phase before moving to the next.
2. Follow the design in `02_architecture.md` when implementing.
3. After each code change, run `make lint` and `make test` and fix any errors.
4. At the end of each phase, update the plan's checkboxes (done → `[x]`, skipped → `[-]`) and commit.
5. Repeat steps 1–4 until all phases are complete, then proceed to review.

## Review

1. Run `make deadcode` and check for any dead code made obsolete by the current changes. Remove any found and commit.
2. Review the diff between the current branch and its base branch. Check:
   - All acceptance criteria in `01_requirements.md` are satisfied
   - Implementation is consistent with the design in `02_architecture.md`
   - Test coverage is sufficient (non-trivial logic, error paths, and boundary values are covered)
   - No duplicate tests with existing tests; no tests so trivial they add no value
   - No logic reimplemented from scratch when an existing function in the codebase can be used
   - All comments are in English
   - No development document references (e.g. AC-1) left in comments
3. Fix any issues found, commit, and return to step 2. If no issues remain, proceed to PR creation.

## PR Creation

1. If a PR for this branch already exists on GitHub, run `git push` to update it.
2. If no PR exists, run `git push` and create a draft PR.
