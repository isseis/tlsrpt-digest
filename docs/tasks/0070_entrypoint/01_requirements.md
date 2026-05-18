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
2. **副次的目的**: 5つのサブコマンド（`fetch` / `summary` / `reprocess` / `gc` / `recover`）で処理を明確に分離する

---

## 2. スコープ

### 対象範囲（In Scope）

- サブコマンドのパース（`fetch` / `summary` / `reprocess` / `gc` / `recover`）
- コマンドライン引数のパース（設定ファイルパスの指定）
- 設定ファイルの読み込みと各コンポーネントの初期化
- `fetch` サブコマンド：IMAP ポーリング・即時アラート送信・ストア保存の1サイクル実行
- `summary` サブコマンド：定期サマリの生成・送信
- `reprocess` サブコマンド：ローカル保存済み `.eml` からストアを再構築（バグ修正後の復元・開発時データ投入）
- `gc` サブコマンド：古いレポートレコードおよびインデックス済み `.eml` ファイルの削除
- `recover` サブコマンド：UIDVALIDITY 変化検出後の手動復旧支援

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

- `AC-01`: `fetch`、`summary`、`reprocess`、`gc`、`recover` のいずれかのサブコマンドを受け付ける
- `AC-02`: サブコマンドを省略または不正な値を指定した場合、使い方を表示してエラー終了する
- `AC-03`: `-config <path>` フラグで設定ファイルパスを指定できる（全サブコマンド共通）
- `AC-04`: 設定ファイルパスを省略した場合、デフォルトパス（例：`./config.toml`）を使用する
- `AC-05`: `fetch` サブコマンドは `--since <duration>` フラグを受け付ける（例：`--since 30d`）
- `AC-06`: `--since` は設定ファイルの `imap.fetch_days` を上書きする。指定しない場合は設定値（デフォルト 14 日）を使用する
- `AC-07`: `--since` の duration は日単位（`d`）または週単位（`w`）で指定できる（例：`30d`、`4w`）。Go の `time.ParseDuration` は `d`/`w` をサポートしないため、カスタムパーサーを実装する。本パーサーは `fetch --since` / `summary --since` / `gc --before` / `gc --max-email-age` で共用する
- `AC-07a`: `summary` サブコマンドは `--since <duration>` フラグを受け付ける（`AC-07` の共通パーサーを使用）。設定ファイルの集計期間設定を上書きする。`--since` を省略した場合は設定値（TOML キー名はタスク 0060 / `02_architecture.md` で確定、既定値は 7 日を想定）を使用する

### F-002: コンポーネントの初期化

