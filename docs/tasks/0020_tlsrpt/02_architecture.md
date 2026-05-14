# アーキテクチャ設計書：TLSRPTレポートのパース・failure判定

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-14 |
| レビュー日 | 2026-05-14 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 設計概要

### 1.1 設計原則

- **単一責任**: `internal/tlsrpt` パッケージは RFC 8460 JSON のデコード（gzip 自動検出）・パース・`total-failure-session-count` の評価のみを担う。メール取得・MIME 解析・通知送信とは明確に分離する。
- **防御的入力検証**: TLSRPT レポートは外部データとして扱い、展開サイズの上限チェックと必須フィールドの検証を行う。
- **シンプルな公開 API**: `Parse()` 関数と `(*Report).HasFailure()` メソッドのみを公開する。内部処理の詳細は非公開とする。
- **既存パッケージとの協調**: MIME 添付ファイル抽出は `internal/mailparse` が担当し、本パッケージはその結果として受け取った `[]byte` を処理する。

### 1.2 概念モデル

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    GZ[(".json.gz<br>バイト列")]
    PARSE["Parse()<br>internal/tlsrpt"]
    RPT[("Report")]
    HF["HasFailure()"]
    RESULT[("bool<br>failure あり/なし")]

    GZ --> PARSE --> RPT --> HF --> RESULT

    class GZ data
    class RPT data
    class PARSE,HF newpkg
    class RESULT data
```

**凡例（Legend）**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    D[("データ")] --> P["既存コンポーネント"] --> N["新規パッケージ"]
    class D data
    class P process
    class N newpkg
```

---

## 2. システム構成

### 2.1 全体アーキテクチャ

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    IMAP[("IMAP<br>メールボックス")]
    IM["internal/imap<br>MailFetcher"]
    MP["internal/mailparse<br>ExtractAttachments()"]
    TR["internal/tlsrpt<br>Parse()"]
    CMD["cmd/tlsrpt-digest"]
    NT["internal/notify<br>Notifier"]
    ST["internal/store<br>Store"]

    IMAP --> IM
    IM -->|"*mail.Message"| CMD
    CMD -->|"*mail.Message"| MP
    MP -->|"[]Attachment"| CMD
    CMD -->|".json.gz バイト列"| TR
    TR -->|"*Report"| CMD
    CMD --> NT
    CMD --> ST

    class IMAP data
    class IM,MP,CMD process
    class TR newpkg
    class NT,ST enhanced
```

### 2.2 コンポーネント配置

| パッケージ | 役割 | 状態 |
|---|---|---|
| `internal/imap` | IMAP メール取得 | 既存 |
| `internal/mailparse` | MIME 添付ファイル抽出 | 既存 |
| `internal/tlsrpt` | .json.gz 展開・RFC 8460 パース・failure 判定 | **新規** |
| `cmd/tlsrpt-digest` | エントリポイント（パース結果の利用側） | 既存（間接的に影響） |
| `internal/notify` | 通知送信 | スコープ外（全体アーキテクチャ図の文脈として参照） |
| `internal/store` | データ蓄積 | スコープ外（全体アーキテクチャ図の文脈として参照） |

### 2.3 データフロー / シーケンス図

```mermaid
sequenceDiagram
    participant CMD as cmd/tlsrpt-digest
    participant MP as internal/mailparse
    participant TR as internal/tlsrpt

    CMD->>MP: ExtractAttachments(msg)
    MP-->>CMD: []Attachment
    loop 各 Attachment
        alt filename ends with .json.gz
            CMD->>TR: ParseGzip(attachment.Content)
            Note over TR: gzip 展開（サイズ上限チェックあり）
        else filename ends with .json
            CMD->>TR: ParseJSON(attachment.Content)
            Note over TR: サイズ上限チェック
        end
        Note over TR: JSON パース
        Note over TR: 必須フィールド検証
        alt パース失敗
            TR-->>CMD: nil, error
        else パース成功
            TR-->>CMD: *Report, nil
            CMD->>TR: report.HasFailure()
            TR-->>CMD: bool
        end
        alt HasFailure() == true
            CMD->>CMD: 即時アラート処理（別パッケージ）
        else HasFailure() == false
            CMD->>CMD: 週次サマリー蓄積（別パッケージ）
        end
    end
