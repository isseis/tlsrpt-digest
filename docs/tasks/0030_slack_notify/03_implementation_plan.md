# 実装計画書：Slack 通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-15 |
| レビュー日 | 2026-05-16 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

`internal/notify` パッケージを新規実装し、Slack Incoming Webhook を通じた即時アラートおよび定期サマリの送信機能を提供する。
設計詳細は [02_architecture.md](02_architecture.md) を参照。

### 1.2 実装原則

- アーキテクチャ設計書（§3〜§6）に記載したインターフェース・型定義に従う
- Go コード内のコメント・識別子・文字列リテラルは英語で記述する
- 既存の `config.Secret` 型を再利用する（`internal/config/secret.go`）
- テストヘルパーは [test_organization.md](../../dev/developer_guide/test_organization.md) の分類ルールに従う

---

## 2. 実装ステップ

### Phase 1: 設定契約・コア型・検証

---

#### Step 1-1: TOML 設定への `notify.slack` セクション追加

**対象ファイル**: `internal/config/config.go`（新規または既存ファイルへ追記）, `internal/config/config_test.go`（新規）, `internal/config/secret_test.go`（新規）

- [x] `NotifySlackConfig` 構造体を定義する（フィールド: `AllowedHost string`）
- [x] `Config` 構造体に `Notify NotifyConfig` を追加し、`NotifyConfig` が `Slack NotifySlackConfig` を持つ
- [x] TOML デコードに strict モード（unknown-key 拒否）を適用する
- [x] `allowed_host` の形式検証を追加する（空文字は許容、スキーム・ポート番号・前後空白は拒否）

**成功基準**: TOML に `notify.slack.allowed_host = "hooks.slack.com"` を記述してデコードが成功する。未知キー（例: `webhook_url`）または不正な `allowed_host` を記述するとデコードまたは設定検証エラーになる。

**対応 AC**: `AC-26a`

**テスト**:
- [x] `internal/config/config_test.go::TestNotifySlackConfig_UnknownKey`: `notify.slack.webhook_url = "..."` でデコードエラーになること
- [x] `internal/config/config_test.go::TestNotifySlackConfig_AllowedHostValidation`: `hooks.slack.com` は許容し、スキーム・ポート・前後空白付き入力は拒否すること
- [x] `internal/config/secret_test.go::TestSecret_RedactsStringAndLogValue`: `String()` と `LogValue()` が常に `[REDACTED]` を返すこと

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-2: エラー型定義

**対象ファイル**: `internal/notify/errors.go`

- [x] `WebhookValidationError` 型（`Error() string` 実装）を定義する
- [x] `SlackServerError` 型を定義する（HTTP 5xx / 429 / 接続エラー）
- [x] `SlackClientError` 型を定義する（HTTP 4xx、429 を除く）
- [x] 必要に応じて `Unwrap()` を実装し、呼び出し側が `errors.AsType[T]` で失敗種別を識別できるようにする

**成功基準**: 呼び出し側が `errors.AsType[*WebhookValidationError]`、`errors.AsType[*SlackServerError]`、`errors.AsType[*SlackClientError]` で失敗種別を識別できる。

**対応 AC**: `AC-04`, `AC-05`, `AC-30`, `AC-31`, `AC-35`

**テスト**: `internal/notify/errors_test.go`
- [x] `TestWebhookValidationError_AsType`: `errors.AsType[*WebhookValidationError]` による型一致確認
- [x] `TestSlackServerError_AsType`
- [x] `TestSlackClientError_AsType`

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-3: コア型・オプション定義

**対象ファイル**: `internal/notify/options.go`, `internal/notify/types.go`

- [x] `LevelMode` 型（`string` 基底）と定数 `LevelModeExactInfo`、`LevelModeWarnAndAbove` を定義する
- [x] `BackoffConfig` 構造体（`Base time.Duration`, `RetryCount int`）を定義する
- [x] `DefaultBackoffConfig` 変数（Base: 2s, RetryCount: 3）を定義する
- [x] `SlackHandlerOptions` 構造体を定義する（`WebhookURL config.Secret`, `AllowedHost string`, `RunID string`, `LevelMode LevelMode`, `IsDryRun bool`, `BackoffConfig BackoffConfig`, `DebugLogger *slog.Logger`, `HTTPClient *http.Client`）。`DebugLogger` は dry-run ログ・送信失敗ログの出力先（`nil` の場合は無音）。`HTTPClient` はテスト用 TLS クライアント注入用（`nil` の場合はデフォルト 5 秒タイムアウト）。AC-27 の 5 秒タイムアウトは注入された `HTTPClient` の `Timeout` ではなく、retry.go がリクエストごとに付与する `context` デッドライン（`context.WithTimeout`）で保証するため、注入クライアントが `Timeout == 0` でも AC-27 は満たされる
- [x] `PolicyType` 型と定数（`PolicyTypeSTS`, `PolicyTypeTLSA`, `PolicyTypeNoPolicyFound`, `PolicyTypeUnknown`）を定義する
- [x] `DateRange` 構造体（`Start, End time.Time`）を定義する
- [x] `Alert` 構造体を定義する（`OrganizationName string`, `PolicyType PolicyType`, `FailureCount int64`, `DateRange DateRange`）
- [x] `SystemError` 構造体を定義する（`ErrorType string`, `Message string`, `Component string`）
- [x] `Summary` 構造体を定義する（`Period DateRange`, `OrganizationCount int`, `ReportCount int`）
- [x] `Flusher` インターフェースを定義する（`Flush(ctx context.Context) error`）

**成功基準**: `go build ./internal/notify/...` が通る。

**対応 AC**: `AC-37`（dry-run フラグ）, `AC-18`（PolicyType 定数）

**テスト**:
- [-] `internal/notify/types_test.go::TestPolicyType_Constants` — 削除済み（constants asserting their own values; trivial）。`PolicyType` 定数の正確さは `TestFormatAlerts_NoPolicyFound`・`TestFormatAlerts_PolicyTypeUnknown`（format_test.go）でカバー
- [x] `internal/notify/options_test.go::TestSlackHandlerOptions_DryRun`: `IsDryRun` フィールドが option として保持されること（`AC-37`）

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-4: URL・環境変数の組み合わせ検証

