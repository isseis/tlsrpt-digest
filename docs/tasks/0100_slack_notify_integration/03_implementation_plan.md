# 実装計画書：Slack 通知インテグレーションテスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-06-05 |
| レビュー日 | 2026-06-05 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

failure を含む TLS-RPT メール（`testdata/tlsrpt_failure.eml`）から failure を検出し、本番と同一の通知経路で実 Slack Webhook にアラートを送信できることを検証する、手動実行専用の統合テストを追加する。設計の詳細は [`02_architecture.md`](02_architecture.md) を参照する。

### 1.2 実装方針

- 本番経路（`parseTLSRPTAttachment` / `setupNotifyHandlers` / `logAlerts`）を再利用し、テスト専用の送信処理は新規実装しない（[`02_architecture.md`](02_architecture.md) §1.1、§3）。
- テスト本体は `//go:build test && slack_notify` タグ付きファイルに置き、`make test` / `make test-integration` から除外する（[`02_architecture.md`](02_architecture.md) §7.3）。
- 環境変数 `TLSRPT_SLACK_WEBHOOK_URL_ERROR` 未設定時は `t.Skip` する。検出ロジックは注入可能な純粋ヘルパー（既存 `missingRecoveryEnv` と同型）に切り出し、`//go:build test` ファイルへ置いて `make test` で常時ユニットテストする。実 Slack 送信を伴う統合テスト本体のみ `//go:build test && slack_notify` に隔離する。
- store / `fetch` ランナーを一切呼ばず、永続ファイル（`tlsrpt.json`）を作成しない（[`02_architecture.md`](02_architecture.md) §5.2）。
- Go ソース内のコメント・識別子・文字列リテラルはすべて英語で記述する。

### 1.3 既存コード調査結果

| 領域 | 既存の状態 | 不足・変更点 |
|---|---|---|
| パース経路 | `parseTLSRPTAttachment`（[`fetch.go`](../../../cmd/tlsrpt-digest/fetch.go)）、`mailparse.ExtractAttachments(msg *mail.Message, maxBytes int64)`（[`mailparse.go`](../../../internal/mailparse/mailparse.go) 39 行）、`tlsrpt.ParseGzip` / `(*Report).HasFailure`（[`tlsrpt.go`](../../../internal/tlsrpt/tlsrpt.go)）が存在。 | 変更不要。テストから呼び出すのみ。 |
| 通知経路 | `setupNotifyHandlers(successURL, errorURL config.Secret, cfg *config.Config, runID string, dryRun bool) (NotificationSink, error)`、`notificationSink`、`logAlerts(ctx, notifier NotificationSink, report *tlsrpt.Report, component string)` が存在（[`boot.go`](../../../cmd/tlsrpt-digest/boot.go)、[`notify_helpers.go`](../../../cmd/tlsrpt-digest/notify_helpers.go)）。 | 変更不要。テストから呼び出すのみ。 |
| 許可ホスト検証 | `BuildHandlers` 経由で `validateWebhookURL`（[`validate.go`](../../../internal/notify/validate.go) 26 行）が呼ばれ、`AllowedHost` が空または不一致でエラー。 | テストは `cfg.Notify.Slack.AllowedHost` に Webhook URL のホストを設定する必要あり。新規実装不要。 |
| 環境変数スキップ | `missingRecoveryEnv(env map[string]string) []string` と `loadRecoveryTestEnv(t)`（[`recovery_integration_test.go`](../../../cmd/tlsrpt-digest/recovery_integration_test.go) 63・86 行）、ユニットテスト `TestIntegration_RecoveryEnvRequirements`（134 行）が存在。ただしこのファイルは `//go:build integration` であり `make test-integration` 経路で実行される。 | 本タスク用に同型のヘルパー `missingSlackNotifyEnv`（単一変数版）を新規作成し、`//go:build test` ファイルへ置いて `make test` で実行されるようにする。 |
| ビルドタグ依存 | `cmd/tlsrpt-digest` の `test_helpers.go`（`SpyNotificationSink` 等）や `boot_test.go`・`fetch_test.go` 等は `//go:build test`。`go test` 時はパッケージ内の他テストファイルも同時にコンパイルされる。 | 統合テストファイルも `//go:build test && slack_notify` にし、`-tags test,slack_notify` でのみ対象化する（[`02_architecture.md`](02_architecture.md) §7.3）。 |
| config 構築 | テストでは `cfg := &config.Config{}` を手組みし必要フィールドのみ設定する慣習（[`reprocess_test.go`](../../../cmd/tlsrpt-digest/reprocess_test.go) 78・82 行）。 | 同様に手組みする。新規ヘルパー不要。 |
| パース単体検証 | `internal/tlsrpt/tlsrpt_test.go::TestParseRealReport` が `tlsrpt_failure.eml` を含むテーブル駆動で `HasFailure()==true` を検証済み（`make test` で常時実行）。 | 変更不要。AC-01／AC-02 の常時実行セーフティネットとして参照。 |
| testdata | `testdata/tlsrpt_failure.eml`（リポジトリルート、org=`Google Inc.`、policy-type=`sts`、failure=2、期間 2026-02-08〜2026-02-09）が存在。 | 変更不要。 |
| ulid | `github.com/oklog/ulid/v2` が既存依存（[`recovery_integration_test.go`](../../../cmd/tlsrpt-digest/recovery_integration_test.go) 17 行）。 | runID 生成に再利用。 |

