# 実装計画書：Slack サマリ通知インテグレーションテスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-09 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装概要

### 目的

`02_architecture.md` の設計に基づき、複数組織の success-only TLS-RPT メールを集約して実 Slack Webhook にサマリ通知を送信する統合テストを追加する。あわせて、環境変数欠落判定ヘルパーとユニットテスト、手動実行用 make ターゲットを実装する。

加えて、本タスクの実装に先立ち、Slack Webhook 関連の環境変数名文字列を `internal/notify` に一元化するリファクタリングを行う（フェーズ 0）。

### 実装方針

- タスク 0100 の `TestSlackNotify_FailureAlert_Integration` パターンを対称的に踏襲し、差分を最小化する。
- プロダクションコードパス（`setupNotifyHandlers`・`notify.GenerateSummary`・`notifier.LogSummary`）をそのまま再利用し、テスト専用の送信ロジックは実装しない（DRY）。
- `FakeStore`（インメモリ）を使用してディスク書き込みをゼロにする。
- ビルドタグ `test && slack_notify` で CI および `make test` / `make test-integration` から隔離する。
- 環境変数名 `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` / `TLSRPT_SLACK_WEBHOOK_URL_ERROR` は `internal/notify` に exported 定数として定義し、`boot.go`・`validate.go`・テストファイルがすべてこれを参照する（source of truth の一元化）。

### 既存コード調査結果

#### 環境変数名の分散状況（フェーズ 0 の対象）

現状、環境変数名の文字列が以下の 4 箇所に分散しており、名称変更時に漏れが生じるリスクがある。

| ファイル | 行 | 内容 |
|---|---|---|
| `cmd/tlsrpt-digest/boot.go` | 259, 262 | `os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS/ERROR")` — 環境変数の読み取り |
| `internal/notify/validate.go` | 17 | エラーメッセージ内の文字列リテラル |
| `cmd/tlsrpt-digest/slack_notify_env_test.go` | 13 | `slackNotifyWebhookEnvKey = "TLSRPT_SLACK_WEBHOOK_URL_ERROR"` — テスト用定数 |
| フェーズ 1 追加予定 | — | `slackSummaryWebhookEnvKey = "TLSRPT_SLACK_WEBHOOK_URL_SUCCESS"` — テスト用定数 |

フェーズ 0 で `internal/notify` に exported 定数を定義し、上記のすべてを定数参照に置き換える。`internal/notify` は `boot.go` がすでに import しており、テストファイルからも import 可能であるため、循環依存は発生しない。

#### 再利用可能な既存実装

| シンボル | ファイル | 役割 |
|---|---|---|
| `setupNotifyHandlers` | `cmd/tlsrpt-digest/boot.go:347` | success/error URL を受け取り `NotificationSink` を構築する。変更不要。 |
| `notify.GenerateSummary` | `internal/notify/aggregate.go:15` | `start <= EndDatetime < end` の条件でレポートを集約。変更不要。 |
| `notify.LogSummary` | `internal/notify/helpers.go:104` | サマリを `slog.Handler` にバッファリング。変更不要。 |
| `storetestutil.NewFakeStore` | `internal/store/testutil/mocks.go:84` | インメモリの `FakeStore` を生成。変更不要。 |
| `parseTLSRPTAttachment` | `cmd/tlsrpt-digest/fetch.go:326` | EML 添付を TLSRPT レポートにパース。変更不要。 |
| `missingSlackNotifyEnv` / `TestSlackNotify_EnvRequirements` | `cmd/tlsrpt-digest/slack_notify_env_test.go` | 追加する `missingSlackSummaryEnv` の設計パターン。フェーズ 0 で `slackNotifyWebhookEnvKey` の定義を変更する。 |
| `TestSlackNotify_FailureAlert_Integration` | `cmd/tlsrpt-digest/slack_notify_integration_test.go` | 統合テスト実装パターン（60s タイムアウト・runID・AllowedHost 設定・nolint G304）。変更不要。 |

