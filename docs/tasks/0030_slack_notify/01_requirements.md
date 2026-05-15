# 要件定義書：Slack通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-12 |
| レビュー日 | 2026-05-15 |
| レビュアー | isseis |
| コメント | 過去プロジェクト（`go-safe-cmd-runner`）の Slack 関連タスク（`0055`・`0068`・`0091`）の知見を反映し、リトライ・タイムアウト（`F-007`）、二段階起動フロー（`F-008`）、Dry-run モード（`F-009`）、CLI ログレベル独立性、Run ID、メッセージ truncation、TOML への URL 混入検知を追加 |

---

## 1. 背景と目的

### 1.1 背景

TLSRPT レポートで TLS 接続失敗が検出された場合、管理者に即座に通知する必要がある。
このタスクでは `internal/notify` パッケージを実装し、Slack Incoming Webhook を使った通知機能を担う。

通知には以下の 2 種類の役割がある。

- **即時アラート**: TLS 接続失敗を検出した際に管理者へ迅速に伝える
- **定期サマリ（週次）**: TLS 接続失敗ゼロの週も含め、定期的に通知を送ることでシステムが正常稼働していることを確認する。通知が途絶えた場合は本システム自体の障害として検知できる

通知先は正常時（`INFO`）と異常時（`WARN`/`ERROR`）で分け、それぞれ異なる Slack チャンネルへ送信できるようにする。これにより、緊急度の高いアラートが通常の報告に埋もれることを防ぐ。

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
- Webhook URL の検証（HTTPS スキーム・ホスト名照合・TOML への混入検知）
- HTTP 送信時のリトライ・バックオフ・タイムアウト制御
- TOML 読み込み完了後に Slack ハンドラを初期化する二段階起動フロー
- Dry-run モード（実際の送信を抑制し、ハンドラ動作のみ確認）
- Slack メッセージのリッチ表現（attachment / color / emoji によるログレベル別の視覚的な区別）

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

**集約バッファ型の送信モデル**:

1 回のポーリング処理で複数の失敗を検出した場合、それぞれを個別に投稿するのではなく 1 つのメッセージにまとめる。これにより Slack の通知量を抑え、HTTP 呼び出し回数を最小化する。

- **`Handle()` はバッファに積む**: `slog.Handler.Handle()` は即座には HTTP 送信せず、内部バッファにレコードを蓄積する。この操作は非同期であり呼び出し元をブロックしない
- **`Flush(ctx) error` で一括送信**: ポーリング処理の終了時点で呼び出し元が `Flush()` を呼ぶ。バッファに積まれたレコードを 1 つのメッセージにフォーマットして送信する。ここでリトライ・タイムアウト制御（`F-007`）を行う
- **`Flush()` はエラーを返す**: HTTP 送信失敗は `Flush()` の戻り値として呼び出し元へ伝達される。送信失敗の詳細は Debug Logger（stderr・ファイル）にも記録する
- **空バッファの `Flush()`**: バッファが空のとき `Flush()` は何もせず `nil` を返す

**型安全性の確保**:

`slog.Handler` は `slog.Record`（自由形式の Message を含む）を受け取るが、以下の設計で型制約を維持する:

1. 通知イベントは型付きヘルパー関数（例：`LogAlert(ctx context.Context, alert Alert)`）を通じてのみ内部ロガーへ書き込む。外部コードは直接 `logger.Info(...)` を呼ばず、必ずこれらのヘルパーを使う
2. Slack ハンドラを接続した `*slog.Logger` は `internal/notify` パッケージ内に閉じ込め、外部に公開しない
3. デバッグ出力（IMAP 通信ログ等）は別の `io.Writer` に書き込み、Slack ハンドラには流れない

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: `slog.Handler` インターフェースを実装した Slack ハンドラを提供する
- `AC-02`: INFO レベルのログレコードは内部バッファに蓄積し、`Flush()` 呼び出し時に正常時 Webhook URL へ送信する
- `AC-03`: WARN・ERROR レベルのログレコードは内部バッファに蓄積し、`Flush()` 呼び出し時に異常時 Webhook URL へ送信する
- `AC-04`: `Flush()` での HTTP 送信が最終的に失敗した場合（リトライ上限到達）、`Flush()` はエラーを返し、Debug Logger にエラー詳細を記録する
- `AC-05`: `Flush()` 中に Slack API が回復不能なエラーレスポンス（リトライ不対象の 4xx）を返した場合は即座にエラーを返し、Debug Logger に記録する
- `AC-05a`: バッファが空のとき `Flush()` は何もせず `nil` を返す
- `AC-05b`: `Handle()` はバッファへの書き込みのみを行い、HTTP 送信は行わない

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

