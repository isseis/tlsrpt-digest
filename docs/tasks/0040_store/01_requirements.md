# 要件定義書：レポートデータの永続化

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

failure のないレポートは即時通知ではなく、定期サマリとしてまとめて通知する（送信間隔は呼び出し側で指定可能、デフォルトは 7 日。タスク 0030 / 0050 を参照）。
そのためにレポートデータを永続化する仕組みが必要である。
このタスクでは `internal/store` パッケージを実装し、以下の2種類のデータ永続化を担う。

1. **メール本文の保存**（`.eml` ファイル）: トラブル時の問題解析・再処理、および単体テスト用 canned データとして活用できる
2. **集計データの保存**（JSON ファイル）: 定期サマリ生成のためのレポートフィールドを蓄積する

### 1.2 目的

1. **主目的**: 処理済み TLSRPT レポートデータを JSON ファイルに保存する
2. **副次的目的**: 受信メール原本を `.eml` ファイルとして保存し、再処理・テストを可能にする

---

## 2. スコープ

### 対象範囲（In Scope）

- ストレージルートディレクトリの初期化（sentinel メタファイル作成・検証、サブディレクトリ作成）
- JSON データファイルの初期化（存在しない場合の新規作成）
- レポートデータの保存（`SaveReport`）
- 指定期間のレポート取得（`GetReportsSince`）
- 指定日時より古いレポートレコードの削除（`DeleteReportsBefore`、累積件数の抑制用）
- 指定日時より古い `.eml` ファイルの削除（`DeleteEmailsBefore`、ストレージ抑制用）
- `.eml` ファイルへのメール本文保存（`SaveEmail`）
- `.eml` ファイルからのメール本文読み込み（`LoadEmails`、reprocess 用）

### 対象外（Out of Scope）

- 定期サマリの生成・フォーマット（タスク 0050 で担当）
- レポート削除（GC）の自動スケジューリング（呼び出し元が任意のタイミングで `DeleteReportsBefore` / `DeleteEmailsBefore` を呼ぶ。スケジューラ統合はエントリポイント／運用設定の責務）
- UIDVALIDITY 変化の検出・対応（タスク 0070 エントリポイントで担当。本パッケージは値の永続化と取得のみ提供する。自動 invalidate、summary 停止、手動復旧コマンドは本パッケージの責務外）

### 影響を受けるコンポーネント