```

---

## 3. コンポーネント設計

### 3.1 インターフェース・型定義

```go
// ParseGzip decompresses gzip data and parses it as an RFC 8460 report.
// The caller determines the format from the attachment filename or Content-Type.
func ParseGzip(data []byte) (*Report, error)

// ParseJSON parses plain JSON data as an RFC 8460 report.
func ParseJSON(data []byte) (*Report, error)

// Report は RFC 8460 のトップレベル構造体。
type Report struct {
    OrganizationName string        `json:"organization-name"`
    ReportID         string        `json:"report-id"`
    DateRange        DateRange     `json:"date-range"`
    Policies         []PolicyRecord `json:"policies"`
}

// HasFailure はいずれかのポリシーレコードの total-failure-session-count が 1 以上のとき true を返す。
func (r *Report) HasFailure() bool

type DateRange struct {
    StartDatetime time.Time `json:"start-datetime"`
    EndDatetime   time.Time `json:"end-datetime"`
}

type PolicyRecord struct {
    Policy         Policy         `json:"policy"`
    Summary        Summary        `json:"summary"`
    FailureDetails []FailureDetail `json:"failure-details"`
}

type Policy struct {
    PolicyType   string   `json:"policy-type"`
    PolicyString []string `json:"policy-string"`
    PolicyDomain string   `json:"policy-domain"`
    MXHost       []string `json:"mx-host"`
}

type Summary struct {
    TotalSuccessfulSessionCount int64 `json:"total-successful-session-count"`
    TotalFailureSessionCount    int64 `json:"total-failure-session-count"`
}

type FailureDetail struct {
    ResultType            string `json:"result-type"`
    SendingMTAIP          string `json:"sending-mta-ip"`
    ReceivingMXHostname   string `json:"receiving-mx-hostname"`
    ReceivingIP           string `json:"receiving-ip"`
    FailedSessionCount    int64  `json:"failed-session-count"`
    AdditionalInformation string `json:"additional-information"`
    FailureReasonCode     string `json:"failure-reason-code"`
}
```

RFC 8460 の JSON フィールド名はケバブケース（例: `failure-session-count`）であるため、各構造体フィールドには `json` タグを付与する。

### 3.2 コンポーネントの責務

| コンポーネント | 責務 | 変更種別 |
|---|---|---|
| `internal/tlsrpt/tlsrpt.go` | `Report` および関連構造体の定義、gzip 圧縮レポートのパース API、failure 判定 API、エラー型の定義 | 新規追加 |
| `cmd/tlsrpt-digest/main.go` | `internal/tlsrpt` のパース結果を受け取り、通知・保存系コンポーネントへ橋渡しする統合ポイント | 既存ファイルの変更対象 |

---

## 4. エラーハンドリング設計

### 4.1 エラー型定義

```go
// ErrDecompressedSizeLimitExceeded は展開後のサイズが上限を超えたときに返される。
// Limit と Actual の値から上限超過の程度を把握できる。
type ErrDecompressedSizeLimitExceeded struct {
    Limit  int64
    Actual int64
}