**対象ファイル**: `internal/notify/validate.go`

- [x] `ValidateEnvCombination(successURL, errorURL string) error` を実装する
  - success のみ → `WebhookValidationError`
  - 両方未設定 → `nil`（Slack 無効）
  - error のみ、または両方設定 → `nil`（継続）
- [x] `validateWebhookURL(webhookURL string, allowedHost string) error` を実装する
  - スキームが `https` 以外 → エラー
  - ホスト名が `allowedHost` と不一致 → エラー（ポート除去、大小文字無視の完全一致）
  - `allowedHost` が空かつ URL あり → エラー
- [-] `validateBothURLs` — 削除済み。両 URL が同一 `allowedHost` に一致することで推移律によりホスト一致が保証されるため、個別 `validateWebhookURL` 呼び出しで十分

**成功基準**: 各パターンの入力に対して期待通りのエラー型が返る。

**対応 AC**: `AC-06`, `AC-07`, `AC-08`, `AC-09`, `AC-10`, `AC-21`, `AC-22`, `AC-23`, `AC-24`, `AC-25`, `AC-26`

**テスト**: `internal/notify/validate_test.go`
- [x] `TestValidateEnvCombination`: success のみはエラー、両方未設定は `nil`、error のみまたは両方設定は `nil` になること（`AC-06`〜`AC-09`）
- [x] `TestValidateWebhookURL_SameURLAllowed`: success / error が同一 URL でも許容されること（`AC-10`）
- [x] `TestValidateWebhookURL_HTTPScheme`: `http://` がエラーになること（`AC-21`）
- [x] `TestValidateWebhookURL_HostMismatch`: `allowed_host` と異なるホスト名がエラーになること（`AC-22`）
- [x] `TestBuildHandlers_DifferentHosts`（旧 `TestValidateWebhookURL_BothURLsDifferentHost`）: success / error で異なるホスト名がエラーになること（`AC-23`）
- [x] `TestValidateWebhookURL_PortStripped`: ポート番号付き URL でもホスト照合できること（`AC-24`）
- [x] `TestValidateWebhookURL_CaseInsensitive`: ホスト名の大文字小文字を無視して照合すること（`AC-24`）
- [x] `TestValidateWebhookURL_NoAllowedHost`: `allowed_host` が空で URL がある場合にエラーになること（`AC-25`）
- [x] `TestValidateWebhookURL_BothEmpty`: 両 URL 未設定時は URL 検証をスキップすること（`AC-26`）

**推定工数**: 0.5 日

**実績**: -

---

### Phase 2: ペイロード・配送・ハンドラ

---

#### Step 2-1: Slack API ペイロード型の定義

**対象ファイル**: `internal/notify/message.go`

- [x] `slackMessage`、`slackAttachment`、`slackField` を定義する
- [x] JSON フィールド定義を [02_architecture.md](02_architecture.md) の §6.3 と Slack Incoming Webhook 仕様に整合させる
- [x] `attachment.fields` を使った構造化表現を前提にした最小限の型に留める

**成功基準**: `encoding/json` でシリアライズした JSON が Slack Incoming Webhook の仕様に準拠する。

**対応 AC**: `AC-20i`

**テスト**: `internal/notify/message_test.go`
- [x] `TestSlackMessage_JSONShape`: `text` と `attachments` が期待するキー名で出力されること
- [x] `TestSlackAttachment_FieldsEncoding`: `fields` が配列としてシリアライズされること

**推定工数**: 0.25 日

**実績**: -

---

#### Step 2-2: HTTP 送信とリトライ実装

**対象ファイル**: `internal/notify/retry.go`

- [x] Slack Webhook への POST 処理を実装する
- [x] 各 HTTP リクエストに `context.WithTimeout(ctx, 5*time.Second)` でデッドラインを付与する（`http.Client.Timeout` ではなくコンテキストで制御することで、注入された `HTTPClient` の `Timeout` 設定に依存せず AC-27 を保証する）
- [x] 5xx / 429 / リクエスト発行失敗をリトライ対象にする
- [x] `Retry-After` ヘッダーがある場合はその値（秒単位の整数）を優先して待機し、ない場合は指数バックオフを使う。Slack は秒整数のみ返すが、パース失敗時はバックオフにフォールバックする（HTTP-date 形式は Slack では使用されないためスコープ外）
- [x] 累積待機時間を追跡し、残り余裕（例: `14s - 既払い待機時間`）が次の待機に満たない場合は次のリトライを行わず即エラーにする。これにより `5s × 4 + 待機 ≤ 34s` の保証が維持される
- [x] 4xx（429 を除く）は即座に `SlackClientError` を返す
- [x] `context` キャンセル時は待機を中断して `ctx.Err()` を返す
- [x] 各レスポンスの `resp.Body.Close()` を確実に呼ぶ（リトライ前にもクローズしてコネクション再利用を保証する）
- [x] 待機処理は注入可能な関数または時刻取得抽象に切り出し、テストで実時間の `sleep` を避ける

**成功基準**: `go test ./internal/notify/...` が通り、回復可能な失敗のみが再試行される。

**対応 AC**: `AC-27`, `AC-28`, `AC-29`, `AC-30`, `AC-31`, `AC-32`

**テスト**: `internal/notify/retry_test.go`
- [x] `TestHTTPPost_Timeout`: 5 秒タイムアウトになること（`AC-27`）
- [x] `TestHTTPPost_5xxRetry`: 5xx 応答で再試行すること（`AC-28`）
- [x] `TestHTTPPost_429WithRetryAfter`: `Retry-After` を優先して待機すること（`AC-28`）
- [x] `TestHTTPPost_429WithoutRetryAfter`: 指数バックオフで待機すること（`AC-28`）
- [x] `TestHTTPPost_RequestFailureRetry`: 接続エラーで再試行すること（`AC-29`）
- [x] `TestHTTPPost_4xxImmediate`: 4xx（429 以外）で即時 `SlackClientError` を返すこと（`AC-30`）
- [x] `TestHTTPPost_AllRetriesExhausted`: 全リトライ失敗後に `SlackServerError` を返すこと（`AC-31`）
- [x] `TestHTTPPost_ContextCancel`: `context` キャンセルで待機を中断すること（`AC-32`）
- [x] `TestHTTPPost_ResponseBodyClosed`: 5xx リトライ時に前のレスポンスボディがクローズされること
- [x] `TestHTTPPost_RetryAfterCapped`: 過大な `Retry-After` 値が上限でキャップされること