- **直接変更**: `internal/store/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（store の利用側）

---

## 3. 機能要件

### F-001: ストレージの初期化

ストレージルートディレクトリ（`root_dir`）を初期化する。`root_dir` 配下のパスはプログラムが自動的に導出する。

- データファイル：`{root_dir}/tlsrpt.json`
- メール保存ディレクトリ：`{root_dir}/emails/`
- sentinel メタファイル：`{root_dir}/.tlsrpt-digest-meta.json`

`root_dir` を単一の設定キーにすることで、データファイルとメール保存ディレクトリが常に同じルートを共有し、片方だけを誤指定することが構造的に不可能になる。sentinel を `root_dir` 直下に置くことで、データファイルとメール保存ディレクトリの両方を単一の identity チェックで保護できる（JSON データファイルへの `imap_identity` 埋め込みは不要）。

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: `root_dir` が存在しない場合、ディレクトリを作成する
- `AC-02`: `root_dir/emails/` が存在しない場合、自動的に作成される
- `AC-03`: `root_dir/tlsrpt.json` が存在しない場合、空のレコードセットで新規作成される
- `AC-04`: 既存の `root_dir` に対して初期化を呼び出しても既存データが失われない
- `AC-05`: `root_dir/.tlsrpt-digest-meta.json`（sentinel、§6.1 参照）が存在しない場合、現設定の IMAP 識別子（host・port・mailbox）と初期化日時を含めてアトミックに新規作成する
- `AC-06`: sentinel が既に存在する場合、現設定の IMAP 識別子と一致するかを検証する。一致しない場合は ERROR 終了する。エラーメッセージには「期待された識別子」「実際の識別子」「`root_dir` のパス」を含める

### F-002: レポートデータの保存

パース済みの TLSRPT レポートデータを JSON ファイルに保存する。

1 fetch サイクルで複数のレポートが得られる場合、個別に保存すると JSON ファイルの全読み書きが繰り返され I/O 効率が悪い。そのため、バッチ保存メソッド（`SaveReports`）を提供し、1 サイクル分のレポートをまとめて 1 回のアトミック書き込みで保存できるようにする。

`SaveReports` はレポートと同時にメールインデックスの `report_end_date` を更新する必要があるが、`tlsrpt.Report` 自体には IMAP の UID / UIDVALIDITY が含まれない。そのため、呼び出し元から `{report, uid, uidvalidity}` のペア情報を受け取る形とする（詳細シグネチャは `02_architecture.md` で確定）。

**受け入れ条件（Acceptance Criteria）**:

- `AC-07`: `tlsrpt.Report` の主要フィールド（`report-id`、組織名、レポート期間、ポリシー種別、success/failure カウント）が保存される
- `AC-08`: 同一レポート（`report-id` が同一）を重複保存しても整合性が保たれる（UPSERT）
- `AC-08a`: バッチ保存メソッドで複数レポートをまとめて保存できる。個別保存（`SaveReport`）と同一の UPSERT セマンティクスを持ち、1 回のアトミック書き込みで完結する
- `AC-08b`: バッチ保存メソッドはレポートの保存と同時に、対応するメールインデックスエントリの `report_end_date` を更新する。`report_end_date` は `tlsrpt.Report.DateRange.EndDatetime` を使用する。レポート保存とインデックス更新は同一のアトミック書き込みに含める
- `AC-08c`: `SaveEmailMetas` でメールインデックスに `{uid, uidvalidity, saved_at}` エントリをまとめて登録できる（バッチ操作・1 回のアトミック書き込み）。fetch サイクルで全 `.eml` を保存した後、1 回だけ呼び出すことで O(N²) の JSON 読み書きを回避する
- `AC-08d`: `SaveEmailMetas` は同一 `{uid, uidvalidity}` の既存エントリには影響しない（`saved_at` の上書きや `report_end_date` のリセットを行わない、冪等動作）。これにより、呼び出し側がローカル `.eml` のある全 UID を毎回渡すことで、過去 fetch サイクルで `SaveEmailMetas` 直前にクラッシュした「インデックス未登録の `.eml`」（孤立ファイル）も次回 fetch で自動的にインデックスに救済される
- `AC-09`: 書き込みはアトミックに行う（一時ファイルへ書き込み後に rename）
- `AC-10`: 保存に失敗した場合はエラーを返す

### F-003: レポートデータの取得

指定期間（呼び出し側が `since time.Time` で指定。典型値は過去 7 日間）のレポートデータを取得する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-11`: 指定した開始日時以降に保存されたレポートをすべて返す
- `AC-12`: 対象レポートが存在しない場合、空のスライスを返す（エラーにしない）
- `AC-13`: 保存後に取得した結果のレポートは、保存前の `tlsrpt.Report` と全フィールドが一致する（ラウンドトリップ保証）

### F-004: メール本文の保存（`.eml`）

IMAP から取得したメール原本を `.eml` 形式でファイルに保存する。

保存タイミングはパース処理より前とする。これにより、パース中にプロセスが停止した場合でもメールがローカルに残り、再処理が可能になる。

ファイル名は `{uidvalidity}/{YYYYMM}/{uid}.eml` の形式とする。IMAP UID は同一 UIDVALIDITY エポック内でメールボックス単位に一意（RFC 3501）であるため、UID だけでファイル名として十分な識別性を持ち、Message-ID をファイル名に含める必要はない。`UIDVALIDITY` をパスに含めることで、エポック変化前後のデータが自動的に別ディレクトリに分離される。月単位のサブディレクトリは古いファイルの手動削除を容易にする目的で維持する。