設定ファイルを読み込み、各コンポーネントを初期化する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-08`: 設定読み込みに失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
- `AC-09`: ストアの初期化に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
- `AC-10`: `fetch` サブコマンドで IMAP 接続に失敗した場合、エラーメッセージを出力して終了コード 1 で終了する
- `AC-10a`: `fetch`・`gc`・`recover`・`reprocess` の各サブコマンドは以下の順序で初期化を行う：(1) 設定ファイルを読み込む、(2) `{root_dir}` が存在しない場合のみ `os.MkdirAll({root_dir}, 0700)` を実行する（初回実行でロックファイルの親ディレクトリが存在しない問題を回避するための最小限の操作。sentinel 検証や JSON 作成等の完全な初期化はロック取得後に行う）、(3) Slack ハンドラを初期化する（Webhook URL は環境変数から読み取り、TOML からは `notify.slack.allowed_host` のみ参照する。ストアまたはロックには依存しない）、(4) `{root_dir}/.tlsrpt-digest-store.lock` を `0600` で作成し `flock(2)` の排他ロック（`LOCK_EX | LOCK_NB`）で取得する。取得できない場合は前回の実行が完了していないことを示す ERROR レベルのメッセージを出力し（Slack 通知あり）、終了前にハンドラの `Flush()` を呼び出して配送を試行したうえで終了コード 1 で終了する、(5) ストアの完全な初期化（sentinel 検証・JSON 作成等、0040 F-001 参照）を行う
- `AC-10b`: 取得したロックはプロセス終了時に OS によって自動解放される。明示的な unlock 処理は不要
- `AC-10c`: `summary` はストア JSON を読み取るのみであり書き込み×書き込みの競合は発生しないため、ロック取得を行わない

### F-003: `fetch` サブコマンドの処理フロー

IMAP サーバーからメールを取得し、レポートを処理・保存する。常に設定期間（`imap.fetch_days`、`--since` で上書き可）内の全メールを対象とし、SEEN フラグとローカル `.eml` ファイルの有無のみでダウンロード要否を判定する（RFC822.SIZE はダウンロード判定に使用しない）。

**ステップ0: recovery-required 状態確認**

ストア初期化完了後、IMAP 接続およびメタ情報取得の前に recovery-required 状態を確認する。未解決なら fail closed のまま停止し、メール取得は一切開始しない。

**受け入れ条件（Acceptance Criteria）**:

- `AC-10d`: `fetch` サブコマンドはストア初期化直後、IMAP 接続およびステップ1のメタ情報取得の前に `internal/store` の recovery-required 状態を確認する
- `AC-10e`: recovery-required 状態が未解決の場合、`fetch` サブコマンドは IMAP 接続・メールメタ情報取得・整合性チェック・ダウンロード・SEEN 更新を一切行わず、`recover` サブコマンドの実行を案内して終了コード 1 で終了する

**ステップ1: メタ情報取得**

**受け入れ条件（Acceptance Criteria）**:

- `AC-11`: 設定期間内の全メールのメタ情報（UID・RFC822.SIZE・SEEN フラグ・Message-ID・IMAP ENVELOPE の Date）および `UIDVALIDITY` を取得する（本文はダウンロードしない）

**ステップ1.5: UIDVALIDITY 変化検出**

ステップ0で recovery-required 状態が未解決でないことを確認できた場合にのみ、取得済みメタ情報の `UIDVALIDITY` を検証する。`UID` はメールボックス内で `UIDVALIDITY` が同一である間のみ安定であり、変化した場合はサーバー側で UID が再割り当てされている可能性がある。ローカルの `.eml` ファイル名は UID を含むため、UIDVALIDITY が変化すると既存ファイルと UID の対応が無効になり得る。さらに、旧メールボックス由来のレポートがユーザーの意図に反して定期サマリへ継続混入することを防ぐため、UIDVALIDITY 変化時は fail closed とし、オペレータが明示的に復旧を完了するまで自動処理を再開しない。

**受け入れ条件（Acceptance Criteria）**:

- `AC-11a`: `internal/store` の `LoadUIDValidity()` で前回値を取得し、ステップ1で取得した現在値と比較する
- `AC-11b`: 前回値が未保存（初回実行）または現在値と一致する場合、通常のステップ2（整合性チェック）へ進む
- `AC-11c`: 前回値と現在値が異なる場合、ERROR レベルでログを出力する（Slack 通知あり）。期待値・現在値・メールボックス識別子を含む永続的な recovery-required 状態を記録し、当該 fetch サイクルではステップ2・ステップ3へ進まず、終了前にハンドラの `Flush()` を呼び出して配送を試行したうえで終了コード 1 で終了する
- `AC-11d`: `AC-11c` により `recovery-required` 状態が記録された以降の `fetch` サブコマンドは、次回以降もステップ0で停止し、IMAP 接続・メールメタ情報取得・整合性チェック・ダウンロード・SEEN 更新を一切行わず、`recover` サブコマンドの実行を案内して終了コード 1 で終了する

**ステップ2: ダウンロード対象の選定**

`AC-11b` のケース（UIDVALIDITY 変化なし）のみ本ステップを実行する。`.eml` 書き込みはアトミック（0040 F-004 AC-17）なので最終パスに存在するファイルは常に完全であり、ファイルの有無のみでダウンロード要否を判定できる。

| SEEN フラグ | ローカル .eml | アクション |
|---|---|---|
| UNSEEN | なし | ダウンロード対象 |
| UNSEEN | あり | スキップ（既存ファイルを処理対象に含める・SEEN マークは付与する） |
| SEEN | なし | ダウンロード対象（`.eml` 消失。再アラートなし） |
| SEEN | あり | スキップ（処理済み） |

RFC822.SIZE とローカルファイルサイズが一致しない場合は WARN ログを出力するが、ダウンロード判定には影響しない。IMAP サーバーが RFC822.SIZE を不正確に申告する実装（Exchange 等）でも誤った再ダウンロードが発生しないようにする。

**受け入れ条件（Acceptance Criteria）**:

- `AC-12`: SEEN かつローカルファイルが存在するメールはスキップする（サイズ不一致の有無に関わらず）
- `AC-13`: ローカルファイルのサイズが RFC822.SIZE と一致しない場合、WARN レベルでログを出力する（Slack 通知あり）。ダウンロード判定には影響しない
- `AC-14`: WARN ログを出力した場合でも処理は継続する

**ステップ3: ダウンロードと処理**

**受け入れ条件（Acceptance Criteria）**:

- `AC-15`: ダウンロード対象のメールをまとめて取得し、メール原本を `.eml` ファイルにアトミックに保存する。同一 UID かつ同一 UIDVALIDITY のファイルが既に存在する場合は上書きしない（0040 F-004 AC-18 の冪等動作）。全 `.eml` 保存後に `SaveEmailMetas`（0040 F-002 AC-08c）を 1 回呼び、ステップ1で取得したメタ情報のうち **ローカルに `.eml` が存在するすべての UID**（今回ダウンロードしたものと、既に存在していたもの両方）のインデックスエントリ `{uid, uidvalidity, sent_at, saved_at}` をバッチ登録する。`sent_at` は IMAP ENVELOPE の Date フィールド（RFC 2822 Date ヘッダー由来の送信日時）を使用する。`Date:` が欠損または parse 不能な場合は `saved_at` を代用し WARN ログを出力する（0040 F-004 AC-16、AC-08c 参照）。既登録エントリは変更されない（0040 AC-08d の冪等動作）ため、過去サイクルで `SaveEmailMetas` 前にクラッシュした孤立 `.eml` も自動的にインデックスへ救済される
- `AC-15a`: `AC-15` の「ローカル `.eml` 存在確認」は、ステップ1で取得したメタ情報とローカルファイルシステムのみを使って実行する（追加の IMAP ネットワーク取得は行わない）。実行方式は逐次でも並列でもよいが、実装は無制限並列を避け、上限付きワーカー等で I/O 並列度を制御できること
- `AC-16`: 各メールの添付 `.json.gz` をパースする
- `AC-16a`: パースに失敗した場合、WARN レベルでログを出力し Slack 通知する（タスク 0030 の最低重度分類表より。IMAP フィルタ設定ミスやメールボックス汚染の可能性はあるが処理全体の停止を要する障害ではない）。`.eml` はディスクに残し手動確認対象とする。当該メールのレポート保存（`AC-17`～`AC-18`）はスキップするが、SEEN マーク（`AC-19`）は付与する。SEEN を付与することで次回 `fetch` での再通知を防ぐ（at-least-once 保証は `.eml` を保持することで確保する）
- `AC-17`: UNSEEN だったメールで `failure_session_count > 0` の場合、即時アラートを送信する（WARN レベル）
- `AC-18`: レポートをストアにバッチ UPSERT する（0040 F-002 AC-08a）。このとき、対応するメールインデックスエントリの `report_end_date` を更新する（0040 F-002 AC-08b）。パース失敗メールは `report_end_date` が null のまま残るが、`saved_at` による最大保持期間（`--max-email-age`）で強制削除される
- `AC-18a`: 全メール処理（AC-16〜AC-18）が完了した後、Slack バッファの `Flush()` を呼び出す（0030 `SlackHandler.Flush(ctx)` に相当）。Flush が成功した場合のみ AC-19 の SEEN 付与へ進む。Flush が失敗した場合は SEEN を付与せず終了コード 1 で終了する。この順序により、**通知は必ず SEEN 付与より前に配送が確認され**、クラッシュや Flush 失敗時は次回 `fetch` で再処理・再通知される（at-least-once 保証の実装上の要）
- `AC-19`: UNSEEN だったメールの処理完了後（AC-18a の Flush 成功後）、SEEN マークを付与する（既に SEEN のメールは変更しない）
- `AC-20`: 1 件のメール処理失敗が他のメール処理に影響しない
- `AC-20a`: ステップ3の全メール処理が正常に完了した後、ステップ1で取得した現在の `UIDVALIDITY` を `internal/store` の `SaveUIDValidity(v)` で sentinel に保存する（次回実行時の比較に使用）。正常完了前に保存しないことで、未解決の UIDVALIDITY 変化が以後の `fetch` / `summary` で確実に検出される
- `AC-21`: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-004: `reprocess` サブコマンドの処理フロー

ローカルに保存した `.eml` ファイルを再処理し、ストアを再構築する。バグ修正後の復元や、開発時のデータ投入に使用する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-21a`: `reprocess` はストア初期化完了後、`.eml` 読み込みの前に `internal/store` の `LoadRecoveryRequired()`（0040 F-008 AC-34）で recovery-required 状態を確認する。`found = true` の場合は処理を一切行わず、`recover` サブコマンドの実行を案内して終了コード 1 で終了する（未解決の UIDVALIDITY 変化下で store への書き込みを行うと、旧 epoch のデータと新 epoch のデータが混在する可能性があるため、保守的に停止する）
- `AC-22`: `LoadEmails`（0040 F-005）でストアの `{root_dir}/emails/` 以下の `.eml` ファイルを再帰的に列挙・読み込み、TLSRPT レポートをパースする（LoadEmails は常に `{root_dir}/emails/` 起点で動作する）
- `AC-23`: パース成功したレポートをバッチ保存メソッドで UPSERT する（0040 F-002 AC-08a）。同時に、`AC-23a` で先に登録済みの対応メールインデックスエントリの `report_end_date` も更新される（0040 AC-08b）
- `AC-23a`: `LoadEmails` で得た全エントリの `{uid, uidvalidity, sent_at, saved_at}` を `SaveEmailMetas` でバッチ登録する（0040 AC-08c）。`reprocess` ではこの登録を `AC-23`（レポート UPSERT）より前に実行し、未登録エントリでも `report_end_date` を更新可能にする。既登録エントリは変更されず（0040 AC-08d）、過去 fetch サイクルで未登録だった孤立 `.eml` を救済する効果も持つ
- `AC-24`: `--notify` フラグを指定した場合のみ Slack アラートを送信する（デフォルトは送信しない）。パース失敗時は AC-25 でスキップ・記録するのみで、通常 Slack 通知は行わない（`--notify` 指定時のみ通知する）
- `AC-24a`: `--notify` を指定した場合、全ファイル処理完了後に Slack バッファの `Flush()` を呼び出す。Flush が失敗した場合は終了コード 1 で終了する。reprocess は SEEN フラグを使わないため、Flush 失敗時の自動再送は保証されない（at-least-once が不要な手動操作であるため許容）
- `AC-25`: 1 件の処理失敗（読み込み失敗・パース失敗・保存失敗いずれも）は記録してスキップし、残りのファイルの処理を継続する
- `AC-26`: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-005: `summary` サブコマンドの処理フロー

