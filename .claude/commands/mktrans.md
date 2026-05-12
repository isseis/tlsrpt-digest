## Preparation

Get the target file path from the argument (e.g. `docs/dev/foo.ja.md`).
If no argument is provided, ask the user.

Verify that the target file is a Japanese document (`.ja.md`).
If not, stop.

Determine the output path for the English version (`.ja.md` → `.md`).

## Mode Selection: Full Translation or Differential Translation

**If the English version does not exist**: proceed with full translation (see "Full Translation" section).

**If the English version already exists**: proceed with differential translation (see "Differential Translation" section).

---

## Full Translation

Translate the entire Japanese file from scratch.

### Load Glossary

Read `docs/translation_glossary.md`.
Use the terms listed there consistently throughout the translation.

### Translate

Translate strictly following these principles:

- **Accuracy over fluency**: Prioritize precise translation over natural-sounding English.
- **Faithful translation**: Do not delete any content from the Japanese version. Do not add any content not present in the Japanese version.
- **Structural consistency**: Match chapter headings and sentence structure to the Japanese version.

Write the translated English version to the output path.

---

## Differential Translation

Translate only the sections that changed in the Japanese file since the English file was last updated.

### Find the Sync Point

Run the following command to find the last commit that modified the English file:

```bash
git log -1 --format=%H -- <en-file>
```

Record the commit hash (call it `SYNC_HASH`). If no hash is returned (e.g., the file is not yet committed), stop and inform the user that differential translation requires the English file to be committed.

### Get the Diff

Run:

```bash
git diff SYNC_HASH -- <ja-file>
```

If the diff is empty, the English file is already up to date. Stop and report this to the user.

### Load Glossary

Read `docs/translation_glossary.md`.
Use the terms listed there consistently throughout the translation.

### Translate Only Changed Sections

From the diff, identify which sections (by heading) were added, modified, or removed.

For each changed section:
- **Added lines** (`+`): translate the new Japanese content into English.
- **Removed lines** (`-`): identify the corresponding content in the English file and remove it.
- **Modified lines**: translate the new Japanese content and replace the old English content.

Apply these changes to the English file. Do not touch sections that are not in the diff.

---

## Update Glossary

If any terms were used during translation that are not in the glossary, add them to `docs/translation_glossary.md`.
Skip this step if no new terms were introduced.

## Commit

Commit in the following order:
1. Commit the English file only (do not include glossary changes).
2. If the glossary was updated, commit that as a separate commit.

(No need to wait for user confirmation before committing.)
