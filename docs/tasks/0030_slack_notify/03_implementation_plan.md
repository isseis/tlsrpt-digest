# 実装計画書：Slack 通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-15 |
| レビュー日 | - |
| レビュアー | - |
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

- [ ] `NotifySlackConfig` 構造体を定義する（フィールド: `AllowedHost string`）
- [ ] `Config` 構造体に `Notify NotifyConfig` を追加し、`NotifyConfig` が `Slack NotifySlackConfig` を持つ
- [ ] TOML デコードに strict モード（unknown-key 拒否）を適用する
- [ ] `allowed_host` の形式検証を追加する（空文字は許容、スキーム・ポート番号・前後空白は拒否）

**成功基準**: TOML に `notify.slack.allowed_host = "hooks.slack.com"` を記述してデコードが成功する。未知キー（例: `webhook_url`）または不正な `allowed_host` を記述するとデコードまたは設定検証エラーになる。

**対応 AC**: `AC-26a`

**テスト**:
- [ ] `internal/config/config_test.go::TestNotifySlackConfig_UnknownKey`: `notify.slack.webhook_url = "..."` でデコードエラーになること
- [ ] `internal/config/config_test.go::TestNotifySlackConfig_AllowedHostValidation`: `hooks.slack.com` は許容し、スキーム・ポート・前後空白付き入力は拒否すること
- [ ] `internal/config/secret_test.go::TestSecret_RedactsStringAndLogValue`: `String()` と `LogValue()` が常に `[REDACTED]` を返すこと

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-2: エラー型定義

**対象ファイル**: `internal/notify/errors.go`

- [ ] `WebhookValidationError` 型（`Error() string` 実装）を定義する
- [ ] `SlackServerError` 型を定義する（HTTP 5xx / 429 / 接続エラー）
- [ ] `SlackClientError` 型を定義する（HTTP 4xx、429 を除く）
- [ ] 必要に応じて `Unwrap()` を実装し、呼び出し側が `errors.AsType[T]` で失敗種別を識別できるようにする

**成功基準**: 呼び出し側が `errors.AsType[*WebhookValidationError]`、`errors.AsType[*SlackServerError]`、`errors.AsType[*SlackClientError]` で失敗種別を識別できる。

**対応 AC**: `AC-04`, `AC-05`, `AC-30`, `AC-31`, `AC-35`

**テスト**: `internal/notify/errors_test.go`
- [ ] `TestWebhookValidationError_AsType`: `errors.AsType[*WebhookValidationError]` による型一致確認
- [ ] `TestSlackServerError_AsType`
- [ ] `TestSlackClientError_AsType`

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-3: コア型・オプション定義

**対象ファイル**: `internal/notify/options.go`, `internal/notify/types.go`

- [ ] `LevelMode` 型（`string` 基底）と定数 `LevelModeExactInfo`、`LevelModeWarnAndAbove` を定義する
- [ ] `BackoffConfig` 構造体（`Base time.Duration`, `RetryCount int`）を定義する
- [ ] `DefaultBackoffConfig` 変数（Base: 2s, RetryCount: 3）を定義する
- [ ] `SlackHandlerOptions` 構造体を定義する（`WebhookURL config.Secret`, `AllowedHost string`, `RunID string`, `LevelMode LevelMode`, `IsDryRun bool`, `BackoffConfig BackoffConfig`）
- [ ] `PolicyType` 型と定数（`PolicyTypeSTS`, `PolicyTypeTLSA`, `PolicyTypeNoPolicyFound`, `PolicyTypeUnknown`）を定義する
- [ ] `DateRange` 構造体（`Start, End time.Time`）を定義する
- [ ] `Alert` 構造体を定義する（`OrganizationName string`, `PolicyType PolicyType`, `FailureCount int64`, `DateRange DateRange`）
- [ ] `SystemError` 構造体を定義する（`ErrorType string`, `Message string`, `Component string`）
- [ ] `Summary` 構造体を定義する（`Period DateRange`, `OrganizationCount int`, `ReportCount int`）
- [ ] `Flusher` インターフェースを定義する（`Flush(ctx context.Context) error`）