新規に追加するのは env ヘルパー用テストファイル 1 本、統合テストファイル 1 本、`Makefile` ターゲット 1 つのみ。既存の処理ロジック・既存テストの変更は不要。

---

## 2. 実装ステップ

### ステップ 1-1: 環境変数ヘルパーとユニットテスト（常時実行）

**対象ファイル**: `cmd/tlsrpt-digest/slack_notify_env_test.go`（新規、`//go:build test`、`package main`）

純粋な env 検出ヘルパーとそのユニットテストを `//go:build test` 側に置くことで、`make test` で常時実行され、レグレッションを検出できるようにする（統合テスト本体だけを `slack_notify` タグへ隔離する）。ヘルパーは独立した `test_helpers.go` ではなく自身のユニットテストと同一の `_test.go` に同居させる。これは既存 `recovery_integration_test.go`（`missingRecoveryEnv` とその検証テストを同居）の先例に倣う配置であり、[test_organization.md](../../dev/developer_guide/test_organization.md) の分類 B（パッケージ内・非公開 API 利用）に適合する。

なお `t.Skip` を行うラッパー `loadSlackNotifyTestEnv` は、唯一の呼び出し元が統合テスト（`slack_notify` タグ側）であるためステップ 1-2 の `slack_notify` ファイルに置く。これを `test` タグ側に置くと、`make lint`（`golangci-lint run --build-tags test`、`slack_notify` を含まない）で呼び出し元ゼロとなり `unused` 違反になるため避ける。`test` タグ側に置く純粋ヘルパー `missingSlackNotifyEnv` は自身のユニットテストから呼ばれるため `unused` にならない。

- [x] 環境変数キー定数 `slackNotifyWebhookEnvKey = "TLSRPT_SLACK_WEBHOOK_URL_ERROR"` を定義する。
- [x] 純粋ヘルパー `missingSlackNotifyEnv(env map[string]string) []string` を実装する。`env` が nil のときはプロセス環境を、非 nil のときは注入マップを参照し、`slackNotifyWebhookEnvKey` が空なら `"<key> (empty)"` を返す（`missingRecoveryEnv` と同型）。
- [x] ユニットテスト `TestSlackNotify_EnvRequirements(t *testing.T)` を実装する。
  - [x] 空マップ `map[string]string{}` で `missingSlackNotifyEnv` を呼び、結果が `slackNotifyWebhookEnvKey` の欠落（`"<key> (empty)"`）を含むことをアサートする。
  - [x] `slackNotifyWebhookEnvKey` を設定したマップで呼び、結果が空であることをアサートする。

**完了基準**:
- [x] `go test -tags test -run TestSlackNotify_EnvRequirements ./cmd/tlsrpt-digest/...` が pass する（`make test` 経路で常時実行されることの確認。実送信なし）。
- [x] `golangci-lint run --build-tags test ./cmd/tlsrpt-digest/...` が 0 issues（`slack_notify` を含まないタグで `unused` 等が出ないことの確認）。

### PR-1 作成ポイント: env helper unit coverage

**対象ステップ**: 1-1

**推奨タイトル**: `feat(0100): add Slack notify env helper tests`

**レビュー観点**: env 欠落判定の純粋性 / `make test` 経路での常時実行 / `slack_notify` 非指定 lint で unused が出ない構成

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