#### 変更・追加が必要なファイル

| ファイル | 種別 | フェーズ | 内容 |
|---|---|---|---|
| `internal/notify/validate.go` | 変更 | 0 | `EnvSlackWebhookURLSuccess`・`EnvSlackWebhookURLError` 定数を追加し、エラーメッセージをこれらの定数参照に変更 |
| `cmd/tlsrpt-digest/boot.go` | 変更 | 0 | `withDefaults()` 内の `os.Getenv` 引数を `notify.EnvSlackWebhookURLSuccess`・`notify.EnvSlackWebhookURLError` に変更 |
| `cmd/tlsrpt-digest/slack_notify_env_test.go` | 変更 | 0 | `internal/notify` import を追加し、`slackNotifyWebhookEnvKey` の定義を `= notify.EnvSlackWebhookURLError` に変更 |
| `cmd/tlsrpt-digest/slack_notify_env_test.go` | 追記 | 1 | `slackSummaryWebhookEnvKey` 定数と `missingSlackSummaryEnv` 関数、`TestSlackSummary_EnvRequirements` テストを追加 |
| `cmd/tlsrpt-digest/slack_summary_integration_test.go` | 新規作成 | 2 | `loadSlackSummaryTestEnv`・`TestSlackSummary_Summary_Integration` |
| `Makefile` | 追記 | 2 | `test-slack-summary` ターゲットと `.PHONY` 追加 |

#### 確認済みシンボル・設計判断

- `notify.ValidateEnvCombination` は `successURL != "" && errorURL == ""` の組み合わせを拒否する（`internal/notify/validate.go:15`）。そのため `missingSlackSummaryEnv` は **両方の URL** の設定を必須とし、`loadSlackSummaryTestEnv` はどちらかが欠けている場合もスキップする。
- `setupNotifyHandlers` シグネチャ: `(successURL, errorURL config.Secret, cfg *config.Config, runID string, dryRun bool) (NotificationSink, error)`。
- `notify.BuildHandlers` は `successURL` と `errorURL` の**両方**を `allowedHost` と照合する。テストでは `successURL` のホスト名を `cfg.Notify.Slack.AllowedHost` に設定するが、`errorURL` も同じホストを使用している前提となる（§5 リスク参照）。
- アーキテクチャ設計書 `02_architecture.md` には以下の 3 箇所に誤りがあり、本計画書の記述が正しい:
  - §2.2 シーケンス図: `setupNotifyHandlers(successURL, config.Secret(""), ...)` → 正しくは `config.Secret(errorURL)` を渡す。
  - §3.2 コードスニペット: `slackSummaryErrorWebhookEnvKey` を新規定数として宣言しているが、フェーズ 0 で `internal/notify` に定義する `EnvSlackWebhookURLError` を参照するため追加不要。
  - §6.2 フローチャートノード G: `"setupNotifyHandlers successURL のみ設定"` → 正しくは `successURL + errorURL` の両方を渡す。
  上記の誤りは `02_architecture.md` 自体でも訂正済み。
- テストデータ（3 通の EML）は `testdata/` 直下に配置済み。統合テストからのパスは `filepath.Join("..", "..", "testdata", "tlsrpt_success_google_1.eml")` 形式（既存テストと同パターン）。

---

## 2. 実装フェーズ

### フェーズ 0: 環境変数名定数の一元化（リファクタリング）

**目的**: 環境変数名の文字列を `internal/notify` に集約し、`boot.go`・`validate.go`・テストファイルがすべて定数参照する状態にする。フェーズ 1・2 の前提として実施する。

#### 0-A: `internal/notify/validate.go` への定数追加

