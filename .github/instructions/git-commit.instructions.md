---
applyTo: '**'
---
1. Get staged change `git --no-pager diff --staged`. If the output is empty, inform the user that there are no staged changes to commit and stop.
2. Propose commit message for it.
  - The commit message must be in English
  - The commit message must not contain backquote characters (`).
  - The commit message should be concise and descriptive.
  - One line summary + 3-5 bullets points would be expected. If the change is complex and large, longer and more detailed message is acceptable.
  - The commit message body should be broken down into lines every 80 characters (the summary line should remain a single line).
3. Ask confirmation for proceeding commit with y/n prompt
4. If a user lets move forward, commit the change `git commit` with the proposed commit message.
