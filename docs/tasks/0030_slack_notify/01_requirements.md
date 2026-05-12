# 要件定義書：Slack通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-12 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

TLSRPT レポートで TLS 接続失敗が検出された場合、管理者に即座に通知する必要がある。
このタスクでは `internal/notify` パッケージを実装し、Slack Incoming Webhook を使った通知機能を担う。

通知先は正常時（INFO）と異常時（WARN/ERROR）で分け、それぞれ異なる Slack チャンネルへ送信できるようにする。これにより、緊急度の高いアラートが通常の報告に埋もれることを防ぐ。

Slack Webhook URL はポスト権限を持つ機密情報であるため、設定ファイルには記載せず環境変数で管理する。意図しない外部サーバーへの誤送信を防ぐため、接続先ホスト名のポリシーのみを設定ファイルに記載し、起動時に照合する。

機密情報の通知メッセージへの混入リスクについては [通知セキュリティガイドライン](../../dev/developer_guide/notification_security.ja.md) に従う。

### 1.2 目的

1. **主目的**: Slack Webhook を通じて即時アラートおよび定期サマリを送信する
2. **副次的目的**: slog.Handler として実装することで、ログレベルに基づいた通知先の自動振り分けを実現する

---

## 2. スコープ

### 対象範囲（In Scope）

- Slack 通知ハンドラの実装（`slog.Handler` として）
- 正常時・異常時の Webhook URL を個別に設定する仕組み
- ログレベルによる通知先の振り分け（INFO → 正常時、WARN/ERROR → 異常時）
- 標準出力への通知送信（開発・テスト用）
- テスト用スパイハンドラの実装
- Webhook URL の検証（HTTPS スキーム・ホスト名照合）

### 対象外（Out of Scope）

- メール通知（将来の拡張）
- 週次サマリの通知メッセージ生成（タスク 0050 で担当、本タスクのハンドラを利用）
- 通知の重複制御・レート制限
- Webhook URL の暗号化保存

### 影響を受けるコンポーネント

- **直接変更**: `internal/notify/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（ハンドラの利用側）、`internal/config/`（`allowed_host` 設定項目・`Secret` 型）

---

## 3. 機能要件

### F-001: Slack ハンドラの実装

Slack 通知を `slog.Handler` として実装し、ログレベルにより正常時・異常時の Webhook URL に振り分ける。

**型安全性の確保**:

`slog.Handler` は `slog.Record`（自由形式の Message を含む）を受け取るが、以下の設計で型制約を維持する:

1. 通知イベントは型付きヘルパー関数（例：`LogAlert(ctx context.Context, logger *slog.Logger, alert Alert)`）を通じてのみ `slog.Record` として記録する。外部コードは直接 `logger.Info(...)` を呼ばず、必ずこれらの関数を使う
2. Slack ハンドラを接続した `*slog.Logger` は `internal/notify` パッケージ内に閉じ込め、外部に公開しない
3. デバッグ出力（IMAP 通信ログ等）は別の `io.Writer` に書き込み、Slack ハンドラには流れない

**受け入れ条件（Acceptance Criteria）**:

1. `slog.Handler` インターフェースを実装した Slack ハンドラを提供する
2. INFO レベルのログレコードは正常時 Webhook URL に送信する
3. WARN・ERROR レベルのログレコードは異常時 Webhook URL に送信する
4. HTTP リクエストが失敗した場合（タイムアウト、接続エラー等）はエラーを返す
5. Slack API がエラーレスポンス（4xx, 5xx）を返した場合はエラーを返す

### F-002: 正常時・異常時 Webhook URL の設定

正常時と異常時でそれぞれ独立した Webhook URL を環境変数で設定できる。

**環境変数**:

| 環境変数 | 用途 |
|---|---|
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | 正常時通知用 Webhook URL（INFO レベル） |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | 異常時通知用 Webhook URL（WARN/ERROR レベル） |

**設定の組み合わせと動作**:

| SUCCESS | ERROR | 動作 |
|---|---|---|
| 設定あり | 設定あり | 両方の Webhook に通知 |
| 設定なし | 設定あり | 異常時通知のみ（正常時通知なし） |
| 設定あり | 設定なし | **設定エラー**（異常を見逃すリスクがあるため禁止） |
| 設定なし | 設定なし | Slack 通知無効 |

**受け入れ条件（Acceptance Criteria）**:

1. `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` と `TLSRPT_SLACK_WEBHOOK_URL_ERROR` の両方が設定されている場合、ログレベルに従って送信先を振り分ける
2. `TLSRPT_SLACK_WEBHOOK_URL_ERROR` のみ設定されている場合、WARN/ERROR レベルのみ送信し INFO レベルは送信しない
3. `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` のみ設定されている場合は設定エラーとして起動を中断する
4. 両方未設定の場合は Slack 通知を無効化する（エラーにならない）
5. 正常時と異常時の URL が同一でも許容する（単一チャンネル運用への後方互換）

### F-003: 標準出力への通知送信（開発・テスト用）

開発・テスト時に Slack の代わりとして標準出力へ通知メッセージを出力する。

**受け入れ条件（Acceptance Criteria）**:

1. `slog.Handler` として実装し、Slack ハンドラと同じインターフェースで利用できる
2. ログレベルをメッセージに含めて標準出力に書き出す
3. デバッグ出力（IMAP 通信ログ等）を受け取るインターフェースを持たない

### F-004: ログレベルと通知先の対応

各イベントのログレベルを定義し、通知先を決定する。

**ログレベル定義**:

| イベント | ログレベル | 送信先 |
|---|---|---|
| 週次サマリ（TLS 失敗なし） | INFO | success webhook |
| TLS failure 検出（即時アラート） | WARN | error webhook |
| TLSRPT レポートのパースエラー | WARN | error webhook |
| 処理済みメールの既読マーク失敗 | WARN | error webhook |
| IMAP 認証失敗 | ERROR | error webhook |
| IMAP 接続断 | ERROR | error webhook |
| ストレージ読み書き失敗 | ERROR | error webhook |
| 週次サマリ作成時のデータ読み出し失敗 | ERROR | error webhook |

**受け入れ条件（Acceptance Criteria）**:

1. 上表のログレベルに従い、各イベントが正しい Webhook URL に送信される
2. INFO レベルのイベントが error webhook に送信されることはない
3. WARN・ERROR レベルのイベントが success webhook のみに送信されることはない

### F-005: 即時アラートのメッセージフォーマット

TLSRPT failure 検出時の通知メッセージを適切にフォーマットする。

**受け入れ条件（Acceptance Criteria）**:

1. 通知メッセージに送信元組織名（`organization-name`）が含まれる
2. 通知メッセージに対象ポリシータイプ（`sts` / `tlsa`）が含まれる
3. 通知メッセージに failure_session_count の値が含まれる
4. 通知メッセージにレポートの対象期間（`date-range`）が含まれる

### F-006: Webhook URL の検証

起動時に Webhook URL を検証し、不正な場合は設定エラーとして起動を中断する。

本検証の主目的は**運用ミスの防止**（コピペ間違い・テスト用 URL の本番混入など）である。

**設定項目**:

| 項目 | 場所 | 内容 |
|---|---|---|
| `notify.slack.allowed_host` | TOML 設定ファイル | 許可するホスト名（ポート番号なし） |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | 環境変数 | 正常時 Webhook URL（機密情報） |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | 環境変数 | 異常時 Webhook URL（機密情報） |

**受け入れ条件（Acceptance Criteria）**:

1. いずれかの Webhook URL のスキームが `https` でない場合（`http` など）は設定エラーを返す
2. いずれかの Webhook URL のホスト名が `allowed_host` と一致しない場合は設定エラーを返す
3. 正常時 URL と異常時 URL のホスト名が異なる場合は設定エラーを返す（両 URL は同一ホストを前提とする）
4. ホスト名の照合はポート番号を除いた形で行い、大文字/小文字を区別しない完全一致とする
   - 例: `https://hooks.slack.com:443/...` のホスト名は `hooks.slack.com` として照合する
