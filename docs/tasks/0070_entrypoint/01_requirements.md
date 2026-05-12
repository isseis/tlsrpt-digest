# 要件定義書：エントリポイントとサブコマンド

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-12 |
| レビュー日 | 2026-05-12 |
| レビュアー | isseis |
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
2. **副次的目的**: 3つのサブコマンド（`fetch` / `summary` / `reprocess`）で処理を明確に分離する

---

## 2. スコープ

### 対象範囲（In Scope）

- サブコマンドのパース（`fetch` / `summary` / `reprocess`）
- コマンドライン引数のパース（設定ファイルパスの指定）
- 設定ファイルの読み込みと各コンポーネントの初期化
- `fetch` サブコマンド：IMAP ポーリング・即時アラート送信・ストア保存の1サイクル実行
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

1. `fetch`、`summary`、`reprocess` のいずれかのサブコマンドを受け付ける
2. サブコマンドを省略または不正な値を指定した場合、使い方を表示してエラー終了する
3. `-config <path>` フラグで設定ファイルパスを指定できる（全サブコマンド共通）
4. 設定ファイルパスを省略した場合、デフォルトパス（例：`./config.toml`）を使用する
5. `fetch` サブコマンドは `--since <duration>` フラグを受け付ける（例：`--since 30d`）
6. `--since` は設定ファイルの `imap.fetch_days` を上書きする。指定しない場合は設定値（デフォルト 14 日）を使用する
7. `--since` の duration は日単位（`d`）または週単位（`w`）で指定できる（例：`30d`、`4w`）。Go の `time.ParseDuration` は `d`/`w` をサポートしないため、カスタムパーサーを実装する

### F-002: コンポーネントの初期化

設定ファイルを読み込み、各コンポーネントを初期化する。

**受け入れ条件（Acceptance Criteria）**:

1. 設定読み込みに失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
2. ストアの初期化に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
3. `fetch` サブコマンドで IMAP 接続に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する

### F-003: `fetch` サブコマンドの処理フロー

IMAP サーバーからメールを取得し、レポートを処理・保存する。常に設定期間（`imap.fetch_days`、`--since` で上書き可）内の全メールを対象とし、SEEN フラグとローカルファイルサイズを組み合わせてダウンロード要否を判定する。

**ステップ1: メタ情報取得**

**受け入れ条件（Acceptance Criteria）**:

1. 設定期間内の全メールのメタ情報（UID・RFC822.SIZE・SEEN フラグ・Message-ID）を取得する（本文はダウンロードしない）

**ステップ2: 整合性チェックとダウンロード対象の選定**

各メールについて以下の判定を行い、ダウンロードが必要かどうかを決定する。

| SEEN フラグ | ローカル .eml | サイズ比較 | アクション |
|---|---|---|---|
| UNSEEN | なし | — | ダウンロード対象 |
| UNSEEN | あり | 一致 | スキップ（ダウンロード不要、既存ファイルを処理対象に含める・SEEN マークは付与する） |
| UNSEEN | あり | 不一致 | WARN ログ出力 → ダウンロード対象 |
| SEEN | なし | — | ダウンロード対象（ファイル消失） |
| SEEN | あり | 一致 | スキップ（処理済み） |
| SEEN | あり | 不一致 | WARN ログ出力 → ダウンロード対象 |

**受け入れ条件（Acceptance Criteria）**:

2. SEEN かつローカルファイルが存在しサイズが一致するメールはスキップする
3. ローカルファイルのサイズが RFC822.SIZE と一致しない場合、WARN レベルでログを出力する（Slack 通知あり）
4. WARN ログを出力した場合でも処理は継続する（スキップしない）

**ステップ3: ダウンロードと処理**

**受け入れ条件（Acceptance Criteria）**:

