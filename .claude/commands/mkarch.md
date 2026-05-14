Read the following documents before starting.

**Task documents** (in the current task directory under `docs/tasks/`):
- `01_requirements.md` — requirements and acceptance criteria (must be `approved`)

**Project guides** (read these in full; the document must conform to them):
- `docs/dev/developer_guide/requirements_process.md` — required sections and content guidelines for architecture documents
- `docs/dev/developer_guide/mermaid_reference.md` — Mermaid diagram conventions (node quoting, cylinder shape, `<br>` line breaks)
- `docs/dev/developer_guide/package_reference.md` — existing package structure and responsibilities (read if the task introduces new packages or modifies existing ones)

Based on these, create `02_architecture.md` in the same task directory.
- Write in Japanese
- Status must be `draft`
- Include all required sections from the requirements process guide
- Use Mermaid diagrams for concept model, system structure, and key processing flows
- Restrict code examples to high-level interfaces, types, and error type definitions only — no implementation details

After creating the file, review the entire document from the following perspectives and fix any issues found.

- [ ] Status is `draft`
- [ ] All functional requirements in `01_requirements.md` are reflected in the design
- [ ] Mermaid: data nodes use cylinder shape `[("label")]`; labels with special characters are double-quoted; line breaks use `<br>`
- [ ] No implementation details in code examples (only interfaces, type definitions, error types)
- [ ] Security section is present (write "N/A" if not applicable)
- [ ] Component responsibilities table lists all new and modified files
- [ ] No overlap with existing packages: the design does not re-implement logic already provided by another package in the codebase

When done, commit. (No need to wait for user confirmation.)
