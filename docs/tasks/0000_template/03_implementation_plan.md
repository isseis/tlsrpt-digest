# Implementation Plan: [Feature Name]

## Document Status

| Item | Value |
|---|---|
| Status | `draft` |
| Created | YYYY-MM-DD |
| Review date | - |
| Reviewer | - |
| Comments | - |

## 1. Implementation Overview

- **Purpose**: ...
- **Implementation principles**: Follow the design in `02_architecture.md`.

---

## 2. Implementation Phases

### Phase 1: [Phase Name]

- [ ] **1.1** [Task Name]
  - File: `internal/xxx/yyy.go`
  - Work content: ...

- [ ] **1.2** [Task Name]
  - File: `internal/xxx/yyy_test.go`
  - Work content: Implement test cases XX-01 through XX-03.

### Phase 2: [Phase Name]

- [ ] **2.1** [Task Name]
  - File: `internal/zzz/www.go`
  - Work content: ...

---

## 3. Acceptance Criteria Traceability

Record the mapping between each acceptance criterion in `01_requirements.md` and its corresponding test.

`AC-1`: [Condition 1 of F-001]
- Test: `internal/xxx/yyy_test.go::TestXxx`
- Implementation: `internal/xxx/yyy.go:XX-YY`

`AC-2`: [Condition 2 of F-001]
- Test: `internal/xxx/yyy_test.go::TestXxx_ErrorCase`
- Implementation: `internal/xxx/yyy.go:ZZ-WW`

---

## 4. Completion Criteria

- [ ] `make lint` completes without errors
- [ ] `make test` passes for all tests
- [ ] Tests exist for all acceptance criteria in `01_requirements.md`
- [ ] `make deadcode` reports no unused code