**成功基準**: `go build ./internal/notify/...` が通る。

**対応 AC**: `AC-37`（dry-run フラグ）, `AC-18`（PolicyType 定数）

**テスト**:
- [ ] `internal/notify/types_test.go::TestPolicyType_Constants`: `PolicyTypeSTS`、`PolicyTypeTLSA`、`PolicyTypeNoPolicyFound`、`PolicyTypeUnknown` の値が RFC 8460 仕様に一致すること
- [ ] `internal/notify/options_test.go::TestSlackHandlerOptions_DryRun`: `IsDryRun` フィールドが option として保持されること

**推定工数**: 0.5 日

**実績**: -

---

#### Step 1-4: URL・環境変数の組み合わせ検証

**対象ファイル**: `internal/notify/validate.go`

- [ ] `ValidateEnvCombination(successURL, errorURL string) error` を実装する
  - success のみ → `WebhookValidationError`
  - 両方未設定 → `nil`（Slack 無効）
  - error のみ、または両方設定 → `nil`（継続）
- [ ] `validateWebhookURL(webhookURL string, allowedHost string) error` を実装する
  - スキームが `https` 以外 → エラー
  - ホスト名が `allowedHost` と不一致 → エラー（ポート除去、大小文字無視の完全一致）
  - `allowedHost` が空かつ URL あり → エラー
- [ ] `validateBothURLs(successURL, errorURL, allowedHost string) error` を実装する（両 URL のホスト名一致確認）

**成功基準**: 各パターンの入力に対して期待通りのエラー型が返る。

**対応 AC**: `AC-06`, `AC-07`, `AC-08`, `AC-09`, `AC-10`, `AC-21`, `AC-22`, `AC-23`, `AC-24`, `AC-25`, `AC-26`

**テスト**: `internal/notify/validate_test.go`
- [ ] `TestValidateEnvCombination`: success のみはエラー、両方未設定は `nil`、error のみまたは両方設定は `nil` になること（`AC-06`〜`AC-09`）
- [ ] `TestValidateWebhookURL_SameURLAllowed`: success / error が同一 URL でも許容されること（`AC-10`）
- [ ] `TestValidateWebhookURL_HTTPScheme`: `http://` がエラーになること（`AC-21`）
- [ ] `TestValidateWebhookURL_HostMismatch`: `allowed_host` と異なるホスト名がエラーになること（`AC-22`）
- [ ] `TestValidateWebhookURL_BothURLsDifferentHost`: success / error で異なるホスト名がエラーになること（`AC-23`）
- [ ] `TestValidateWebhookURL_PortStripped`: ポート番号付き URL でもホスト照合できること（`AC-24`）
- [ ] `TestValidateWebhookURL_CaseInsensitive`: ホスト名の大文字小文字を無視して照合すること（`AC-24`）
- [ ] `TestValidateWebhookURL_NoAllowedHost`: `allowed_host` が空で URL がある場合にエラーになること（`AC-25`）
- [ ] `TestValidateWebhookURL_BothEmpty`: 両 URL 未設定時は URL 検証をスキップすること（`AC-26`）

**推定工数**: 0.5 日

**実績**: -

---

### Phase 2: ペイロード・配送・ハンドラ

---

#### Step 2-1: Slack API ペイロード型の定義

**対象ファイル**: `internal/notify/message.go`

- [ ] `slackMessage`、`slackAttachment`、`slackField` を定義する
- [ ] JSON フィールド定義を [02_architecture.md](02_architecture.md) の §6.3 と Slack Incoming Webhook 仕様に整合させる
- [ ] `attachment.fields` を使った構造化表現を前提にした最小限の型に留める