ストアに蓄積されたレポートを集計し、定期サマリを送信する。集計対象期間は `--since` フラグまたは設定ファイルの集計期間設定で指定する（`AC-07a` を参照）。

**受け入れ条件（Acceptance Criteria）**:

- `AC-27`: `--since <duration>` または設定値で指定された期間（実行時刻からその期間遡った範囲）のレポートをストアから取得して集計する
- `AC-27a`: UIDVALIDITY 変化に起因する recovery-required 状態が未解決の場合、`summary` サブコマンドは集計・送信を行わず、復旧手順または `recover` サブコマンドの実行を案内して終了コード 1 で終了する
- `AC-28`: 集計結果を定期サマリとして Slack に送信する（INFO レベル）。送信メッセージには集計対象期間（開始・終了日時）を含める
- `AC-29`: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-006: `gc` サブコマンドの処理フロー

ストアに蓄積された古いレポートレコードおよび `.eml` ファイルを削除し、ストレージの累積を抑制する。JSON レポートレコードと `.eml` ファイル（メールインデックス経由）の両方が GC 対象である。

**受け入れ条件（Acceptance Criteria）**:

- `AC-29a`: `gc` はストア初期化完了後、削除処理の前に recovery-required 状態を確認する。未解決の場合は処理を一切行わず、`recover` サブコマンドの実行を案内して終了コード 1 で終了する（未解決の UIDVALIDITY 変化下で旧 epoch のデータを `gc` で削除すると `recover --mode keep-old` での復旧手段が失われる可能性があるため、保守的に停止する）
- `AC-30`: `gc` サブコマンドは `--before <duration>` フラグを受け付ける（日単位 `d` または週単位 `w`、`fetch` の `--since` と同じカスタムパーサーを共用する）
- `AC-31`: `--before` を省略した場合、設定ファイルの保持期間設定（設定キー名は仮称。タスク 0060 / `02_architecture.md` で確定。現在の仮称: `store.retention_days`、デフォルトあり）を使用する（タスク 0060 AC-16 参照）
- `AC-32`: `internal/store` の `DeleteReportsBefore(time.Now().Add(-before))` を呼び出して JSON レポートレコードを削除する
- `AC-32a`: `gc` サブコマンドは `--max-email-age <duration>` フラグを受け付ける（日/週単位、`--before` と同じパーサを共用）。省略した場合は設定ファイルのメール最大保持期間設定（設定キー名は仮称。タスク 0060 / `02_architecture.md` で確定。現在の仮称: `store.max_email_age_days`、デフォルトあり、0060 AC-17 参照）を使用する
- `AC-32b`: `internal/store` の `DeleteEmailsBefore(reportCutoff, savedAtCutoff)` を呼び出してメールインデックスおよび対応する `.eml` ファイルを削除する。`reportCutoff` は `time.Now().Add(-before)`、`savedAtCutoff` は解決済みの maxEmailAge（CLI フラグまたは設定値）を用いて `time.Now().Add(-maxEmailAge)` として計算する。通常の設定では maxEmailAge は 1 日以上（0060 AC-10a）なので savedAtCutoff は非ゼロとなるが、store API は ゼロ値（`time.Time{}`）を受け入れた場合は saved_at ベースの強制削除を無効化するセマンティクスを持つ（0040 AC-29 参照）
- `AC-33`: JSON レコードおよび `.eml` ファイルそれぞれの削除件数を INFO レベルで構造化ログに出力する（Slack への定期通知は行わない。失敗時のみ ERROR ログ → Slack 通知）
- `AC-34`: 正常終了の場合は終了コード 0、エラー終了の場合は終了コード 1 で終了する