### ステップ 1-2: 統合テストドライバの実装（手動実行）

**対象ファイル**: `cmd/tlsrpt-digest/slack_notify_integration_test.go`（新規、`//go:build test && slack_notify`、`package main`）

- [x] スキップラッパー `loadSlackNotifyTestEnv(t *testing.T) string` を実装する。`missingSlackNotifyEnv(nil)`（ステップ 1-1 で定義、`test,slack_notify` 併用時に可視）が非空なら `t.Skip` し、設定済みなら `os.Getenv(slackNotifyWebhookEnvKey)` の Webhook URL 文字列を返す。
- [x] 統合テスト `TestSlackNotify_FailureAlert_Integration(t *testing.T)` を実装する。処理フローは [`02_architecture.md`](02_architecture.md) §6 に従う。
  - [x] `loadSlackNotifyTestEnv(t)` で Webhook URL を取得（未設定なら skip）。
  - [x] `../../testdata/tlsrpt_failure.eml`（`filepath.Join("..", "..", "testdata", "tlsrpt_failure.eml")`）を `os.ReadFile` で読み込む。`//nolint:gosec` は既存 `reprocess_test.go:418` と同様にハードコードパス前提で付与する。
  - [x] `mail.ReadMessage` → `mailparse.ExtractAttachments(msg, 10<<20)` → `parseTLSRPTAttachment` で `*tlsrpt.Report` を得る（`require.NoError`、`require.NotNil`）。
  - [x] `report.HasFailure()` が `true` であることを `require` する。
  - [x] 送信元レポートの値を自動アサートする: `OrganizationName == "Google Inc."`、failure を持つポリシーが 1 件・その `Policy.PolicyType == "sts"`・`Summary.TotalFailureSessionCount == int64(2)`、`DateRange.StartDatetime` / `EndDatetime` が 2026-02-08 / 2026-02-09（UTC）。
  - [x] `cfg := &config.Config{}` を作り、`cfg.Notify.Slack.AllowedHost` に Webhook URL のホストを設定する。`url.Parse` は `(*url.URL, error)` を返すため、戻り値を個別に受け取ってエラーチェックを行った後（`require.NoError`）、`u.Hostname()` を取得する。
  - [x] `runID` を `ulid.Make().String()` で生成する（繰り返し実行時に Slack 上でメッセージを区別するため）。
  - [x] `setupNotifyHandlers(config.Secret(""), config.Secret(webhookURL), cfg, runID, false)` で `NotificationSink` を構築する（`require.NoError`）。success URL は空でよい（`ValidateEnvCombination` は success のみ設定を禁じるが、error のみ設定は許容する）。
  - [x] `logAlerts(ctx, sink, report, "slack-notify-test")` を呼ぶ。
  - [x] `sink.Flush(ctx)` の戻り値が `nil` であることを `require.NoError` する。

**完了基準**:
- [x] `go test -run '^$' -tags test,slack_notify ./cmd/tlsrpt-digest/...` がコンパイル成功する（型・シグネチャ検証。実送信なし）。
- [x] `gofumpt -l cmd/tlsrpt-digest/slack_notify_env_test.go cmd/tlsrpt-digest/slack_notify_integration_test.go` が差分を出力しない（`make fmt` 適用後）。
- [x] `golangci-lint run --build-tags test,slack_notify ./cmd/tlsrpt-digest/...` が 0 issues。

### PR-2 作成ポイント: Slack notify integration driver

**対象ステップ**: 1-2

**推奨タイトル**: `feat(0100): add Slack notify integration driver`

**レビュー観点**: 本番パース・通知経路の再利用 / 実 Slack 送信の build tag 隔離 / Webhook host 検証と永続ファイル非作成

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

### ステップ 2-1: Makefile ターゲットの追加

**対象ファイル**: `Makefile`

- [ ] `.PHONY` 行に `test-slack-notify` を追加する（現状: `.PHONY: build test test-integration lint fmt deadcode clean`）。
- [ ] `test-slack-notify` ターゲットを追加する。レシピは次のとおり（環境変数はシェルから継承される）:
  ```
  # Manually send a real Slack alert from testdata to verify webhook
  # connectivity and message formatting. Requires
  # TLSRPT_SLACK_WEBHOOK_URL_ERROR; skipped when unset.
  test-slack-notify:
  	go test -v -count=1 -tags test,slack_notify -run TestSlackNotify ./cmd/tlsrpt-digest/...
  ```
  - `-run TestSlackNotify` は統合テストと環境変数ユニットテストの両方（`TestSlackNotify_*`）に一致し、それ以外のパッケージ内テストを除外する。