**成功基準**: `encoding/json` でシリアライズした JSON が Slack Incoming Webhook の仕様に準拠する。

**対応 AC**: `AC-20i`

**テスト**: `internal/notify/message_test.go`
- [ ] `TestSlackMessage_JSONShape`: `text` と `attachments` が期待するキー名で出力されること
- [ ] `TestSlackAttachment_FieldsEncoding`: `fields` が配列としてシリアライズされること

**推定工数**: 0.25 日

**実績**: -

---

#### Step 2-2: HTTP 送信とリトライ実装

**対象ファイル**: `internal/notify/retry.go`

- [ ] Slack Webhook への POST 処理を実装する
- [ ] タイムアウトを 5 秒に設定する
- [ ] 5xx / 429 / リクエスト発行失敗をリトライ対象にする
- [ ] `Retry-After` がある場合はその値を優先し、ない場合は指数バックオフを使う
- [ ] 4xx（429 を除く）は即座に `SlackClientError` を返す
- [ ] `context` キャンセル時は待機を中断して `ctx.Err()` を返す
- [ ] 待機処理は注入可能な関数または時刻取得抽象に切り出し、テストで実時間の `sleep` を避ける

**成功基準**: `go test ./internal/notify/...` が通り、回復可能な失敗のみが再試行される。

**対応 AC**: `AC-27`, `AC-28`, `AC-29`, `AC-30`, `AC-31`, `AC-32`

**テスト**: `internal/notify/retry_test.go`
- [ ] `TestHTTPPost_Timeout`: 5 秒タイムアウトになること（`AC-27`）
- [ ] `TestHTTPPost_5xxRetry`: 5xx 応答で再試行すること（`AC-28`）
- [ ] `TestHTTPPost_429WithRetryAfter`: `Retry-After` を優先して待機すること（`AC-28`）
- [ ] `TestHTTPPost_429WithoutRetryAfter`: 指数バックオフで待機すること（`AC-28`）
- [ ] `TestHTTPPost_RequestFailureRetry`: 接続エラーで再試行すること（`AC-29`）
- [ ] `TestHTTPPost_4xxImmediate`: 4xx（429 以外）で即時 `SlackClientError` を返すこと（`AC-30`）
- [ ] `TestHTTPPost_AllRetriesExhausted`: 全リトライ失敗後に `SlackServerError` を返すこと（`AC-31`）
- [ ] `TestHTTPPost_ContextCancel`: `context` キャンセルで待機を中断すること（`AC-32`）

**推定工数**: 0.5 日

**実績**: -

---

#### Step 2-3: SlackHandler 実装

**対象ファイル**: `internal/notify/handler.go`

- [ ] `SlackHandler` が `slog.Handler` と `Flusher` を実装する
- [ ] `NewSlackHandler` で URL 検証、レベルモード、dry-run、Run ID、backoff 設定を受け取る
- [ ] `Handle()` は内部バッファへの書き込みのみを行い、HTTP 送信は行わない
- [ ] `Enabled()` はハンドラ自身の `LevelMode` に基づいて判定し、CLI のコンソールログレベル設定と独立させる
- [ ] `Flush()` は空バッファなら `nil` を返し、蓄積済みメッセージを逐次送信する
- [ ] dry-run 時は HTTP POST を抑止し、Debug Logger へ記録したうえでバッファをクリアする
- [ ] 最終失敗時は Debug Logger に詳細を記録してエラーを返す

**成功基準**: `Handle()` と `Flush()` の責務が分離され、success / error webhook の振り分け、dry-run、エラー伝播が設計通りに動作する。

**対応 AC**: `AC-01`, `AC-02`, `AC-03`, `AC-04`, `AC-05`, `AC-05a`, `AC-05b`, `AC-14`, `AC-15`, `AC-16`, `AC-16a`, `AC-37`, `AC-38`, `AC-39`

