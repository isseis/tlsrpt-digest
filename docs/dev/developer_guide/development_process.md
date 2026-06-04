# Developer Onboarding Guide

This guide is for engineers joining the project for the first time. It explains the flow from development environment setup through feature development and PR merge.

---

## 1. Development Environment Setup

### Recommended: VS Code + devcontainer

The recommended approach is to use the [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) extension for VS Code. All required tools — Go, golangci-lint, gofumpt, and others — are included in the container, so no host environment dependencies are required. The host OS can be Linux, macOS, or Windows.

**Steps:**

1. Install [VS Code](https://code.visualstudio.com/) and [Docker](https://www.docker.com/)
2. Install the [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) in VS Code
3. Clone the repository on the **host machine**:
   ```bash
   git clone https://github.com/isseis/tlsrpt-digest.git
   ```
4. Open the cloned directory in VS Code (`code tlsrpt-digest`, or use File → Open Folder)
5. When VS Code detects the devcontainer configuration, select "Reopen in Container"
6. Once the container is running, verify the build in the VS Code terminal (inside the container):
   ```bash
   make build && make test && make lint
   ```

### Setting Up a Local Environment Directly

If you are not using devcontainer, install the following tools. For the versions used in devcontainer, see [`.devcontainer/Dockerfile`](../../../.devcontainer/Dockerfile).

**Go** (1.26 or later)

Download an installer for your OS from [https://go.dev/dl/](https://go.dev/dl/), or use a package manager:

```bash
# macOS (Homebrew)
brew install go

# Ubuntu / Debian
sudo apt-get install golang-go
```

**golangci-lint**

```bash
# Official install script (Linux / macOS)
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# macOS (Homebrew)
brew install golangci-lint
```

**gofumpt**

```bash
go install mvdan.cc/gofumpt@latest
```

**Claude Code**

```bash
npm install -g @anthropic-ai/claude-code
```

After installing, clone the repository and verify the build:

```bash
git clone https://github.com/isseis/tlsrpt-digest.git
cd tlsrpt-digest

make build && make test && make lint
```

If all commands complete without errors, the environment is ready.

---

## 2. Codebase Overview

For the overall project design, see the [Project Overview](../../overview.md). The directory structure and responsibilities of each package are described in the [Package Reference](package_reference.md).

---

## 3. Development Workflow

New features are developed in the following order: **requirements definition → architecture design → implementation plan → implementation**. Each phase requires human review and approval before proceeding to the next. Claude Code slash commands assist with document generation at each phase.

### Overall Flow

```mermaid
flowchart TD
    A["Create task directory<br>docs/tasks/NNNN_name/"]
    B["Create 01_requirements.md<br>(using template)"]
    C{"Requirements review<br>and approval"}
    D["Create 02_architecture.md<br>/mkarch"]
    E{"Architecture<br>review and approval"}
    F["Create 03_implementation_plan.md<br>/mkplan"]
    G{"Implementation plan<br>review and approval"}
    H["Design PR boundaries<br>/mkplan2"]
    I["Implement and create PRs<br>/runplan"]
    J{"PR review"}
    K["Address review comments<br>/fixpr"]
    L["Merge"]

    A --> B --> C
    C -- "Rejected" --> B
    C -- "Approved" --> D --> E
    E -- "Rejected" --> D
    E -- "Approved" --> F --> G
    G -- "Rejected" --> F
    G -- "Approved" --> H --> I --> J
    J -- "Comments" --> K --> I
    J -- "Approved" --> L
```

---

## 4. Step-by-Step Details

### Step 1: Create the Task Directory

Create a directory under `docs/tasks/` in the format `NNNN_task-name`. `NNNN` is a four-digit sequential number, one greater than the highest existing task number.

```bash
mkdir docs/tasks/0042_new_feature
```

### Step 2: Create the Requirements Document (`01_requirements.md`)

Copy `docs/tasks/0000_template/01_requirements.ja.md` and edit it.

**Required sections:**
- Background and purpose
- Scope (in scope / out of scope)
- Functional requirements with **acceptance criteria (`AC-NN`)**: each criterion must describe a specific, independently verifiable behavior
- Non-functional requirements and constraints

For detailed guidance on writing acceptance criteria, see the [Requirements and Acceptance Criteria Process](requirements_process.md).

> **Important:** Documents must always be created in `draft` status. Do not proceed to the next step before review and approval.

### Step 3: Review and Approve the Requirements Document

A human reviewer examines the document and, if there are no issues, updates the status section at the top:

```markdown
## Document Status

| Item | Content |
|---|---|
| Status | `approved` |
| Created | YYYY-MM-DD |
| Reviewed | YYYY-MM-DD |
| Reviewer | Name |
| Comments | - |
```

### Step 4: Create the Architecture Design Document (`/mkarch`)

Run `/mkarch` in Claude Code. You can also specify the task directory as an argument.

```
/mkarch
/mkarch 0042        # Specify task by number
```

The command inspects the current codebase and generates `02_architecture.md`. The design document includes Mermaid diagrams, component structure, error handling design, and test strategy. After generation, an automated review runs and any issues are fixed.

For how the command identifies the task directory, see [Task Identification](task_identification.md).

### Step 5: Review and Approve the Architecture Design Document

A human reviewer reviews the document and updates its status to `approved`.

### Step 6: Create the Implementation Plan (`/mkplan`)

```
/mkplan
/mkplan 0042
```

The command inspects the codebase and generates `03_implementation_plan.md` as a checkbox-based implementation plan. It includes implementation tasks for each phase and an acceptance criteria (AC) traceability table mapping each AC to its tests.

### Step 7: Review and Approve the Implementation Plan

A human reviewer reviews the document and updates its status to `approved`.

### Step 8: Design PR Boundaries (`/mkplan2`)

When the plan has multiple phases, define the PR boundaries.

```
/mkplan2
/mkplan2 0042
```

`### PR-N checkpoint` sections are appended to `03_implementation_plan.md`. This allows `/runplan` to create PRs at the appropriate PR-N checkpoints.

### Step 9: Implement and Create PRs (`/runplan`)

```
/runplan
/runplan 0042
```

The command processes the implementation plan checkboxes in order. After each file change, it runs `make fmt && make test && make lint` and fixes any errors. When it reaches a PR-N checkpoint, it automatically creates a PR and stops. After the PR is merged, run `/runplan` again to proceed to the next phase.

> **Note:** After merging, update your local repository with `git pull` before running the next `/runplan`.

### Step 10: Address PR Review Comments (`/fixpr`)

When review comments are posted on a PR, use `/fixpr` to address them.

```
/fixpr
```

The command retrieves unresolved review comments and proposes and applies fixes. **Always verify the diff yourself before committing.** Do not accept automatically applied changes as-is; it is important to scrutinize whether the changes reflect your actual intent.

---

## 5. Document Translation (`/mktrans`)

Create the document in either Japanese or English first, then translate it with `/mktrans`.

```
/mktrans docs/dev/developer_guide/new_document.ja.md   # Japanese → English
/mktrans docs/dev/developer_guide/new_document.md      # English → Japanese
```

**Translation workflow:**

1. Create and commit the primary language (Japanese) version first
2. Generate the translation with `/mktrans`
3. For subsequent edits, update only the primary language version directly; update the translation exclusively via `/mktrans` (never edit both files directly in the same session)

---

## 6. Status Transition Rules

No step may proceed until the relevant document's status is `approved`.

| Document | Action prohibited before approval |
|---|---|
| `01_requirements.md` is `draft` | Creating `02_architecture.md` (`/mkarch`) |
| `02_architecture.md` is `draft` | Creating `03_implementation_plan.md` (`/mkplan`) |
| `03_implementation_plan.md` is `draft` | Writing implementation code (`/runplan`) |

Approval is performed by a human reviewer. Claude Code always creates documents in `draft` status and never changes them to `approved` itself.

---

## 7. Coding Conventions

- **Formatting:** Run `make fmt` after any change
- **Testing:** Run `make test` after any change; see the [Test Organization Guide](test_organization.md) for test structure conventions
- **Static analysis:** Verify there are no errors with `make lint`
- **Comments, identifiers, and string literals:** Write comments, identifiers, and string literals in English
- **Commit messages:** Write in English
- **Modern Go idioms:** Actively use Go 1.21+ features such as `any`, the `slices`/`maps` packages, and the `min`/`max` built-in functions (see [CLAUDE.md](../../../CLAUDE.md) for details)

---

## 8. Reference Documents

| Document | Content |
|---|---|
| [Project Overview](../../overview.md) | Architecture and design decisions |
| [Requirements and Acceptance Criteria Process](requirements_process.md) | How to write ACs and detailed review flow |
| [Test Organization Guide](test_organization.md) | Rules for placing test helpers |
| [Mermaid Diagram Reference](mermaid_reference.md) | Diagram notation and conventions |
| [Package Reference](package_reference.md) | Responsibilities and internal structure of each package |
| [Task Identification](task_identification.md) | How slash commands identify the target task |
| [Robustness Principle](robustness_principle.md) | Design guidelines for handling external data |