// ErrMissingRequiredField は必須フィールドが欠如しているときに返される。
// Field の値からどのフィールドが欠如しているかを把握できる。
type ErrMissingRequiredField struct {
    Field string
}
```

不正な gzip データおよび不正な JSON については、原因となる低レベルエラーを保持したまま `tlsrpt` パッケージの文脈を付加して返す。これにより、呼び出し側は失敗種別を保ったまま原因を判別できる。

### 4.2 エラーメッセージパターン

| エラー種別 | メッセージパターン |
|---|---|
| 不正 gzip | `tlsrpt: decompress: <原因>` |
| 展開サイズ超過 | `ErrDecompressedSizeLimitExceeded.Error()` で詳細を提供 |
| 不正 JSON | `tlsrpt: parse json: <原因>` |
| 必須フィールド欠如 | `ErrMissingRequiredField.Error()` で欠如フィールド名を提供 |

---

## 5. セキュリティ考慮事項

### 5.1 脅威モデル

TLSRPT レポートは外部の送信者（メール送信者のドメイン）が作成する外部データである。悪意あるレポートによる攻撃を以下の脅威モデルで示す。

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    EXT[(".json.gz<br>（外部データ）")]
    GZIP["gzip 展開"]
    SIZECHECK{"展開サイズ<br>上限チェック"}
    PARSEJSON["JSON パース"]
    VALIDATE["必須フィールド<br>検証"]
    OK["Report 返却"]
    ERRSIZE["ErrDecompressedSizeLimitExceeded"]
    ERRJSON["エラー返却<br>（不正 JSON）"]
    ERRFIELD["ErrMissingRequiredField"]

    EXT --> GZIP --> SIZECHECK
    SIZECHECK -->|"上限超過"| ERRSIZE
    SIZECHECK -->|"上限内"| PARSEJSON
    PARSEJSON -->|"パース失敗"| ERRJSON
    PARSEJSON -->|"パース成功"| VALIDATE
    VALIDATE -->|"フィールド欠如"| ERRFIELD
    VALIDATE -->|"検証成功"| OK

    class EXT data
    class SIZECHECK problem
    class GZIP,PARSEJSON,VALIDATE enhanced
```

### 5.2 Zip Bomb 対策

- **脅威**: 圧縮率を極端に高めた .json.gz ファイルにより、展開時にメモリを過大消費させる DoS 攻撃
- **対策**: gzip 展開時にストリーム読み込みと展開後サイズの監視を組み合わせ、上限超過時は `ErrDecompressedSizeLimitExceeded` を返す
- **参考パターン**: `internal/mailparse` の `maxBytes` 引数によるサイズ制限と同様のアプローチを採用する
- **上限値**: パッケージ内定数として定義する（例: 10 MB）

### 5.3 不正 JSON・フィールド欠如対策

- 標準ライブラリ `encoding/json` を使用し、不正 JSON を安全に拒否する
- 必須フィールドの欠如を明示的に検証し、不完全なレポートをエラーとして扱う
- 通知先の動的設定や外部コマンド実行は行わないため、通知先インジェクション攻撃の対象外となる

---

## 6. 処理フロー詳細

### 6.1 ParseGzip() / ParseJSON() の処理フロー

```mermaid
flowchart TD
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    StartG(["ParseGzip(data []byte) 呼び出し"])
    StartJ(["ParseJSON(data []byte) 呼び出し"])
    Decompress["gzip 展開<br>サイズ上限を監視"]
    CheckSizeG{"展開サイズ<br>上限超過?"}
    CheckSizeJ{"データサイズ<br>上限超過?"}
    ParseJSONNode["RFC 8460 JSON を<br>構造体へ変換"]
    CheckJSON{"パース成功?"}
    Validate["トップレベル必須フィールドを検証<br>organization-name<br>report-id<br>date-range<br>policies"]
    CheckField{"全必須フィールド<br>存在?"}
    ReturnOK["*Report を返す"]
    ErrSize["ErrDecompressedSizeLimitExceeded を返す"]
    ErrJSON["パース失敗を表すエラーを返す"]
    ErrField["ErrMissingRequiredField を返す"]

    StartG --> Decompress
    StartJ --> CheckSizeJ
    Decompress --> CheckSizeG
    CheckSizeG -->|"Yes"| ErrSize
    CheckSizeG -->|"No"| ParseJSONNode
    CheckSizeJ -->|"Yes"| ErrSize
    CheckSizeJ -->|"No"| ParseJSONNode
    ParseJSONNode --> CheckJSON
    CheckJSON -->|"No"| ErrJSON
    CheckJSON -->|"Yes"| Validate
    Validate --> CheckField
    CheckField -->|"No"| ErrField
    CheckField -->|"Yes"| ReturnOK
```