**テスト**: `internal/notify/handler_test.go`
- [ ] `TestSlackHandler_ImplementsInterface`: `slog.Handler` / `Flusher` を実装していること（`AC-01`）
- [ ] `TestFlush_InfoGoesToSuccessWebhook`: INFO が success webhook に送信されること（`AC-02`）
- [ ] `TestFlush_WarnGoesToErrorWebhook`: WARN が error webhook に送信されること（`AC-03`）
- [ ] `TestFlush_ErrorGoesToErrorWebhook`: ERROR が error webhook に送信されること（`AC-03`）
- [ ] `TestFlush_OnError_LogsToDebugLogger`: 送信失敗時に Debug Logger にエラー詳細が記録されること（`AC-04`）
- [ ] `TestFlush_4xx_ImmediateError`: 4xx（429 以外）で `SlackClientError` を返すこと（`AC-05`）
- [ ] `TestFlush_EmptyBuffer`: 空バッファでは `nil` を返すこと（`AC-05a`）
- [ ] `TestHandle_BufferOnly`: `Handle()` 後にモックサーバへのリクエストがないこと（`AC-05b`）
- [ ] `TestFlush_InfoNotToErrorWebhook`: INFO が error webhook に送信されないこと（`AC-15`）
- [ ] `TestFlush_WarnNotToSuccessOnly`: WARN が success 専用ハンドラに送信されないこと（`AC-16`）
- [ ] `TestCLILogLevel_Independent`: `Enabled()` が CLI のコンソールログレベル設定と独立していること（`AC-16a`）
- [ ] `TestFlush_DryRun`: dry-run 時に HTTP POST 不発・Debug Logger 出力があること（`AC-38`）
- [ ] `TestNewSlackHandler_URLValidation`: dry-run を含め不正 URL で `WebhookValidationError` が返ること（`AC-39`）

**推定工数**: 0.75 日

**実績**: -

---

### Phase 3: フォーマット・ヘルパー

---

#### Step 3-1: メッセージフォーマット実装

**対象ファイル**: `internal/notify/format.go`

- [ ] `truncateText(s string, maxLen int) string` を実装する（末尾 `...` 付与）
- [ ] `formatAlerts(alerts []Alert, runID string) slackMessage` を [02_architecture.md](02_architecture.md) の §6.3 に従って実装する
- [ ] `formatSystemError(e SystemError, runID string) slackMessage` を [02_architecture.md](02_architecture.md) の §6.3 に従って実装する
- [ ] `formatSummary(s Summary, runID string) slackMessage` を [02_architecture.md](02_architecture.md) の §6.3 に従って実装する
- [ ] 定期サマリは呼び出し側から与えられた `Summary.Period` をそのまま表示し、7 日固定などの間隔仮定を持たない

**成功基準**: 各フォーマット関数が期待する `slackMessage` を返す。

**対応 AC**: `AC-16b`, `AC-17`, `AC-18`, `AC-19`, `AC-20`, `AC-20a`, `AC-20b`, `AC-20c`, `AC-20e`, `AC-20f`, `AC-20g`, `AC-20h`, `AC-20i`, `AC-20j`, `AC-20k`, `AC-20l`

