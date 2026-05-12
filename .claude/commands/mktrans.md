## Preparation

Get the target file path from the argument (e.g. `docs/dev/foo.ja.md`).
If no argument is provided, ask the user.

Verify that the target file is a Japanese document (`.ja.md`).
If not, stop.

Determine the output path for the English version (`.ja.md` → `.md`).
If the English version already exists, ask the user to confirm overwrite before continuing.

## Load Glossary

Read `docs/translation_glossary.md`.
Use the terms listed there consistently throughout the translation.

## Translate

Translate strictly following these principles:

- **Accuracy over fluency**: Prioritize precise translation over natural-sounding English.
- **Faithful translation**: Do not delete any content from the Japanese version. Do not add any content not present in the Japanese version.
- **Structural consistency**: Match chapter headings and sentence structure to the Japanese version.

Write the translated English version to the output path.

## Update Glossary

If any terms were used during translation that are not in the glossary, add them to `docs/translation_glossary.md`.
Skip this step if no new terms were introduced.

## Commit

Commit in the following order:
1. Commit the English file only (do not include glossary changes).
2. If the glossary was updated, commit that as a separate commit.

(No need to wait for user confirmation before committing.)
