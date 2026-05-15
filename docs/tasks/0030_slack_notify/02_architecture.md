# アーキテクチャ設計書：Slack 通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-15 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 設計の全体像

### 1.1 設計原則

1. **出力パスの分離**: Debug Logger（stdout/stderr/file）と Slack ハンドラは完全に独立したパスとする（通知セキュリティガイドラインに準拠）
2. **集約バッファ型**: `Handle()` はバッファに積み、ポーリング実行の終了時に `Flush()` で 1 回の HTTP POST にまとめる
3. **型安全な通知**: 外部コードは型付きヘルパー（`LogAlert`、`LogSystemError`、`LogSummary`）経由でのみ通知ロガーに書き込む
4. **二段階初期化**: TOML 読み込み前は Debug Logger のみ初期化し、Slack ハンドラは TOML 読み込み後に追加する
5. **Secret 型**: Webhook URL を保持する全フィールドは `config.Secret` でラップし、ログ漏洩を防ぐ

### 1.2 コンセプトモデル

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    IMAP[("IMAP<br>Mailbox")] --> Fetch["Polling Loop"]
    Fetch --> Parse["TLSRPT Parse"]
    Parse -->|"failure > 0"| Alert["LogAlert()"]
    Parse -->|"failure = 0"| Accum["Accumulate"]
    Fetch -->|"system error"| SysErr["LogSystemError()"]
    Accum -->|"at interval"| Summary["LogSummary()"]
    Alert --> Notifier["SlackHandler<br>(buffer)"]
    SysErr --> Notifier
    Summary --> Notifier
    Notifier -->|"Flush()"| SlackAPI[("Slack API")]
    Parse -.->|"debug logs"| DebugLog["Debug Logger"]
    Fetch -.->|"debug logs"| DebugLog

    class IMAP,SlackAPI data
    class Fetch,Parse process
    class Alert,SysErr,Summary,Notifier,Accum,DebugLog newpkg
```

**凡例（Legend）**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    D[("設定・環境データ")] --> P["既存コンポーネント"] --> N["新規コンポーネント"]
    class D data
    class P process
    class N newpkg
```

---

## 2. システム構成

### 2.1 全体アーキテクチャ

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    subgraph Bootstrap["二段階起動フロー"]
        ENV[("環境変数<br>WEBHOOK_URL_*")] --> Phase1["Phase 1:<br>Debug Logger 初期化"]
        Phase1 --> TOML[("TOML<br>allowed_host")]
        TOML --> Phase2["Phase 2:<br>Slack ハンドラ追加"]
    end

    subgraph PollingRun["ポーリング実行"]
        Loop["main loop"] --> HandleRec["Handle()<br>× N 回"]
        HandleRec --> Buffer[("内部バッファ")]
        Buffer --> Flush["Flush()"]
        Flush -->|"WARN/ERROR"| ErrorHook[("Slack<br>error webhook")]
        Flush -->|"INFO"| SuccessHook[("Slack<br>success webhook")]
        Flush -.->|"dry-run 時"| DebugLog["Debug Logger"]
    end

    class ENV,TOML,Buffer,ErrorHook,SuccessHook data
    class Loop,Phase1 process
    class Phase2,HandleRec,Flush enhanced
    class DebugLog newpkg
```

**凡例（Legend）**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    D[("設定・環境データ")] --> P["既存コンポーネント"]
    P --> E["変更・追加コンポーネント"] --> N["新規パッケージ"]
    class D data
    class P process
    class E enhanced
    class N newpkg
```

### 2.2 パッケージ依存関係

```mermaid
flowchart LR
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef newpkg fill:#ffe8f5,stroke:#d946ef,stroke-width:2px,color:#701a75;

    CMD["cmd/tlsrpt-digest"] --> NOTIFY["internal/notify"]
    CMD --> IMAP["internal/imap"]
    CMD --> TLSRPT["internal/tlsrpt"]
    NOTIFY --> CONFIG["internal/config"]
    IMAP --> CONFIG

    class CMD,IMAP,TLSRPT,CONFIG process
    class NOTIFY newpkg
```

### 2.3 起動フロー（シーケンス）

