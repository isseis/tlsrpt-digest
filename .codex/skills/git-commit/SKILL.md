---
name: git-commit
description: Use when the user asks to create or amend a git commit, or when another skill needs shared git commit message guidelines.
---

# Git Commit

Use this as the shared commit workflow whenever any skill or task flow
creates or amends a git commit. If another skill says to commit, apply these
message guidelines and confirmation steps unless the user has already approved
an exact message.

1. Run `git --no-pager diff --staged`. For a message-only amend, inspect the
   latest commit instead.
2. For a new commit, if there are no staged changes, report that there are
   no staged changes to commit and stop.
3. Propose an English commit message.
   - Do not use backquote characters.
   - Keep it concise and descriptive.
   - Use a single-line message for small changes.
   - For larger changes, use a summary line plus 3-5 bullet points.
   - Wrap body lines at 80 characters; keep the summary on one line.
4. Ask for confirmation with a y/n prompt.
5. If the user confirms, run `git commit -m "<message>"` for a new commit
   or `git commit --amend` with the proposed message for an amend.