### F-007: `recover` サブコマンドの処理フロー

UIDVALIDITY 変化検出後に fail closed で停止した状態から、オペレータが明示的に復旧方針を選択して再開できるようにする。

**受け入れ条件（Acceptance Criteria）**:

- `AC-35`: `recover` サブコマンドは `--mode <keep-old|discard-old>` フラグを受け付ける
- `AC-36`: `recover` は未解決の recovery-required 状態を読み取り、前回 UIDVALIDITY・現在 UIDVALIDITY・対象メールボックス・ローカルデータパスを表示する
- `AC-37`: `recover --mode keep-old` は既存のレポートデータと `.eml` を保持したまま、保存済み UIDVALIDITY を recovery-required 状態に記録された現在値へ更新し、recovery-required 状態を解消する。その後の `fetch` / `summary` は通常運転へ戻る。このモードでは旧 UIDVALIDITY エポック由来の既存レポートも保持されるため、期間条件に一致する限り `summary` 集計に含まれ得ることを明示し、実行前にオペレータが挙動を確認できるようにする
- `AC-38`: `recover --mode discard-old --yes` は以下の変更を行う：
  - `tlsrpt.json` を空のレコードセットで再作成する
  - `{root_dir}/emails/` 配下を再帰削除し、空のディレクトリとして再作成する
  - sentinel ファイル（`.tlsrpt-digest-meta.json`）は削除せずフィールド更新のみ行う：`imap_host`・`imap_port`・`imap_mailbox` と `initialized_at` は変更しない（ディレクトリ初回稼働日時のトレーサビリティを保持）、`uid_validity` を recovery-required 状態に記録された現在値に更新し、`recovery_required` フィールドを除去する
  - これにより当該ディレクトリの「いつから運用しているか」「どのメールボックスに紐付いているか」の履歴が失われない