**推定工数**: 0.5 日

**実績**: -

---

#### Step 2-3: SlackHandler 実装

**対象ファイル**: `internal/notify/handler.go`

- [x] `SlackHandler` が `slog.Handler` と `Flusher` を実装する
- [x] Step 1-3 で定義した `DebugLogger` / `HTTPClient` を利用し、dry-run ログ・送信失敗ログ・テスト用 TLS クライアント注入を `slog.Default()` に依存せず扱えるようにする
- [x] `NewSlackHandler` で URL 検証、レベルモード、dry-run、Run ID、backoff 設定、DebugLogger、HTTPClient を受け取る
- [x] `Handle()` は `Record.Clone()` してから内部バッファに追加する（`slog.Record` は共有バッキングストアを持つため、`Clone()` 必須）
- [x] `Handle()` と `Flush()` は内部バッファの読み書きを `sync.Mutex` で保護する（`slog.Handler` は並行呼び出しを受けうる）
- [x] `Flush()` はスナップショット戦略を採る: mutex 取得 → バッファのスナップショットを取り現バッファをクリア → mutex 解放 → スナップショットを処理して HTTP 送信。これにより `Flush()` 実行中に `Handle()` が受け取った新レコードは次回の `Flush()` に回り、通知が失われない
- [x] `WithAttrs()` / `WithGroup()` は `nop`（`return s`）で実装する。Slack 通知は型付きヘルパー経由でのみ書き込まれるため、`With` 経由の属性付加は行われない。`*slog.Logger` の `With`/`WithGroup` API 経由での利用は設計上禁止する
- [x] `Enabled()` はハンドラ自身の `LevelMode` に基づいて判定し、CLI のコンソールログレベル設定と独立させる
- [x] `Flush()` は空バッファなら `nil` を返し、蓄積済みメッセージをフォーマットして逐次送信する
- [x] `Flush()` 内でのフォーマット→切り詰め→送信の順番: フォーマット関数は切り詰めを**行わず**全文のメッセージを返す。切り詰めは HTTP 送信直前に適用し、Debug Logger への記録には切り詰め前の全文を渡す
- [x] 送信成功後（またはエラーに関わらず Flush() 終了前）にバッファをクリアして二重送信を防ぐ
- [x] dry-run 時は HTTP POST を抑止し、DebugLogger へ記録したうえでバッファをクリアする
- [x] 最終失敗時は DebugLogger に詳細を記録してエラーを返す。このとき `net/http`/`url.Error` が内包する URL が外部に漏れないよう、`%w` を用いてエラーをラップし Webhook URL を除去したメッセージで包む（`%w` 使用でエラーチェーンを保持し、呼び出し側が `errors.AsType[*SlackServerError]` 等で型識別できる）

**成功基準**: `Handle()` と `Flush()` の責務が分離され、success / error webhook の振り分け、dry-run、エラー伝播が設計通りに動作する。

**対応 AC**: `AC-01`, `AC-02`, `AC-03`, `AC-04`, `AC-05`, `AC-05a`, `AC-05b`, `AC-14`, `AC-15`, `AC-16`, `AC-16a`, `AC-37`, `AC-38`, `AC-39`

**テスト**: `internal/notify/handler_test.go`
- [x] `TestSlackHandler_ImplementsInterface`: `slog.Handler` / `Flusher` を実装していること（`AC-01`）
- [x] `TestFlush_InfoGoesToSuccessWebhook`: INFO が success webhook に送信されること（`AC-02`）
- [x] `TestFlush_WarnGoesToErrorWebhook`: WARN が error webhook に送信されること（`AC-03`）
- [x] `TestFlush_ErrorGoesToErrorWebhook`: ERROR が error webhook に送信されること（`AC-03`）
- [x] `TestFlush_OnError_LogsToDebugLogger`: 送信失敗時に Debug Logger にエラー詳細が記録されること（`AC-04`）
- [x] `TestFlush_4xx_ImmediateError`: 4xx（429 以外）で `SlackClientError` を返すこと（`AC-05`）
- [x] `TestFlush_EmptyBuffer`: 空バッファでは `nil` を返すこと（`AC-05a`）
- [x] `TestHandle_BufferOnly`: `Handle()` 後にモックサーバへのリクエストがないこと（`AC-05b`）
- [x] `TestFlush_InfoNotToErrorWebhook`: INFO が error webhook に送信されないこと（`AC-15`）
- [x] `TestFlush_WarnNotToSuccessOnly`: WARN が success 専用ハンドラに送信されないこと（`AC-16`）
- [x] `TestCLILogLevel_Independent`: `Enabled()` が CLI のコンソールログレベル設定と独立していること（`AC-16a`）
- [x] `TestFlush_DryRun`: dry-run 時に HTTP POST 不発・DebugLogger 出力があること（`AC-38`）
- [x] `TestNewSlackHandler_URLValidation`: dry-run を含め不正 URL で `WebhookValidationError` が返ること（`AC-39`）
- [x] `TestHandle_ClonesRecord`: `Handle()` 後に元の `slog.Record` を変更してもバッファ内容が変化しないこと
- [x] `TestFlush_Concurrent`: `Handle()` と `Flush()` を goroutine で並行実行してもレースやパニックが起きないこと（`-race` フラグで実行）
- [x] `TestFlush_RecordsDuringFlushPreserved`: `Flush()` 実行中に `Handle()` で追加されたレコードが次回 `Flush()` で送信され、失われないこと（スナップショット戦略の確認）
- [x] `TestFlush_ClearsBufferAfterSend`: `Flush()` 成功後に再度 `Flush()` しても HTTP リクエストが発行されないこと
- [x] `TestFlush_MultipleAlerts_SinglePost`: 複数の `LogAlert` 呼び出し後の `Flush()` が 1 回の HTTP POST を発行すること（集約確認）

**推定工数**: 1.0 日

**実績**: -

---

### Phase 3: フォーマット・ヘルパー

---

#### Step 3-1: メッセージフォーマット実装