- [ ] **0.1** `validate.go` の先頭（`package notify` 直下）に以下の定数を追加する。
  ```go
  // EnvSlackWebhookURLSuccess is the environment variable name for the Slack
  // success (summary) webhook URL.
  const EnvSlackWebhookURLSuccess = "TLSRPT_SLACK_WEBHOOK_URL_SUCCESS"

  // EnvSlackWebhookURLError is the environment variable name for the Slack
  // error (alert) webhook URL.
  const EnvSlackWebhookURLError = "TLSRPT_SLACK_WEBHOOK_URL_ERROR"
  ```

- [ ] **0.2** `validate.go` のエラーメッセージを定数参照に変更する。
  - 変更前の `Msg` 値: `"TLSRPT_SLACK_WEBHOOK_URL_SUCCESS is set but TLSRPT_SLACK_WEBHOOK_URL_ERROR is not; error notifications must be enabled to prevent silent failures"`
  - 変更後の `Msg` 値: `EnvSlackWebhookURLSuccess + " is set but " + EnvSlackWebhookURLError + " is not; error notifications must be enabled to prevent silent failures"`

#### 0-B: `cmd/tlsrpt-digest/boot.go` の更新

- [ ] **0.3** `withDefaults()` 内の `os.Getenv` 呼び出しを定数参照に変更する。
  - 変更前: `config.Secret(os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS"))`
  - 変更後: `config.Secret(os.Getenv(notify.EnvSlackWebhookURLSuccess))`
  - 変更前: `config.Secret(os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR"))`
  - 変更後: `config.Secret(os.Getenv(notify.EnvSlackWebhookURLError))`

#### 0-C: `cmd/tlsrpt-digest/slack_notify_env_test.go` の更新

- [ ] **0.4** import ブロックに `"github.com/isseis/tlsrpt-digest/internal/notify"` を追加する。

- [ ] **0.5** `slackNotifyWebhookEnvKey` の定義を文字列リテラルから定数参照に変更する。
  - 変更前: `const slackNotifyWebhookEnvKey = "TLSRPT_SLACK_WEBHOOK_URL_ERROR"`
  - 変更後: `const slackNotifyWebhookEnvKey = notify.EnvSlackWebhookURLError`
  - `TestSlackNotify_EnvRequirements` の既存テストは `slackNotifyWebhookEnvKey` を名前で参照しているため変更不要。

**完了確認**: `make test` が通過すること（既存テストのリグレッションなし）。

---

### フェーズ 1: 環境変数ヘルパーとユニットテスト

**フェーズ 0 完了後に実施する。**

**対象ファイル**: `cmd/tlsrpt-digest/slack_notify_env_test.go`

- [ ] **1.1** `slackSummaryWebhookEnvKey` 定数を追加する。
  - 定義: `const slackSummaryWebhookEnvKey = notify.EnvSlackWebhookURLSuccess`
  - 既存の `slackNotifyWebhookEnvKey` 定数の直後に追加する。

- [ ] **1.2** `missingSlackSummaryEnv(env map[string]string) []string` 関数を実装する。
  - `slackSummaryWebhookEnvKey`（`notify.EnvSlackWebhookURLSuccess`）と `slackNotifyWebhookEnvKey`（`notify.EnvSlackWebhookURLError`）の**両方**を確認する。
  - `env == nil` のとき `os.Getenv` にフォールバックする（`missingSlackNotifyEnv` と同パターン）。
  - 値が空文字列のキーは `"<KEY> (empty)"` 形式で missing リストに追加する。
  - `missingSlackNotifyEnv` の直後に配置する。