**完了基準**:
- [ ] `TLSRPT_SLACK_WEBHOOK_URL_ERROR` 未設定で `make test-slack-notify` を実行すると、統合テストは skip、環境変数ユニットテストは pass する（実送信は発生しない）。

### ステップ 3-1: ローカル実行と目視確認（手動）

- [ ] 実 Webhook URL を指定して `TLSRPT_SLACK_WEBHOOK_URL_ERROR=https://hooks.slack.com/services/... make test-slack-notify` を実行する。
- [ ] Slack チャンネルにアラートが届き、組織名（`Google Inc.`）・ポリシー種別（`sts`）・失敗セッション数（`2`）・レポート期間（2026-02-08〜2026-02-09）が表示されることを目視確認する（[`02_architecture.md`](02_architecture.md) §7.2）。
- [ ] 実行後に `git status --porcelain` が空である（`tlsrpt.json` 等の永続ファイルが残らない）ことを確認する。

### PR-3 作成ポイント: manual Slack notify target and verification

**対象ステップ**: 2-1 / 3-1

**推奨タイトル**: `feat(0100): add Slack notify manual test target`

**レビュー観点**: 通常 test 経路からの隔離 / 手動実行コマンドの最小性 / 実 Webhook 送信結果と作業ツリー無副作用の確認

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

## 3. 実装順序とマイルストーン

### 3.1 マイルストーン

| マイルストーン | 内容 | 成果物 |
|---|---|---|
| M1 | ステップ 1-1・1-2 完了 | `slack_notify_env_test.go`（env ヘルパー＋ユニットテスト、`make test` で実行）と `slack_notify_integration_test.go`（統合テスト、コンパイル・lint 通過） |
| M2 | ステップ 2-1 完了 | `Makefile` の `test-slack-notify` ターゲット |
| M3 | ステップ 3-1 完了 | 実 Slack への送信成功と目視確認、永続ファイル非作成の確認 |

ステップ 1-1 は [`02_architecture.md`](02_architecture.md) §8 の step 1（env ヘルパー実装）に、ステップ 1-2 は §8 の step 2（統合テストドライバ実装）に対応する。ステップ 1-2（統合テスト）はステップ 1-1 の純粋ヘルパー `missingSlackNotifyEnv` を前提とするため、1-1→1-2 の順で実装する。ステップ 2-1 は §8 の step 3、ステップ 3-1 は §8 の step 4 に対応する。

### 3.2 PR 構成

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | 1-1 | `//go:build test` の env キー定数・欠落検出ヘルパー・常時実行ユニットテストを追加する。 |
| PR-2 | 1-2 | `//go:build test && slack_notify` の統合テストドライバを追加し、本番パース・通知経路で実 Slack アラートを送信できるようにする。 |
| PR-3 | 2-1 / 3-1 | `Makefile` に手動実行専用の `test-slack-notify` ターゲットを追加し、実 Webhook での手動送信・Slack 表示・永続ファイル非作成を同じ PR の検証として記録する。 |

---

## 4. テスト戦略

- **ユニットテスト**: 環境変数検出ヘルパー `missingSlackNotifyEnv` を `TestSlackNotify_EnvRequirements` で検証（欠落検出・設定時の空結果）。注入マップを用いるためネットワーク・実環境変数に依存せず、`//go:build test` 側に置くことで `make test` で常時実行される。
- **統合テスト**: `TestSlackNotify_FailureAlert_Integration` が本番経路を通して実 Slack へ送信。`TLSRPT_SLACK_WEBHOOK_URL_ERROR` 設定時のみ実送信、未設定時は skip。
- **常時実行のセーフティネット**: パース＋failure 判定は既存 `internal/tlsrpt/tlsrpt_test.go::TestParseRealReport`（`make test` で常時実行）が `tlsrpt_failure.eml` を含めて担保。
- **メッセージ書式**: フィールド→Slack メッセージのマッピングは既存 `internal/notify/format_test.go` が担保（常時実行）。本テストは送信元レポートのフィールド値を自動アサートし、最終的な Slack 表示は目視確認で補完。
- **重複の回避**: failure=0 ポリシーのスキップ等、既存ユニットテストが担保する挙動は再検証しない（要件 §2 Out of Scope）。

