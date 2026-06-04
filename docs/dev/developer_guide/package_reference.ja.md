# パッケージ構造リファレンス

本ドキュメントは、このコードベースにおけるパッケージ構造の詳細なリファレンスを提供する。

## ディレクトリ構造

- `cmd/`: コマンドラインエントリーポイント
  - `tlsrpt-digest/`: メインバイナリ
- `internal/`: コア実装
  - `config/`: 共有設定型と TOML ロード
  - `imap/`: IMAP クライアント
    - `testutil/`: imap パッケージ用テストダブル
  - `mailparse/`: MIME 添付ファイル抽出
  - `tlsrpt/`: TLSRPT レポートパース
  - `notify/`: 通知送信
    - `testutil/`: notify パッケージ用テストダブル
  - `store/`: データ永続化
    - `testutil/`: store パッケージ用テストダブル
  - `storelock/`: ストア書き込みプロセスロック
- `docs/`: プロジェクトドキュメント
- `testdata/`: テスト用データファイル

## パッケージの責務

### エントリーポイント（`cmd/`）

- **`tlsrpt-digest/`**: TOML 設定を読み込み、各コンポーネントを初期化して one-shot 処理を実行するバイナリ。スケジューリングは外部スケジューラー（systemd timer / cron）に委ねる。以下のサブコマンドで処理を分離する。
  - `fetch`: IMAP からレポートを取得・処理し、失敗検出時にアラートを送信する
  - `summary`: 蓄積済みレポートを集計して定期サマリを送信する
  - `reprocess`: 保存済み `.eml` ファイルを再解析してレポートストアを再構築する
  - `gc`: 保持期間を超えたレポートデータと `.eml` ファイルを削除する
  - `recover`: UIDVALIDITY 変化を検出した際のストア整合性を確認・修復する

### コアパッケージ（`internal/`）

#### メール取得

- **`imap/`**: `MailFetcher` インターフェースを定義し、IMAP サーバへの TLS 接続・メタ情報取得・選択的ダウンロード・既読マーク処理を実装する。
- **`imap/testutil/`**: `MailFetcher` のテストダブル（`FakeMailFetcher`）を提供する。スパイ機能（呼び出し記録）を持つ。パッケージ名は `imaptestutil`。

#### MIME 解析

- **`mailparse/`**: `*mail.Message` から添付ファイルのバイト列とファイル名を取り出す。`imap` パッケージと `tlsrpt` パッケージの間に位置し、MIME パース処理を両パッケージから分離する。

#### TLSRPT 解析

- **`tlsrpt/`**: `.json.gz` バイト列の gzip 展開と RFC 8460 JSON のパースを行い、`failure_session_count` を評価する。

#### 通知

- **`notify/`**: `SlackHandler`（`slog.Handler` 実装）を通じて Slack Incoming Webhook へ通知を送信する。INFO レベルの正常サマリと WARN/ERROR レベルのアラートで送信先 Webhook を分ける。バッファリングして `Flush()` 時に送信する設計。Webhook URL は環境変数で管理する。`BuildHandlers` でハンドラを構築し、Webhook URL のホスト名バリデーションを行う。
- **`notify/testutil/`**: `slog.Handler` のスパイ実装（`SpyHandler`）を提供する。受信レコードを記録し、テストでの通知内容検証に使用する。パッケージ名は `notifytestutil`。

#### データ永続化

- **`store/`**: 処理済みレポートデータを JSON ファイルに保存する（定期サマリ生成用）。受信メール原本を `.eml` ファイルとして保存する（問題解析・再処理・テスト用）。`SummaryConsistencyGuard` による共有ロックで summary サブコマンドとの整合性を保証する。UIDVALIDITY 変化時のリカバリ状態をセンチネルファイルで管理する。
- **`store/testutil/`**: ストアのインメモリ実装（`FakeStore`）を提供する。テストでのストア操作の注入・検証に使用する。パッケージ名は `storetestutil`。

#### プロセスロック

- **`storelock/`**: ストアへの書き込み操作に対するプロセス間排他ロックを提供する。書き込み系サブコマンド（`fetch`・`gc`・`reprocess`・`recover`）は `store.Open` の前に `Acquire` でロックを取得し、処理完了まで保持する。

#### 共有型

- **`config/`**: 設定ファイルの TOML ロードと型定義を提供する。`Secret` 型はログへの漏洩を防ぐため、`String()` / `LogValue()` は常にマスク値を返す。`Load` / `LoadFile` で厳格なバリデーション（未知キー拒否・フィールド値検証）を行う。

## 主要な設計パターン

- **関心の分離**: 各パッケージは単一の責務を持つ
- **インターフェースベースの設計**: テスタビリティのためにインターフェースを多用する（例: `MailFetcher`）
- **one-shot 実行**: バイナリは起動・処理・終了を1サイクルで行い、内部スケジューリングを持たない
- **エラーハンドリング**: 包括的なエラー型とバリデーション