`.eml` の書き込みはアトミックに行う（一時ファイルへ書き込み後に rename）。これにより、書き込み途中でプロセスが停止した場合でも最終パスには完全なファイルのみが存在し、部分ファイルによる不整合が発生しない。

`SaveEmail` は `.eml` ファイルの書き込みのみを担う。メールインデックスへの `{uid, uidvalidity, saved_at}` エントリ登録は、fetch サイクルで全 `.eml` を保存した後に `SaveEmailMetas`（F-002 AC-08c）を 1 回呼ぶことでまとめて行う。これにより SaveEmail ごとの JSON 全読み書きを避け、O(N) の I/O に抑える。TLSRPT パース成功後にバッチ保存メソッド（F-002 AC-08b）が同エントリの `report_end_date` を更新する。

`report_end_date` の遠未来設定やパース失敗による `.eml` の無制限蓄積を防ぐため、`DeleteEmailsBefore` は `savedAtCutoff time.Time` を別途受け取り、`saved_at < savedAtCutoff` を満たすファイルも削除する（呼び出し元が `time.Now().Add(-maxAge)` を計算して渡す）。

**受け入れ条件（Acceptance Criteria）**:

- `AC-14`: メール1件につき1ファイルが作成され、RFC 2822 形式（`.eml`）で保存される
- `AC-15`: ファイル名は `{uid}.eml` 形式とする（Message-ID 等を付与しない）。UID は IMAP `FETCH` で取得した uint32 値を 10 進文字列として使用し、0 パディングして 10 桁に揃える（例：`0000000123.eml`）。これにより、ファイルシステムでの一覧表示・ソート・手動操作が容易になる
- `AC-16`: パスは `{root_dir}/emails/{uidvalidity}/{YYYYMM}/{uid}.eml` の形式。`{YYYYMM}` はメールの受信日時（IMAP ENVELOPE の Date フィールド）に基づく
- `AC-17`: `.eml` の書き込みは一時ファイルへ書き込み後に rename するアトミック操作で行う
- `AC-18`: 同一 UID かつ同一 UIDVALIDITY の `.eml` が既に存在する場合、ファイルを変更せず、エラーも返さない（冪等動作）
- `AC-19`: 保存に失敗した場合（既存ファイルとの衝突を除く）はエラーを返す

### F-005: メール本文の読み込み（reprocess 用）

指定ディレクトリ以下の `.eml` ファイルを列挙・読み込む。

**受け入れ条件（Acceptance Criteria）**:

- `AC-20`: 指定ディレクトリ以下のすべての `.eml` ファイルを再帰的に列挙できる
- `AC-21`: 各ファイルを読み込み、`*net/mail.Message` として返す（`internal/imap` の `Download` の戻り値型と揃える）
- `AC-22`: 読み込みに失敗したファイルはエラーを記録してスキップし、処理を継続する

### F-006: UIDVALIDITY の永続化

IMAP UID はメールボックス単位で割り当てられ、`UIDVALIDITY` 値が変化したときサーバーが UID を再割り当てしていることを示す（メールボックスの再作成・移行等で発生）。UID を含むローカルファイル名（`.eml`）が誤ったメールに対応しないよう、`UIDVALIDITY` を追跡できるようにする。

`internal/imap` の `FetchMeta` は `FetchMetaResult.UIDValidity` として `UIDVALIDITY` を返す。本パッケージの責務は **`UIDVALIDITY` 値の永続化と取得のみ** とする。前回値との比較・変化検出・変化時の対応（fail closed による停止、`recovery_required` 状態の sentinel への記録（F-008 が API を提供）、summary の停止、手動復旧コマンドの提供等）はエントリポイント（タスク 0070）の責務である。

`UIDVALIDITY` は sentinel（`§6.1`）に保存する。sentinel が IMAP 識別子と UIDVALIDITY をまとめて管理することで、IMAP 関連の状態が 1 か所に集約される。`root_dir` に対して 1 つのメールボックスのみを扱うため、`mailbox` をキーとするマップは不要でスカラー値として保持する。