---

## 5. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| 実 Slack への送信が CI や `make test` で誤発火する | スパム送信・レート消費 | `//go:build test && slack_notify` で隔離し、`make test` / `make test-integration` のタグに含めない。env 未設定時は skip。 |
| Webhook URL のホストが `hooks.slack.com` 以外（プロキシ等） | `AllowedHost` 不一致で常に失敗 | `AllowedHost` を Webhook URL の `Hostname()` から動的に導出し、本番検証経路を通しつつ任意ホストに対応。 |
| パッケージ内の `//go:build test` ファイル（`test_helpers.go` の `SpyNotificationSink` 等）に依存する定義が `slack_notify` 単独タグでは欠ける | 意図しないタグ組み合わせで統合テストが対象外になる | 統合テストファイルの build constraint を `//go:build test && slack_notify` にし、実行コマンドも `-tags test,slack_notify` に統一する（[`02_architecture.md`](02_architecture.md) §7.3）。完了基準のコンパイルチェックで検出。 |
| 繰り返し送信時に Slack 上でメッセージが区別できない | 目視確認が困難 | `runID` を実行ごとに一意（`ulid.Make()`）にし、メッセージに含める。 |

---

## 6. 実装チェックリスト

- [ ] PR-1 マージ済み（対象ステップ: 1-1）
- [ ] PR-2 マージ済み（対象ステップ: 1-2）
- [ ] PR-3 マージ済み（対象ステップ: 2-1 / 3-1）

---

## 7. 受け入れ条件の検証（Acceptance Criteria Verification）

凡例: **test** = 実行可能テスト（誤挙動で失敗）、**static** = `rg`/コンパイルによる静的検証、**manual** = 実行・目視。各 AC は少なくとも 1 つの test または static を持つ。

| AC | 種別 | 検証手段と期待結果 |
|---|---|---|
| AC-01 | test | `cmd/tlsrpt-digest/slack_notify_integration_test.go::TestSlackNotify_FailureAlert_Integration`（`parseTLSRPTAttachment` で `*tlsrpt.Report` 取得を `require`）。常時実行の補強として `internal/tlsrpt/tlsrpt_test.go::TestParseRealReport`（`tlsrpt_failure.eml` を含む）。 |
| AC-02 | test | `internal/tlsrpt/tlsrpt_test.go::TestParseRealReport`（`HasFailure()==true` をアサート、常時実行）。加えて `…::TestSlackNotify_FailureAlert_Integration` がレポートのフィールド値（org=`Google Inc.`、policy-type=`sts`、failure=2、期間 2026-02-08／09）を自動アサート。 |
| AC-03 | test | `…::TestSlackNotify_FailureAlert_Integration`（failure ポリシー 1 件を確認後 `logAlerts`→`Flush()==nil` を `require`）。manual: Slack 受信をステップ 3-1 で目視。 |
| AC-04 | test | `internal/notify/format_test.go::TestFormatAlerts_Fields`（`notify.Alert` から Slack 送信本文に org／policy-type／failure-count／期間が出力されることをアサート、`make test` で常時実行）。加えて `…::TestSlackNotify_FailureAlert_Integration` が送信元レポートの該当フィールド値を自動アサート。manual: Slack 表示をステップ 3-1 で目視。 |
| AC-05 | test | `…::TestSlackNotify_FailureAlert_Integration`（`Flush()` の戻り値を `require.NoError`。非 nil で失敗）。 |
| AC-06 | static | `rg -n 'setupNotifyHandlers|logAlerts' cmd/tlsrpt-digest/slack_notify_integration_test.go` 期待: 両シンボルが一致（本番経路を再利用し独自送信を実装しない）。 |
| AC-07 | static | `rg -n 'TLSRPT_SLACK_WEBHOOK_URL_ERROR' cmd/tlsrpt-digest/slack_notify_env_test.go cmd/tlsrpt-digest/boot.go` 期待: 両ファイルで一致（テストと本番が同一の error チャネル環境変数名を使用）。 |
| AC-08 | test | `cmd/tlsrpt-digest/slack_notify_env_test.go::TestSlackNotify_EnvRequirements`（空マップで欠落を返し、設定時に空を返すことをアサート、`//go:build test` で `make test` 常時実行）。 |
| AC-09 | static | `rg -n 'go:build test && slack_notify' cmd/tlsrpt-digest/slack_notify_integration_test.go` 期待: 1 件一致（テストが `test,slack_notify` タグ併用時のみ対象化される）。かつ `rg -n 'slack_notify' Makefile` 期待: 一致行がすべて `test-slack-notify` ターゲット内のみ（`test:` および `test-integration:` のレシピには `slack_notify` が出現しない）。 |
| AC-10 | static | `rg -n 'slackNotifyWebhookEnvKey' cmd/tlsrpt-digest/slack_notify_env_test.go cmd/tlsrpt-digest/slack_notify_integration_test.go` 期待: env_test.go で定数定義、integration_test.go の `loadSlackNotifyTestEnv` が同定数で `os.Getenv` し URL を取得。かつ `rg -n 'test-slack-notify' Makefile` 期待: ターゲット存在。manual: `TLSRPT_SLACK_WEBHOOK_URL_ERROR=… make test-slack-notify` で送信成功（ステップ 3-1）。 |
| AC-11 | static | `rg -n 'SaveReports|fetchRunner|store\.Open|Bootstrap' cmd/tlsrpt-digest/slack_notify_integration_test.go` 期待: 一致なし（store／fetch 経路を呼ばない）。manual: 実行後 `git status --porcelain` が空。 |
| AC-12 | static | 本テストはストレージを使わない設計のため、固定パスの store を導入していないことを確認する: `rg -n 'store\.Open|RootDir|tlsrpt\.json' cmd/tlsrpt-digest/slack_notify_integration_test.go` 期待: 一致なし。これにより並列・繰り返し実行でのファイル競合が構造的に発生しない（AC-11 と同じ不在確認を AC-12 の前提充足としても用いる）。将来ストレージが必要になった場合に `t.TempDir` を用いる方針は [`02_architecture.md`](02_architecture.md) §5.2 に記載。 |