**テスト**: `internal/notify/format_test.go`
- [ ] `TestFormatAlerts_Fields`: org name, PolicyType, failure count, date range が含まれること（`AC-17`〜`AC-20`）
- [ ] `TestFormatAlerts_RunID`: Run ID フィールドが含まれること（`AC-20a`）
- [ ] `TestFormatAlerts_TitleOrgCount`: タイトルに影響組織数 N が含まれること（`AC-20e`）
- [ ] `TestFormatAlerts_Color`: `attachment.color = "warning"` / 絵文字 ⚠️ であること（`AC-20f`）
- [ ] `TestFormatAlerts_TruncateText`: 4001 文字入力で末尾が `...` になること（`AC-20b`）
- [ ] `TestFormatAlerts_TruncateField`: 1001 文字フィールドが切り詰められること（`AC-20c`）
- [ ] `TestFormatAlerts_AttachmentFields`: `fields` 形式で構造化されていること（`AC-20i`）
- [ ] `TestFormatSystemError_Title`: タイトルにエラー種別が含まれること（`AC-20j`）
- [ ] `TestFormatSystemError_Fields`: Message, Component, Run ID が含まれること（`AC-20k`, `AC-20l`）
- [ ] `TestFormatSystemError_Color`: `attachment.color = "danger"` / 絵文字 🚨 であること（`AC-20g`）
- [ ] `TestFormatSummary_Color`: `attachment.color = "good"` / 絵文字 ✅ であること（`AC-20h`）
- [ ] `TestFormatSummary_UsesProvidedPeriod`: 呼び出し側から与えられた期間をそのまま表示し、7 日固定を仮定しないこと（`AC-16b`）
- [ ] `TestFormatAlerts_NoPolicyFound`: `no-policy-found` が正しく表示されること（`AC-18`）
- [ ] `TestFormatAlerts_PolicyTypeUnknown`: `PolicyTypeUnknown`（空値）が通知に含まれること（`AC-18`）

**推定工数**: 0.75 日

**実績**: -

---

#### Step 3-2: 型付きヘルパー実装

**対象ファイル**: `internal/notify/helpers.go`

- [ ] `LogAlert(ctx context.Context, h slog.Handler, alert Alert) error` を実装する
  - `slog.Record` を構築して `h.Handle(ctx, record)` を呼ぶ
  - ログレベル: `slog.LevelWarn`
  - `Handle()` の戻り値をそのまま返す
- [ ] `LogSystemError(ctx context.Context, h slog.Handler, e SystemError) error` を実装する
  - ログレベル: `slog.LevelError`
- [ ] `LogSummary(ctx context.Context, h slog.Handler, s Summary) error` を実装する
  - ログレベル: `slog.LevelInfo`

**成功基準**: 型付きヘルパーが正しいレベルでレコードを書き込む。外部コードが `logger.Info(...)` を直接呼べないことを設計上保証（プライベートロガー）。

**対応 AC**: `AC-14`, `AC-16a`

**テスト**: `internal/notify/helpers_test.go`
- [ ] `TestLogAlert_Level`: `LogAlert` が WARN レベルのレコードを書き込むこと（`AC-14`）
- [ ] `TestLogSystemError_Level`: `LogSystemError` が ERROR レベルのレコードを書き込むこと（`AC-14`）
- [ ] `TestLogSummary_Level`: `LogSummary` が INFO レベルのレコードを書き込むこと（`AC-14`）
- [ ] `TestLogAlert_StructuredPayloadOnly`: `Alert` 構造体のフィールドのみが `slog.Record` に含まれ、生文字列や config 情報が混入しないこと（通知セキュリティガイドライン）

**推定工数**: 0.5 日

**実績**: -

---

#### Step 3-3: ファイルログへの全文出力確認

**対象ファイル**: `internal/notify/handler.go`, `internal/notify/handler_test.go`

`Flush()` 内で `slackMessage` を HTTP 送信する前に、`Debug Logger` を通じて `slog.Debug` でフォーマット済みペイロードを記録する。切り詰めはHTTP 送信ペイロードにのみ適用し、Debug Logger への出力は切り詰めなし。

**成功基準**: Slack 送信ペイロードにのみ切り詰めが適用され、Debug Logger には全文が出力される。

**対応 AC**: `AC-20d`

**テスト**: `internal/notify/handler_test.go` に追加
- [ ] `TestFlush_FileLog_NoTruncation`: 4001 文字の組織名を含む Alert を Flush した際、Debug Logger のレコードには切り詰めなしで記録されること（`AC-20d`）

**推定工数**: 0.25 日

**実績**: -

---

### Phase 4: 起動統合・テスト

---

#### Step 4-1: スパイハンドラ（テストヘルパー）