本パッケージは旧 epoch データを自動 invalidate しない。旧データを保持したまま運用継続するか、破棄して空状態から再開するかの判断はオペレータが行い、そのための補助はタスク 0070 の `recover` サブコマンドが担う。

**受け入れ条件（Acceptance Criteria）**:

- `AC-23`: `SaveUIDValidity(v uint32) error` で UIDVALIDITY を sentinel にアトミックに保存できる
- `AC-24`: `LoadUIDValidity() (v uint32, found bool, err error)` で保存済みの値を取得できる（未保存の場合は `found = false`、`err = nil`）

### F-007a: 期間指定でのレポート削除（GC）

累積件数を抑制するため、指定日時より古いレポートレコードを削除する API を提供する。

呼び出しタイミング（毎週・毎日など）の決定およびスケジューラからの起動は呼び出し元（エントリポイント／systemd timer 等）の責務とする。

**受け入れ条件（Acceptance Criteria）**:

- `AC-25`: `DeleteReportsBefore(cutoff time.Time) (deleted int, err error)` で `date-range.end-datetime < cutoff` のレポートレコードを削除し、削除件数を返す
- `AC-26`: 削除対象が 0 件の場合でもエラーを返さず、`deleted = 0` を返す
- `AC-27`: 削除後の書き込みも F-002 と同様にアトミック rename で行う
- `AC-28`: 削除は `report-id` 単位で冪等に動作する（同じ `cutoff` で再実行しても、対応するレコードが既に消えていれば差分は出ない）

### F-007b: 期間指定での `.eml` ファイル削除（GC）

累積した `.eml` ファイルを削除する API を提供する。メールインデックス（§6.2）の `report_end_date` および `saved_at` を参照することで、`.eml` 本文をスキャンせずに日単位の精度で削除対象を判定できる。

2 種類の削除条件を組み合わせることで、`report_end_date` の遠未来設定（不正メールによるストレージ攻撃）やパース失敗による `.eml` の無制限蓄積を防ぐ：

- **通常削除**：`report_end_date < cutoff`（レポート期間終了日ベース）
- **強制削除（savedAtCutoff）**：`saved_at < savedAtCutoff`（ダウンロード日ベース）。`report_end_date` の値によらず削除する。`savedAtCutoff` にゼロ値を渡すとこの削除は行われない

呼び出しタイミングの決定およびスケジューラからの起動は呼び出し元の責務とする。

**受け入れ条件（Acceptance Criteria）**:

- `AC-29`: `DeleteEmailsBefore(reportCutoff time.Time, savedAtCutoff time.Time) (deleted int, err error)` でメールインデックスの各エントリを評価し、`report_end_date < reportCutoff` または `saved_at < savedAtCutoff` のいずれかを満たす `.eml` ファイルを削除して削除件数を返す。`savedAtCutoff` にゼロ値（`time.Time{}`）を渡した場合、`saved_at` ベースの強制削除は行わない（通常削除のみ）。`time.Now()` の参照は呼び出し元（エントリポイント）で行い、Store に渡すことでユニットテスト時の時刻の固定が容易になる
- `AC-30`: 対象の `.eml` ファイルをすべて削除してから、インデックスエントリをまとめて除去して JSON をアトミックに書き戻す（ファイル削除 → インデックス更新の順序を厳守する）。逆順（インデックス更新 → ファイル削除）だと、クラッシュ時にインデックスエントリを失った孤立 `.eml` が生じ、`savedAtCutoff` 判定もできず永続的に残存する。ファイル削除を先に行うこの順序では、クラッシュ時の不整合は「ファイル消失・エントリ残存」方向のみとなり、`AC-31` によって次回実行でべき等に回収できる
- `AC-31`: `.eml` ファイルが既に存在しない場合もエラーにせず、インデックスエントリのみ除去して処理を継続する（冪等動作）。これにより、`.eml` 削除途中のクラッシュからの自動回復を保証する
- `AC-32`: 削除対象が 0 件の場合でもエラーを返さず、`deleted = 0` を返す

### F-008: recovery-required 状態の永続化

