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

スケジューリング（ポーリング間隔・定期サマリのタイミング）は外部スケジューラー（systemd timer または cron）に委ねる。プログラム自体は起動・処理・終了の one-shot 実行とする。この方針により：

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
- `summary` サブコマンド：定期サマリの生成・送信

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

- **`AC-01`**: `fetch`、`summary`、`reprocess`、`gc` のいずれかのサブコマンドを受け付ける
- **`AC-02`**: サブコマンドを省略または不正な値を指定した場合、使い方を表示してエラー終了する
- **`AC-03`**: `-config <path>` フラグで設定ファイルパスを指定できる（全サブコマンド共通）
- **`AC-04`**: 設定ファイルパスを省略した場合、デフォルトパス（例：`./config.toml`）を使用する
- **`AC-05`**: `fetch` サブコマンドは `--since <duration>` フラグを受け付ける（例：`--since 30d`）
- **`AC-06`**: `--since` は設定ファイルの `imap.fetch_days` を上書きする。指定しない場合は設定値（デフォルト 14 日）を使用する
- **`AC-07`**: `--since` の duration は日単位（`d`）または週単位（`w`）で指定できる（例：`30d`、`4w`）。Go の `time.ParseDuration` は `d`/`w` をサポートしないため、カスタムパーサーを実装する。本パーサーは `fetch --since` / `summary --since` / `gc --before` で共用する
- **`AC-07a`**: `summary` サブコマンドは `--since <duration>` フラグを受け付ける（`AC-07` の共通パーサーを使用）。設定ファイルの集計期間設定を上書きする。`--since` を省略した場合は設定値（TOML キー名はタスク 0060 / `02_architecture.md` で確定、既定値は 7 日を想定）を使用する

### F-002: コンポーネントの初期化

設定ファイルを読み込み、各コンポーネントを初期化する。

**受け入れ条件（Acceptance Criteria）**:

- **`AC-08`**: 設定読み込みに失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
- **`AC-09`**: ストアの初期化に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
- **`AC-10`**: `fetch` サブコマンドで IMAP 接続に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する

### F-003: `fetch` サブコマンドの処理フロー

IMAP サーバーからメールを取得し、レポートを処理・保存する。常に設定期間（`imap.fetch_days`、`--since` で上書き可）内の全メールを対象とし、SEEN フラグとローカルファイルサイズを組み合わせてダウンロード要否を判定する。

**ステップ1: メタ情報取得**

**受け入れ条件（Acceptance Criteria）**:

- **`AC-11`**: 設定期間内の全メールのメタ情報（UID・RFC822.SIZE・SEEN フラグ・Message-ID）および `UIDVALIDITY` を取得する（本文はダウンロードしない）

**ステップ1.5: UIDVALIDITY 変化検出**

`UID` はメールボックス内で `UIDVALIDITY` が同一である間のみ安定であり、変化した場合はサーバー側で UID が再割り当てされている可能性がある。ローカルの `.eml` ファイル名は UID を含むため、UIDVALIDITY が変化すると既存ファイルと UID の対応が無効になり得る。

**受け入れ条件（Acceptance Criteria）**:

- **`AC-11a`**: `internal/store` の `LoadUIDValidity(mailbox)` で前回値を取得し、ステップ1で取得した現在値と比較する
- **`AC-11b`**: 前回値が未保存（初回実行）または現在値と一致する場合、通常のステップ2（整合性チェック）へ進む
- **`AC-11c`**: 前回値と現在値が異なる場合、WARN レベルでログを出力する（Slack 通知あり）。当該 fetch サイクルではローカル `.eml` ファイルとの整合性チェック（ステップ2）をスキップし、対象メールを全件ダウンロード対象とする。同一レポートが再ダウンロード・再保存されても、レポート保存は `report-id` を一意識別子とした UPSERT で行うため整合性は保たれる
- **`AC-11d`**: ステップ1で取得した現在の `UIDVALIDITY` を `internal/store` の `SaveUIDValidity(mailbox, v)` で保存する（次回実行時の比較に使用）

**ステップ2: 整合性チェックとダウンロード対象の選定**

`AC-11b` のケース（UIDVALIDITY 変化なし）のみ本ステップを実行する。各メールについて以下の判定を行い、ダウンロードが必要かどうかを決定する。

| SEEN フラグ | ローカル .eml | サイズ比較 | アクション |
|---|---|---|---|
| UNSEEN | なし | — | ダウンロード対象 |
| UNSEEN | あり | 一致 | スキップ（ダウンロード不要、既存ファイルを処理対象に含める・SEEN マークは付与する） |
| UNSEEN | あり | 不一致 | WARN ログ出力 → ダウンロード対象 |
| SEEN | なし | — | ダウンロード対象（ファイル消失） |
| SEEN | あり | 一致 | スキップ（処理済み） |
| SEEN | あり | 不一致 | WARN ログ出力 → ダウンロード対象 |

**受け入れ条件（Acceptance Criteria）**:

- **`AC-12`**: SEEN かつローカルファイルが存在しサイズが一致するメールはスキップする
- **`AC-13`**: ローカルファイルのサイズが RFC822.SIZE と一致しない場合、WARN レベルでログを出力する（Slack 通知あり）
- **`AC-14`**: WARN ログを出力した場合でも処理は継続する（スキップしない）

**ステップ3: ダウンロードと処理**

**受け入れ条件（Acceptance Criteria）**:

