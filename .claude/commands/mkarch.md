Your goal is to create `02_architecture.md` for one task under `docs/tasks/`.

Work in the following order.

1. Identify the target task directory by following the rules in `docs/dev/developer_guide/task_identification.md`.

2. Read the required input documents.
- `01_requirements.md` in the target task directory
- `docs/dev/developer_guide/requirements_process.md`
- `docs/dev/developer_guide/mermaid_reference.md`

3. Verify that architecture work is allowed.
- Check the document status in `01_requirements.md`.
- If the status is not `approved`, do not create `02_architecture.md`.
- In that case, stop and report that architecture work cannot begin until `01_requirements.md` is `approved`.

4. Read conditional guidance only when relevant.
- Read `docs/dev/developer_guide/notification_security.md` if the feature sends notifications or handles notification destinations.
- Read `docs/dev/developer_guide/package_reference.md` if that file exists and the task introduces new packages or modifies existing packages.

5. Inspect the current codebase before writing the design.
- Check the relevant packages under `cmd/` and `internal/`.
- Identify existing components that should be reused.
- Do not design new logic that duplicates responsibilities already handled elsewhere in the repository.

6. Create `02_architecture.md` in the same task directory.
- Write in Japanese.
- Set the document status to `draft`.
- Include all required sections defined in `docs/dev/developer_guide/requirements_process.md`.
- Reflect all functional requirements and acceptance criteria from `01_requirements.md`.
- Use Mermaid diagrams for the concept model, system structure, key processing flows, and a threat model when applicable.
- Restrict code examples to high-level interfaces, type definitions, and error type definitions only.
- Do not include implementation details, pseudocode, step-by-step algorithms, or low-level code.

7. Review the document end to end and fix any issues you find before finishing.

Review checklist:
- [ ] `01_requirements.md` is `approved`.
- [ ] `02_architecture.md` is written in Japanese and its status is `draft`.
- [ ] All required sections from the requirements process guide are present.
- [ ] All functional requirements and acceptance criteria in `01_requirements.md` are reflected in the design.
- [ ] Mermaid diagrams follow the documented conventions consistently.
- [ ] Data nodes use cylinder shape `[("label")]`.
- [ ] Labels with special characters are double-quoted.
- [ ] Line breaks inside labels use `<br>`.
- [ ] Code examples contain only interfaces, type definitions, and error type definitions.
- [ ] No implementation details, pseudocode, or concrete algorithms are included.
- [ ] The security section is present and uses `N/A` when not applicable.
- [ ] For notification-related features, the security section reflects `notification_security.md`.
- [ ] The component responsibilities table lists all new and modified files.
- [ ] The design does not overlap with existing packages or re-implement existing responsibilities.

When finished, provide a concise summary of what you created and any assumptions you had to make. If your runtime instructions allow committing at this stage, commit with an English commit message after the review is complete.