- [ ] **1.3** `TestSlackSummary_EnvRequirements` テスト関数を実装する。
  - `TestSlackNotify_EnvRequirements` の直後に追加する。
  - 以下の 6 サブテストを実装する（`02_architecture.md` § 7.1 の 5 ケースに `error_webhook_url_missing` を追加）:

    | サブテスト名 | 入力 | 期待 |
    |---|---|---|
    | `webhook_url_missing` | `map[string]string{}` | missing リストに `slackSummaryWebhookEnvKey+" (empty)"` が含まれる |
    | `webhook_url_empty_value` | `map[string]string{slackSummaryWebhookEnvKey: ""}` | missing リストに `slackSummaryWebhookEnvKey+" (empty)"` が含まれる |
    | `error_webhook_url_missing` | `map[string]string{slackSummaryWebhookEnvKey: "https://hooks.slack.com/services/test"}` | missing リストに `slackNotifyWebhookEnvKey+" (empty)"` が含まれる |
    | `webhook_url_set` | 両方のキーに `"https://hooks.slack.com/services/test"` を設定 | missing リストが空 |
    | `nil_env_fallback_present` | `nil`（`t.Setenv` で両方設定） | missing リストが空 |
    | `nil_env_fallback_missing` | `nil`（`t.Setenv` で両方を空文字列に設定） | missing リストに両キーのエントリが含まれる |

    `error_webhook_url_missing` は `ValidateEnvCombination` が拒否する success-only 組み合わせを `missingSlackSummaryEnv` が確実に検出できることを確認する。

**完了確認**: `make test` が通過すること。

---

### フェーズ 2: 統合テストと Makefile

**フェーズ 1 完了後に実施する。**

#### 2-A: 統合テストファイルの新規作成

**対象ファイル**: `cmd/tlsrpt-digest/slack_summary_integration_test.go`

- [ ] **2.1** ファイルを新規作成し、ビルドタグと `package main` 宣言を追加する。
  - 先頭行: `//go:build test && slack_notify`
  - パッケージ: `package main`

- [ ] **2.2** 必要な import を追加する（`TestSlackNotify_FailureAlert_Integration` の import を参考に以下を含める）。
  - `bytes`, `context`, `net/mail`, `net/url`, `os`, `path/filepath`, `strings`, `testing`, `time`
  - `github.com/oklog/ulid/v2`
  - `github.com/stretchr/testify/assert`, `github.com/stretchr/testify/require`
  - `github.com/isseis/tlsrpt-digest/internal/config`
  - `github.com/isseis/tlsrpt-digest/internal/mailparse`
  - `github.com/isseis/tlsrpt-digest/internal/notify`
  - `github.com/isseis/tlsrpt-digest/internal/store`
  - `storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"`

- [ ] **2.3** `loadSlackSummaryTestEnv(t *testing.T) (successURL, errorURL string)` 関数を実装する。
  - この関数は `slack_summary_integration_test.go`（ビルドタグ `test && slack_notify`）にのみ定義し、`slack_notify_env_test.go` には追加しないこと（`test` のみのビルドで参照されないよう隔離するため）。
  - `t.Helper()` を呼び出す。
  - `missingSlackSummaryEnv(nil)` で欠落確認し、欠落がある場合は `t.Skip("Slack summary env not configured: " + strings.Join(missing, ", "))` でスキップする。
  - `os.Getenv(notify.EnvSlackWebhookURLSuccess)` と `os.Getenv(notify.EnvSlackWebhookURLError)` の両方を返す。