UIDVALIDITY 変化検出時にエントリポイント（タスク 0070）が記録する recovery-required 状態を sentinel に永続化する。sentinel に保存することで、プロセス再起動後も状態が維持され、次回 `fetch` / `summary` 実行時に未解決の状態を確実に検出できる。

**受け入れ条件（Acceptance Criteria）**:

- `AC-33`: `SaveRecoveryRequired(prev, curr uint32) error` で `recovery_required` フィールドを sentinel にアトミックに書き込む
- `AC-34`: `LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error)` で recovery-required 状態を取得できる。フィールドが存在しない場合は `found = false`
- `AC-35`: `ClearRecoveryRequired() error` で `recovery_required` フィールドを sentinel から除去する（`recover` サブコマンドの復旧完了時に呼ぶ）

---

## 4. 非機能要件

### パフォーマンス

- 定期サマリ用の取得（典型値：過去 7 日分）は 1 秒以内に完了すること
- 全件メモリ読み込み後にフィルタする実装で、**想定累積上限は 1 万件** とする（典型運用で週数百件、F-007 の GC により上限以下に抑える）
- 1 万件規模での `GetReportsSince` および `DeleteReportsBefore` は 1 秒以内に完了すること
- 上限を超えた場合の性能劣化、より効率的なクエリ（インデックス化等）への移行は将来の拡張で扱う

### 信頼性

- JSON 書き込みは一時ファイルへの書き込みと rename によるアトミック操作で行い、プロセスクラッシュ時のファイル破損を防ぐ
- `.eml` 保存はパース処理より前に行い、処理途中でプロセスが停止しても再処理できる状態を保つ

### セキュリティ（ファイルパーミッション）

`{root_dir}` 配下のファイルは以下の機密情報を含むため、umask に依らず明示的に厳格なパーミッションを設定する：

- sentinel（`.tlsrpt-digest-meta.json`）: IMAP サーバー識別子
- データファイル（`tlsrpt.json`）: TLSRPT レポート本文（failing MX・IP アドレス等）
- `.eml` ファイル: メール本文・添付（TLSRPT JSON 本体）

なお、プロセスロックファイル（`.tlsrpt-digest-store.lock`）は本パッケージではなくエントリポイント（タスク 0070 AC-10a）が作成するため、そのパーミッションはエントリポイントの責務とする。

**受け入れ条件**:

- `AC-36`: 本パッケージが作成するすべてのファイルは `0600`（オーナーのみ読み書き可）で作成する。一時ファイル（atomic rename 用）も同様に `0600` とする
- `AC-37`: 本パッケージが作成するすべてのディレクトリ（`{root_dir}` 自体、および配下の `emails/`・`{uidvalidity}/`・`{YYYYMM}/` サブディレクトリ）は `0700`（オーナーのみアクセス可）で作成する
- `AC-38`: 既存ファイル・ディレクトリのパーミッションが上記より緩い場合は WARN ログを出力するが、自動修正は行わない（運用者の意図を尊重）

#### 並行アクセスの前提

`fetch` / `gc` / `recover` / `reprocess` の各サブコマンドと `summary` サブコマンドは外部スケジューラー（systemd timer / cron）から **別プロセス** として起動されるため、同時実行が発生し得る。本パッケージは以下の前提で動作する：

- **読み取り×書き込みの同時実行**（例: `summary` と `fetch` が同時実行）: アトミック rename（AC-09）により、読み取り側が破損ファイルや書き込み途中のファイルを観測しないことを保証する
- **書き込み×書き込みの同時実行**（例: `fetch` が重複起動、`fetch` と `gc` の同時実行、`reprocess` と `fetch` の同時実行）: 本パッケージは整合性を保証しない（last-writer-wins）。この競合が発生すると、後から書いたプロセスが先に書いたプロセスの更新結果を上書きし、レポートデータが失われる。同時実行を避ける責任はエントリポイント（タスク 0070）にあり、本パッケージではプロセスロック（flock 等）を実装しない

### 保守性

