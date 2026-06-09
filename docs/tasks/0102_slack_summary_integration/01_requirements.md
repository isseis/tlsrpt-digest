# 要件定義書：Slack サマリ通知インテグレーションテスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-09 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

タスク 0100 では failure を含む TLS-RPT メールを起点とした Slack アラート送信の統合テストを実装した。一方、週次サマリ（`summary` サブコマンドが送信する TLS Report Summary）については、実際の Slack Webhook への送信が正常に完了することや、Slack 上での表示品質は手動確認するしかない状態にある。

特に以下の点が未検証である。

- 複数組織・複数メールを集約したサマリが実 Slack Webhook に正常送信されること。
- Slack が受信したサマリメッセージが、期待どおりの書式で描画されること。

### 1.2 目的

1. **主目的**: 複数組織（Google Inc. × 2 通・Microsoft Corporation × 1 通）の success-only TLS-RPT メールを集約し、実際の Slack Webhook にサマリ通知を送信できることを確認する統合テストを追加する。
2. **副次的目的**: 開発者が Slack 上でサマリメッセージ書式を目視検証するための再現可能な手順を提供する。

---

## 2. スコープ

### 対象範囲（In Scope）

- 専用ビルドタグ（`test && slack_notify`）付き統合テストの新規作成
  - 以下の 3 通の匿名化済みテストデータ EML を入力とする
    - `testdata/tlsrpt_success_google_1.eml`（Google Inc.、2026-05-11、success 1 セッション）
    - `testdata/tlsrpt_success_google_2.eml`（Google Inc.、2026-05-12、success 4 セッション）
    - `testdata/tlsrpt_success_microsoft.eml`（Microsoft Corporation、2026-05-13、success 2 セッション）
  - EML パース → TLSRPT レポート取得 → インメモリストア（`FakeStore`）への保存 → サマリ生成 → 実 Slack Webhook へのサマリ送信、という一連のフローを実行する
  - 対象 Webhook URL は環境変数（`TLSRPT_SLACK_WEBHOOK_URL_SUCCESS`）で指定し、未設定時はテストをスキップする
  - 永続アーティファクト（`tlsrpt.json` 等）を残さない（`FakeStore` 使用）
- 環境変数欠落判定ヘルパーと常時実行ユニットテストの追加
- `make test-integration` への組み込み（`test-slack-notify` と同様に `slack_notify` タグで隔離）
- 既存の `slack_notify_env_test.go` への success Webhook URL サポートの追加

### 対象外（Out of Scope）

- GitHub Actions など CI への組み込み（本テストは手動実行専用）。
- failure を含むレポートのサマリ送信テスト（failure は summary から除外される仕様であり、既存ユニットテストで検証済み）。
- Slack 上のメッセージ内容の自動アサーション（目視確認を想定）。
- 環境変数名の TOML 設定ファイルへの追加。

### 影響を受けるコンポーネント

- **新規作成**: `testdata/tlsrpt_success_google_1.eml`、`testdata/tlsrpt_success_google_2.eml`、`testdata/tlsrpt_success_microsoft.eml`（匿名化済みテストデータ、本タスク開始時点で作成済み）。
- **変更**: `cmd/tlsrpt-digest/slack_notify_env_test.go`（success Webhook URL 用ヘルパーとユニットテストを追加）。
- **新規作成**: `cmd/tlsrpt-digest/slack_summary_integration_test.go`（実 Slack 送信を行う手動実行専用統合テスト）。
- **変更**: `Makefile`（`test-slack-summary` ターゲットを追加し、`test-integration` に組み込む）。

---

## 3. 機能要件

### `F-001`: 複数 TLS-RPT メールのパースとストア保存

3 通の EML をそれぞれ読み込み、TLSRPT 添付ファイルをパースして `FakeStore` に保存する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: `testdata/tlsrpt_success_google_1.eml`、`testdata/tlsrpt_success_google_2.eml`、`testdata/tlsrpt_success_microsoft.eml` の 3 通それぞれから TLS-RPT レポートをパースできること。
- `AC-02`: パース結果がすべて failure セッション 0（success-only）であること。
- `AC-03`: パースした 3 レポートが `FakeStore` に保存されること。

### `F-002`: サマリ生成

