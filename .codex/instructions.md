# Patch strategy

Do not use apply_patch.

Use:
- python scripts
- perl -pi
- sed
- direct file rewrites

After edits:
- run git diff HEAD -- <files>
- keep edits minimal
- avoid sandbox mount operations