- `AC-39`: `recover --mode discard-old` は `--yes` が指定されない限り破壊的変更を行わず、実行予定内容を表示して終了コード 1 で終了する
- `AC-40`: recovery-required 状態が存在しない場合、`recover` は変更を行わず説明付きで終了コード 1 で終了する
- `AC-41`: `recover` 実行中に失敗した場合、中途半端な状態を残さない。破壊的変更を伴う場合もアトミックに近い手順で行い、失敗時は recovery-required 状態を維持する

---

## 4. 非機能要件

### 保守性

- 構造化ログ（`log/slog`）を使用してログを出力する
- ログレベルは INFO（通常動作）、WARN（即時アラート・回復可能なエラー）、ERROR（処理失敗）を使い分ける

### 信頼性

- 1 件のメール処理失敗が他のメール処理に影響しないこと
- 外部スケジューラーの `Persistent` 設定（後述）により、システム停止中に実行できなかった `fetch` を復旧時に補完できること

#### 通知のデリバリー保証（at-least-once）

Slack への通知は **at-least-once** を保証する。見逃し（miss）は発生しないが、重複（duplicate）は発生し得る。

IMAP の SEEN フラグが通知完了状態のマーカーとして機能する。`internal/notify` の Slack ハンドラはメッセージをバッファリングし、`Flush()` 呼び出し時に一括送信する。at-least-once 保証を維持するには **Flush() の成功を SEEN 付与より前に確認する**必要がある。ステップ3の実装上の順序は以下とする：