5. ダウンロード対象のメールをまとめて取得し、メール原本を `.eml` ファイルに保存する（既存ファイルは原則上書きしないが、サイズ不一致が検出された場合は上書きする）
6. 各メールの添付 `.json.gz` をパースする
7. UNSEEN だったメールで `failure_session_count > 0` の場合、即時アラートを送信する（WARN レベル）
8. レポートをストアに UPSERT する（重複実行しても安全）
9. UNSEEN だったメールの処理完了後、SEEN マークを付与する（既に SEEN のメールは変更しない）
10. 1 件のメール処理失敗が他のメール処理に影響しない
11. 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-004: `reprocess` サブコマンドの処理フロー

ローカルに保存した `.eml` ファイルを再処理し、ストアを再構築する。バグ修正後の復元や、開発時のデータ投入に使用する。

**受け入れ条件（Acceptance Criteria）**:

1. 指定ディレクトリ以下の `.eml` ファイルをすべて読み込み、TLSRPT レポートをパースする
2. パース結果をストアに保存する（UPSERT のため重複実行しても安全）
3. `--notify` フラグを指定した場合のみ Slack アラートを送信する（デフォルトは送信しない）
4. 1 件の処理失敗は記録してスキップし、残りのファイルの処理を継続する
5. 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-005: `summary` サブコマンドの処理フロー

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
- 外部スケジューラーの `Persistent` 設定（後述）により、システム停止中に実行できなかった `fetch` を復旧時に補完できること

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- ログには `log/slog` を使用する
- テストには `stretchr/testify` を使用する

---

## 6. 運用設定例

スケジューリングは systemd timer または crontab のいずれかで設定する。本質的な動作は同じであり、環境に応じて選択する。

### 6.1 systemd timer を使う場合

`fetch` 用と `summary` 用でそれぞれ `.service` / `.timer` ファイルを作成する。

```ini
# /etc/systemd/system/tlsrpt-digest-fetch.service
[Unit]
Description=tlsrpt-digest IMAP fetch (one-shot)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest fetch -config /etc/tlsrpt-digest/config.toml
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-fetch.timer
[Unit]
Description=Run tlsrpt-digest fetch hourly

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
systemctl enable --now tlsrpt-digest-fetch.timer
systemctl enable --now tlsrpt-digest-summary.timer
```

### 6.2 crontab を使う場合

`/etc/cron.d/tlsrpt-digest` などに記述する。本質的には systemd timer と同等であり、環境変数は別ファイルで管理するか、実行前に source する。

```crontab
# 毎時0分に IMAP メール取得
0 * * * *  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest fetch -config /etc/tlsrpt-digest/config.toml

# 毎週月曜9時に週次サマリ
0 9 * * 1  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml
```

---

## 7. テスト方針

### 単体テスト

- `fetch` 処理フローのテスト（`FakeMailFetcher`・スパイハンドラ・`FakeStore` を使用）
  - SEEN + サイズ一致 → スキップされること
  - UNSEEN + ファイルなし → ダウンロード・処理・SEEN マークが行われること
  - UNSEEN + ファイルあり + サイズ一致 → ダウンロードせず既存ファイルを処理・SEEN マークが行われること
  - SEEN + ファイルなし → ダウンロードされること（SEEN マーク変更なし）
  - サイズ不一致 → WARN ログが出力されダウンロード・処理が行われること
  - failure あり / failure なし のアラート分岐テスト
  - 1 件エラー時の継続動作テスト
  - 重複実行しても結果が変わらないこと（冪等性）
- `summary` 処理フローのテスト（`FakeStore`・スパイハンドラ を使用）
- `reprocess` 処理フローのテスト（`testdata/` の実際の `.eml` を canned データとして使用）
  - `--notify` なしでアラートが送信されないこと
  - 重複実行しても結果が変わらないこと（冪等性）
- エラー終了時の終了コードテスト

### 統合テスト

- コマンドライン引数・サブコマンドパースのテスト
- 設定バリデーションエラー時の終了コードテスト