- `AC-06`: `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` と `TLSRPT_SLACK_WEBHOOK_URL_ERROR` の両方が設定されている場合、ログレベルに従って送信先を振り分ける
- `AC-07`: `TLSRPT_SLACK_WEBHOOK_URL_ERROR` のみ設定されている場合、WARN/ERROR レベルのみ送信し INFO レベルは送信しない
- `AC-08`: `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` のみ設定されている場合は設定エラーとして起動を中断する
- `AC-09`: 両方未設定の場合は Slack 通知を無効化する（エラーにならない）
- `AC-10`: 正常時と異常時の URL が同一でも許容する（単一チャンネル運用への後方互換）

### F-003: 標準出力への通知送信（開発・テスト用）

開発・テスト時に Slack の代わりとして標準出力へ通知メッセージを出力する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-11`: `slog.Handler` として実装し、Slack ハンドラと同じインターフェースで利用できる
- `AC-12`: ログレベルをメッセージに含めて標準出力に書き出す
- `AC-13`: デバッグ出力（IMAP 通信ログ等）を受け取るインターフェースを持たない

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

- `AC-14`: 上表のログレベルに従い、各イベントが正しい Webhook URL に送信される
- `AC-15`: INFO レベルのイベントが error webhook に送信されることはない
- `AC-16`: WARN・ERROR レベルのイベントが success webhook のみに送信されることはない
- `AC-16a`: CLI のログレベル設定（例：`--log-level`）は Slack 通知の振り分けに影響しない。コンソール出力を `WARN` 以上に制限しても、`INFO` レベルの success 通知は引き続き送信される

### F-005: 即時アラートのメッセージフォーマット

1 ポーリング実行で複数の TLSRPT failure を検出した場合、1 つのメッセージに集約してフォーマットする。

**集約メッセージの構造**（TLS failure アラート）:

```
タイトル行（text）: ⚠️ TLS Failures – N organizations affected
attachment（color: warning）:
  fields:
    - [組織名1] | sts | failure: 3 | 2024-01-01 – 2024-01-07
    - [組織名2] | tlsa | failure: 1 | 2024-01-03 – 2024-01-07
    ...
    - Run ID: xxxxxxxx