- `Store` インターフェースを定義し、テスト時にインメモリ実装やモックに差し替えられること。インターフェースは少なくとも以下のメソッドを含む：
  - `SaveReport` / バッチ保存メソッド / `GetReportsSince`（F-002・F-003）
  - `SaveEmailMetas`（バッチインデックス登録）/ `SaveEmail` / `LoadEmails`（F-002 AC-08c・F-004・F-005）
  - `SaveUIDValidity(v uint32)` / `LoadUIDValidity()`（F-006、sentinel に保存）
  - `SaveRecoveryRequired` / `LoadRecoveryRequired` / `ClearRecoveryRequired`（F-008、sentinel に保存）
  - `DeleteReportsBefore`（F-007a）
  - `DeleteEmailsBefore`（F-007b）
  - 初期化用メソッド（F-001 相当。コンストラクタで担う設計も可）。初期化は IMAP 識別子（host・port・mailbox）を引数として受け取り、sentinel を管理する
- 詳細なシグネチャは `02_architecture.md` で確定する
- 保存した `.eml` ファイルを `testdata/` にコピーすることで、単体テストの canned データとして利用できること

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- プロダクションコードでは外部ライブラリを使用しない（`encoding/json`・`os` 等の標準ライブラリのみ）
- テストコードでは `stretchr/testify` を使用してよい。テスト用に `t.TempDir()` でテンポラリディレクトリを使用する

---

## 6. データファイル形式（概要）

### 6.1 sentinel メタファイル

`{root_dir}/.tlsrpt-digest-meta.json` を配置し、この `root_dir` がどの IMAP サーバー・メールボックスのデータを保持しているかを記録する。`root_dir` を単一の設定キーとすることで、データファイルとメール保存ディレクトリが常に同じルートを共有し、sentinel 1 つで両方を保護できる。

```json
{
  "format_version": 1,
  "imap_host": "imap.example.com",
  "imap_port": 993,
  "imap_mailbox": "INBOX",
  "initialized_at": "2026-05-12T10:00:00Z",
  "uid_validity": 1234567890,
  "recovery_required": {
    "prev_uid_validity": 1111111111,
    "curr_uid_validity": 1234567890,
    "detected_at": "2026-05-18T10:00:00Z"
  }
}
```

- `format_version`: sentinel ファイル自体のスキーマバージョン整数
- `imap_host` / `imap_port` / `imap_mailbox`: 初期化時の IMAP 識別子（現設定との一致を `AC-06` で検証）
- `initialized_at`: 初期化日時（RFC 3339）。検証には使用せず、運用上のトレーサビリティのため記録する
- `uid_validity`: IMAP メールボックスの UIDVALIDITY 値（`uint32`）。未取得の場合はフィールドを省略し、`LoadUIDValidity` は `found = false` を返す
- `recovery_required`: UIDVALIDITY 変化を検出して手動復旧を待っている状態を示す。フィールドが存在しない場合は正常状態。`prev_uid_validity`・`curr_uid_validity`・`detected_at` を含む（F-008 参照）

### 6.2 JSON データファイル

`{root_dir}/tlsrpt.json` に保存する。将来のスキーマ移行に備えてバージョン番号を保持する。IMAP 識別子・UIDVALIDITY はすべて sentinel で管理するため、データファイルには含めない。`reports` はフラットな配列とする（複数メールボックスを監視する場合は `root_dir` をメールボックス毎に分離して運用する）。

トップレベル構造の概要：

```json
{
  "version": 1,
  "reports": [
    { "report-id": "...", "organization-name": "...", "date-range": { ... }, "policies": [ ... ] }
  ],
  "emails": [
    { "uid": 123, "uidvalidity": 1234567890, "saved_at": "2026-05-18T10:00:00Z", "report_end_date": "2026-05-12T00:00:00Z" },
    { "uid": 456, "uidvalidity": 1234567890, "saved_at": "2026-05-18T10:01:00Z", "report_end_date": null }
  ]
}
```

