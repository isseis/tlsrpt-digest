# 要件定義書：エントリポイントとサブコマンド

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

各パッケージ（imap、tlsrpt、notify、store）が実装されたのち、それらを組み合わせて動作させるエントリポイントが必要である。このタスクでは `cmd/tlsrpt-digest` を実装し、アプリケーション全体の制御フローを担う。

スケジューリング（ポーリング間隔・週次サマリのタイミング）は外部スケジューラー（systemd timer または cron）に委ねる。プログラム自体は起動・処理・終了の one-shot 実行とする。この方針により：

- プログラムの実装をシンプルに保てる（内部ループ・タイマー管理が不要）
- 実行タイミングの管理が OS 標準の仕組みに集約される
- 障害時の再試行・ログ管理が systemd の標準機能で対応できる

### 1.2 目的

1. **主目的**: 設定を読み込み、各コンポーネントを初期化して1回の処理を実行して終了する
2. **副次的目的**: 2つのサブコマンド（`poll` / `summary`）で処理を明確に分離する

---

## 2. スコープ

### 対象範囲（In Scope）

- サブコマンドのパース（`poll` / `summary`）
- コマンドライン引数のパース（設定ファイルパスの指定）
- 設定ファイルの読み込みと各コンポーネントの初期化
- `poll` サブコマンド：IMAP ポーリング・即時アラート送信・ストア保存の1サイクル実行
- `summary` サブコマンド：週次サマリの生成・送信

### 対象外（Out of Scope）

- ポーリングループ・内部タイマー（外部スケジューラーが担う）
- graceful shutdown（one-shot 実行のため不要）
- デーモン化・systemd サービス管理
- 各コンポーネントの詳細実装（imap、tlsrpt、notify、store の各タスクで担当）

### 影響を受けるコンポーネント

- **直接変更**: `cmd/tlsrpt-digest/main.go`（新規または拡張）
- **間接的影響**: すべての `internal/` パッケージ（依存先）

---

## 3. 機能要件

### F-001: サブコマンドとコマンドライン引数

起動時のサブコマンドおよびコマンドライン引数を処理する。

**受け入れ条件（Acceptance Criteria）**:

1. `poll` または `summary` のいずれかのサブコマンドを受け付ける
2. サブコマンドを省略または不正な値を指定した場合、使い方を表示してエラー終了する
3. `-config <path>` フラグで設定ファイルパスを指定できる（`poll` / `summary` 共通）
4. 設定ファイルパスを省略した場合、デフォルトパス（例：`./config.toml`）を使用する

### F-002: コンポーネントの初期化

設定ファイルを読み込み、各コンポーネントを初期化する。

**受け入れ条件（Acceptance Criteria）**:

1. 設定読み込みに失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
2. ストアの初期化に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
3. `poll` サブコマンドで IMAP 接続に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する

### F-003: `poll` サブコマンドの処理フロー

IMAP サーバーから未読メールを取得し、レポートを処理・保存する。

**受け入れ条件（Acceptance Criteria）**:

1. 未読メールを取得し、各メールの添付 `.json.gz` をパースする
2. `failure_session_count > 0` のレポートは即時アラートを送信する（WARN レベル）
3. レポートをストアに保存する（failure の有無に関わらず保存する）
4. 処理完了後、メールを既読にマークする
5. 1 件のメール処理失敗が他のメール処理に影響しない
6. 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-004: `summary` サブコマンドの処理フロー

ストアに蓄積されたレポートを集計し、週次サマリを送信する。

**受け入れ条件（Acceptance Criteria）**:

1. ストアから過去 7 日分のレポートを取得して集計する
2. 集計結果を週次サマリとして Slack に送信する（INFO レベル）
3. 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

---

## 4. 非機能要件

### 保守性

- 構造化ログ（`log/slog`）を使用してログを出力する
- ログレベルは INFO（通常動作）、WARN（即時アラート・回復可能なエラー）、ERROR（処理失敗）を使い分ける

### 信頼性

- 1 件のメール処理失敗が他のメール処理に影響しないこと
- 外部スケジューラーの `Persistent` 設定（後述）により、システム停止中に実行できなかった `poll` を復旧時に補完できること

---

## 5. 制約

- 使用言語は Go とする（Go 1.23 以上）
- ログには `log/slog` を使用する
- テストには `stretchr/testify` を使用する

---

## 6. 運用設定例

スケジューリングは systemd timer または crontab のいずれかで設定する。本質的な動作は同じであり、環境に応じて選択する。

### 6.1 systemd timer を使う場合

`poll` 用と `summary` 用でそれぞれ `.service` / `.timer` ファイルを作成する。

```ini
# /etc/systemd/system/tlsrpt-digest-poll.service
[Unit]
Description=tlsrpt-digest IMAP poll (one-shot)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest poll -config /etc/tlsrpt-digest/config.toml
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-poll.timer
[Unit]
Description=Run tlsrpt-digest poll hourly

[Timer]
OnCalendar=hourly
Persistent=true   # システム停止中の実行分を復旧時に補完する

[Install]
WantedBy=timers.target
```

```ini
# /etc/systemd/system/tlsrpt-digest-summary.service
[Unit]
Description=tlsrpt-digest weekly summary (one-shot)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-summary.timer
[Unit]
Description=Run tlsrpt-digest weekly summary every Monday morning

[Timer]
OnCalendar=Mon 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

有効化：

```bash
systemctl enable --now tlsrpt-digest-poll.timer
systemctl enable --now tlsrpt-digest-summary.timer
```

### 6.2 crontab を使う場合

`/etc/cron.d/tlsrpt-digest` などに記述する。本質的には systemd timer と同等であり、環境変数は別ファイルで管理するか、実行前に source する。

```crontab
# 毎時0分に IMAP ポーリング
0 * * * *  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest poll -config /etc/tlsrpt-digest/config.toml

# 毎週月曜9時に週次サマリ
0 9 * * 1  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml
```

---

## 7. テスト方針

### 単体テスト

- `poll` 処理フローのテスト（`FakeMailFetcher`・`SpyNotifier`・`FakeStore` を使用）
  - failure あり / failure なし の分岐テスト
  - 1 件エラー時の継続動作テスト
- `summary` 処理フローのテスト（`FakeStore`・`SpyNotifier` を使用）
- エラー終了時の終了コードテスト

### 統合テスト

- コマンドライン引数・サブコマンドパースのテスト
- 設定バリデーションエラー時の終了コードテスト