**対象ファイル**: `internal/notify/format.go`

- [x] `truncateText(s string, maxLen int) string` を実装する。ルーン単位で切り詰め（マルチバイト文字の途中で切らない）、`...` を末尾に付与した結果がちょうど `maxLen` 文字以内になるよう `maxLen-3` ルーンで切断する
- [x] `formatAlerts(alerts []Alert, runID string) slackMessage` を [02_architecture.md](02_architecture.md) の §6.3 に従って実装する。**切り詰めは行わず**全文のメッセージを返す（切り詰めは呼び出し元の `Flush()` が HTTP 送信直前に適用する）
- [x] `formatSystemError(e SystemError, runID string) slackMessage` を同様に実装する（切り詰めなし）
- [x] `formatSummary(s Summary, runID string) slackMessage` を同様に実装する（切り詰めなし）
- [x] 定期サマリは呼び出し側から与えられた `Summary.Period` をそのまま表示し、7 日固定などの間隔仮定を持たない

**成功基準**: 各フォーマット関数が期待する `slackMessage` を返す。

**対応 AC**: `AC-16b`, `AC-17`, `AC-18`, `AC-19`, `AC-20`, `AC-20a`, `AC-20b`, `AC-20c`, `AC-20e`, `AC-20f`, `AC-20g`, `AC-20h`, `AC-20i`, `AC-20j`, `AC-20k`, `AC-20l`

**テスト**: `internal/notify/format_test.go`
- [x] `TestFormatAlerts_Fields`: org name, PolicyType, failure count, date range が含まれること（`AC-17`〜`AC-20`）
- [x] `TestFormatAlerts_RunID`: Run ID フィールドが含まれること（`AC-20a`）
- [x] `TestFormatAlerts_TitleOrgCount`: タイトルに影響組織数 N が含まれること（`AC-20e`）
- [x] `TestFormatAlerts_Color`: `attachment.color = "warning"` / 絵文字 ⚠️ であること（`AC-20f`）
- [x] `TestTruncateText_ExactLimit`: 4000 ルーン入力で切り詰めなし、4001 ルーン入力で `...` 付与かつ結果が 4000 ルーン以内であること（`AC-20b`）
- [x] `TestTruncateText_MultibyteRune`: マルチバイト rune を含む文字列が途中で切れずルーン境界で切り詰められること
- [x] `TestTruncateField_ExactLimit`: 1000 ルーン入力で切り詰めなし、1001 ルーン入力で `...` 付与かつ結果が 1000 ルーン以内であること（`AC-20c`）
- [x] `TestFormatAlerts_NoTruncation`: `formatAlerts` 自体は切り詰めを行わないこと（切り詰めロジックを `Flush()` 側に委ねる）
- [x] `TestFormatAlerts_AttachmentFields`: `fields` 形式で構造化されていること（`AC-20i`）
- [x] `TestFormatSystemError_Title`: タイトルにエラー種別が含まれること（`AC-20j`）
- [x] `TestFormatSystemError_Fields`: Message, Component, Run ID が含まれること（`AC-20k`, `AC-20l`）
- [x] `TestFormatSystemError_Color`: `attachment.color = "danger"` / 絵文字 🚨 であること（`AC-20g`）
- [x] `TestFormatSummary_Color`: `attachment.color = "good"` / 絵文字 ✅ であること（`AC-20h`）
- [x] `TestFormatSummary_Fields`: Run ID、対象期間、組織数、レポート数が `fields` に含まれること（`AC-20a`）
- [x] `TestFormatSummary_UsesProvidedPeriod`: 呼び出し側から与えられた期間をそのまま表示し、7 日固定を仮定しないこと（`AC-16b`）
- [x] `TestFormatAlerts_NoPolicyFound`: `no-policy-found` が正しく表示されること（`AC-18`）
- [x] `TestFormatAlerts_PolicyTypeUnknown`: `PolicyTypeUnknown`（空値）が通知に含まれること（`AC-18`）

**推定工数**: 0.75 日

**実績**: -

---

#### Step 3-2: 型付きヘルパー実装

**対象ファイル**: `internal/notify/helpers.go`

- [x] `LogAlert(ctx context.Context, h slog.Handler, alert Alert) error` を実装する
  - 呼び出し前に `h.Enabled(ctx, slog.LevelWarn)` を確認し、`false` なら `nil` を即返す（`LevelMode` フィルタリングが正しく機能するよう `Handle()` を直接呼ぶ前に必ず `Enabled()` を確認する）
  - `slog.Record` を構築して `h.Handle(ctx, record)` を呼ぶ
  - ログレベル: `slog.LevelWarn`
  - `Handle()` の戻り値をそのまま返す
- [x] `LogSystemError(ctx context.Context, h slog.Handler, e SystemError) error` を実装する
  - `h.Enabled(ctx, slog.LevelError)` を確認してから `h.Handle()` を呼ぶ
  - ログレベル: `slog.LevelError`
- [x] `LogSummary(ctx context.Context, h slog.Handler, s Summary) error` を実装する
  - `h.Enabled(ctx, slog.LevelInfo)` を確認してから `h.Handle()` を呼ぶ
  - ログレベル: `slog.LevelInfo`

**成功基準**: 型付きヘルパーが正しいレベルでレコードを書き込む。外部コードが `logger.Info(...)` を直接呼べないことを設計上保証（プライベートロガー）。

**対応 AC**: `AC-14`, `AC-16a`

**テスト**: `internal/notify/helpers_test.go`
- [x] `TestLogAlert_Level`: `LogAlert` が WARN レベルのレコードを書き込むこと（`AC-14`）
- [x] `TestLogSystemError_Level`: `LogSystemError` が ERROR レベルのレコードを書き込むこと（`AC-14`）
- [x] `TestLogSummary_Level`: `LogSummary` が INFO レベルのレコードを書き込むこと（`AC-14`）
- [x] `TestLogAlert_StructuredPayloadOnly`: `Alert` 構造体のフィールドのみが `slog.Record` に含まれ、生文字列や config 情報が混入しないこと（通知セキュリティガイドライン）

**推定工数**: 0.5 日

**実績**: -

---

#### Step 3-3: ファイルログへの全文出力確認

**対象ファイル**: `internal/notify/handler.go`, `internal/notify/handler_test.go`