```

**リッチ表現**:

Slack attachment を用いて、ログレベルや通知種別を色・絵文字で視覚的に区別する。

| 通知種別 | attachment color | 絵文字 |
|---|---|---|
| TLS failure 検出（WARN） | `warning`（橙） | ⚠️ |
| システムエラー（ERROR、IMAP 認証失敗・接続断等） | `danger`（赤） | 🚨 |
| 週次サマリ・正常（INFO） | `good`（緑） | ✅ |

**メッセージ長**:

通知メッセージが上限長を超えた場合、可読性を維持するため切り詰めを行う。**切り詰めは Slack への送信のみに適用する。**

| 項目 | 上限（Slack 送信時のみ） |
|---|---|
| メッセージ本文（text フィールド） | 4000 文字（Slack のメッセージ長上限を踏まえた安全側の値） |
| 個別フィールド値（例：FQDN 列挙） | 1000 文字 |

障害調査のために全文が必要な場合は、ローカルのログファイル（Debug Logger 経由のファイル出力）を参照する。ファイルログには切り詰めを適用せず、通知イベントの全内容を記録する。これにより「Slack で素早く把握 → ログファイルで詳細確認」という運用が可能になる。

**Run ID**:

実行（プロセス起動）ごとに一意な識別子（Run ID）を生成し、すべての通知メッセージの attachment fields に含める。これにより、集約アラート・週次サマリ・サーバ側ログを相関できる。

**受け入れ条件（Acceptance Criteria）**:

- `AC-17`: TLS failure 集約メッセージに各エントリの送信元組織名（`organization-name`）が含まれる
- `AC-18`: TLS failure 集約メッセージに各エントリの対象ポリシータイプ（`sts` / `tlsa` / `no-policy-found`）が含まれる。`no-policy-found` の失敗も他のポリシータイプと同様に集約・通知する
- `AC-19`: TLS failure 集約メッセージに各エントリの `failure_session_count` の値が含まれる
- `AC-20`: TLS failure 集約メッセージに各エントリのレポート対象期間（`date-range`）が含まれる
- `AC-20a`: 通知メッセージに Run ID（プロセス起動ごとに一意な識別子）が含まれる
- `AC-20b`: Slack 送信時にメッセージ本文が 4000 文字を超える場合、末尾を切り詰め `...` サフィックスを付与する
- `AC-20c`: Slack 送信時に個別フィールド値が 1000 文字を超える場合、同様に切り詰めを行う
- `AC-20b2`: ファイルログ（Debug Logger 経由）への出力には切り詰めを適用せず、通知イベントの全内容を記録する
- `AC-20d`: TLS failure 集約メッセージはタイトル行に影響組織数（N）を含む
- `AC-20e`: WARN レベルの集約メッセージは attachment color `warning`（橙）と絵文字 ⚠️ を使用する
- `AC-20f`: ERROR レベルの通知は attachment color `danger`（赤）と絵文字 🚨 を使用する
- `AC-20g`: INFO レベルの通知（週次サマリ）は attachment color `good`（緑）と絵文字 ✅ を使用する
- `AC-20h`: 各通知エントリのフィールド情報を Slack attachment の `fields` として構造化して送信する

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

- `AC-21`: いずれかの Webhook URL のスキームが `https` でない場合（`http` など）は設定エラーを返す
- `AC-22`: いずれかの Webhook URL のホスト名が `allowed_host` と一致しない場合は設定エラーを返す
- `AC-23`: 正常時 URL と異常時 URL のホスト名が異なる場合は設定エラーを返す（両 URL は同一ホストを前提とする）
- `AC-24`: ホスト名の照合はポート番号を除いた形で行い、大文字/小文字を区別しない完全一致とする
  - 例: `https://hooks.slack.com:443/...` のホスト名は `hooks.slack.com` として照合する
- `AC-25`: `allowed_host` が未設定の場合、いずれかの環境変数が設定されていれば設定エラーを返す
- `AC-26`: 両環境変数が未設定の場合は `allowed_host` の値によらず検証をスキップする（Slack 無効）
- `AC-26a`: TOML 設定ファイルの `notify.slack` セクションに定義されていないキーが存在する場合（例：誤って `webhook_url` を記入した場合）、設定デコード時に unknown-key エラーとして起動を中断する。このエラーは `internal/config` の strict デコード機構（未知キーを拒否）によって実現し、`internal/notify` 側での追加検知は行わない

**`allowed_host` の有効な値**:

| TOML の値 | 結果 |
|---|---|
| `"hooks.slack.com"` | OK |
| `""` （未設定） | OK（Slack 無効化） |
| `"hooks.slack.com:443"` | 設定エラー（ポート番号不可） |
| `" hooks.slack.com "` | 設定エラー（前後空白不可） |
| `"https://hooks.slack.com"` | 設定エラー（スキーム不可） |

### F-007: HTTP リトライ・タイムアウト

Slack Webhook への HTTP 送信失敗時の挙動を一貫させ、過渡的な失敗（タイムアウト・サーバエラー・レート制限）でアラートを失わないようにする。

**リトライ設定**:

| 項目 | 値 |
|---|---|
| HTTP タイムアウト | 5 秒 |
| リトライ回数 | 3 回（初回送信 + 3 回再試行 = 最大 4 回送信） |
| バックオフ方式 | 指数バックオフ（基準値 2 秒、第 n 回再試行で `2s × 2^(n-1)` 待機） |
| リトライ対象 | HTTP 5xx、HTTP 429（レートリミット）、HTTP リクエスト発行失敗（タイムアウト・コネクションエラー等） |
| リトライ非対象 | HTTP 4xx（429 を除く） |

**受け入れ条件（Acceptance Criteria）**:

- `AC-27`: HTTP リクエストのタイムアウトは 5 秒とする
- `AC-28`: HTTP 5xx または 429 が返された場合、指数バックオフ（基準値 2 秒）で最大 3 回リトライする
- `AC-29`: HTTP リクエスト自体の発行に失敗した場合（タイムアウト含む）もリトライ対象とする
- `AC-30`: HTTP 4xx（429 を除く）が返された場合は即時にエラーを返し、リトライしない
- `AC-31`: リトライをすべて使い切っても成功しない場合は、最終的なエラーを返す
- `AC-32`: リトライ待機中に `context.Context` がキャンセルされた場合、その時点で中断しコンテキストのエラーを返す

### F-008: 二段階起動フロー

`allowed_host` は TOML 設定に依存するため、Slack ハンドラの初期化は TOML 読み込み完了後に行う必要がある。これにより、TOML 由来のポリシーが Slack ハンドラ生成時に必ず適用されることを保証する。

**起動フロー**:

```
Phase 1（TOML 読み込み前）:
  - 標準出力ハンドラ / ファイルハンドラのみ初期化
  - Slack ハンドラは初期化しない
  - 環境変数の存在チェック・組み合わせ妥当性検証（`F-002` `AC-08` 等）は実施可

TOML 読み込み:
  - allowed_host を含む TOML 設定を読み込む

Phase 2（TOML 読み込み後）:
  - allowed_host を用いて Webhook URL を検証
  - Slack ハンドラを生成し、既存のロガーに追加する
  - 検証エラーは設定エラーとして起動を中断する
```

**受け入れ条件（Acceptance Criteria）**:

- `AC-33`: TOML 読み込み前のログ初期化は、標準出力・ファイル等のローカル出力先ハンドラに限定される（Slack ハンドラを含まない）
- `AC-34`: TOML 読み込み完了後、`allowed_host` を引数として Slack ハンドラを生成し、既存ロガーに追加する
- `AC-35`: Phase 2 で Slack ハンドラ初期化が失敗（URL 検証エラー等）した場合は、起動を中断し設定エラーとして報告する
- `AC-36`: TOML から読み込んだ `allowed_host` が、Slack ハンドラ生成時の URL 検証まで漏れなく伝播されること

### F-009: Dry-run モード

設定検証・接続性確認のための dry-run モードを提供する。本モードでは Webhook URL の検証は通常どおり実施するが、実際の HTTP リクエストは送信しない。

**受け入れ条件（Acceptance Criteria）**:

- `AC-37`: Slack ハンドラ生成オプションに dry-run フラグを設けられる
- `AC-38`: dry-run モード時は HTTP リクエストを発行せず、送信予定のメッセージをデバッグログ（Debug Logger）に出力する
- `AC-39`: dry-run モード時も Webhook URL の検証（F-006）は通常どおり実施し、検証失敗時は設定エラーを返す
- `AC-40`: dry-run モードは CLI フラグ（例：`--dry-run`）で有効化できる

---

## 4. 非機能要件

### パフォーマンス

- Slack API へのリクエストの HTTP タイムアウトは 5 秒とする（F-007）
- リトライ最大 4 回（初回 + 3 回再試行）と指数バックオフ（基準値 2 秒）により、最悪ケースの同期ブロック時間は約 `5s × 4 + (2s + 4s + 8s) = 34 秒` を上限とする
- 通知は同期処理であり、送信中は呼び出し元がブロックされることを許容する。IMAP ポーリングとの非同期化が必要かどうかはアーキテクチャ設計（タスク 02）で判断する

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