`FakeStore` に保存された 3 レポートを集約してサマリを生成する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-04`: `notify.GenerateSummary` が `ReportCount == 3` のサマリを返すこと。
- `AC-05`: サマリの `OrganizationStats` に `"Google Inc."` と `"Microsoft Corporation"` の両方が含まれること。
- `AC-06`: Google Inc. の成功セッション合計が 5（1 + 4）であること。
- `AC-07`: Microsoft Corporation の成功セッション合計が 2 であること。

### `F-003`: Slack サマリ送信

生成したサマリを、環境変数で指定された実 Slack Webhook に送信する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-08`: 環境変数 `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` が設定されている場合、サマリ送信処理がエラーなく完了すること。
- `AC-09`: 送信に用いる通知経路・メッセージ書式・リトライ挙動は本番（`summary` サブコマンド実行時のサマリ送信）と同一であること。
- `AC-10`: `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` が未設定の場合、テストはスキップされること。

### `F-004`: 手動実行用 make ターゲット

開発者が手動で簡単に実行できる make ターゲットを提供する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-11`: 専用 make ターゲット `test-slack-summary` により、本統合テストのみを実行できること。
- `AC-12`: `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` を環境変数で渡して実行できること。
- `AC-13`: 通常の `make test` および `make test-integration` では本テストが実行されないこと。

### `F-005`: 永続アーティファクトの非作成

**受け入れ条件（Acceptance Criteria）**:

- `AC-14`: 本テストは `tlsrpt.json` をはじめとする store の永続ファイルを一切作成しないこと（`FakeStore` をストレージとして使用する）。

---

## 4. 非機能要件

### セキュリティ

- Webhook URL はコード内にハードコードせず、環境変数からのみ取得する。
- Webhook URL は、本番と同じ許可ホスト検証を経て送信されること。

### 保守性

- 通知経路は本番コードパス（`setupNotifyHandlers`、`notify.GenerateSummary`、`notifier.LogSummary`）を再利用し、テスト専用の送信処理を重複実装しないこと（DRY）。

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）。
- 本テストは専用ビルドタグ（`test && slack_notify`）で隔離し、CI および `make test` / `make test-integration` では実行しない。
- テストは、本番の通知経路を再利用できる位置（`cmd/tlsrpt-digest` パッケージ）に配置する。
- ストレージは `FakeStore`（インメモリ）を使用し、ディスク書き込みを一切行わない。

---

## 6. テスト方針

### このタスク自体のテスト

環境変数欠落判定ヘルパーは純粋関数として切り出し、`//go:build test` のユニットテストで常時検証する。実 Slack 送信を伴う統合テスト本体は `//go:build test && slack_notify` で隔離し、専用 make ターゲット `test-slack-summary` からのみ手動実行する。

### 手動実行手順

```
TLSRPT_SLACK_WEBHOOK_URL_SUCCESS=<webhook-url> make test-slack-summary
```

実行後、指定した Slack チャンネルに以下の内容を含むサマリが届いていることを目視確認する。

- レポート件数: 3 件
- Google Inc.: 5 セッション成功
- Microsoft Corporation: 2 セッション成功

---

## 7. テストデータ詳細

本タスク開始時点で、以下の匿名化済み EML ファイルを `testdata/` 下に作成済み。

| ファイル | 送信組織 | レポート期間 | 成功セッション | 失敗セッション |
|---|---|---|---|---|
| `tlsrpt_success_google_1.eml` | Google Inc. | 2026-05-11 | 1 | 0 |
| `tlsrpt_success_google_2.eml` | Google Inc. | 2026-05-12 | 4 | 0 |
| `tlsrpt_success_microsoft.eml` | Microsoft Corporation | 2026-05-13 | 2 | 0 |

匿名化の内容:

- 受信ドメイン → `example.com`
- 受信サーバーのホスト名（MTA、内部ホスト）: `mail.example.com` に統一
- 受信サーバーの内部 IP アドレス（`10.202.2.x`）: RFC 5737 ドキュメント用アドレス（`192.0.2.1`）に置換
- JSON 添付ファイル（gzip 圧縮）内の `policy-domain`・`report-id` 等: `example.com` に置換
- 送信者側（Google / Microsoft）の IP アドレス・ホスト名: 変更なし（公開情報）