`Flush()` 内の処理順序: フォーマット関数（切り詰めなし）でメッセージを構築 → `DebugLogger` に全文記録 → 切り詰め適用 → HTTP POST。これによりファイルログには全文が、Slack には上限内の文字数が届く。フォーマット関数は切り詰めを行わないため（Step 3-1）、`Flush()` 内で `truncateText` を呼び出す責務を持つ。

**成功基準**: Slack 送信ペイロードにのみ切り詰めが適用され、Debug Logger には全文が出力される。

**対応 AC**: `AC-20d`

**テスト**: `internal/notify/handler_test.go` に追加
- [x] `TestFlush_FileLog_NoTruncation`: 4001 文字の組織名を含む Alert を Flush した際、Debug Logger のレコードには切り詰めなしで記録されること（`AC-20d`）

**推定工数**: 0.25 日

**実績**: -

---

### Phase 4: 起動統合・テスト

---

#### Step 4-1: スパイハンドラ（テストヘルパー）

`SpyHandler` は `slog.Handler` / `Flusher` の public API のみを使うため、[test_organization.md](../../dev/developer_guide/test_organization.md) の分類 A に従って `testutil/` へ配置する。

**対象ファイル**: `internal/notify/testutil/mocks.go`（新規）, `internal/notify/testutil/mocks_test.go`（新規）

- [x] ファイルの先頭に `//go:build test` ビルドタグを付与する（テスト専用コードが通常ビルドに混入しないようにする）
- [x] `package notifytestutil` として `SpyHandler` 構造体を実装する（`Records`, `FlushCalled bool`, `FlushErr error`）
- [x] `Handle()` では `record.Clone()` を呼んでから `Records` に追加する（`slog.Record` の共有バッキングストア問題を回避する）
- [x] `slog.Handler` インターフェースの全メソッドを実装する
- [x] `Flusher.Flush()` を実装する（記録後 `FlushErr` を返す）
- [x] `internal/notify/testutil/mocks_test.go` に `SpyHandler` の自己テストを追加する
  - `TestSpyHandler_RecordsHandle`: `Handle()` 後に Records に蓄積されること
  - `TestSpyHandler_FlushCalled`: `Flush()` 呼び出し後に `FlushCalled == true`

**成功基準**: `internal/notify` 配下の複数テストから `notifytestutil.SpyHandler` を import して再利用できる。

**推定工数**: 0.25 日

**実績**: -

---

#### Step 4-2: 複数メッセージの逐次送信確認

**対象ファイル**: `internal/notify/handler_test.go`

**テスト**: `internal/notify/handler_test.go` に追加（`httptest.NewTLSServer` + カスタム `http.Client` 使用、後述）
- [x] `TestFlush_SequentialMessages`: TLS failure と system error が同一 `Flush()` で発生した場合、2 回に分けて逐次 POST されること（`AC-20m`）

**成功基準**: 単一の `Flush()` 呼び出しで複数種別の通知が混在しても、期待順序で独立した HTTP リクエストとして送信される。

**推定工数**: 0.25 日

**実績**: -

---

#### Step 4-3: 二段階起動フローの実装

**対象ファイル**: `cmd/tlsrpt-digest/main.go`

- [x] [02_architecture.md](02_architecture.md) の §2.3 と §6.1 に従い、Phase 1 / Phase 2 の初期化をテスト可能な小関数へ切り出す
- [x] Phase 1: TOML 読み込み前にローカルハンドラ（`slog.NewTextHandler(os.Stderr, ...)` 等）を初期化する（Slack ハンドラ含まない）。`slog.SetDefault(slog.New(localHandler))` で設定する
- [x] 環境変数から `successURL`、`errorURL` を読み込み `notify.ValidateEnvCombination` を呼ぶ（`ValidateEnvCombination` は `internal/notify` でエクスポートする）
- [x] TOML を読み込んで `notify.slack.allowed_host` を取得する
- [x] Phase 2: エクスポートされた `notify.BuildHandlers(successURL, errorURL, allowedHost, opts)` を呼び、内部で `validateBothURLs`（AC-23）や各 URL の検証（`AC-21`〜`AC-26`）を行う。この関数は `0〜2` 個の `SlackHandler` を返す（`validateBothURLs` は unexported のままで `BuildHandlers` から呼ぶ）
- [x] `--dry-run` + URL 未設定の場合: `BuildHandlers` に `IsDryRun=true` を渡したうえで空 URL も許容するモード（`DryRunNoURL`）で呼び出し、URL 検証をスキップして DebugLogger 専用ハンドラを生成する（AC-38「Webhook URL を設定せずに確認」の実現）
- [x] Phase 2 の Slack ハンドラ追加は `setupNotifyHandlers()` の戻り値として `0〜2` 個の `SlackHandler` を返し、`cmd/tlsrpt-digest` 側で typed helper と `Flush()` を明示的に呼び出す。`internal/notify` に新しい合成責務は追加しない
- [x] `--dry-run` フラグを CLI に追加し、`SlackHandlerOptions.IsDryRun` に渡す
- [x] `runID` を `github.com/oklog/ulid/v2` の `ulid.Make().String()` で生成する（毎回 unique な ULID。プロセス再起動や複数同時実行でも衝突しない）

**成功基準**: Phase 1 の補助関数は Slack ハンドラ 0 件でローカル出力のみを返す。Phase 2 後は `BuildHandlers` の結果として期待する Slack ハンドラ数と `allowed_host` 伝播をテストで確認できる。

**対応 AC**: `AC-23`, `AC-33`, `AC-34`, `AC-35`, `AC-36`, `AC-38`, `AC-40`

**テスト**: `cmd/tlsrpt-digest/main_test.go`（統合テスト）

- [x] `TestBootstrap_Phase1_NoSlackHandler`: `setupPhase1Logging` がローカルハンドラのみ設定すること（`AC-33`）
- [x] `TestBootstrap_ErrorOnly_NoSuccessHandler`: error webhook のみ設定時に success ハンドラを生成しないこと（`AC-07`）
- [x] `TestBootstrap_Phase2_SlackAdded`: `setupNotifyHandlers` が期待件数の Slack ハンドラを返すこと（`AC-06`, `AC-34`, `AC-36`）
- [x] `TestBootstrap_Phase2_ValidationFail_Abort`: URL 検証失敗でエラーを返すこと（`AC-35`）
- [x] `TestBootstrap_DryRunFlag`: `--dry-run` フラグが `SlackHandlerOptions.IsDryRun` に伝播されること（`AC-40`）
- [x] `TestBootstrap_DryRun_NoURLs`: URL 未設定 + `--dry-run` で両ハンドラが DebugLogger 出力すること（`AC-38`）