1. 各メールを処理し通知メッセージをバッファに追加する（AC-17 等）
2. `Flush()` を呼び出して Slack へ送信する。Flush に失敗した場合は SEEN を付与せずエラー終了する（終了コード 1）
3. SEEN マークを付与する（AC-19）。この時点で SEEN 付与後クラッシュしても通知は送信済みである

| クラッシュタイミング | 次回実行の結果 |
|---|---|
| `.eml` 保存後・Flush() 完了前 | UNSEEN + ローカルファイルありとして再処理 → Flush を含む全ステップを再実行 → 通知が送られる（重複の可能性あり） |
| Flush() 完了後・SEEN 付与前 | 同上 → 次回 Flush でも通知が送られる（重複） |
| SEEN 付与後 | SEEN + ファイルありとしてスキップ → 再通知しない（矛盾なし） |

上記より、通知が必要なメールは最終的に必ず Slack へ送信される。ただし Flush 失敗後の再実行では重複通知が発生し得る。重複通知への対処はユーザー向けドキュメントで説明する。

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- ログには `log/slog` を使用する
- テストには `stretchr/testify` を使用する

---

## 6. 運用設定例

スケジューリングは systemd timer または crontab のいずれかで設定する。本質的な動作は同じであり、環境に応じて選択する。

### 6.1 systemd timer を使う場合

`fetch` 用・`summary` 用・`gc` 用でそれぞれ `.service` / `.timer` ファイルを作成する。

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

> **多重起動について**: `fetch` は起動時にストアディレクトリを `flock(2)` で排他ロックするため、前回の実行が長引いてタイマーが再発火した場合は後発プロセスが ERROR レベルのメッセージを出力し（Slack 通知あり。Slack ハンドラはロック取得前に初期化されるため通知可能）、終了コード 1 で終了する（`AC-10a`）。これにより想定以上に処理が長引いていることをオペレータが把握できる。systemd の場合、`OnFailure=` ディレクティブで追加通知を設定することも可能。

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

