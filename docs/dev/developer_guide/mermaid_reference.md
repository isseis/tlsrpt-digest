# Mermaid Diagram Reference

This document provides conventions and examples for Mermaid diagrams used in architecture design documents.

## 1. Basic Rules

### Node Label Quoting
Always wrap labels in double quotes if they contain special characters (parentheses, colons, slashes, etc.).

```
A["label (with parens)"]
B["pkg/path:FuncName()"]
```

### Line Breaks in Labels
Use `<br>` for line breaks inside node labels (not `\n`).

```
A["line1<br>line2"]
```

### Cylinder Shape for Data Nodes
Use the cylinder shape `[(label)]` for nodes that represent "data" such as config files, environment variables, or databases.

```
A[("TOML config file")]
B[("Environment variable<br>GSCR_SLACK_WEBHOOK_URL")]
```

---

## 2. Standard Color Scheme (classDef)

Use the following classDef definitions consistently across all architecture diagrams.

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;

    D[("Config / env data")] --> P["Existing component"]
    P --> E["Modified / added component"]
    E --> N["New package"]
    X["Problematic component"]

    class D data
    class P process
    class E enhanced
    class N newpkg
    class X problem
```

| Class | Color | Usage |
|-------|-------|-------|
| `data` | Blue | Static data: config files, environment variables, databases |
| `process` | Orange | Existing components with no changes |
| `enhanced` | Green | Components being modified or added |
| `newpkg` | Purple | Newly added packages or types |
| `problem` | Red | Problematic existing code (used in Before diagrams) |

---

## 3. Flowcharts

### Direction Guidelines
- `TD` / `TB` (top → bottom): startup flows, processing flows, phase dependencies
- `LR` (left → right): package dependency graphs, data propagation paths
- `RL` (right → left): avoid (poor readability)

### Before / After Comparison Pattern

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    subgraph Before["Before"]
        A[("Config")] --> B["Init()"]
        B --> C["Process()"]
        class B problem
    end

    subgraph After["After"]
        A2[("Config")] --> B2["Phase 1: BasicInit()"]
        B2 --> C2[("TOML load")]
        C2 --> D2["Phase 2: AdditionalInit()"]
        D2 --> E2["Process()"]
        class B2,D2 enhanced
    end

    class A,A2 data
    class C,C2,E2 process
```

### Decision / Branching Pattern

```mermaid
flowchart TD
    Start(["Start"]) --> Check{"Condition?"}
    Check -->|"Yes"| PathA["Process A"]
    Check -->|"No"| PathB["Process B"]
    PathA --> End(["End"])
    PathB --> End
```

### Package Dependency Graph

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    CMD["cmd/runner"]
    CORE["internal/core"]
    SEC["internal/security"]
    CFG[("config/")]

    CMD --> CORE
    CMD --> SEC
    CORE --> CFG
    SEC -.->|"implements"| CORE

    class CFG data
    class CMD,CORE process
    class SEC enhanced
```

---

## 4. Sequence Diagrams

Use sequence diagrams to show call order or async processing flows.

```mermaid
sequenceDiagram
    participant M as main.go
    participant E as environment.go
    participant L as logger.go
    participant S as slack_handler.go

    M->>E: SetupLogging(opts)
    E->>L: SetupLoggerWithConfig(config)
    L->>L: create console handler
    L-->>E: nil
    E-->>M: nil

    M->>E: SetupSlackLogging(slackConfig)
    E->>L: AddSlackHandlers(config)
    L->>S: NewSlackHandler(opts)
    alt validation fails
        S-->>L: ErrInvalidWebhookURL
        L-->>E: error
        E-->>M: PreExecutionError
    else validation succeeds
        S-->>L: *SlackHandler
        L-->>E: nil
        E-->>M: nil
    end
```

---

## 5. Class Diagrams

Use class diagrams to show relationships between types and interfaces.

```mermaid
classDiagram
    class Notifier {
        <<interface>>
        +SendAlert(msg string) error
        +SendSummary(report Report) error
    }

    class SlackNotifier {
        <<struct>>
        -webhookURL string
        +SendAlert(msg string) error
        +SendSummary(report Report) error
    }

    class Report {
        <<struct>>
        +Period string
        +Entries []ReportEntry
    }

    Notifier <|.. SlackNotifier : implements
    SlackNotifier --> Report : uses
```

---

## 6. graph TB with Subgraphs (Package Structure)

Combine `graph TB` with `subgraph` to show internal package structure.

```mermaid
graph TB
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    subgraph pkg_new ["internal/notify/ (new)"]
        N1["notifier.go<br>Notifier interface"]
        N2["slack.go<br>SlackNotifier"]
        N3["email.go<br>EmailNotifier"]
    end

    subgraph pkg_existing ["internal/imap/ (existing)"]
        I1["fetcher.go<br>MailFetcher"]
    end

    pkg_existing --> pkg_new

    class N1,N2,N3 enhanced
    class I1 process
```

---

## 7. Checklist

Review this checklist when creating diagrams:

- [ ] Labels containing special characters are wrapped in double quotes
- [ ] Line breaks inside labels use `<br>`
- [ ] Data nodes use the cylinder shape `[(label)]`
- [ ] `classDef` entries are defined and match the legend
- [ ] A Legend block is placed below the diagram or at the end of the section
- [ ] Node IDs do not use Mermaid reserved keywords — the following words are reserved
      in flowcharts and will cause a parse error if used as node IDs:
      `call`, `end`, `style`, `linkStyle`, `classDef`, `class`, `click`, `direction`,
      `subgraph`, `default`. Use descriptive alternatives (e.g., `invoke` instead of `call`).