`SpyHandler` は `slog.Handler` / `Flusher` の public API のみを使うため、[test_organization.md](../../dev/developer_guide/test_organization.md) の分類 A に従って `testutil/` へ配置する。

**対象ファイル**: `internal/notify/testutil/mocks.go`（新規）, `internal/notify/testutil/mocks_test.go`（新規）

- [ ] `package notifytestutil` として `SpyHandler` 構造体を実装する（`Records`, `FlushCalled bool`, `FlushErr error`）
- [ ] `slog.Handler` インターフェースの全メソッドを実装する
- [ ] `Flusher.Flush()` を実装する（記録後 `FlushErr` を返す）
- [ ] `internal/notify/testutil/mocks_test.go` に `SpyHandler` の自己テストを追加する
  - `TestSpyHandler_RecordsHandle`: `Handle()` 後に Records に蓄積されること
  - `TestSpyHandler_FlushCalled`: `Flush()` 呼び出し後に `FlushCalled == true`

**成功基準**: `internal/notify` 配下の複数テストから `notifytestutil.SpyHandler` を import して再利用できる。

**推定工数**: 0.25 日

**実績**: -

---

#### Step 4-2: 複数メッセージの逐次送信確認

**対象ファイル**: `internal/notify/handler_test.go`

**テスト**: `internal/notify/handler_test.go` に追加（`httptest.NewServer` 使用）
- [ ] `TestFlush_SequentialMessages`: TLS failure と system error が同一 `Flush()` で発生した場合、2 回に分けて逐次 POST されること（`AC-20m`）

**成功基準**: 単一の `Flush()` 呼び出しで複数種別の通知が混在しても、期待順序で独立した HTTP リクエストとして送信される。

**推定工数**: 0.25 日

**実績**: -

---

#### Step 4-3: 二段階起動フローの実装

**対象ファイル**: `cmd/tlsrpt-digest/main.go`

- [ ] Phase 1: TOML 読み込み前にローカルハンドラ（`slog.NewTextHandler(os.Stderr, ...)` 等）を初期化する（Slack ハンドラ含まない）
- [ ] 環境変数から `successURL`、`errorURL` を読み込み `ValidateEnvCombination` を呼ぶ
- [ ] TOML を読み込んで `notify.slack.allowed_host` を取得する
- [ ] Phase 2: `NewSlackHandler` で success / error 各ハンドラを生成し、既存ロガーに追加する
- [ ] `--dry-run` フラグを CLI に追加し、`SlackHandlerOptions.IsDryRun` に渡す
- [ ] `runID` を `fmt.Sprintf("%x", ...)` 等でプロセス起動ごとに一意に生成する

**成功基準**: Phase 1 が完了したロガーに Slack ハンドラが含まれない。Phase 2 後に Slack ハンドラが追加されている。

**対応 AC**: `AC-33`, `AC-34`, `AC-35`, `AC-36`, `AC-40`

**テスト**: `cmd/tlsrpt-digest/main_test.go`（統合テスト）
- [ ] `TestBootstrap_Phase1_NoSlackHandler`: Phase 1 終了時点でロガーに Slack ハンドラが含まれないこと（`AC-33`）
- [ ] `TestBootstrap_ErrorOnly_NoSuccessHandler`: error webhook のみ設定時に success ハンドラを生成せず、INFO 通知が無効になること（`AC-07`）
- [ ] `TestBootstrap_Phase2_SlackAdded`: 両方設定時に Phase 2 後のロガーへ Slack ハンドラが追加されること（`AC-06`, `AC-34`, `AC-36`）
- [ ] `TestBootstrap_Phase2_ValidationFail_Abort`: URL 検証失敗で起動が中断されること（`AC-35`）
- [ ] `TestBootstrap_DryRunFlag`: `--dry-run` フラグが `SlackHandlerOptions.IsDryRun` に伝播されること（`AC-40`）

**推定工数**: 0.75 日

**実績**: -

---

