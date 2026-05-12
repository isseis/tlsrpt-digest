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

Slack Webhook URL はポスト権限を持つ機密情報であるため、設定ファイルには記載せず環境変数で管理する。一方で意図しない外部サーバーへの誤送信を防ぐため、接続先ホスト名のポリシーのみを設定ファイルに記載し、起動時に照合する。

### 1.2 目的

1. **主目的**: Slack Webhook を通じて即時アラートを送信する
2. **副次的目的**: テスト容易性のための `Notifier` インターフェース定義と `SpyNotifier` の提供

---

## 2. スコープ

### 対象範囲（In Scope）

- `Notifier` インターフェースの定義
- Slack Incoming Webhook を使った通知送信
- 通知メッセージのフォーマット（即時アラート用）
- テスト用 `SpyNotifier` の実装
- Webhook URL の検証（HTTPS スキーム・ホスト名照合）

### 対象外（Out of Scope）

- メール通知（将来の拡張）
- 週次サマリの通知（タスク 0050 で担当）
- 通知の重複制御・レート制限
- Webhook URL の暗号化保存

### 影響を受けるコンポーネント

- **直接変更**: `internal/notify/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（Notifier の利用側）、`internal/config/`（`allowed_host` 設定項目）

---

## 3. 機能要件

### F-001: Notifier インターフェース

通知機能を抽象化するインターフェースを定義する。

**受け入れ条件（Acceptance Criteria）**:

1. `Notifier` インターフェースが定義され、Slack 実装と `SpyNotifier` が実装する
2. `SpyNotifier` は送信された通知メッセージの内容を記録し、テストから検証できる

### F-002: Slack Incoming Webhook 送信

環境変数 `TLSRPT_SLACK_WEBHOOK_URL` から取得した Webhook URL に対して HTTP POST で通知を送信する。

**受け入れ条件（Acceptance Criteria）**:

1. 有効な Webhook URL と通知内容を与えた場合、Slack に通知が送信される
2. HTTP リクエストが失敗した場合（タイムアウト、接続エラー等）はエラーを返す
3. Slack API がエラーレスポンス（4xx, 5xx）を返した場合はエラーを返す
4. 環境変数 `TLSRPT_SLACK_WEBHOOK_URL` が未設定の場合は Slack 通知を無効化する（エラーにならない）

### F-003: 即時アラートのメッセージフォーマット

TLSRPT failure 検出時の通知メッセージを適切にフォーマットする。

**受け入れ条件（Acceptance Criteria）**:

1. 通知メッセージに送信元組織名（`organization-name`）が含まれる
2. 通知メッセージに対象ポリシータイプ（`sts` / `tlsa`）が含まれる
3. 通知メッセージに failure_session_count の値が含まれる
4. 通知メッセージにレポートの対象期間（`date-range`）が含まれる

### F-004: Webhook URL の検証

起動時に Webhook URL を検証し、不正な場合は設定エラーとして起動を中断する。

本検証の主目的は**運用ミスの防止**（コピペ間違い・テスト用 URL の本番混入など）である。

**設定項目**:

| 項目 | 場所 | 内容 |
|---|---|---|
| `notify.slack.allowed_host` | TOML 設定ファイル | 許可するホスト名（ポート番号なし） |
| `TLSRPT_SLACK_WEBHOOK_URL` | 環境変数 | Webhook の完全 URL（機密情報） |

**受け入れ条件（Acceptance Criteria）**:

1. Webhook URL のスキームが `https` でない場合（`http` など）は設定エラーを返す
2. Webhook URL のホスト名が `allowed_host` と一致しない場合は設定エラーを返す
3. ホスト名の照合はポート番号を除いた形で行い、大文字/小文字を区別しない完全一致とする
   - 例: `https://hooks.slack.com:443/...` のホスト名は `hooks.slack.com` として照合する
   - 例: `HOOKS.SLACK.COM` は `hooks.slack.com` と一致する
4. `allowed_host` が未設定の場合、環境変数が設定されていれば設定エラーを返す
5. 環境変数 `TLSRPT_SLACK_WEBHOOK_URL` が未設定の場合は `allowed_host` の値によらず検証をスキップする（Slack 無効）

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

- Webhook URL はログに出力しない
- Webhook URL（`TLSRPT_SLACK_WEBHOOK_URL`）は環境変数で管理し、設定ファイルには記載しない
- 設定ファイルには接続先ホスト名（`notify.slack.allowed_host`）のみを記載する

### 保守性

- `Notifier` インターフェースを通じて依存性を注入できること

---

## 5. 制約

- 使用言語は Go とする（Go 1.23 以上）
- Slack Incoming Webhook の仕様に従った JSON ペイロードを送信する
- テストには `stretchr/testify` を使用する

---

## 6. テスト方針

### 単体テスト

- `SpyNotifier` を使った通知呼び出しの検証テスト
- メッセージフォーマットの単体テスト
- エラーケース（Webhook URL 未設定、HTTP エラー）のテスト
- URL 検証テスト:
  - HTTP スキームはエラーになること
  - ホスト不一致（例: `evil.example.com`）はエラーになること
  - ホスト一致（例: `hooks.slack.com`）は通過すること
  - 大文字ホスト（例: `HOOKS.SLACK.COM`）が許可ホストと一致すること
  - ポート番号付き URL（例: `https://hooks.slack.com:443/...`）が正しく照合されること
  - `allowed_host` が未設定かつ URL が設定されている場合はエラーになること
  - 環境変数未設定の場合は検証をスキップすること（Slack 無効）

### 統合テスト

- モック HTTP サーバを使った Slack Webhook 送信テスト