```mermaid
sequenceDiagram
    participant M as "cmd/tlsrpt-digest"
    participant NB as "notify/bootstrap"
    participant SH as "notify.SlackHandler"

    M->>NB: SetupLogging(opts)
    Note over NB: Phase 1: Debug Logger のみ初期化<br>Slack ハンドラなし（AC-33）

    M->>M: LoadTOML()
    Note over M: allowed_host を取得

    M->>NB: AddSlackHandlers(config)
    NB->>SH: NewSlackHandler(SlackHandlerOptions{...})
    alt URL 検証失敗（AC-35）
        SH-->>NB: ErrInvalidWebhookURL
        NB-->>M: 設定エラーで起動中断
    else 検証成功（AC-34）
        SH-->>NB: "success 用 SlackHandler"
        SH-->>NB: "error 用 SlackHandler"
        NB-->>M: nil
    end
```

### 2.4 ポーリング実行フロー（シーケンス）

```mermaid
sequenceDiagram
    participant L as "main loop"
    participant H as "notify.SlackHandler"
    participant S as "Slack API"

    Note over L,H: ポーリング実行開始
    L->>H: Handle(tlsFailureRecord)
    Note over H: バッファに追記・HTTP 送信なし（AC-05b）
    L->>H: Handle(systemErrorRecord)
    Note over H: バッファに追記・HTTP 送信なし（AC-05b）

    Note over L,H: ポーリング実行終了
    L->>H: Flush(ctx)
    H->>H: formatAlerts() → 集約メッセージ（AC-17〜AC-20e）
    H->>S: POST error webhook
    alt 5xx または 429
        S-->>H: エラーレスポンス
        H->>H: バックオフ後リトライ（AC-28）
        H->>S: POST error webhook
        S-->>H: 200 OK
    else 4xx（429 以外）
        S-->>H: エラーレスポンス
        H-->>L: ErrClientError（AC-30）
    end
    H->>H: formatSystemError() → 個別メッセージ（AC-20j〜AC-20l）
    H->>S: POST error webhook
    H-->>L: nil / error（AC-04）
```

---

## 3. コンポーネント設計

### 3.1 主要インターフェース

```go
// Flusher はバッファに蓄積されたレコードを送信するインターフェース。
// SlackHandler は slog.Handler に加えてこのインターフェースを実装する。
type Flusher interface {
    Flush(ctx context.Context) error
}
```

### 3.2 主要型定義

```go
// SlackHandlerOptions は SlackHandler の生成オプション。
// Webhook URL は config.Secret でラップし、ログへの漏洩を防ぐ。
type SlackHandlerOptions struct {
    WebhookURL    config.Secret
    AllowedHost   string
    RunID         string
    LevelMode     LevelMode
    IsDryRun      bool
    BackoffConfig BackoffConfig
}

// LevelMode は SlackHandler のレベルフィルタリングモードを定義する。
type LevelMode int

const (
    LevelModeExactInfo    LevelMode = iota // INFO のみ（success webhook 用）
    LevelModeWarnAndAbove                  // WARN 以上（error webhook 用）
)

// BackoffConfig はリトライ時のバックオフ設定。
type BackoffConfig struct {
    Base       time.Duration
    RetryCount int
}

// Alert は即時アラート（TLS failure）の通知ペイロード。
// public フィールドのみ含み、機密情報は含まない。
type Alert struct {
    OrganizationName string
    PolicyType       string // "sts" / "tlsa" / "no-policy-found"
    FailureCount     int64
    DateRange        tlsrpt.DateRange
}

// SystemError はシステムエラーアラートの通知ペイロード。
type SystemError struct {
    ErrorType string
    Message   string
    Component string // "imap" / "storage" / "tlsrpt" 等
}
```

### 3.3 コンポーネント責務表

| ファイル | 責務 | 新規/変更 |
|---|---|---|
| `internal/notify/handler.go` | `SlackHandler` 実装（`slog.Handler` + `Flusher`）、バッファ管理 | **新規** |
| `internal/notify/options.go` | `SlackHandlerOptions`、`BackoffConfig`、`LevelMode` 型定義 | **新規** |
| `internal/notify/message.go` | Slack API ペイロード型（`SlackMessage`、`SlackAttachment` 等） | **新規** |
| `internal/notify/format.go` | メッセージフォーマット（`formatAlerts()`、`formatSystemError()`、`formatSummary()`）、切り詰め処理 | **新規** |
| `internal/notify/helpers.go` | 型付きヘルパー（`LogAlert()`、`LogSystemError()`、`LogSummary()`） | **新規** |
| `internal/notify/retry.go` | HTTP 送信とリトライロジック（`Retry-After` ヘッダー対応） | **新規** |
| `internal/notify/validate.go` | Webhook URL 検証（`validateWebhookURL()`） | **新規** |
| `internal/notify/errors.go` | エラー型定義（`ErrInvalidWebhookURL` 等） | **新規** |
| `internal/notify/spy.go` | テスト用スパイハンドラ（`SpyHandler`） | **新規** |
| `internal/config/secret.go` | `Secret` 型（Webhook URL を保持するフィールドに適用） | 既存・変更なし |
| `cmd/tlsrpt-digest/main.go` | 二段階起動フローの呼び出し側 | **変更** |