### 6.3 UIDVALIDITY 変化時の手動復旧

UIDVALIDITY 変化を検出した場合、`fetch` と `summary` は fail closed で停止する。復旧はオペレータが明示的に方針を選んで実施する。

旧データも保持して運転を再開する場合:

```bash
/usr/local/bin/tlsrpt-digest recover -config /etc/tlsrpt-digest/config.toml --mode keep-old
```

旧データを破棄して空状態から再開する場合:

```bash
/usr/local/bin/tlsrpt-digest recover -config /etc/tlsrpt-digest/config.toml --mode discard-old --yes
```

`discard-old` はローカルのレポートデータと `.eml` を削除するため、必要に応じて事前にバックアップを取得すること。

---

## 7. テスト方針

### 単体テスト

- `fetch` 処理フローのテスト（`FakeMailFetcher`・スパイハンドラ・`FakeStore` を使用）
  - SEEN + サイズ一致 → スキップされること
  - UNSEEN + ファイルなし → ダウンロード・処理・SEEN マークが行われること
  - UNSEEN + ファイルあり + サイズ一致 → ダウンロードせず既存ファイルを処理・SEEN マークが行われること
  - SEEN + ファイルなし → ダウンロードされること（SEEN マーク変更なし）
  - サイズ不一致 → WARN ログが出力されるが、ダウンロード判定には影響しないこと（SEEN + ファイルありの場合はスキップされること）
  - failure あり / failure なし のアラート分岐テスト
  - UIDVALIDITY 不一致検出時に recovery-required 状態が記録され、メール処理を行わず終了すること
  - recovery-required 状態が残っている間は `fetch` が即座に停止すること
  - 別プロセスが実行中の場合（ロック取得失敗）に ERROR レベルのメッセージを出力し（Slack 通知あり）終了コード 1 で終了すること
  - 1 件エラー時の継続動作テスト
  - 重複実行しても結果が変わらないこと（冪等性）
- `summary` 処理フローのテスト（`FakeStore`・スパイハンドラ を使用）
  - `--since` 指定時に対応する期間のレポートが取得され集計されること
  - `--since` 未指定 + 設定値ありで設定値が使われること
  - recovery-required 状態が残っている間は集計・送信せず終了すること
  - 集計対象期間（開始・終了日時）がメッセージに含まれること
- `reprocess` 処理フローのテスト（`testdata/` の実際の `.eml` を canned データとして使用）
  - `--notify` なしでアラートが送信されないこと
  - 重複実行しても結果が変わらないこと（冪等性）
- `gc` 処理フローのテスト（`FakeStore`・スパイハンドラ を使用）
  - `--before` 指定時に `DeleteReportsBefore` が正しいカットオフ時刻で呼ばれること
  - `--before` 未指定で設定値（デフォルト）が使われること
  - `--max-email-age` 指定時に `DeleteEmailsBefore(reportCutoff, savedAtCutoff)` が正しいカットオフ時刻で呼ばれること
  - `--max-email-age` 未指定時に設定値（デフォルト）が `savedAtCutoff` として解決され `DeleteEmailsBefore` に渡されること（AC-32a/32b より、設定値は常に 1 日以上のためゼロ値にならない）
  - JSON レコードおよび `.eml` それぞれの削除件数が INFO ログに出力されること
  - 重複実行しても結果が変わらないこと（冪等性）
- `recover` 処理フローのテスト
  - `keep-old` で UIDVALIDITY が更新され、既存レポートと `.eml` が保持されること
  - `discard-old --yes` でローカルデータが削除・再初期化されること
  - `discard-old` が `--yes` なしでは破壊的変更を行わないこと
  - recovery-required 状態が存在しない場合は変更せず終了すること
- エラー終了時の終了コードテスト

### 統合テスト

- コマンドライン引数・サブコマンドパースのテスト
- 設定バリデーションエラー時の終了コードテスト
