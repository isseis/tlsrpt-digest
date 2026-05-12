# アーキテクチャ設計書：[機能名]

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | YYYY-MM-DD |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 設計概要

### 1.1 設計原則

- **[原則名]**: ...

### 1.2 概念モデル

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    A[("入力データ")] --> B["既存コンポーネント"]
    B --> C["変更・追加コンポーネント"]
    C --> D[("出力データ")]

    class A,D data
    class B process
    class C enhanced
```

**凡例（Legend）**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    D[("設定・環境データ")] --> P["既存コンポーネント"] --> E["変更・追加コンポーネント"]
    class D data
    class P process
    class E enhanced
```

---

## 2. システム構成

### 2.1 全体アーキテクチャ

```mermaid
graph TB
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    subgraph pkg_new ["internal/新パッケージ/"]
        N1["新規型・インターフェース"]
    end

    subgraph pkg_existing ["internal/既存パッケージ/ (変更あり)"]
        E1["変更される既存コンポーネント"]
    end

    pkg_existing --> pkg_new

    class N1 enhanced
    class E1 process
```

### 2.2 処理フロー

```mermaid
flowchart TD
    Start(["開始"]) --> Step1["ステップ 1"]
    Step1 --> Check{"条件?"}
    Check -->|"Yes"| Step2["ステップ 2"]
    Check -->|"No"| Step3["ステップ 3"]
    Step2 --> End(["終了"])
    Step3 --> End
```

### 2.3 データフロー / シーケンス図

```mermaid
sequenceDiagram
    participant A as コンポーネント A
    participant B as コンポーネント B

    A->>B: リクエスト
    B-->>A: レスポンス
```

---

## 3. コンポーネント設計

### 3.1 インターフェース・型定義

（高レベルのインターフェース・エラー型のみ記述。具体的な実装はコードに委ねる）

### 3.2 コンポーネントの責務

| コンポーネント | 責務 | 変更種別 |
|-------------|------|---------|
| `internal/xxx/yyy.go` | ... | 新規追加 |
| `internal/zzz/www.go` | ... | 変更あり |

---

## 4. エラーハンドリング設計

（エラー型の定義方針・エラーメッセージの設計パターンを記述）

---

## 5. セキュリティ考慮事項

（セキュリティ設計・脅威モデルを記述。関係ない場合は「該当なし」）

---

## 6. テスト戦略

### 単体テスト

- ...

### 統合テスト

- ...

---

## 7. 実装優先順位

### フェーズ 1: [フェーズ名]

1. ...

### フェーズ 2: [フェーズ名]

1. ...
