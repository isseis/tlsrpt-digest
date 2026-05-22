## Preparation

Get the source file path from the argument.
If no argument is provided, ask the user.

Determine the translation direction from the file extension:

- Source ends in `.ja.md` → **Japanese → English**
  - Source: `foo.ja.md`
  - Output: `foo.md`
- Source ends in `.md` (but not `.ja.md`) → **English → Japanese**
  - Source: `foo.md`
  - Output: `foo.ja.md`

If the source file does not match either pattern, stop and ask the user to clarify.

## Mode Selection: Full Translation or Differential Translation

**If the output file does not exist**: proceed with full translation (see "Full Translation" section).

**If the output file already exists**: proceed with differential translation (see "Differential Translation" section).

---

## Full Translation

Translate the entire source file from scratch.

### Load Glossary

Read `docs/translation_glossary.md`.
Use the terms listed there consistently throughout the translation.

### Translate

Translate strictly following these principles:

- **Accuracy over fluency**: Prioritize precise translation over natural-sounding output.
- **Faithful translation**: Do not delete any content from the source. Do not add any content not present in the source.
- **Structural consistency**: Match chapter headings and sentence structure to the source.

Write the translated output to the output path.

---

## Differential Translation

Translate only the sections that changed in the source file since the output file was last updated.

### Find the Sync Point

Run the following command to find the last commit that modified the output file:

```bash
git log -1 --format=%H -- <output-file>
```

Record the commit hash (call it `SYNC_HASH`). If no hash is returned (e.g., the output file is not yet committed), stop and inform the user that differential translation requires the output file to be committed.

### Get the Diff

Run:

```bash
git diff SYNC_HASH -- <source-file>
```

If the diff is empty, the output file is already up to date. Stop and report this to the user.

### Load Glossary

Read `docs/translation_glossary.md`.
Use the terms listed there consistently throughout the translation.

### Translate Only Changed Sections

From the diff, identify which sections (by heading) were added, modified, or removed.

For each changed section:
- **Added lines** (`+`): translate the new source content into the target language.
- **Removed lines** (`-`): identify the corresponding content in the output file and remove it.
- **Modified lines**: translate the new source content and replace the old output content.

Apply these changes to the output file. Do not touch sections that are not in the diff.

---

## Update Glossary

If any terms were used during translation that are not in the glossary, add them to `docs/translation_glossary.md`.
Skip this step if no new terms were introduced.

## Review the Translation (via Subagent)

Spawn a review subagent using the Agent tool to critically evaluate the translation.
Construct a self-contained prompt that includes all of the following:
- **Persona**: act as an experienced technical translator and editor whose job is to find real problems — not to approve. Be thorough and unsparing. Surface omissions, additions not in the source, mistranslations, and inconsistent terminology. Do not soften findings.
- **Files to read**: embed the resolved absolute path of each of the following as a literal string in the prompt so the subagent can read them without relying on your context: the source file, the translated output file, and `docs/translation_glossary.md` (resolve to its absolute path as well).
- **Evaluation criteria**: every item from the Accuracy checklist and the Readability checklist below, copied verbatim.
- **Output format**: for each issue found, report Severity (Critical / Major / Minor), Location (section heading or paragraph), Problem (what is wrong), and Suggestion (concrete fix). If a checklist category has no issues, state that explicitly.

After receiving findings:
- Fix all Critical and Major issues.
- Apply Minor fixes at your discretion.
- If any Critical or Major issue required a fix, spawn a second review subagent to verify the fixes. Repeat, subject to the three-pass limit below, until the subagent reports no Critical or Major issues.
- After three review passes, continue only if the remaining Critical or Major issues are concrete, scoped to this document, and clearly fixable without expanding the translation scope. Otherwise, stop and report the remaining issues instead of continuing automatically.

**Accuracy checklist (use verbatim as evaluation criteria in the subagent prompt above):**
- [ ] No content from the source is missing in the translation.
- [ ] No content was added that is not present in the source.
- [ ] All technical terms match the glossary.

**Readability checklist (use verbatim as evaluation criteria in the subagent prompt above):**

The translation principles (Accuracy over fluency, Structural consistency) take precedence. The following checks apply only within those constraints: do not restructure sentences, reorder clauses, or rephrase in ways that would diverge from the source structure.

- [ ] Word choices that are technically correct but unnecessarily obscure are replaced with clearer equivalents that carry the same meaning and preserve the source structure.
- [ ] Terminology is used consistently throughout the translation; the same concept always uses the same term in the target language.
- [ ] Sentence structure follows target-language conventions where the source structure permits it; literal carry-overs from source syntax that produce unnatural output are corrected as long as doing so does not alter meaning or structure.

## Commit

Commit only after all review passes are complete and all Critical and Major issues are resolved.

Commit in the following order:
1. Commit the translated file only (do not include glossary changes).
2. If the glossary was updated, commit that as a separate commit.

(No need to wait for user confirmation before committing.)