**推定工数**: 0.75 日

**実績**: -

---

#### Step 4-4: 統合テスト（モック HTTP サーバ）

**対象ファイル**: `internal/notify/handler_integration_test.go`（`httptest.NewTLSServer` + カスタム `http.Client` 使用）

`NewSlackHandler` は HTTPS スキームのみ許可するため、テスト用サーバには `httptest.NewTLSServer` を使用する。`SlackHandlerOptions.HTTPClient`（テスト用に注入可能なフィールド）を介してサーバの自己署名証明書を信頼する `http.Client` を渡す。

- [-] `TestIntegration_SuccessWebhook`: success webhook のみに INFO レコードが届くこと（Step 2-3/3-1 のハンドラテストに統合済み）
- [-] `TestIntegration_ErrorWebhook`: error webhook のみに WARN/ERROR レコードが届くこと（Step 2-3/3-1 のハンドラテストに統合済み）
- [-] `TestIntegration_SeparateServers`: success / error で異なるサーバを使った振り分け検証（Step 2-3/3-1 のハンドラテストに統合済み）
- [-] `TestIntegration_RetryRecovery`: 5xx → 200 のリトライ復帰シナリオ（`AC-28`, `AC-31`）（Step 2-3/3-1 のハンドラテストに統合済み）
- [-] `TestIntegration_4xxImmediate`: 4xx（429 以外）で即停止すること（`AC-30`）（Step 2-3/3-1 のハンドラテストに統合済み）

**成功基準**: `httptest.NewTLSServer` ベースの実 TLS HTTP 通信で送信先振り分けとリトライ制御を検証できる。

**推定工数**: 0.5 日

**実績**: -

---

#### Step 4-5: セキュリティテスト

**対象ファイル**: `internal/notify/security_test.go`

- [x] `TestSecretNotInMessage`: `config.Secret` フィールドが通知メッセージ JSON に含まれないこと
- [x] `TestWebhookURLNotLogged`: `SlackHandler` を使用するログ出力に Webhook URL の実値が現れないこと（`slog` ログ出力先を検査）
- [x] `TestFlushError_NoURLInErrorString`: 送信失敗時に `Flush()` が返すエラー文字列に Webhook URL の実値が含まれないこと（`url.Error` 等のラップによる漏洩を防ぐ）
- [x] `TestDebugWriterNotTriggerSlack`: Debug Logger への書き込みが `SlackHandler.Handle()` を起動しないこと
- [x] `TestPrivateLogger_NotExported`: `internal/notify` パッケージが通知用 `*slog.Logger` をエクスポートしていないこと（シンボル検査）
- [x] `TestRedactionAlwaysEnabled`: 通知ハンドラ側に redaction を無効化する option / code path が存在しないこと

**成功基準**: Webhook URL や secret 相当値が通知 JSON と通知経路へ混入せず、redaction を無効化する迂回経路も存在しない。

**推定工数**: 0.5 日

**実績**: -

---

#### Step 4-6: 最終確認

**対象ファイル**: リポジトリ全体

- [x] `make fmt` を実行して全 Go ファイルをフォーマットする
- [x] `make test` を実行して全テストが通ること
- [x] `make lint` を実行してエラーがないこと
- [x] `make deadcode` を実行して未使用の関数がないこと

**成功基準**: 変更済みドキュメントと実装計画に対応するコードベース全体が formatter / test / lint / deadcode を通過する。

**推定工数**: 0.25 日

**実績**: -

---

## 3. 実装順序とマイルストーン

| マイルストーン | 完了基準 | 対応ステップ |
|---|---|---|
| M1: 設定・型・検証 | `go build ./...` 通過、Step 1-1〜1-4 の全テスト通過 | 1-1〜1-4 |
| M2: HTTP・ハンドラ | Step 2-1〜2-3 の全テスト通過 | 2-1〜2-3 |
| M3: フォーマット | Step 3-1〜3-3 の全テスト通過 | 3-1〜3-3 |
| M4: 統合完了 | `make test` 全通過、`make lint` クリア | 4-1〜4-6 |

**想定総工数**: 約 7.75 日

---

## 4. テスト戦略

アーキテクチャ設計書 §7「テスト戦略」に詳細を記載。本計画では実施方法のみ補足する。

- **単体テスト**: HTTP 通信を伴うテストには `httptest.NewTLSServer` を使用する（`NewSlackHandler` が HTTPS のみ許可するため）。リトライロジック等ハンドラ URL 検証を経由しない低レベルテストのみ `httptest.NewServer` を使用可。`SlackHandlerOptions.HTTPClient` にテスト用自己署名証明書を信頼する `*http.Client` を注入する
- **スパイ**: `internal/notify/testutil/mocks.go` の `SpyHandler` でバッファへの書き込みを検査する
- **統合テスト**: モック Slack サーバ 2 台（success / error 各 1 台）を立て、振り分けを検証する
- **後方互換性テスト**: success/error が同一 URL でも許容されること、および定期サマリが呼び出し側から与えられた任意期間をそのまま扱えることを確認する
- **セキュリティテスト**: JSON シリアライズ後の文字列を検査してシークレット漏洩がないことを確認する

---

## 5. リスク管理