- Webhook URL は `internal/config` の `Config` 構造体には含まれない（環境変数から直接 runtime options へ読み込む）
- Webhook URL を保持する runtime options 構造体（例：`SlackHandlerOptions`）のフィールドは `Secret` 型でラップする
- IMAP パスワード等、`Config` 構造体の機密フィールドも同様に `Secret` 型でラップする
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

- バッファ・Flush モデルのテスト（`AC-05a`/`AC-05b`）:
  - `Handle()` 呼び出しだけでは HTTP リクエストが発行されないこと
  - `Flush()` 呼び出しでバッファ内レコードを 1 メッセージにまとめて送信すること
  - `Flush()` 後はバッファが空になること
  - 空バッファでの `Flush()` が HTTP リクエストなしで `nil` を返すこと
- 集約メッセージのフォーマットテスト（F-005 の各フィールド・影響組織数・Run ID・truncation・color・emoji・attachment fields 含む）:
  - 複数 failure が 1 メッセージに集約されること
  - タイトル行に影響組織数 N が含まれること
  - Slack 送信ペイロードは上限長で切り詰められること（`AC-20b`/`AC-20c`）
  - ファイルログ出力には同じイベントが切り詰めなしで記録されること（`AC-20b2`）
- 送信失敗の観測テスト（`AC-04`/`AC-05`）:
  - 全リトライ失敗時に `Flush()` がエラーを返すこと
  - 失敗詳細が Debug Logger に記録されること
- ログレベルと送信先の振り分けテスト:
  - INFO レベルが success webhook に送信されること
  - WARN レベルが error webhook に送信されること
  - ERROR レベルが error webhook に送信されること
  - CLI ログレベル設定（例：`--log-level=ERROR`）が Slack 通知の振り分けに影響しないこと
- Webhook URL 設定の組み合わせテスト（F-002 の4パターン）
- URL 検証テスト（F-006）:
  - HTTP スキームはエラーになること
  - ホスト不一致はエラーになること
  - 正常時・異常時 URL のホスト名不一致はエラーになること
  - `allowed_host` が未設定かつ URL が設定されている場合はエラーになること
  - 両環境変数未設定の場合は検証をスキップすること（Slack 無効）
  - 大文字/小文字の差異が許容されること（例：`HOOKS.SLACK.COM` ↔ `hooks.slack.com`）
  - ポート番号付き URL のホスト名照合（ポート番号を除いた照合）
  - TOML に未知キー（例：`webhook_url`）が存在した場合、config strict デコードでエラーになること
- HTTP リトライテスト（F-007）:
  - HTTP 5xx 応答時に最大 3 回リトライすること
  - HTTP 429 応答時にもリトライすること
  - HTTP 4xx（429 を除く）応答時にリトライせず即エラーを返すこと
  - 全リトライ失敗後に最終エラーを返すこと
  - `context.Context` のキャンセルでリトライ待機が中断されること
- 二段階起動フローテスト（F-008）:
  - TOML 読み込み前のロガーに Slack ハンドラが含まれないこと
  - TOML 読み込み後、`allowed_host` が Slack ハンドラ生成まで伝播されること
  - Phase 2 での Slack ハンドラ初期化失敗時に起動が中断されること
- Dry-run モードテスト（F-009）:
  - dry-run モード時に HTTP リクエストが発行されないこと
  - dry-run モード時も URL 検証は実施されること
- セキュリティテスト:
  - `Secret` 型のフィールドが通知メッセージに含まれないこと
  - Slack ハンドラがデバッグ出力（`io.Writer` 経由）を受け取らないこと
  - Webhook URL がログに出力されないこと

### 統合テスト

- モック HTTP サーバを使った Slack Webhook 送信テスト（success/error 各ハンドラ）
- モック HTTP サーバを使ったリトライ動作のエンドツーエンドテスト（5xx → 200 で成功復帰、4xx で即停止）
- 起動フロー全体のテスト：TOML 読み込み → Phase 2 で Slack ハンドラ追加 → 通知発行までの一連の流れ