- [ ] **2.4** `TestSlackSummary_Summary_Integration(t *testing.T)` 関数を実装する。
  処理の骨格（`02_architecture.md` § 3.3 参照）:

  1. `ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)` でタイムアウト付きコンテキストを作成し `defer cancel()` する。
  2. `successURL, errorURL := loadSlackSummaryTestEnv(t)` で環境変数を取得（未設定ならスキップ）。
  3. `runID := ulid.Make().String()` で実行ごとに一意な runID を生成する。
  4. 以下の 3 通の EML を順番に読み込み、それぞれからレポートをパースする:
     - `filepath.Join("..", "..", "testdata", "tlsrpt_success_google_1.eml")`
     - `filepath.Join("..", "..", "testdata", "tlsrpt_success_google_2.eml")`
     - `filepath.Join("..", "..", "testdata", "tlsrpt_success_microsoft.eml")`
     - 各 `os.ReadFile` 呼び出しに `//nolint:gosec // G304: path is a hardcoded testdata literal` コメントを付与する（計 3 箇所すべてに必要）。
     - `mail.ReadMessage` → `mailparse.ExtractAttachments(msg, 10<<20)` → `parseTLSRPTAttachment` でレポートを取得する。
     - 各 EML について `require.NotNil(t, report)` で nil チェックする（計 3 回）。
     - 各 EML について `assert.False(t, report.HasFailure())` で failure ゼロを確認する（計 3 回）。
  5. `fakeStore := storetestutil.NewFakeStore()` を生成する。
  6. 3 レポートを `store.ReportInput` スライスに変換し `fakeStore.SaveReports(inputs)` で保存する。`require.NoError` でエラーを確認した後、`require.Len(t, fakeStore.Reports, 3)` で 3 件が保存されたことを確認する（AC-03）。
  7. サマリ期間を設定する:
     - `start := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)`
     - `end := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)`
  8. `summary, err := notify.GenerateSummary(ctx, fakeStore, start, end, nil)` でサマリを生成し `require.NoError` でエラーを確認する。
  9. サマリ内容を以下のアサーションで確認する:
     - `require.Equal(t, int64(3), summary.ReportCount)` (AC-04)
     - `assert.Contains(t, summary.OrganizationStats, "Google Inc.")` (AC-05)
     - `assert.Contains(t, summary.OrganizationStats, "Microsoft Corporation")` (AC-05)
     - `assert.Equal(t, int64(5), summary.OrganizationStats["Google Inc."])` (AC-06)
     - `assert.Equal(t, int64(2), summary.OrganizationStats["Microsoft Corporation"])` (AC-07)
  10. `AllowedHost` を `successURL` のホスト名から設定する（`TestSlackNotify_FailureAlert_Integration` と同パターン、`errorURL` も同じホストであることが前提）:
      - `u, err := url.Parse(successURL)` / `require.NoError` / `cfg.Notify.Slack.AllowedHost = u.Hostname()`
  11. `notifier, err := setupNotifyHandlers(config.Secret(successURL), config.Secret(errorURL), cfg, runID, false)` でノーティファイアを構築し `require.NoError` でエラーを確認する。
  12. `require.NoError(t, notifier.LogSummary(ctx, summary))` でサマリをバッファリングする。
  13. `require.NoError(t, notifier.Flush(ctx))` で送信し、エラーなし（= Slack 疎通成功）を確認する (AC-08)。

#### 2-B: Makefile の更新

**対象ファイル**: `Makefile`

- [ ] **2.5** `.PHONY` 行に `test-slack-summary` を追加する。
  - 変更前: `.PHONY: build test test-integration test-slack-notify lint fmt deadcode clean`
  - 変更後: `.PHONY: build test test-integration test-slack-notify test-slack-summary lint fmt deadcode clean`

- [ ] **2.6** `test-slack-summary` ターゲットを追加する。
  - `test-slack-notify` ターゲットの直後に追加する。
  - コメントと make コマンドを以下の内容で記述する:
    ```makefile
    # Manually send a real Slack weekly summary from testdata to verify webhook
    # connectivity and message formatting. Requires both
    # TLSRPT_SLACK_WEBHOOK_URL_SUCCESS and TLSRPT_SLACK_WEBHOOK_URL_ERROR; skipped when unset.
    test-slack-summary:
    	go test -v -count=1 -tags test,slack_notify -run ^TestSlackSummary ./cmd/tlsrpt-digest/...
    ```

**完了確認**: `make test && make lint` が通過すること。

---

## 3. 実装順序とマイルストーン

| マイルストーン | 完了条件 |
|---|---|
| M0: フェーズ 0 完了 | `make test` 通過（既存テスト全通過）、`rg -n "EnvSlackWebhookURLSuccess" internal/notify/validate.go` がヒット |
| M1: フェーズ 1 完了 | `make test` 通過、`TestSlackSummary_EnvRequirements` が 6 ケースすべて通過 |
| M2: フェーズ 2 完了 | `make test && make lint` 通過、`make test-slack-summary` コマンドが正しく構築される |
| M3: 手動検証完了 | `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` と `TLSRPT_SLACK_WEBHOOK_URL_ERROR` を設定して `make test-slack-summary` を実行し、Slack チャンネルにサマリが届くことを目視確認 |