### 6.2 HasFailure() の処理フロー

```mermaid
flowchart TD
    Start(["HasFailure() 呼び出し"])
    Loop["全 PolicyRecord を評価"]
    Check{"total-failure-session-count<br>> 0?"}
    ReturnTrue["true を返す"]
    ReturnFalse["false を返す"]

    Start --> Loop
    Loop --> Check
    Check -->|"Yes（いずれかが条件を満たす）"| ReturnTrue
    Check -->|"No（全て 0、または空）"| ReturnFalse
```

---

## 7. テスト戦略

### 単体テスト

| テスト対象 | テストケース | 対応要件 |
|---|---|---|
| `ParseGzip()` | 有効な gzip 圧縮 JSON → `*Report` が返る | `AC-01`, `AC-06` |
| `ParseJSON()` | 有効な非圧縮 JSON → `*Report` が返る | `AC-02`, `AC-06` |
| `ParseGzip()` | 不正な gzip データ → エラー返却 | `AC-03` |
| `ParseGzip()` | 有効な gzip だが展開後 JSON 不正 → エラー返却 | `AC-04` |
| `ParseGzip()` | gzip 展開サイズ上限超過 → `ErrDecompressedSizeLimitExceeded` | `AC-05`, NFR セキュリティ |
| `ParseJSON()` | 入力サイズ上限超過 → `ErrDecompressedSizeLimitExceeded` | `AC-05` |
| `ParseGzip()` / `ParseJSON()` | 必須フィールド欠如（各フィールド個別）→ `ErrMissingRequiredField` | `AC-07` |
| `ParseGzip()` / `ParseJSON()` | `policies` 配列の各フィールドが正しくパースされる | `AC-08` |
| `ParseGzip()` / `ParseJSON()` | `failure-details` フィールドが存在する場合に正しく取得できる | `AC-09` |
| `HasFailure()` | 全ポリシーレコードの `total-failure-session-count` が 0 → `false` | `AC-11` |
| `HasFailure()` | いずれかのポリシーレコードの `total-failure-session-count` が 1 以上 → `true` | `AC-12` |
| `HasFailure()` | `policies` が空 → `false` | `AC-13` |

### 統合テスト

| テスト対象 | テストケース | 対応要件 |
|---|---|---|
| `Parse()` | `testdata/` 内の実際のレポートファイルを正しくパースできる | `AC-10` |

統合テストでは `testdata/tlsrpt_google.eml` から `internal/mailparse` で抽出した `.json.gz` 添付ファイルのバイト列を使用する。

### セキュリティテスト

| テスト対象 | テストケース |
|---|---|
| `Parse()` | zip bomb 相当の高圧縮データ → `ErrDecompressedSizeLimitExceeded` |

---

## 8. 実装優先順位

### フェーズ 1: 構造体定義とパース（`F-001`, `F-002`）

1. `Report` および関連構造体の定義（JSON タグ含む）
2. エラー型 `ErrDecompressedSizeLimitExceeded`・`ErrMissingRequiredField` の定義
3. `Parse()` 関数の実装（gzip 展開 → JSON パース → 必須フィールド検証）
4. 単体テストの実装（有効データ・不正データ・必須フィールド欠如）

### フェーズ 2: failure 評価（`F-003`）

1. `HasFailure()` メソッドの実装
2. 境界値テストの実装（0件・1件・複数件・空）

### フェーズ 3: 統合テスト（`F-002` AC-5）

1. `testdata/tlsrpt_google.eml` を使った統合テストの実装

---

## 9. 将来の拡張性

- **RFC 8460 オプションフィールドの追加**: RFC 8460 には `contact-info` など省略可能なフィールドが多数存在する。現時点では必須フィールドのみを定義するが、将来的に必要に応じてフィールドを追加できる構造体レイアウトとする。
- **FailureDetail の活用**: `FailureDetail` は現時点では即時アラートのトリガー判定に使用しないが、将来の詳細通知機能で利用できるよう構造体として保持する。