#### Step 4-4: 統合テスト（モック HTTP サーバ）

**対象ファイル**: `internal/notify/handler_integration_test.go`（`httptest.NewServer` 使用）

- [ ] `TestIntegration_SuccessWebhook`: success webhook のみに INFO レコードが届くこと
- [ ] `TestIntegration_ErrorWebhook`: error webhook のみに WARN/ERROR レコードが届くこと
- [ ] `TestIntegration_SeparateServers`: success / error で異なるサーバを使った振り分け検証
- [ ] `TestIntegration_RetryRecovery`: 5xx → 200 のリトライ復帰シナリオ（`AC-28`, `AC-31`）
- [ ] `TestIntegration_4xxImmediate`: 4xx（429 以外）で即停止すること（`AC-30`）

**成功基準**: 送信先振り分けとリトライ制御を、`httptest.NewServer` ベースの実 HTTP 通信で再現確認できる。

**推定工数**: 0.5 日

**実績**: -

---

#### Step 4-5: セキュリティテスト

**対象ファイル**: `internal/notify/security_test.go`

- [ ] `TestSecretNotInMessage`: `config.Secret` フィールドが通知メッセージ JSON に含まれないこと
- [ ] `TestWebhookURLNotLogged`: `SlackHandler` を使用するログ出力に Webhook URL の実値が現れないこと（`slog` ログ出力先を検査）
- [ ] `TestDebugWriterNotTriggerSlack`: Debug Logger への書き込みが `SlackHandler.Handle()` を起動しないこと
- [ ] `TestPrivateLogger_NotExported`: `internal/notify` パッケージが通知用 `*slog.Logger` をエクスポートしていないこと（シンボル検査）
- [ ] `TestRedactionAlwaysEnabled`: 通知ハンドラ側に redaction を無効化する option / code path が存在しないこと

**成功基準**: Webhook URL や secret 相当値が通知 JSON と通知経路へ混入せず、redaction を無効化する迂回経路も存在しない。

**推定工数**: 0.5 日

**実績**: -

---

#### Step 4-6: 最終確認

**対象ファイル**: リポジトリ全体

- [ ] `make fmt` を実行して全 Go ファイルをフォーマットする
- [ ] `make test` を実行して全テストが通ること
- [ ] `make lint` を実行してエラーがないこと
- [ ] `make deadcode` を実行して未使用の関数がないこと

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

**想定総工数**: 7.5 日

---

## 4. テスト戦略

アーキテクチャ設計書 §7「テスト戦略」に詳細を記載。本計画では実施方法のみ補足する。

- **単体テスト**: `httptest.NewServer` でモック HTTP サーバを用意し、実際の HTTP 送受信を行う（`net/http` のモックは使わない）
- **スパイ**: `internal/notify/testutil/mocks.go` の `SpyHandler` でバッファへの書き込みを検査する
- **統合テスト**: モック Slack サーバ 2 台（success / error 各 1 台）を立て、振り分けを検証する
- **後方互換性テスト**: success/error が同一 URL でも許容されること、および定期サマリが呼び出し側から与えられた任意期間をそのまま扱えることを確認する
- **セキュリティテスト**: JSON シリアライズ後の文字列を検査してシークレット漏洩がないことを確認する

---

## 5. リスク管理

| リスク | 影響度 | 対策 |
|---|---|---|
| `Retry-After` ヘッダーの解析誤り | 中 | RFC 7231 §7.1.3 に従いヘッダー値を秒単位の整数として解析。パース失敗時はバックオフにフォールバック |
| `context` キャンセルと `time.After` の競合 | 中 | `select` で `ctx.Done()` と `time.After()` を同時に待機し、キャンセルを優先する |
| `slog.Handler.WithAttrs`/`WithGroup` の不完全実装 | 低 | `SlackHandler` では通知ペイロードにのみ型付きヘルパー経由で書き込むため、これらは `nop` 実装で可 |
| `internal/config` への変更が既存 IMAP 設定に影響 | 低 | `NotifyConfig` を独立した構造体として追加し、既存フィールドを変更しない |
| `main.go` の二段階初期化が他の起動処理と干渉 | 中 | Phase 1 / Phase 2 を明確に分離し、各フェーズを関数として切り出す |
| テストケース増加で工数が膨らむ | 中 | Phase 2 完了時点で見積もりを見直し、Phase 4 に 0.5 日のバッファを確保する |