フェーズ 0 → 1 → 2 の順で実施する。フェーズ 0 は既存テストのリグレッションなしを確認してからフェーズ 1 に進む。

---

## 4. テスト戦略

### フェーズ 0 のテスト検証

フェーズ 0 はリファクタリングであり、新 AC は追加しない。以下で正確性を確認する:
- 既存の `TestSlackNotify_EnvRequirements`（`slack_notify_env_test.go`）が引き続き通過すること（`slackNotifyWebhookEnvKey` の定義変更後も名前参照を使うため意味は変わらない）。
- `make test` 全体が通過すること。
- 静的チェック: `rg -n "EnvSlackWebhookURLSuccess\|EnvSlackWebhookURLError" internal/notify/validate.go` で定数定義と使用箇所がヒットすること。

### 常時実行テスト（ビルドタグ: `test`）

- `TestSlackSummary_EnvRequirements`（`slack_notify_env_test.go`）: `missingSlackSummaryEnv` の純粋関数としての正確性を 6 ケースで検証する。`make test` に含まれる。

### 手動実行専用テスト（ビルドタグ: `test && slack_notify`）

- `TestSlackSummary_Summary_Integration`（`slack_summary_integration_test.go`）: 実 Slack Webhook への送信を伴う統合テスト。`make test-slack-summary` からのみ実行する。

### 既存テストへの影響

- フェーズ 0 で `slack_notify_env_test.go` の `slackNotifyWebhookEnvKey` 定義を変更するが、同ファイルのテスト関数はこの定数を名前で参照しているため影響なし。
- `slack_notify_integration_test.go`・`slack_notify_env_test.go` の既存テスト本体は変更しない。

---

## 5. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| `internal/notify` への import 追加による循環依存 | ビルドエラー | `cmd/tlsrpt-digest` → `internal/notify` の依存はすでに存在するため循環しない。事前に `go build ./...` で確認する |
| `ValidateEnvCombination` が success URL のみの設定を拒否する | `setupNotifyHandlers` が失敗しテストがエラー終了 | `missingSlackSummaryEnv` で error URL の設定も必須にし、両方の URL を `loadSlackSummaryTestEnv` で取得・渡す |
| `successURL` と `errorURL` が異なるホスト名を持つ | `BuildHandlers` が `errorURL` の AllowedHost 検証で失敗 | `cfg.Notify.Slack.AllowedHost` は `successURL` のホスト名から設定するため、両 URL が同一ホストである前提。手動検証時は同一 Slack ワークスペースの URL を使用する |
| G304 lint エラー（`os.ReadFile` にハードコードパス） | `make lint` 失敗 | 3 通分の `os.ReadFile` 呼び出しすべてに `//nolint:gosec // G304: path is a hardcoded testdata literal` を付与する |
| EML ファイルのパスが誤っている | `require.NoError` でテスト失敗 | 既存テストと同じ相対パスパターン `filepath.Join("..", "..", "testdata", "...")` を使用する |

---

## 6. 実装チェックリスト

### フェーズ 0

- [ ] `EnvSlackWebhookURLSuccess` 定数追加（`internal/notify/validate.go`）
- [ ] `EnvSlackWebhookURLError` 定数追加（`internal/notify/validate.go`）
- [ ] `validate.go` エラーメッセージを定数参照に変更（before/after は §2 フェーズ 0 タスク 0.2 参照）
- [ ] `boot.go` の `os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS")` を `os.Getenv(notify.EnvSlackWebhookURLSuccess)` に変更
- [ ] `boot.go` の `os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR")` を `os.Getenv(notify.EnvSlackWebhookURLError)` に変更
- [ ] `slack_notify_env_test.go` に `internal/notify` import 追加
- [ ] `slackNotifyWebhookEnvKey` の定義を `= notify.EnvSlackWebhookURLError` に変更
- [ ] `make test` 通過確認（既存テスト全通過）

