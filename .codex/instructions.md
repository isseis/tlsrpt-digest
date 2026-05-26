# Patch strategy

Do not use apply_patch.

Use:
- python scripts
- perl -pi
- sed
- direct file rewrites

After edits:
- run git diff
- keep edits minimal
- avoid sandbox mount operations