# アーキテクチャ設計書：ストア GC の簡略化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-19 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 設計の全体像

### 1.1 設計原則

- **信頼できる日時のみを使う**: GC 判定・パス決定ともに `INTERNALDATE`（IMAP サーバー制御）に一本化し、送信側が設定する値への依存を排除する
- **責務の分離を維持する**: `SaveReports` はレポートレコードの永続化に専念し、メールインデックスを変更しない
- **削除コードを減らす**: `sweepOrphanedEmailDirs` を廃止することで、日時の月差異に起因するディレクトリ誤削除のリスクを根本から除去する

### 1.2 変更前後の概念モデル

#### 変更前（現状）

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    SR["SaveReports"] -->|"report_end_date を更新"| IDX[("メールインデックス<br>uid / uidvalidity<br>sent_at / saved_at<br>report_end_date")]
    DEB["DeleteEmailsBefore<br>(reportCutoff, savedAtCutoff)"] -->|"読む"| IDX
    DEB -->|"条件1: report_end_date < reportCutoff"| DEL1["通常削除<br>（送信側制御）"]
    DEB -->|"条件2: saved_at < savedAtCutoff"| DEL2["強制削除<br>（ローカル制御）"]
    DEB -->|"ディレクトリ名で比較"| SWP["sweepOrphanedEmailDirs<br>sent_at 由来 YYYYMM<br>vs savedAtCutoff 月"]

    class IDX data
    class DEL1,SWP problem
    class SR,DEB,DEL2 process
```

矢印 `A --> B` は「A が B に作用する・参照する」を表す。

**Legend**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    D[("永続データ")]
    P["既存コンポーネント（変更なし）"]
    X["問題のある設計"]

    class D data
    class P process
    class X problem
```

#### 変更後（本タスク）

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    SR["SaveReports"] -->|"レポートのみ更新"| RPT[("レポートレコード")]
    DEB["DeleteEmailsBefore<br>(cutoff)"] -->|"読む"| IDX[("メールインデックス<br>uid / uidvalidity<br>internal_date / saved_at")]
    DEB -->|"条件: internal_date < cutoff"| DEL["削除<br>（INTERNALDATE 基準）"]

    class RPT,IDX data
    class DEL enhanced
    class SR,DEB process
```

矢印 `A --> B` は「A が B に作用する・参照する」を表す。

**Legend**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;

    D[("永続データ")]
    E["変更・改善するコンポーネント"]
    P["既存コンポーネント（変更なし）"]

    class D data
    class E enhanced
    class P process
```

---

## 2. システム構成

### 2.1 全体アーキテクチャ（変更対象のみ）

```mermaid
graph TB
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;

    subgraph store_pkg ["internal/store/ （変更）"]
        IFACE["store.go<br>Store インターフェース"]
        TYPES["types.go<br>EmailMeta / LoadedEmail<br>internalEmailIndexEntry"]
        RPT["reports.go<br>SaveReports"]
        EML["emails.go<br>SaveEmail / SaveEmailMetas<br>DeleteEmailsBefore"]
    end

    subgraph testutil_pkg ["internal/store/testutil/ （変更）"]
        MOCK["mocks.go<br>FakeStore / FakeEmailEntry"]
    end

    subgraph data_pkg ["ストレージ"]
        JSON[("tlsrpt.json<br>reports + emails インデックス")]
        EMLF[("emails/<br>.eml ファイル群")]
    end

    IFACE --> RPT
    IFACE --> EML
    RPT --> JSON
    EML --> JSON
    EML --> EMLF
    TYPES -.->|"定義"| JSON
    MOCK -.->|"実装"| IFACE

    class IFACE,TYPES,RPT,EML enhanced
    class MOCK enhanced
    class JSON,EMLF data
```

矢印 `A --> B` は「A が B を使う・書き込む」を表す。破線 `A -.-> B` は「A が B の構造を定義・実装する」を表す。

**Legend**

```mermaid
flowchart LR
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;

    E["変更するファイル"]
    D[("永続データ")]

    class E enhanced
    class D data
```

### 2.2 コンポーネント配置と変更概要

