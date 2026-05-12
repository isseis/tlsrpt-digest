# Mermaid ダイアグラム リファレンス

このドキュメントは、アーキテクチャ設計書で使用する Mermaid ダイアグラムの凡例とサンプルを提供する。

## 1. 基本ルール

### ノードラベルのクォート
特殊文字（括弧・コロン・スラッシュ等）を含むラベルは必ずダブルクォートで囲む。

```
A["label (with parens)"]
B["pkg/path:FuncName()"]
```

### ラベル内の改行
ラベル内の改行は `<br>` を使う（`\n` は使わない）。

```
A["line1<br>line2"]
```

### データノードのシリンダー形状
設定ファイル・環境変数・DB 等の「データ」を表すノードはシリンダー形状 `[(label)]` を使う。

```
A[("TOML 設定ファイル")]
B[("環境変数<br>GSCR_SLACK_WEBHOOK_URL")]
```

---

## 2. 標準カラースキーム（classDef）

アーキテクチャ図では以下の classDef を統一して使用する。

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;

    D[("設定・環境データ")] --> P["既存コンポーネント"]
    P --> E["変更・追加コンポーネント"]
    E --> N["新規パッケージ"]
    X["問題箇所"]

    class D data
    class P process
    class E enhanced
    class N newpkg
    class X problem
```

| クラス名 | 色 | 用途 |
|---------|---|------|
| `data` | 青 | 設定ファイル・環境変数・DB など静的データ |
| `process` | オレンジ | 変更なしの既存コンポーネント |
| `enhanced` | 緑 | 変更・追加されるコンポーネント |
| `newpkg` | 紫 | 新規追加するパッケージ・型 |
| `problem` | 赤 | 問題のある既存箇所（Before 図で使用） |

---

## 3. フローチャート

### 方向の使い分け
- `TD` / `TB`（上→下）: 起動フロー・処理フロー・フェーズ依存関係
- `LR`（左→右）: パッケージ依存グラフ・データ伝播経路
- `RL`（右→左）: 使わない（可読性が低い）

### Before / After 比較パターン

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    subgraph Before["変更前"]
        A[("設定")] --> B["初期化()"]
        B --> C["処理()"]
        class B problem
    end

    subgraph After["変更後"]
        A2[("設定")] --> B2["Phase 1: 基本初期化()"]
        B2 --> C2[("TOML 読み込み")]
        C2 --> D2["Phase 2: 追加初期化()"]
        D2 --> E2["処理()"]
        class B2,D2 enhanced
    end

    class A,A2 data
    class C,C2,E2 process
```

### 処理分岐パターン（フロー判定）

```mermaid
flowchart TD
    Start(["開始"]) --> Check{"条件?"}
    Check -->|"Yes"| PathA["処理 A"]
    Check -->|"No"| PathB["処理 B"]
    PathA --> End(["終了"])
    PathB --> End
```

### パッケージ依存グラフ

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

## 4. シーケンス図

呼び出し順序や非同期処理のフローを表す場合に使用する。

```mermaid
sequenceDiagram
    participant M as main.go
    participant E as environment.go
    participant L as logger.go
    participant S as slack_handler.go

    M->>E: SetupLogging(opts)
    E->>L: SetupLoggerWithConfig(config)
    L->>L: コンソールハンドラ生成
    L-->>E: nil
    E-->>M: nil

    M->>E: SetupSlackLogging(slackConfig)
    E->>L: AddSlackHandlers(config)
    L->>S: NewSlackHandler(opts)
    alt 検証失敗
        S-->>L: ErrInvalidWebhookURL
        L-->>E: error
        E-->>M: PreExecutionError
    else 検証成功
        S-->>L: *SlackHandler
        L-->>E: nil
        E-->>M: nil
    end
```

---

## 5. クラス図

型・インターフェース間の関係を表す場合に使用する。

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

## 6. graph TB（サブグラフ付きパッケージ構成）

パッケージ内部の構造を示す場合は `graph TB` + `subgraph` を組み合わせる。

```mermaid
graph TB
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    subgraph pkg_new ["internal/notify/ (新設)"]
        N1["notifier.go<br>Notifier interface"]
        N2["slack.go<br>SlackNotifier"]
        N3["email.go<br>EmailNotifier"]
    end

    subgraph pkg_existing ["internal/imap/ (既存)"]
        I1["fetcher.go<br>MailFetcher"]
    end

    pkg_existing --> pkg_new

    class N1,N2,N3 enhanced
    class I1 process
```

---

## 7. チェックリスト

ダイアグラム作成時の確認事項：

- [ ] 特殊文字を含むラベルはダブルクォートで囲んでいる
- [ ] ラベル内改行は `<br>` を使っている
- [ ] データノードはシリンダー形状 `[(label)]` を使っている
- [ ] classDef を定義して凡例に対応させている
- [ ] 図の下または末尾に凡例（Legend）ブロックを置いている
