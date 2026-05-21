---
name: git-commit
description: Use when the user asks to create a git commit from staged changes.
---

# Git Commit

1. Run `git --no-pager diff --staged`.
2. If there are no staged changes, report that there are no staged changes to
   commit and stop.
3. Propose an English commit message.
   - Do not use backquote characters.
   - Keep it concise and descriptive.
   - Use a single-line message for small changes.
   - For larger changes, use a summary line plus 3-5 bullet points.
   - Wrap body lines at 80 characters; keep the summary on one line.
4. Ask for confirmation with a y/n prompt.
5. If the user confirms, run `git commit` with the proposed message.