| ファイル | 変更種別 | 変更内容 |
|---|---|---|
| `internal/store/store.go` | 変更 | `Store` インターフェースの `SaveEmail`・`DeleteEmailsBefore` シグネチャ更新、ドキュメントコメント修正 |
| `internal/store/types.go` | 変更 | `EmailMeta.SentAt` → `InternalDate`、`LoadedEmail.SentAt` 削除、`internalEmailIndexEntry.SentAt` → `InternalDate`（JSON: `internal_date`）、`ReportEndDate` 削除 |
| `internal/store/reports.go` | 変更 | `SaveReports` からメールインデックス更新ロジック（`report_end_date` 更新・プレースホルダー作成）を削除 |
| `internal/store/emails.go` | 変更 | `SaveEmail` のシグネチャ変更（`sentAt` → `internalDate`）、`SaveEmailMetas` のプレースホルダー補填ロジック削除、`DeleteEmailsBefore` のシグネチャ変更と削除ロジック簡略化、`sweepOrphanedEmailDirs` を削除して空ディレクトリ削除を追加 |
| `internal/store/emails_test.go` | 変更 | 新シグネチャ対応・テスト追加・不要テスト削除 |
| `internal/store/reports_test.go` | 変更 | `SaveReports` がメールインデックスを変更しないことの確認テスト追加・不要テスト削除 |
| `internal/store/testutil/mocks.go` | 変更 | `FakeEmailEntry.SentAt` → `InternalDate`、`FakeStore.SaveEmail` のシグネチャ変更 |

### 2.3 データフロー

#### fetch サイクル（変更後）

```mermaid
sequenceDiagram
    participant EP as "entrypoint(fetch)"
    participant ST as "store"
    participant EF as "emails/"
    participant DF as "tlsrpt.json"

    EP->>ST: "SaveEmail(uid, uidValidity, internalDate, savedAt, rawEML)"
    ST->>EF: ".eml を {internalDate.YYYYMM}/ に保存"
    EP->>ST: "SaveEmailMetas()"
    ST->>DF: "メールインデックスを更新<br>（uid / uidvalidity / internal_date / saved_at のみ）"
    EP->>ST: "SaveReports()"
    ST->>DF: "レポートレコードのみ更新<br>（メールインデックスは変更しない）"
```

#### GC サイクル（変更後）

```mermaid
sequenceDiagram
    participant EP as "entrypoint(gc)"
    participant ST as "store"
    participant EF as "emails/"
    participant DF as "tlsrpt.json"

    EP->>ST: "DeleteEmailsBefore(cutoff)"
    ST->>DF: "メールインデックスを読み込む"
    loop "各インデックスエントリ"
        alt "internal_date != zero && internal_date < cutoff"
            ST->>EF: ".eml を削除（パスは internal_date から再構築）"
            Note over ST,EF: "I/O エラーは集約して継続"
        else "削除対象外"
            Note over ST: "エントリを保持"
        end
    end
    ST->>DF: "インデックスをアトミック更新"
    ST->>EF: "空になった {uidvalidity}/{YYYYMM} および {uidvalidity} ディレクトリを削除"
    ST-->>EP: "deleted, err"
```

---

## 3. コンポーネント設計

### 3.1 インターフェース変更

```go
type Store interface {
    // ...（他のメソッドは変更なし）

    // SaveEmail は .eml ファイルを保存する。
    // パスは internalDate（IMAP INTERNALDATE）から決定する（AC-18・AC-19）。
    // internalDate がゼロ値の場合はエラーを返す（INTERNALDATE は RFC 3501 必須フィールド）。
    SaveEmail(uid, uidValidity uint32, internalDate, savedAt time.Time, rawEML []byte) error

    // SaveReports はレポートレコードのみを保存する。
    // メールインデックスは更新しない（AC-09）。
    SaveReports(inputs []ReportInput) error

    // DeleteEmailsBefore は internal_date < cutoff を満たす .eml ファイルを削除する。
    // cutoff がゼロ値の場合は削除を行わない（AC-02）。
    // internal_date がゼロのエントリは削除対象外とする（AC-03）。
    DeleteEmailsBefore(cutoff time.Time) (deleted int, err error)
}
```

### 3.2 データ型の変更

#### `EmailMeta` および `LoadedEmail`（公開 API）