- `version`: スキーマバージョン整数。互換性のない変更時にインクリメントする。読み込み時に未知のバージョンを検出した場合はエラーを返す
- `reports`: 保存済みレポートの配列。要素のフィールド構成は `tlsrpt.Report` に準拠する（具体的なフィールドの過不足は `02_architecture.md` で確定）
- `emails`: メールインデックス。fetch サイクル完了後に `SaveEmailMetas` で `{uid, uidvalidity, saved_at}` エントリをバッチ登録し、バッチ保存メソッド（`SaveReports`）が `report_end_date` を更新する。`DeleteEmailsBefore` 時に除去する。`report_end_date` が null のエントリはパース失敗メールを示す。`saved_at` によりパース失敗・遠未来 `report_end_date` に対する最大保持期間での強制削除が可能

### 6.3 ストレージレイアウト

`store.root_dir`（TOML 設定キー、デフォルト `./store`）配下のパスはすべてプログラムが自動的に導出する。

```
{root_dir}/
├── .tlsrpt-digest-meta.json          # sentinel（§6.1）
├── .tlsrpt-digest-store.lock         # プロセスロック（flock、0070 AC-10a）
├── tlsrpt.json                       # データファイル（§6.2）
└── emails/
    └── {uidvalidity}/                # UIDVALIDITY エポックで分離
        └── {YYYYMM}/                 # メール受信月で分離
            └── {uid}.eml
```

`root_dir` が TOML の `[store]` セクションで設定できる唯一のパスであり、データファイル・メール保存ディレクトリ・sentinel が同一ルートに置かれることで、片方だけの誤指定を構造的に排除する。TOML キー名と既定値の詳細は [タスク 0060](../0060_config/01_requirements.md) で確定する。

---

## 7. テスト方針

### 単体テスト

- ストレージ初期化のテスト（ファイル新規作成・既存ファイルへの冪等性）
- sentinel メタファイルのテスト
  - 初回初期化時に `.tlsrpt-digest-meta.json` が作成され IMAP 識別子が記録されること
  - 一致する識別子で再初期化しても既存ファイルが保持されること
  - 異なる IMAP 識別子で初期化を試みると ERROR が返り、エラーメッセージに期待値・実値・パスが含まれること
- 保存・取得のラウンドトリップテスト
- 重複保存（UPSERT）のテスト
- アトミック書き込みのテスト（書き込み中断時にデータが破損しないこと）
- sentinel メタファイルのアトミック書き込みテスト（書き込み中断時に破損ファイルが残らないこと）
- 空結果のテスト
- `.eml` 保存のテスト（ファイル名規則 `{uidvalidity}/{YYYYMM}/{uid}.eml`・ディレクトリ構造・冪等性）
- UIDVALIDITY 変化前後で同一 UID のメールが別ディレクトリに保存され、互いに衝突しないテスト
- `.eml` 読み込みのテスト（`testdata/` の実際のメールファイルを canned データとして使用）
- `SaveUIDValidity` / `LoadUIDValidity` のラウンドトリップテスト（未保存時に `found=false` が返ること、再保存で上書きされること）
- `DeleteReportsBefore` のテスト（境界値、削除 0 件、冪等性、削除後の `GetReportsSince` 結果整合性）
- `DeleteEmailsBefore` のテスト（インデックス参照による `.eml` 削除、`.eml` 既消失時の冪等性、インデックスとファイル削除の整合性）
- `SaveEmailMetas` によるバッチインデックス登録テスト（複数エントリの一括登録・既存エントリへの冪等動作・孤立 `.eml` 救済シナリオ）
- F-008 recovery_required のラウンドトリップテスト（`SaveRecoveryRequired` / `LoadRecoveryRequired` / `ClearRecoveryRequired`、未保存時 `found = false`）
- ファイルパーミッション検証テスト（新規作成ファイルが `0600`、新規作成ディレクトリが `0700` であること）
- 未知の `version` を持つデータファイル読み込み時にエラーが返ることのテスト
- 想定累積上限（1 万件）規模での `GetReportsSince` および `DeleteReportsBefore` の性能テスト

### 統合テスト

- テンポラリディレクトリを使ったエンドツーエンドのテスト