---

## 6. 実装チェックリスト

### Phase 1
- [ ] Step 1-1: TOML 設定追加・strict decode（`AC-26a`）
- [ ] Step 1-2: エラー型定義（`AC-04`, `AC-05`, `AC-30`, `AC-31`, `AC-35`）
- [ ] Step 1-3: コア型・オプション定義（`AC-37`, `AC-18`）
- [ ] Step 1-4: URL・環境変数検証（`AC-06`〜`AC-10`, `AC-21`〜`AC-26`）

### Phase 2
- [ ] Step 2-1: Slack API ペイロード型（`AC-20i`）
- [ ] Step 2-2: HTTP 送信・リトライ（`AC-27`〜`AC-32`）
- [ ] Step 2-3: SlackHandler 実装（`AC-01`〜`AC-05b`, `AC-14`〜`AC-16a`, `AC-37`〜`AC-39`）

### Phase 3
- [ ] Step 3-1: メッセージフォーマット（`AC-16b`, `AC-17`〜`AC-20l`）
- [ ] Step 3-2: 型付きヘルパー（`AC-14`, `AC-16a`）
- [ ] Step 3-3: ファイルログ全文出力（`AC-20d`）

### Phase 4
- [ ] Step 4-1: スパイハンドラ（`internal/notify/testutil/`）
- [ ] Step 4-2: 逐次送信確認（`AC-20m`）
- [ ] Step 4-3: 二段階起動フロー（`AC-33`〜`AC-36`, `AC-40`）
- [ ] Step 4-4: 統合テスト
- [ ] Step 4-5: セキュリティテスト
- [ ] Step 4-6: 最終確認（make fmt / test / lint / deadcode）

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
| `AC-20a` | `internal/notify/format_test.go` | `TestFormatAlerts_RunID` | Run ID フィールド確認 |
| `AC-20b` | `internal/notify/format_test.go` | `TestFormatAlerts_TruncateText` | 4001 文字入力で `...` 付与 |
| `AC-20c` | `internal/notify/format_test.go` | `TestFormatAlerts_TruncateField` | 1001 文字フィールドで `...` 付与 |
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
| `AC-33` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase1_NoSlackHandler` | Phase 1 後のハンドラ一覧確認 |
| `AC-34` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_SlackAdded` | Phase 2 後のハンドラ追加確認 |
| `AC-35` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_ValidationFail_Abort` | URL 検証失敗で起動中断確認 |
| `AC-36` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_Phase2_SlackAdded` | `allowed_host` 伝播確認 |
| `AC-37` | `internal/notify/options_test.go` | `TestSlackHandlerOptions_DryRun` | `IsDryRun` フィールド存在確認 |
| `AC-38` | `internal/notify/handler_test.go` | `TestFlush_DryRun` | HTTP POST なし・Debug Logger 出力確認 |
| `AC-39` | `internal/notify/handler_test.go` | `TestNewSlackHandler_URLValidation` | dry-run でも URL 検証実施確認 |
| `AC-40` | `cmd/tlsrpt-digest/main_test.go` | `TestBootstrap_DryRunFlag` | `--dry-run` フラグ伝播確認 |

---

## 9. 次のステップ

1. 本計画書のレビュー・承認後、Phase 1 から順に実装を開始する
2. 各マイルストーン到達時に `make test` / `make lint` を実行して品質を確認する
3. 実装完了後、タスク `0050`（定期サマリ生成）の実装に引き渡す