```go
// 変更前
type EmailMeta struct {
    UID         uint32
    UIDValidity uint32
    SentAt      time.Time // 削除
    SavedAt     time.Time
}

type LoadedEmail struct {
    Message     *mail.Message
    UID         uint32
    UIDValidity uint32
    SentAt      time.Time // 削除（Date: ヘッダーは Message.Header.Get("Date") で参照可能）
    SavedAt     time.Time
    Path        string
}

// 変更後
type EmailMeta struct {
    UID          uint32
    UIDValidity  uint32
    InternalDate time.Time // 追加（IMAP INTERNALDATE）
    SavedAt      time.Time
}

type LoadedEmail struct {
    Message     *mail.Message
    UID         uint32
    UIDValidity uint32
    SavedAt     time.Time
    Path        string
}
```

#### `internalEmailIndexEntry`（内部型）

```go
// 変更前
type internalEmailIndexEntry struct {
    UID           uint32     `json:"uid"`
    UIDValidity   uint32     `json:"uidvalidity"`
    SentAt        time.Time  `json:"sent_at"`      // 削除
    SavedAt       time.Time  `json:"saved_at"`
    ReportEndDate *time.Time `json:"report_end_date"` // 削除
}

// 変更後
type internalEmailIndexEntry struct {
    UID          uint32    `json:"uid"`
    UIDValidity  uint32    `json:"uidvalidity"`
    InternalDate time.Time `json:"internal_date"` // 追加
    SavedAt      time.Time `json:"saved_at"`
}
```

### 3.3 既存 JSON ファイルの扱い

後方互換性は考慮しない。`internal_date` を持たない既存 `tlsrpt.json` は本タスクのサポート対象外とし、追加の読み込み互換やマイグレーション処理は実装しない。

### 3.4 `SaveEmailMetas` の変更

`SaveReports` がプレースホルダーエントリを作成しなくなるため、`SaveEmailMetas` にある「ゼロ値フィールドを埋める救済ロジック」も不要となる。`SaveEmailMetas` は「同一 `{uid, uidvalidity}` が既に存在する場合は何もしない」という純粋な冪等挿入のみに簡略化する（AC-14）。

---

## 4. エラーハンドリング設計

### 4.1 エラー方針

| ケース | 方針 |
|---|---|
| `.eml` の個別削除に I/O エラー | エントリをインデックスに残し、`errors.Join` で集約して継続（AC-05） |
| インデックスの保存失敗 | 失敗前の削除件数を返し、保存エラーを集約して返す（AC-07） |
| `internalDate` がゼロ値（`SaveEmail`） | エラーを返す（AC-19） |
| 空ディレクトリ削除の失敗 | `slog.Warn` を出力して継続し、戻り値エラーには含めない（AC-13） |

既存の `ErrDeleteEmailFailed` 型は変更なしで継続使用する。

---

## 5. セキュリティ考慮事項

本タスクは通知先を持たず、`notification_security.md` の適用対象外である。

セキュリティ上の主な改善点は以下のとおりである。

- 送信側が設定する `report_end_date` を GC 判定から排除することで、遠未来日付による `.eml` の無制限蓄積攻撃のベクターを閉じる
- `SentAt`（送信側制御）をパス決定から排除し、`INTERNALDATE`（サーバー制御）に置き換えることで、パス操作の攻撃ベクターも排除する

---

## 6. 処理フロー詳細

### 6.1 `DeleteEmailsBefore` フロー（変更後）

```mermaid
flowchart TD
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    Start(["DeleteEmailsBefore(cutoff)"]) --> Zero{"cutoff<br>がゼロ値？"}
    Zero -->|"Yes"| RetZero(["deleted=0, err=nil を返す (AC-02)"])
    Zero -->|"No"| Load["インデックスを読み込む"]
    Load --> Loop["各インデックスエントリを評価"]
    Loop --> Cond{"internal_date < cutoff？"}
    Cond -->|"No"| Keep["保持"]
    Cond -->|"Yes"| DelFile["ファイルを削除 (AC-06)<br>パスは internal_date から再構築"]
    DelFile --> IOErr{"I/O エラー？"}
    IOErr -->|"Yes"| KeepEntry["エントリを保持<br>エラーを集約 (AC-05)"]
    IOErr -->|"No"| RemoveEntry["エントリを除去<br>（ファイル不在も同様 AC-04）"]
    RemoveEntry --> CountUp["deleted++"]
    Keep --> NextEntry["次のエントリへ"]
    KeepEntry --> NextEntry
    CountUp --> NextEntry
    NextEntry --> Loop
    Loop --> Save["インデックスをアトミック更新 (AC-06)"]
    Save --> SaveErr{"保存失敗？"}
    SaveErr -->|"Yes"| RetErr(["deleted, errors.Join(deleteErrs, saveErr) を返す (AC-07)"])
    SaveErr -->|"No"| Sweep["空ディレクトリを削除 (AC-13)<br>失敗は slog.Warn のみ"]
    Sweep --> RetOK(["deleted, errors.Join(deleteErrs) を返す"])

    class DelFile,RemoveEntry,Save,Sweep enhanced
```