---

## 4. エラーハンドリング設計

### 4.1 エラー型

```go
// ErrInvalidWebhookURL は Webhook URL 検証失敗を示す sentinel error。
var ErrInvalidWebhookURL = errors.New("invalid webhook URL")

// ErrServerError は Slack API が 5xx または 429 を返した場合。
var ErrServerError = errors.New("slack server error")

// ErrClientError は Slack API が回復不能な 4xx を返した場合。
var ErrClientError = errors.New("slack client error")
```

### 4.2 エラー処理方針

| シナリオ | 発生場所 | 処理 |
|---|---|---|
| URL 検証失敗（スキーム不正・ホスト不一致等） | `NewSlackHandler()` → Phase 2 | 設定エラーとして起動を中断 |
| HTTP タイムアウト・接続エラー | `Flush()` → retry loop | 最大 3 回リトライ後、エラーを返す |
| HTTP 5xx | `Flush()` → retry loop | 指数バックオフでリトライ |
| HTTP 429 | `Flush()` → retry loop | `Retry-After` ヘッダー優先でリトライ（`AC-28`） |
| HTTP 4xx（429 以外） | `Flush()` | 即座に `ErrClientError` を返す（`AC-30`） |
| 全リトライ失敗 | `Flush()` | エラーを返し、Debug Logger にも記録（`AC-04`） |
| `context` キャンセル | リトライ待機中 | 待機を中断し `ctx.Err()` を返す（`AC-32`） |
| TOML への Webhook URL 混入 | config デコード | `internal/config` の strict デコードで unknown-key エラー（`AC-26a`） |

---

## 5. セキュリティ考慮事項

通知セキュリティ設計は [通知セキュリティガイドライン](../../dev/developer_guide/notification_security.ja.md) に準拠する。

### 5.1 脅威モデル

```mermaid
flowchart TD
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    ENV[("環境変数<br>WEBHOOK_URL_*")] --> SHOpts["SlackHandlerOptions<br>(Secret フィールド)"]
    SHOpts --> Val["validateWebhookURL()<br>allowed_host 照合"]
    Val -->|"一致"| SH["SlackHandler"]
    Val -->|"不一致"| Err["ErrInvalidWebhookURL"]

    IMAP[("IMAP 通信")] --> DebugLog["Debug Logger<br>(stdout / file)"]
    IMAP -.->|"機密漏洩リスク"| Leak["❌ Slack に流出"]
    DebugLog -.->|"設計で防止"| Leak

    TypedHelper["LogAlert() / LogSystemError()"] --> SH
    RawLogger["logger.Info() 直接呼び出し"] -.->|"型安全設計で禁止"| SH

    class ENV,IMAP data
    class SHOpts,Val,SH,TypedHelper enhanced
    class DebugLog process
    class Err,Leak,RawLogger problem
```

**凡例（Legend）**

```mermaid
flowchart LR
    classDef data fill:#e6f7ff,stroke:#1f77b4,stroke-width:1px,color:#0b3d91;
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef problem fill:#ffe6e6,stroke:#d62728,stroke-width:2px,color:#7b0000;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    D[("設定・環境データ")] --> P["既存コンポーネント"]
    P --> E["変更・追加コンポーネント"]
    X["問題箇所"]
    class D data
    class P process
    class E enhanced
    class X problem
```

### 5.2 セキュリティ対策一覧