| リスク | 影響度 | 対策 |
|---|---|---|
| `Retry-After` ヘッダーの解析誤り | 中 | Slack は秒整数のみ返す。パース失敗時はバックオフにフォールバック。RFC 7231 の HTTP-date 形式は Slack では使用されないためスコープ外 |
| `Retry-After` の過大値で 34 秒上限を超える | 中 | `Retry-After` に上限（14 秒）を設け、上限を超える値はキャップして使用する |
| `context` キャンセルと `time.After` の競合 | 中 | `select` で `ctx.Done()` と `time.After()` を同時に待機し、キャンセルを優先する |
| `slog.Handler.WithAttrs`/`WithGroup` の不完全実装 | 低 | `SlackHandler` では通知ペイロードにのみ型付きヘルパー経由で書き込む設計のため、`nop` 実装で可。ただし `*slog.Logger.With()` 経由での利用は設計上禁止とし、コードレビューで確認する |
| `slog.Logger` の不変性 | 中 | Phase 2 のハンドラ追加はロガー再構築で行い、fan-out は `cmd/tlsrpt-digest` 側の補助コードへ閉じ込める。既存の `*slog.Logger` インスタンスには Slack ハンドラが反映されないため、初期化後は `slog.Default()` を通じてロギングする |
| `resp.Body.Close()` の漏れによるコネクションリーク | 中 | Step 2-2 に `resp.Body.Close()` 必須化を明記。テストのモックサーバでコネクション数を監視する |
| `url.Error` によるエラー文字列への Webhook URL 混入 | 中 | `Flush()` および validation 関数が返す `error` をラップして URL を除去してから呼び出し元へ返す |
| `internal/config` への変更が既存 IMAP 設定に影響 | 低 | `NotifyConfig` を独立した構造体として追加し、既存フィールドを変更しない |
| `main.go` の二段階初期化が他の起動処理と干渉 | 中 | Phase 1 / Phase 2 を明確に分離し、各フェーズを関数として切り出す |
| テストケース増加で工数が膨らむ | 中 | Phase 2 完了時点で見積もりを見直し、Phase 4 に 0.5 日のバッファを確保する |

---

## 6. 実装チェックリスト

### Phase 1
- [x] Step 1-1: TOML 設定追加・strict decode（`AC-26a`）
- [x] Step 1-2: エラー型定義（`AC-04`, `AC-05`, `AC-30`, `AC-31`, `AC-35`）
- [x] Step 1-3: コア型・オプション定義（`AC-37`, `AC-18`）
- [x] Step 1-4: URL・環境変数検証（`AC-06`〜`AC-10`, `AC-21`〜`AC-26`）

### Phase 2
- [x] Step 2-1: Slack API ペイロード型（`AC-20i`）
- [x] Step 2-2: HTTP 送信・リトライ（`AC-27`〜`AC-32`）
- [x] Step 2-3: SlackHandler 実装（`AC-01`〜`AC-05b`, `AC-14`〜`AC-16a`, `AC-37`〜`AC-39`）

### Phase 3
- [x] Step 3-1: メッセージフォーマット（`AC-16b`, `AC-17`〜`AC-20l`）
- [x] Step 3-2: 型付きヘルパー（`AC-14`, `AC-16a`）
- [x] Step 3-3: ファイルログ全文出力（`AC-20d`）

### Phase 4
- [x] Step 4-1: スパイハンドラ（`internal/notify/testutil/`）
- [x] Step 4-2: 逐次送信確認（`AC-20m`）
- [x] Step 4-3: 二段階起動フロー（`AC-33`〜`AC-36`, `AC-40`）
- [-] Step 4-4: 統合テスト（Step 2-3/3-1 のハンドラテストに統合済み）
- [x] Step 4-5: セキュリティテスト
- [x] Step 4-6: 最終確認（make fmt / test / lint / deadcode 通過）

---

## 7. 成功基準

- すべての AC（`AC-01`〜`AC-40`）のうち、本タスクの責務に属する振る舞いに対応するテストが存在し、`make test` で通過する
- `make lint` がエラーなく完了する
- `make deadcode` で未使用関数が検出されない
- `WebhookURL` を含む文字列が通知 JSON に出力されない（セキュリティテスト通過）
- `internal/notify` パッケージが `internal/tlsrpt` に依存しない（`go list -f '{{.Imports}}' ./internal/notify/...` で確認）

**備考（`AC-16b`）**: 送信間隔そのものの制御はタスク `0050` が実装する。本タスクでは `Summary` が任意期間を受け取って通知できることをもって、通知パッケージ側の受け入れ条件を満たす。

---

## 8. 受け入れ条件検証