- **`AC-15`**: ダウンロード対象のメールをまとめて取得し、メール原本を `.eml` ファイルに保存する（既存ファイルは原則上書きしないが、サイズ不一致が検出された場合は上書きする）
- **`AC-16`**: 各メールの添付 `.json.gz` をパースする
- **`AC-17`**: UNSEEN だったメールで `failure_session_count > 0` の場合、即時アラートを送信する（WARN レベル）
- **`AC-18`**: レポートをストアに UPSERT する（重複実行しても安全）
- **`AC-19`**: UNSEEN だったメールの処理完了後、SEEN マークを付与する（既に SEEN のメールは変更しない）
- **`AC-20`**: 1 件のメール処理失敗が他のメール処理に影響しない
- **`AC-21`**: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-004: `reprocess` サブコマンドの処理フロー

ローカルに保存した `.eml` ファイルを再処理し、ストアを再構築する。バグ修正後の復元や、開発時のデータ投入に使用する。

**受け入れ条件（Acceptance Criteria）**:

- **`AC-22`**: 指定ディレクトリ以下の `.eml` ファイルをすべて読み込み、TLSRPT レポートをパースする
- **`AC-23`**: パース結果をストアに保存する（UPSERT のため重複実行しても安全）
- **`AC-24`**: `--notify` フラグを指定した場合のみ Slack アラートを送信する（デフォルトは送信しない）
- **`AC-25`**: 1 件の処理失敗は記録してスキップし、残りのファイルの処理を継続する
- **`AC-26`**: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-005: `summary` サブコマンドの処理フロー

ストアに蓄積されたレポートを集計し、定期サマリを送信する。集計対象期間は `--since` フラグまたは設定ファイルの集計期間設定で指定する（`AC-07a` を参照）。

**受け入れ条件（Acceptance Criteria）**:

- **`AC-27`**: `--since <duration>` または設定値で指定された期間（実行時刻からその期間遡った範囲）のレポートをストアから取得して集計する
- **`AC-28`**: 集計結果を定期サマリとして Slack に送信する（INFO レベル）。送信メッセージには集計対象期間（開始・終了日時）を含める
- **`AC-29`**: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-006: `gc` サブコマンドの処理フロー

ストアに蓄積された古いレポートレコードを削除し、累積件数を抑制する。`.eml` ファイルは対象外（`.eml` の保持期間管理は将来の拡張）。

**受け入れ条件（Acceptance Criteria）**:

- **`AC-30`**: `gc` サブコマンドは `--before <duration>` フラグを受け付ける（日単位 `d` または週単位 `w`、`fetch` の `--since` と同じカスタムパーサーを共用する）
- **`AC-31`**: `--before` を省略した場合、設定ファイルの保持期間設定（TOML キー名と既定値はタスク 0060 / `02_architecture.md` で確定）を使用する。設定値もない場合はエラー終了する
- **`AC-32`**: `internal/store` の `DeleteReportsBefore(time.Now().Add(-before))` を呼び出して該当レコードを削除する
- **`AC-33`**: 削除件数を INFO レベルで構造化ログに出力する（Slack への定期通知は行わない。失敗時のみ ERROR ログ → Slack 通知）
- **`AC-34`**: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

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
Description=tlsrpt-digest periodic summary (one-shot)

[Service]
Type=oneshot
# --since の値（または設定ファイルの集計期間）はタイマーの送信頻度と整合させる。
# 下のタイマー例は毎週月曜のため --since 7d 相当。日次運用なら --since 1d。
ExecStart=/usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml --since 7d
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-summary.timer
[Unit]
Description=Run tlsrpt-digest periodic summary every Monday morning

[Timer]
OnCalendar=Mon 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

```ini
# /etc/systemd/system/tlsrpt-digest-gc.service
[Unit]
Description=tlsrpt-digest record GC (one-shot)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest gc -config /etc/tlsrpt-digest/config.toml --before 30d
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-gc.timer
[Unit]
Description=Run tlsrpt-digest GC daily

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

有効化：

```bash
systemctl enable --now tlsrpt-digest-fetch.timer
systemctl enable --now tlsrpt-digest-summary.timer
systemctl enable --now tlsrpt-digest-gc.timer
```

### 6.2 crontab を使う場合

`/etc/cron.d/tlsrpt-digest` などに記述する。本質的には systemd timer と同等であり、環境変数は別ファイルで管理するか、実行前に source する。

```crontab
# 毎時0分に IMAP メール取得
0 * * * *  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest fetch -config /etc/tlsrpt-digest/config.toml

# 毎週月曜9時に定期サマリ
0 9 * * 1  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml

# 毎日3時に古いレコードを削除（30 日以前）
0 3 * * *  root  . /etc/tlsrpt-digest/secrets.env && /usr/local/bin/tlsrpt-digest gc -config /etc/tlsrpt-digest/config.toml --before 30d
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
  - `--since` 指定時に対応する期間のレポートが取得され集計されること
  - `--since` 未指定 + 設定値ありで設定値が使われること
  - 集計対象期間（開始・終了日時）がメッセージに含まれること
- `reprocess` 処理フローのテスト（`testdata/` の実際の `.eml` を canned データとして使用）
  - `--notify` なしでアラートが送信されないこと
  - 重複実行しても結果が変わらないこと（冪等性）
- `gc` 処理フローのテスト（`FakeStore`・スパイハンドラ を使用）
  - `--before` 指定時に `DeleteReportsBefore(now - before)` が呼ばれること
  - `--before` 未指定 + 設定値ありで設定値が使われること
  - `--before` 未指定 + 設定値なしでエラー終了すること
  - 削除件数が INFO ログに出力されること
  - 重複実行しても結果が変わらないこと（冪等性）
- エラー終了時の終了コードテスト

### 統合テスト

- コマンドライン引数・サブコマンドパースのテスト
- 設定バリデーションエラー時の終了コードテスト