| 脅威 | 対策 | 対応 AC |
|---|---|---|
| Webhook URL のログ漏洩 | `config.Secret` でラップ、`String()` / `LogValue()` は `[REDACTED]` | 非機能要件 |
| Webhook URL の TOML 混入 | `internal/config` の strict デコードで unknown-key エラー | `AC-26a` |
| 任意ホストへの SSRF | `allowed_host` によるホスト名検証 | `AC-22` |
| HTTP スキームのダウングレード | `https` 以外を設定エラーとする | `AC-21` |
| IMAP 認証情報の Slack 流出 | 型付きヘルパー経由のみ許可・Debug Logger を分離 | F-001 設計原則 |
| 機密情報のメッセージ混入 | `Alert` / `SystemError` 型が公開情報のみ含む設計 | F-001 設計原則 |

---

## 6. 処理フロー詳細

### 6.1 Flush() の処理フロー

```mermaid
flowchart TD
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    Start(["Flush(ctx) 呼び出し"]) --> Empty{"バッファが空?"}
    Empty -->|"Yes"| RetNil(["nil を返す（AC-05a）"])
    Empty -->|"No"| DryRun{"dry-run?"}
    DryRun -->|"Yes"| LogDebug["Debug Logger に出力（AC-38）"]
    LogDebug --> Clear1["バッファをクリア"]
    Clear1 --> RetNil2(["nil を返す"])
    DryRun -->|"No"| Split["レコードを種別ごとに分類"]
    Split --> AggMsg["TLS failure を集約メッセージに変換<br>（AC-17〜AC-20e）"]
    Split --> SysMsgs["システムエラーを個別メッセージに変換<br>（AC-20j〜AC-20l）"]
    AggMsg --> Send["error webhook へ逐次送信（AC-20m）"]
    SysMsgs --> Send
    Send --> Clear2["バッファをクリア"]
    Clear2 --> RetResult(["error / nil を返す（AC-04）"])

    class Start,RetNil,RetNil2,RetResult process
    class Empty,DryRun,Split enhanced
```

### 6.2 HTTP リトライフロー

```mermaid
flowchart TD
    classDef process fill:#fff1e6,stroke:#ff7f0e,stroke-width:1px,color:#8a3e00;
    classDef enhanced fill:#e8f5e8,stroke:#2e8b57,stroke-width:2px,color:#006400;

    Post(["HTTP POST（AC-27: timeout 5s）"]) --> Resp{"レスポンス"}
    Resp -->|"2xx"| OK(["成功（nil）"])
    Resp -->|"5xx または<br>接続エラー"| Retry{"リトライ残あり?"}
    Resp -->|"429"| RetryAfter{"Retry-After<br>ヘッダーあり?"}
    RetryAfter -->|"Yes"| WaitHeader["指定時間待機（AC-28）"]
    RetryAfter -->|"No"| WaitBackoff["指数バックオフ待機（AC-28）"]
    WaitHeader --> Retry
    WaitBackoff --> Retry
    Retry -->|"Yes かつ<br>ctx 未キャンセル"| Post
    Retry -->|"No"| ErrServer(["ErrServerError（AC-31）"])
    Retry -->|"ctx キャンセル"| ErrCtx(["ctx.Err()（AC-32）"])
    Resp -->|"4xx（429 以外）"| ErrClient(["ErrClientError（AC-30）"])

    class Post,OK,ErrServer,ErrCtx,ErrClient process
    class Resp,Retry,RetryAfter enhanced
```

### 6.3 Slack メッセージフォーマット

TLS failure 集約・定期サマリ・システムエラーそれぞれの構造。

| 通知種別 | `text` | `attachment.color` | 絵文字 | 主な `fields` |
|---|---|---|---|---|
| TLS failure 集約 | `"⚠️ TLS Failures – N organizations affected"` | `warning` | ⚠️ | 組織名、ポリシータイプ、failure 数、期間、Run ID |
| システムエラー（1 件ごと） | `"🚨 System Error: <ErrorType>"` | `danger` | 🚨 | エラーメッセージ、コンポーネント、Run ID |
| 定期サマリ | `"✅ TLS Report Summary"` | `good` | ✅ | 対象期間、組織数、Run ID |

メッセージ本文が 4000 文字、個別フィールド値が 1000 文字を超えた場合は `...` で切り詰め（Slack 送信のみ）。ファイルログへは全文を記録（`AC-20d`）。

---

## 7. テスト戦略

### 7.1 単体テスト