### フェーズ 1

- [ ] `slackSummaryWebhookEnvKey` 定数追加（`= notify.EnvSlackWebhookURLSuccess`、`slack_notify_env_test.go`）
- [ ] `missingSlackSummaryEnv` 関数実装（両 URL チェック）
- [ ] `TestSlackSummary_EnvRequirements` テスト実装（6 ケース）
  - [ ] `webhook_url_missing`: success URL 欠落を確認
  - [ ] `webhook_url_empty_value`: success URL が空文字列であることを確認
  - [ ] `error_webhook_url_missing`: success URL 設定済み、error URL 欠落を確認
  - [ ] `webhook_url_set`: 両方設定済みで missing リストが空であることを確認
  - [ ] `nil_env_fallback_present`: `nil` env + 両方 `t.Setenv` で空リストを確認
  - [ ] `nil_env_fallback_missing`: `nil` env + 両方空文字列で両エントリの存在を確認
- [ ] `make test` 通過確認

### フェーズ 2

- [ ] `slack_summary_integration_test.go` 新規作成（ビルドタグ `test && slack_notify`、`package main`）
- [ ] `loadSlackSummaryTestEnv` 関数実装（`slack_summary_integration_test.go` にのみ配置）
- [ ] `TestSlackSummary_Summary_Integration` テスト実装
  - [ ] 60s タイムアウト付きコンテキストと `defer cancel()`
  - [ ] ULID 生成による runID
  - [ ] 3 通 EML 読み込み（各 `os.ReadFile` に `//nolint:gosec // G304` コメント、計 3 箇所）
  - [ ] 各 EML で `require.NotNil(t, report)` を実行（計 3 回）
  - [ ] 各 EML で `assert.False(t, report.HasFailure())` を実行（計 3 回）
  - [ ] `FakeStore` への `SaveReports` と `require.NoError`
  - [ ] `require.Len(t, fakeStore.Reports, 3)` で 3 件保存を確認（AC-03）
  - [ ] `notify.GenerateSummary` でサマリ生成（期間: 2026-05-11 〜 2026-05-14）と `require.NoError`
  - [ ] サマリアサーション（`ReportCount` / `OrganizationStats` 両キー / 各成功セッション数）
  - [ ] `successURL` から `AllowedHost` を設定
  - [ ] `setupNotifyHandlers(config.Secret(successURL), config.Secret(errorURL), ...)` による `NotificationSink` 構築と `require.NoError`
  - [ ] `notifier.LogSummary` と `notifier.Flush` で送信、`require.NoError`（計 2 回）
- [ ] `.PHONY` 行に `test-slack-summary` 追加（`Makefile`）
- [ ] `test-slack-summary` ターゲット追加（`Makefile`）
- [ ] `make test && make lint` 通過確認（`make lint` が実行する 4 invocation すべてを含む）

---

## 7. 完了基準

### 機能完全性

- [ ] `EnvSlackWebhookURLSuccess` / `EnvSlackWebhookURLError` が `internal/notify/validate.go` に定義され、`boot.go`・`validate.go`・テストファイルがすべて定数参照している。
- [ ] `TestSlackSummary_EnvRequirements` が 6 ケースすべて通過する。
- [ ] `TestSlackSummary_Summary_Integration` が環境変数設定時に通過し、未設定時にスキップされる。
- [ ] `make test-slack-summary` で `TestSlackSummary` プレフィックスのテストのみが実行される。

### 品質指標

- [ ] `make test` 通過（既存テストへのリグレッションなし）
- [ ] `make lint` 通過（`make lint` が実行する 4 invocation すべて）

### セキュリティ確認