| AC | テスト場所 | テスト名 | 検証方法 |
|---|---|---|---|
| `AC-01` | `internal/notify/handler_test.go` | `TestSlackHandler_ImplementsInterface` | コンパイル時 interface チェック |
| `AC-02` | `internal/notify/handler_test.go` | `TestFlush_InfoGoesToSuccessWebhook` | モックサーバで受信確認 |
| `AC-03` | `internal/notify/handler_test.go` | `TestFlush_WarnGoesToErrorWebhook`, `TestFlush_ErrorGoesToErrorWebhook` | モックサーバで受信確認 |
| `AC-04` | `internal/notify/handler_test.go` | `TestFlush_OnError_LogsToDebugLogger` | Debug Logger への記録を確認 |
| `AC-05` | `internal/notify/handler_test.go` | `TestFlush_4xx_ImmediateError` | `errors.AsType[*SlackClientError]` で型確認 |
| `AC-05a` | `internal/notify/handler_test.go` | `TestFlush_EmptyBuffer` | 戻り値 `nil` 確認 |
| `AC-05b` | `internal/notify/handler_test.go` | `TestHandle_BufferOnly` | `Handle()` 後にモックサーバへのリクエストがないこと |
| `AC-06` | `internal/notify/validate_test.go` | `TestValidateEnvCombination` | 両方設定時に `nil` 返却 |
| `AC-07` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_ErrorOnly_NoSuccessHandler` | error webhook のみ設定時に success ハンドラを生成しないこと |
| `AC-08` | `internal/notify/validate_test.go` | `TestValidateEnvCombination` | `WebhookValidationError` 確認 |
| `AC-09` | `internal/notify/validate_test.go` | `TestValidateEnvCombination` | `nil` 返却（Slack 無効） |
| `AC-10` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_SameURLAllowed` | エラーなし確認 |
| `AC-14` | `internal/notify/helpers_test.go` | `TestLogAlert_Level`, `TestLogSystemError_Level`, `TestLogSummary_Level` | ログレベル確認 |
| `AC-15` | `internal/notify/handler_test.go` | `TestFlush_InfoNotToErrorWebhook` | error webhook サーバへのリクエストなし |
| `AC-16` | `internal/notify/handler_test.go` | `TestFlush_WarnNotToSuccessOnly` | success webhook サーバへのリクエストなし |
| `AC-16a` | `internal/notify/handler_test.go` | `TestCLILogLevel_Independent` | `Enabled()` がコンソールログレベルと独立 |
| `AC-16b` | `internal/notify/format_test.go` | `TestFormatSummary_UsesProvidedPeriod` | 呼び出し側から与えられた期間をそのまま表示すること |
| `AC-17` | `internal/notify/format_test.go` | `TestFormatAlerts_Fields` | `organization-name` フィールド確認 |
| `AC-18` | `internal/notify/format_test.go` | `TestFormatAlerts_Fields`, `TestFormatAlerts_NoPolicyFound`, `TestFormatAlerts_PolicyTypeUnknown` | PolicyType 文字列確認 |
| `AC-19` | `internal/notify/format_test.go` | `TestFormatAlerts_Fields` | `total-failure-session-count` フィールド確認 |
| `AC-20` | `internal/notify/format_test.go` | `TestFormatAlerts_Fields` | `date-range` フィールド確認 |
| `AC-20a` | `internal/notify/format_test.go` | `TestFormatAlerts_RunID`, `TestFormatSystemError_Fields`, `TestFormatSummary_Fields` | 全通知種別で Run ID フィールド確認 |
| `AC-20b` | `internal/notify/format_test.go` | `TestTruncateText_ExactLimit` | 4001 ルーン入力で `...` 付与かつ結果が 4000 ルーン以内 |
| `AC-20c` | `internal/notify/format_test.go` | `TestTruncateField_ExactLimit` | 1001 ルーン入力で `...` 付与かつ結果が 1000 ルーン以内 |
| `AC-20d` | `internal/notify/handler_test.go` | `TestFlush_FileLog_NoTruncation` | Debug Logger に全文出力 |
| `AC-20e` | `internal/notify/format_test.go` | `TestFormatAlerts_TitleOrgCount` | タイトルに N 含有 |
| `AC-20f` | `internal/notify/format_test.go` | `TestFormatAlerts_Color` | `color = "warning"` / ⚠️ |
| `AC-20g` | `internal/notify/format_test.go` | `TestFormatSystemError_Color` | `color = "danger"` / 🚨 |
| `AC-20h` | `internal/notify/format_test.go` | `TestFormatSummary_Color` | `color = "good"` / ✅ |
| `AC-20i` | `internal/notify/format_test.go` | `TestFormatAlerts_AttachmentFields` | `fields` 配列形式確認 |
| `AC-20j` | `internal/notify/format_test.go` | `TestFormatSystemError_Title` | タイトルにエラー種別含有 |
| `AC-20k` | `internal/notify/format_test.go` | `TestFormatSystemError_Fields` | Message フィールド確認 |
| `AC-20l` | `internal/notify/format_test.go` | `TestFormatSystemError_Fields` | Component フィールド確認 |
| `AC-20m` | `internal/notify/handler_test.go` | `TestFlush_SequentialMessages` | 2 回逐次 POST 確認 |
| `AC-21` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_HTTPScheme` | `WebhookValidationError` 確認 |
| `AC-22` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_HostMismatch` | `WebhookValidationError` 確認 |
| `AC-23` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_BothURLsDifferentHost` | `WebhookValidationError` 確認 |
| `AC-24` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_PortStripped`, `TestValidateWebhookURL_CaseInsensitive` | 照合成功確認 |
| `AC-25` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_NoAllowedHost` | `WebhookValidationError` 確認 |
| `AC-26` | `internal/notify/validate_test.go` | `TestValidateWebhookURL_BothEmpty` | 検証スキップ確認 |
| `AC-26a` | `internal/config/config_test.go` | `TestNotifySlackConfig_UnknownKey` | デコードエラー確認 |
| `AC-27` | `internal/notify/retry_test.go` | `TestHTTPPost_Timeout` | 5 秒タイムアウト確認 |
| `AC-28` | `internal/notify/retry_test.go` | `TestHTTPPost_5xxRetry`, `TestHTTPPost_429WithRetryAfter`, `TestHTTPPost_429WithoutRetryAfter` | リトライ回数・待機時間確認 |
| `AC-29` | `internal/notify/retry_test.go` | `TestHTTPPost_RequestFailureRetry` | 接続エラーでリトライ確認 |
| `AC-30` | `internal/notify/retry_test.go` | `TestHTTPPost_4xxImmediate` | 即時 `SlackClientError` 確認 |
| `AC-31` | `internal/notify/retry_test.go` | `TestHTTPPost_AllRetriesExhausted` | `SlackServerError` 確認 |
| `AC-32` | `internal/notify/retry_test.go` | `TestHTTPPost_ContextCancel` | キャンセルで中断確認 |
| `AC-33` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase1_NoSlackHandler` | Phase 1 の補助関数が Slack ハンドラ 0 件を返すことを確認 |
| `AC-34` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_SlackAdded` | Phase 2 の補助関数が期待件数の Slack ハンドラを返すことを確認 |
| `AC-35` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_ValidationFail_Abort` | URL 検証失敗で起動中断確認 |
| `AC-36` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_SlackAdded` | `allowed_host` 伝播確認 |
| `AC-37` | `internal/notify/options_test.go` | `TestSlackHandlerOptions_DryRun` | `IsDryRun` フィールドが option として保持されることを確認 |
| `AC-38` | `internal/notify/handler_test.go`, `cmd/tlsrpt-digest/main_test.go` | `TestFlush_DryRun`, `TestBootstrap_DryRun_NoURLs` | HTTP POST なし・DebugLogger 出力確認（URL 設定時）、URL 未設定でも DebugLogger 出力確認 |
| `AC-39` | `internal/notify/handler_test.go` | `TestNewSlackHandler_URLValidation` | dry-run でも URL 検証実施確認 |
| `AC-40` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_DryRunFlag` | `--dry-run` フラグ伝播確認 |

---

## 9. 次のステップ

1. 本計画書のレビュー・承認後、Phase 1 から順に実装を開始する
2. 各マイルストーン到達時に `make test` / `make lint` を実行して品質を確認する
3. 実装完了後、タスク `0050`（定期サマリ生成）の実装に引き渡す