| テスト対象 | テスト内容 | 対応 AC |
|---|---|---|
| `SlackHandler.Handle()` | HTTP 送信なし、バッファへの追記のみ | `AC-05b` |
| `Flusher.Flush()` | 空バッファで nil 返却 | `AC-05a` |
| `Flusher.Flush()` | dry-run 時に HTTP POST 不発、Debug Logger 出力 | `AC-38` |
| `formatAlerts()` | 組織名・ポリシータイプ・failure 数・期間・Run ID 含有確認 | `AC-17`〜`AC-20e` |
| `formatAlerts()` | タイトルに影響組織数 N が含まれる | `AC-20e` |
| `formatAlerts()` | `attachment.color = warning`、絵文字 ⚠️ | `AC-20f` |
| `formatAlerts()` | メッセージ長切り詰め（4000 / 1000 文字） | `AC-20b` `AC-20c` |
| `formatAlerts()` | ファイルログには全文出力（切り詰めなし） | `AC-20d` |
| `formatSystemError()` | エラー種別・メッセージ・コンポーネント含有確認 | `AC-20j`〜`AC-20l` |
| `formatSystemError()` | `attachment.color = danger`、絵文字 🚨 | `AC-20g` |
| `validateWebhookURL()` | HTTPS スキーム強制、ホスト一致確認 | `AC-21`〜`AC-26` |
| Retry logic | 5xx リトライ・429 `Retry-After` 優先・4xx 即失敗 | `AC-28`〜`AC-32` |
| `SpyHandler` | テスト用ハンドラの基本動作 | - |

### 7.2 統合テスト

| テスト内容 | 使用ツール |
|---|---|
| `httptest.NewServer` によるモック Slack サーバへの送信検証 | `net/http/httptest` |
| 5xx → 200 のリトライ復帰シナリオ | モックサーバで段階的レスポンス制御 |
| 二段階起動フロー全体（TOML 読み込み → Slack ハンドラ追加） | テスト用 TOML + `testing` パッケージ |

### 7.3 セキュリティテスト

| テスト内容 | 対応 AC |
|---|---|
| `config.Secret` フィールドが通知メッセージに含まれないこと | 非機能要件 |
| Debug Logger への書き込みが Slack ハンドラを起動しないこと | F-001 設計原則 |
| Webhook URL がログ出力に含まれないこと | 非機能要件 |
| `internal/notify` の通知 `*slog.Logger` が外部エクスポートされていないこと | F-001 設計原則 |

---

## 8. 実装優先度

### Phase 1: コア型・検証・エラー

1. `internal/notify/errors.go` — エラー型定義（`ErrInvalidWebhookURL` 等）
2. `internal/notify/options.go` — `SlackHandlerOptions`、`LevelMode`、`BackoffConfig`
3. `internal/notify/validate.go` — `validateWebhookURL()`（`AC-21`〜`AC-26a`）

### Phase 2: HTTP 送信・ハンドラ

4. `internal/notify/message.go` — Slack API ペイロード型
5. `internal/notify/retry.go` — HTTP 送信・リトライロジック（`AC-27`〜`AC-32`）
6. `internal/notify/handler.go` — `SlackHandler`（`AC-01`〜`AC-05b`）

### Phase 3: フォーマット・ヘルパー

7. `internal/notify/format.go` — メッセージフォーマット（`AC-17`〜`AC-20m`）
8. `internal/notify/helpers.go` — 型付きヘルパー（`LogAlert`、`LogSystemError`、`LogSummary`）

### Phase 4: 起動統合・テスト

9. `internal/notify/spy.go` — スパイハンドラ
10. `cmd/tlsrpt-digest/main.go` — 二段階起動フロー（`AC-33`〜`AC-40`）
11. テスト実装（単体・統合・セキュリティ）

---

## 9. 将来の拡張性

| 拡張 | 設計上の準備 |
|---|---|
| メール通知の追加 | `Flusher` インターフェースを `notify` パッケージ内に閉じ込めており、同インターフェースを実装する別ハンドラを追加するだけで対応可能 |
| `slog.MultiHandler` への統合 | `SlackHandler` は `slog.Handler` を実装するため、既存の `MultiHandler` に追加するだけで統合できる |
| 定期サマリ間隔の変更 | 間隔制御は `cmd/tlsrpt-digest` 側の責務（タスク `0050`）であり、本パッケージへの変更は不要 |
| 通知の重複制御 | バッファ設計を拡張することで、重複 ID の除去などを `Flush()` 前処理に追加可能 |
