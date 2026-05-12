# 要件定義書：Slack通知

## 1. 背景と目的

### 1.1 背景

TLSRPT レポートで TLS 接続失敗が検出された場合、管理者に即座に通知する必要がある。
このタスクでは `internal/notify` パッケージを実装し、Slack Incoming Webhook を使った通知機能を担う。

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

### 対象外（Out of Scope）

- メール通知（将来の拡張）
- 週次サマリの通知（タスク 0050 で担当）
- 通知の重複制御・レート制限

### 影響を受けるコンポーネント

- **直接変更**: `internal/notify/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（Notifier の利用側）

---

## 3. 機能要件

### F-001: Notifier インターフェース

通知機能を抽象化するインターフェースを定義する。

**受け入れ条件（Acceptance Criteria）**:

1. `Notifier` インターフェースが定義され、Slack 実装と `SpyNotifier` が実装する
2. `SpyNotifier` は送信された通知メッセージの内容を記録し、テストから検証できる

### F-002: Slack Incoming Webhook 送信

Slack Incoming Webhook URL に対して HTTP POST で通知を送信する。

**受け入れ条件（Acceptance Criteria）**:

1. 有効な Webhook URL と通知内容を与えた場合、Slack に通知が送信される
2. HTTP リクエストが失敗した場合（タイムアウト、接続エラー等）はエラーを返す
3. Slack API がエラーレスポンス（4xx, 5xx）を返した場合はエラーを返す
4. Webhook URL が空の場合は設定エラーを返す

### F-003: 即時アラートのメッセージフォーマット

TLSRPT failure 検出時の通知メッセージを適切にフォーマットする。

**受け入れ条件（Acceptance Criteria）**:

1. 通知メッセージに送信元組織名（`organization-name`）が含まれる
2. 通知メッセージに対象ポリシータイプ（`sts` / `tlsa`）が含まれる
3. 通知メッセージに failure_session_count の値が含まれる
4. 通知メッセージにレポートの対象期間（`date-range`）が含まれる

---

## 4. 非機能要件

### パフォーマンス

- Slack API へのリクエストには適切なタイムアウト（例：10 秒）を設定する

### セキュリティ

- Webhook URL はログに出力しない

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

### 統合テスト

- モック HTTP サーバを使った Slack Webhook 送信テスト
