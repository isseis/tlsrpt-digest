# 要件定義書：通知失敗時の警告ログ出力

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-25 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

`internal/notify` の Slack ハンドラ（`LogAlert`・`LogWarning`・`LogSystemError`・`LogSummary`・`Flush`）が返すエラーは、現状いずれも呼び出し元で `_ =` により破棄されている。webhook URL の誤設定やネットワーク障害など何らかの理由で Slack 通知が失敗した場合でも、オペレータにはその事実が一切伝わらない。

### 1.2 目的

1. **主目的**: Slack 通知の失敗を `slog.Warn` で構造化ログとして出力し、オペレータが問題を検知できるようにする
2. **副次的目的**: 通知失敗はプロセスの主処理（IMAP フェッチ・サマリ生成など）の失敗ではないため、警告にとどめてプロセスの終了コードには影響させない

---

## 2. スコープ

### 対象範囲（In Scope）

- `cmd/tlsrpt-digest` 層（`boot.go` および各サブコマンド実装）における通知エラーのログ出力

### 対象外（Out of Scope）

- `internal/notify` パッケージ自体の変更
- 通知失敗時のリトライ・フォールバック処理
- 通知失敗を理由とした終了コードの変更

### 影響を受けるコンポーネント

- **直接変更**: `cmd/tlsrpt-digest/boot.go`、`cmd/tlsrpt-digest/notify_helpers.go`、各サブコマンド実装ファイル（`fetch.go`・`summary.go`・`reprocess.go` 等）

---

## 3. 機能要件

### `F-001`: 通知失敗時の警告ログ出力

Slack 通知（`LogAlert`・`LogWarning`・`LogSystemError`・`LogSummary`・`Flush` の各呼び出し）が返すエラーを `slog.Warn` で出力する。既存コードで `slog.Error` を使っている箇所も `slog.Warn` に統一する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: `NotificationSink` の各メソッド（`LogAlert`・`LogWarning`・`LogSystemError`・`LogSummary`・`Flush`）が非 nil エラーを返した場合、`slog.Warn` で警告ログを出力する。ログには少なくともエラー内容（`"error"` フィールド）を含む。これには `notify_helpers.go` の `logAlerts`・`logWarn` や、サブコマンド実装内の通知ヘルパー呼び出し（`notifyFetchSystemError` 等）の戻り値も含む
- `AC-02`: 通知失敗はプロセスの終了コードに影響しない（主処理が成功した場合は終了コード 0）。現状 `summary.go` では `LogSummary` のエラーを呼び出し元に返して終了コード非ゼロにしているが、`slog.Warn` で出力しつつ処理を継続するよう変更する
- `AC-03`: `boot.go` 内の `notifySystemError` が返すエラー（Bootstrap 中に発生するシステムエラー通知の失敗）も `slog.Warn` で出力する。現状 `_ =` で破棄されている

---

## 4. 非機能要件

### 保守性

- 通知エラーのログ出力はサブコマンド実装ごとに個別に書くのではなく、共通のヘルパーまたはラッパーで一元化することが望ましい

---

## 5. 制約

- 実装言語は Go（Go 1.26 以降）

---

## 6. テスト方針

### 単体テスト

- `SpyNotificationSink` などのテストダブルを使用し、通知エラー発生時に `slog.Warn` が呼ばれることを検証する
- `slog.Warn` の出力キャプチャには、テスト用のカスタム `slog.Handler` を実装し、`slog.SetDefault(slog.New(customHandler))` でデフォルトロガーを差し替える方法を用いる。各テストケースは `t.Cleanup` でデフォルトロガーを元に戻す
- 終了コードが 0 であることを確認するテストを AC-02 の対象箇所（特に `summary.go` の `LogSummary` 失敗パス）に追加する