---

## 7. テスト戦略

### 7.1 単体テスト

| 対象 | 検証内容 |
|---|---|
| `SaveEmail`（新シグネチャ） | `internalDate` から正しい `{YYYYMM}` パスが生成されること（AC-19） |
| | `internalDate` がゼロ値のときエラーが返されること（AC-19） |
| `DeleteEmailsBefore`（新シグネチャ） | `cutoff` がゼロ → 削除なし（AC-02） |
| | `internal_date < cutoff` → ファイルとインデックスエントリを削除（AC-03, AC-06） |
| | ファイル不在 → 冪等動作（AC-04） |
| | I/O エラー混在 → 成功件数と集約エラーを返す（AC-05） |
| | インデックス更新失敗 → 削除済み件数とエラーを返す（AC-07） |
| | GC 後に空になったディレクトリが削除されること（AC-13） |
| | ディレクトリ削除失敗でもエラーを返さないこと（AC-13） |
| `SaveReports` | メールインデックスを変更しないこと（AC-09） |
| `SaveEmailMetas` | 既存エントリへの冪等挿入動作（AC-14） |

### 7.2 統合テスト

- fetch サイクル（`SaveEmail` → `SaveEmailMetas` → `SaveReports`）後に GC を実行し、`internal_date < cutoff` のエントリのみが削除されること
- GC 後に `GetReportsSince` でレポートレコードが正常に取得できること（メールと独立していることの確認）

### 7.3 セキュリティテスト

- 本タスクにセキュリティ固有のテスト要件はない（N/A）

---

## 8. 実装優先度

### Phase 0: 前提条件の変更（F-000）

1. `EmailMeta.SentAt` → `InternalDate`、`LoadedEmail.SentAt` 削除（`types.go`）
2. `internalEmailIndexEntry.SentAt` → `InternalDate`（`types.go`）
3. `Store.SaveEmail` シグネチャ変更（`store.go`）
4. `SaveEmail` 実装のパス決定ロジック変更（`emails.go`）
5. `FakeEmailEntry.SentAt` → `InternalDate`、`FakeStore.SaveEmail` シグネチャ変更（`testutil/mocks.go`）
6. 関連テストの更新

### Phase 1: 型とインターフェースの変更

1. `internalEmailIndexEntry` から `ReportEndDate` を削除（`types.go`）
2. `Store` インターフェースの `DeleteEmailsBefore` シグネチャ更新（`store.go`）

### Phase 2: 実装の変更

1. `SaveReports` からメールインデックス更新ロジックを削除（`reports.go`）
2. `SaveEmailMetas` のプレースホルダー救済ロジックを削除（`emails.go`）
3. `DeleteEmailsBefore` を新シグネチャ・新ロジックに変更（`emails.go`）
4. `sweepOrphanedEmailDirs` を削除し、空ディレクトリ削除に置き換え（`emails.go`）

### Phase 3: テストの更新と確認

1. `emails_test.go`・`reports_test.go` を新シグネチャ対応に更新
2. `make fmt && make test && make lint` を実行して全テストが通ることを確認

---

## 9. 将来拡張性

- 孤立 `.eml`（インデックスに存在しないファイル）の清掃は `reprocess` サブコマンド（タスク 0070 F-004）が全 `.eml` を再帰走査することで対応する。ディレクトリスイープの廃止はこの設計と整合する
- date-range バリデーション（遠未来 `end-datetime` の拒否）はタスク 0070 のエントリポイントで実装する。本タスクはそのための前提条件（`report_end_date` への依存排除）を整備する
- レポートレコード（`tlsrpt.json` の `reports` 配列）の GC は引き続き `DeleteReportsBefore(cutoff)` で行い、エントリポイント側で上限保持期間を設けることで遠未来 `end-datetime` 攻撃への対策を補完する