---

## 8. 横断検索チェックリスト（Cross-Search）

本タスクは加算的で削除・改名する既存概念はないが、命名衝突と本番との整合を以下で確認する。

- [ ] `rg -n 'slack_notify' Makefile cmd/ internal/` — 期待: 新規テストファイルの build タグと `Makefile` の `test-slack-notify` ターゲットのみに出現。既存コードと衝突しない。
- [ ] `rg -n 'TestSlackNotify' cmd/tlsrpt-digest/` — 期待: 新規 2 テスト（`TestSlackNotify_FailureAlert_Integration`、`TestSlackNotify_EnvRequirements`）のみ。
- [ ] `rg -n 'TLSRPT_SLACK_WEBHOOK_URL_ERROR' cmd/tlsrpt-digest/boot.go` — 期待: 本番が同名を使用（テストの環境変数名と一致）。
- [ ] `rg -n 'test-slack-notify' Makefile` — 期待: `.PHONY` とターゲット定義の 2 箇所。
- [ ] ドキュメント・用語集の更新対象なし（新規環境変数名・タグ名は本タスク 3 文書内で完結）。

---

## 9. 成功基準

- **機能完全性**: AC-01〜AC-12 がすべて §7 の手段で検証可能であり、test／static の項目が満たされている。
- **品質**: `make fmt` 適用済み。`golangci-lint run --build-tags test`（`make lint` 既定）と `golangci-lint run --build-tags test,slack_notify` の双方が 0 issues。`go test -run '^$' -tags test,slack_notify ./cmd/tlsrpt-digest/...` がコンパイル成功。
- **隔離**: `make test` および `make test-integration` で本統合テストが実行されない（build タグ除外）。
- **無副作用**: 実行後に作業ツリーへ未追跡ファイルが残らない（`git status --porcelain` が空）。
- **手動検証**: 実 Webhook 指定時に Slack へアラートが届き、書式が期待どおり（ステップ 3-1、および [`02_architecture.md`](02_architecture.md) §7.2）。

---

## 10. 次のステップ

- 本計画書のレビューと承認（ステータスを `approved` に更新）。
- 承認後、ステップ 1-1〜3-1 を PR-1〜PR-3 の順に実装する。
- 実装完了後、実 Webhook での目視確認結果を PR に記録する。