- [ ] Webhook URL がコード内にハードコードされていないこと（環境変数からのみ取得）。
- [ ] `config.Secret(webhookURL)` でラップして `setupNotifyHandlers` に渡していること。

### ドキュメント完全性

- [ ] 本計画書（`03_implementation_plan.md`）が `approved` になっていること。

---

## 8. 受け入れ基準検証

| AC | 説明 | 検証種別 | テスト / 静的検証 |
|---|---|---|---|
| AC-01 | 3 通の EML から TLS-RPT レポートをパースできること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — 各 EML で `require.NotNil(t, report)` を計 3 回実行 |
| AC-02 | パース結果がすべて failure セッション 0 であること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `assert.False(t, report.HasFailure())` を 3 回実行 |
| AC-03 | 3 レポートが `FakeStore` に保存されること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `require.NoError(t, fakeStore.SaveReports(inputs))` および `require.Len(t, fakeStore.Reports, 3)` |
| AC-04 | `GenerateSummary` が `ReportCount == 3` を返すこと | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `require.Equal(t, int64(3), summary.ReportCount)` |
| AC-05 | `OrganizationStats` に Google と Microsoft の両方が含まれること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `assert.Contains(t, summary.OrganizationStats, "Google Inc.")` / `assert.Contains(t, summary.OrganizationStats, "Microsoft Corporation")` |
| AC-06 | Google Inc. の成功セッション合計が 5 であること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `assert.Equal(t, int64(5), summary.OrganizationStats["Google Inc."])` |
| AC-07 | Microsoft Corporation の成功セッション合計が 2 であること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `assert.Equal(t, int64(2), summary.OrganizationStats["Microsoft Corporation"])` |
| AC-08 | 送信処理がエラーなく完了すること | `test` | `cmd/tlsrpt-digest/slack_summary_integration_test.go::TestSlackSummary_Summary_Integration` — `require.NoError(t, notifier.Flush(ctx))` |
| AC-09 | 通知経路・書式・リトライ挙動が本番と同一であること | `static` | `rg -n "setupNotifyHandlers" cmd/tlsrpt-digest/slack_summary_integration_test.go` — 1 行以上ヒットすること |
| AC-10 | 環境変数未設定時にテストがスキップされること | `test` | `cmd/tlsrpt-digest/slack_notify_env_test.go::TestSlackSummary_EnvRequirements` — `webhook_url_missing` / `error_webhook_url_missing` の各サブテストで `missingSlackSummaryEnv` が非空リストを返すことを確認。統合テスト側は `loadSlackSummaryTestEnv` の `t.Skip` 呼び出しで担保（手動検証） |
| AC-11 | `make test-slack-summary` ターゲットが存在すること | `static` | `rg -n "^test-slack-summary:" Makefile` — 1 行一致すること |
| AC-12 | `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` を環境変数で渡して実行できること | `static` | `rg -n "TLSRPT_SLACK_WEBHOOK_URL_SUCCESS" Makefile` — コメント行またはターゲット定義行に含まれること |
| AC-13 | `make test` と `make test-integration` では実行されないこと | `static` | `rg -n "slack_notify" Makefile` の出力で `test:` 行および `test-integration:` 行に `slack_notify` が含まれないこと |
| AC-14 | 永続ファイルを一切作成しないこと | `static` + `test` | `rg -n "NewFakeStore" cmd/tlsrpt-digest/slack_summary_integration_test.go` — 1 行以上ヒットすること。統合テスト実行後に永続ファイルが作成されないことを手動確認 |

---

## 9. 次のステップ

1. フェーズ 0 実装（`/runplan` コマンドで実施）。
2. フェーズ 1 実装（`/runplan` コマンドで実施）。
3. フェーズ 2 実装（`/runplan` コマンドで実施）。
4. 手動検証: `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` と `TLSRPT_SLACK_WEBHOOK_URL_ERROR` を設定して `make test-slack-summary` を実行し、Slack チャンネルでサマリメッセージの書式を目視確認する。