5. `allowed_host` が未設定の場合、いずれかの環境変数が設定されていれば設定エラーを返す
6. 両環境変数が未設定の場合は `allowed_host` の値によらず検証をスキップする（Slack 無効）

**`allowed_host` の有効な値**:

| TOML の値 | 結果 |
|---|---|
| `"hooks.slack.com"` | OK |
| `""` （未設定） | OK（Slack 無効化） |
| `"hooks.slack.com:443"` | 設定エラー（ポート番号不可） |
| `" hooks.slack.com "` | 設定エラー（前後空白不可） |
| `"https://hooks.slack.com"` | 設定エラー（スキーム不可） |

---

## 4. 非機能要件

### パフォーマンス

- Slack API へのリクエストには適切なタイムアウト（例：10 秒）を設定する

### セキュリティ

本システムの通知セキュリティ設計方針は [通知セキュリティガイドライン](../../dev/developer_guide/notification_security.ja.md) に定める。主要な要件を以下に列挙する。

**機密情報の分離**:

- Webhook URL はログに出力しない
- Webhook URL（`TLSRPT_SLACK_WEBHOOK_URL_SUCCESS`・`TLSRPT_SLACK_WEBHOOK_URL_ERROR`）は環境変数で管理し、設定ファイルには記載しない
- 設定ファイルには接続先ホスト名（`notify.slack.allowed_host`）のみを記載する

**出力パスの分離**:

- Debug Logger（標準出力・ファイル）と Slack ハンドラは独立した出力パスとする
- Slack ハンドラはデバッグ出力（IMAP 通信ログ等）を受け取るインターフェースを持たない
- Slack ハンドラでは redaction を常に適用し、設定による無効化を認めない

**Secret 型の適用**:

- `Config` 構造体のパスワード・Webhook URL フィールドは `Secret` 型でラップする
- `Secret` 型は `String()` と `LogValue()` で常に `[REDACTED]` を返す

### 保守性

- `slog.Handler` として実装することで、既存のログ基盤（`MultiHandler` 等）と組み合わせられること

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- Slack Incoming Webhook の仕様に従った JSON ペイロードを送信する
- テストには `stretchr/testify` を使用する

---

## 6. テスト方針

### 単体テスト

- スパイハンドラを使った通知呼び出しの検証テスト
- メッセージフォーマットの単体テスト（F-005 の各フィールドが含まれること）
- ログレベルと送信先の振り分けテスト:
  - INFO レベルが success webhook に送信されること
  - WARN レベルが error webhook に送信されること
  - ERROR レベルが error webhook に送信されること
- Webhook URL 設定の組み合わせテスト（F-002 の4パターン）
- URL 検証テスト（F-006）:
  - HTTP スキームはエラーになること
  - ホスト不一致はエラーになること
  - 正常時・異常時 URL のホスト名不一致はエラーになること
  - `allowed_host` が未設定かつ URL が設定されている場合はエラーになること
  - 両環境変数未設定の場合は検証をスキップすること（Slack 無効）
- セキュリティテスト:
  - `Secret` 型のフィールドが通知メッセージに含まれないこと
  - Slack ハンドラがデバッグ出力（`io.Writer` 経由）を受け取らないこと

### 統合テスト

- モック HTTP サーバを使った Slack Webhook 送信テスト（success/error 各ハンドラ）
