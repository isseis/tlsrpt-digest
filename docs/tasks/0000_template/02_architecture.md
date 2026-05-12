# Architecture Design Document: [Feature Name]

## Document Status

| Item | Value |
|---|---|
| Status | `draft` |
| Created | YYYY-MM-DD |
| Review date | - |
| Reviewer | - |
| Comments | - |

---

## 1. Design Overview

### 1.1 Design Principles

- **[Principle Name]**: ...

### 1.2 Concept Model

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    A[("Input data")] --> B["Existing component"]
    B --> C["Modified / added component"]
    C --> D[("Output data")]

    class A,D data
    class B process
    class C enhanced
```

**Legend**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    D[("Config / environment data")] --> P["Existing component"] --> E["Modified / added component"]
    class D data
    class P process
    class E enhanced
```

---

## 2. System Structure

### 2.1 Overall Architecture

```mermaid
graph TB
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    subgraph pkg_new ["internal/new-package/"]
        N1["New types / interfaces"]
    end

    subgraph pkg_existing ["internal/existing-package/ (modified)"]
        E1["Existing component (modified)"]
    end

    pkg_existing --> pkg_new

    class N1 enhanced
    class E1 process
```

### 2.2 Processing Flow

```mermaid
flowchart TD
    Start(["Start"]) --> Step1["Step 1"]
    Step1 --> Check{"Condition?"}
    Check -->|"Yes"| Step2["Step 2"]
    Check -->|"No"| Step3["Step 3"]
    Step2 --> End(["End"])
    Step3 --> End
```

### 2.3 Data Flow / Sequence Diagram

```mermaid
sequenceDiagram
    participant A as Component A
    participant B as Component B

    A->>B: Request
    B-->>A: Response
```

---

## 3. Component Design

### 3.1 Interface and Type Definitions

(Describe only high-level interfaces and error types. Leave concrete implementations to the code.)

### 3.2 Component Responsibilities

| Component | Responsibility | Change Type |
|-----------|---------------|-------------|
| `internal/xxx/yyy.go` | ... | New addition |
| `internal/zzz/www.go` | ... | Modified |

---

## 4. Error Handling Design

(Describe error type definition policy and error message design patterns.)

---

## 5. Security Considerations

(Describe security design and threat models. If not applicable, write "N/A".)

---

## 6. Test Strategy

### Unit Tests

- ...

### Integration Tests

- ...

---

## 7. Implementation Priorities

### Phase 1: [Phase Name]

1. ...

### Phase 2: [Phase Name]

1. ...

---

## 8. Future Extensibility

(Describe design considerations for features that are currently out of scope but anticipated in future extensions.)

- ...
