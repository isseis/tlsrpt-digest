# 実装計画書：レポートデータの永続化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-19 |
| レビュー日 | 2026-05-19 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

- **目的**: `internal/store` パッケージを新規作成し、TLSRPT レポートデータ（JSON）・メール原本（`.eml`）・sentinel メタファイルの永続化 API を提供する。
- **実装原則**: `02_architecture.md` の設計に従い、`internal/tlsrpt`・`internal/imap` の型を再実装しない。JSON/sentinel/`.eml` の更新はすべてアトミック rename で行う。

---

## 2. 実装フェーズ

`02_architecture.md` Section 8 の Phase 定義に対応する。

---

### Phase 1: 基盤 I/O と open モード

- [x] **1.1** 型定義の作成
  - ファイル: `internal/store/types.go`
  - 作業内容:
    - `OpenMode` 型と `OpenReadWrite`・`OpenReadOnly` 定数を定義する
    - `IMAPIdentity`（Host/Port/Mailbox）・`EmailMeta`（UID/UIDValidity/SentAt/SavedAt）・`ReportInput`（Report/UID/UIDValidity）・`LoadedEmail`（Message/UID/UIDValidity/SentAt/SavedAt/Path）を定義する
    - JSON データファイル内部モデル（バージョン付きトップレベル構造体・レポートエントリ・メールインデックスエントリ）を定義する
    - sentinel 内部モデル（format_version・IMAP識別子・initialized_at・uid_validity・recovery_required）を定義する
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.2** エラー型の定義
  - ファイル: `internal/store/errors.go`
  - 作業内容: `02_architecture.md` Section 4.2 の各エラー型（`ErrStoreIdentityMismatch`・`ErrUnsupportedSchemaVersion`・`ErrAtomicWriteFailed`・`ErrInvalidEmailPath`・`ErrLoadEmailFailed`・`ErrDeleteEmailFailed`）に `Error()` メソッドを実装する。`ErrStoreIdentityMismatch.Error()` は期待値・実値・`root_dir` を含むメッセージを返す
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.3** アトミック書き込みヘルパーの実装
  - ファイル: `internal/store/atomicfile.go`
  - 作業内容:
    - 同ディレクトリ内の一時ファイルに書き込み後に `rename` する内部ヘルパー関数を実装する
    - 一時ファイルは `0600` パーミッションで作成する
    - write/sync/rename のいずれかで失敗した場合は一時ファイルを削除してエラーを返す
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.4** パーミッション管理ヘルパーの実装
  - ファイル: `internal/store/permission.go`
  - 作業内容:
    - `0700` パーミッションでディレクトリを作成する内部ラッパーを実装する
    - 既存ファイル/ディレクトリのパーミッションが指定値より緩い場合に `slog.Warn` を出力する関数を実装する（自動修正は行わない）
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.5** sentinel 管理の実装
  - ファイル: `internal/store/sentinel.go`
  - 作業内容:
    - `loadSentinel`（ファイルが存在しない場合はゼロ値を返す）・`saveSentinel`（1.3 のアトミックヘルパーを使用）を実装する
    - `initSentinel`：sentinel が存在しない場合は IMAP識別子・`initialized_at` を記録して新規作成し、存在する場合は IMAP識別子を検証して不一致なら `ErrStoreIdentityMismatch` を返す（``AC-05``・``AC-06``）
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.6** `Store` インターフェースと `Open` 関数の実装
  - ファイル: `internal/store/store.go`
  - 作業内容:
    - `Store` インターフェースを `02_architecture.md` Section 3.1 のシグネチャどおりに定義する
    - `store` 構造体（rootDir・identity・mode など）を定義する
    - `Open(rootDir string, identity IMAPIdentity, mode OpenMode) (Store, error)` を実装する：
      - `OpenReadWrite` モード: `root_dir`・`emails/`・`tlsrpt.json` を存在しない場合のみ作成し（`0700`/`0600`）、sentinel を初期化・検証する（``AC-01``〜``AC-03``・``AC-05``・``AC-06``）
      - `OpenReadOnly` モード: ファイルを新規作成せず、sentinel が存在する場合のみ検証する（``AC-04a``）
    - パッケージレベルユーティリティ関数 `SaveReport(s Store, input ReportInput) error` を実装する
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **1.7** Phase 1 のテスト実装
  - ファイル: `internal/store/store_test.go`
  - 作業内容:
    - `OpenReadWrite` で `root_dir`・`emails/`・`tlsrpt.json` が作成されること（``AC-01``〜``AC-03``）
    - 2 回目の `Open` で既存データが失われないこと（``AC-04``）
    - `OpenReadOnly` でファイルが新規作成されず、`GetReportsSince` が空スライスを返すこと（``AC-04a``）
    - sentinel 新規作成で IMAP識別子と `initialized_at` が記録されること（``AC-05``）
    - 異なる IMAP識別子で `Open` すると `ErrStoreIdentityMismatch` が返り、エラーメッセージに期待値・実値・`root_dir` が含まれること（`AC-06`、`errors.AsType` で型を検証）
    - 未知 `format_version` を持つ sentinel 読み込み時にエラーが返ること
    - 作成ファイルのパーミッションが `0600`、作成ディレクトリのパーミッションが `0700` であること（``AC-37``・``AC-38``）
    - 緩いパーミッションのディレクトリを事前作成して `Open` すると `slog.Warn` が出力され、パーミッションが変更されないこと（``AC-39``）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

---

### Phase 2: レポート・インデックス API

- [x] **2.1** レポート保存・取得 API の実装
  - ファイル: `internal/store/reports.go`
  - 作業内容:
    - `tlsrpt.json` の読み込みとスキーマバージョン検証を実装する
    - `SaveReports(inputs []ReportInput) error` を実装する：
      - `report-id` をキーとして UPSERT する（`AC-08`）
      - 対応する `{uid, uidvalidity}` のメールインデックスエントリの `report_end_date` を、対象入力群の `DateRange.EndDatetime` の最大値で更新する（`AC-08b`）
      - アトミック rename で書き戻す（`AC-09`）
    - `GetReportsSince(since time.Time) ([]tlsrpt.Report, error)` を実装する：`DateRange.EndDatetime >= since` でフィルタし、対象がない場合は空スライスを返す（`AC-11`・`AC-12`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **2.2** レポート API のテスト実装
  - ファイル: `internal/store/reports_test.go`
  - 作業内容:
    - `SaveReport` → `GetReportsSince` のラウンドトリップで全フィールド（`failure-details`・`policy-string`・`mx-host` 含む）が一致すること（`AC-07`・`AC-13`）
    - 同一 `report-id` の 2 回保存後、取得件数が 1 件であること（`AC-08`）
    - 複数 `ReportInput` を渡す `SaveReports` で全件が取得できること（`AC-08a`）
    - 同一 `{uid, uidvalidity}` で `EndDatetime` が異なる複数レポートを保存し、`report_end_date` が最大値で更新されること（`AC-08b`）
    - `since` と等しい・1ナノ秒前・1ナノ秒後の `EndDatetime` を持つ 3 件で `GetReportsSince` のフィルタ境界値を確認する（`AC-11`）
    - 空ストアへの `GetReportsSince` が空スライス（nil でない）を返すこと（`AC-12`）
    - 未知 `version` を持つデータファイル読み込み時にエラーが返ること
    - 書き込み失敗時にエラーが返ること（`AC-10`）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

- [x] **2.3** メール保存 API の実装
  - ファイル: `internal/store/emails.go`（SaveEmail・SaveEmailMetas 部分）
  - 作業内容:
    - `SaveEmail(uid, uidValidity uint32, sentAt, savedAt time.Time, rawEML []byte) error` を実装する：
      - 保存先パス `{root_dir}/emails/{uidvalidity}/{YYYYMM}/{padded_uid}.eml` を構築する（`YYYYMM` は `sentAt` から導出、`sentAt` がゼロ値なら `savedAt` を使用して WARN ログを出力）
      - UID を 10 桁ゼロパディング文字列に変換する（`AC-15`）
      - `{uidvalidity}/{YYYYMM}` ディレクトリが存在しない場合は `0700` で作成する（`AC-38`）
      - 同一パスのファイルが既に存在する場合はノーオペレーションで返る（`AC-18`）
      - アトミック rename で `0600` 書き込みを行う（`AC-17`）
    - `SaveEmailMetas(metas []EmailMeta) error` を実装する：
      - `{uid, uidvalidity}` をキーとして、存在しないエントリのみ追加する（`AC-08c`・`AC-08d`）
      - アトミック rename で書き戻す（`AC-09`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [x] **2.4** メール保存 API のテスト実装
  - ファイル: `internal/store/emails_test.go`（保存部分）
  - 作業内容:
    - `SaveEmail` 後に `{root_dir}/emails/{uidvalidity}/{YYYYMM}/0000000123.eml` が `mail.ReadMessage` でパース可能な状態で作成されること（`AC-14`・`AC-15`・`AC-16`）
    - ファイルが `0600`、ディレクトリが `0700` で作成されること（`AC-37`・`AC-38`）
    - `SaveEmail` 後に一時ファイルが残らないこと（`AC-17`）
    - 同一 UID + UIDVALIDITY の 2 回保存でファイルが最初の内容のままであること（`AC-18`）
    - 異なる UIDVALIDITY では別ディレクトリに保存され衝突しないこと
    - `sentAt` がゼロ値のとき `savedAt` の月が使用され WARN ログが出力されること（`AC-16` フォールバック）
    - `SaveEmailMetas` で複数エントリが一括登録されること（`AC-08c`）
    - 既存 `{uid, uidvalidity}` への再 `SaveEmailMetas` で `saved_at` が上書きされず `report_end_date` がリセットされないこと（`AC-08d`）
    - 孤立 `.eml` 救済シナリオ：前回 `SaveEmailMetas` が未実行のエントリが次回呼び出しで登録されること（`AC-08d`）
    - 保存失敗時にエラーが返ること（`AC-19`）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

---

### Phase 3: 読み込み・GC・復旧 API

- [ ] **3.1** メール読み込み API の実装
  - ファイル: `internal/store/emails.go`（LoadEmails 部分）
  - 作業内容:
    - `LoadEmails() ([]LoadedEmail, error)` を実装する：
      - `{root_dir}/emails/` 以下を再帰 walk し、`{uidvalidity}/{YYYYMM}/{padded_uid}.eml` パターンのファイルを列挙する（`AC-20`）
      - パスから `uidvalidity`・`uid` を `uint32` に逆算する（`AC-20`）
      - `mail.ReadMessage` でパースし `LoadedEmail` を構築する
      - `SentAt` は `Date:` ヘッダーから取得し、欠損・parse 失敗時は `syscall.Stat` の ctime（`SavedAt`）を代用する（`AC-21`）
      - 個別ファイルの失敗は `ErrLoadEmailFailed` に wrap して `errors.Join` で集約し、処理を継続する（`AC-22`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [ ] **3.2** メール読み込み API のテスト実装
  - ファイル: `internal/store/emails_test.go`（読み込み部分）
  - 作業内容:
    - 複数の `{uidvalidity}/{YYYYMM}/{uid}.eml` を保存し、`LoadEmails` が全件を返し、UID・UIDVALIDITY がパスから正しく逆算されること（`AC-20`）
    - `SentAt`・`SavedAt`・`Path` が正しく返ること（`AC-21`）
    - `Date:` ヘッダー欠損・不正な `.eml` で `SentAt` に `SavedAt` が代用されること（`AC-21` フォールバック）
    - 破損した `.eml` が混在するとき、成功分の `[]LoadedEmail` とエラーがともに返ること（`AC-22`）
    - reprocess 統合シナリオ：`LoadEmails` → `SaveEmailMetas` → `SaveReports` で結果が整合すること
  - 完了判定: `go test ./internal/store/...` がすべて通ること

- [ ] **3.3** レポート GC の実装
  - ファイル: `internal/store/reports.go`（DeleteReportsBefore 部分）
  - 作業内容:
    - `DeleteReportsBefore(cutoff time.Time) (deleted int, err error)` を実装する：
      - `date-range.end-datetime < cutoff` のレコードを除去し、削除件数を返す（`AC-25`）
      - 削除 0 件でも `err = nil` を返す（`AC-26`）
      - アトミック rename で書き戻す（`AC-27`）
      - 書き込み失敗時は `deleted = 0` とエラーを返す（`AC-28a`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [ ] **3.4** レポート GC のテスト実装
  - ファイル: `internal/store/reports_test.go`（GC 部分）
  - 作業内容:
    - `cutoff` と等しい・1ナノ秒前・1ナノ秒後の `EndDatetime` を持つレポートで削除件数を確認する（`AC-25`）
    - 削除後の `GetReportsSince` 結果が整合していること（`AC-25`）
    - 削除 0 件で `err = nil` が返ること（`AC-26`）
    - 同じ `cutoff` で 2 回呼んで 2 回目が `deleted = 0` を返すこと（`AC-28`）
    - 削除後に一時ファイルが残らないこと（`AC-27`）
    - 1 万件規模での `GetReportsSince` および `DeleteReportsBefore` が 1 秒以内に完了すること（パフォーマンステスト）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

- [ ] **3.5** `.eml` GC の実装
  - ファイル: `internal/store/emails.go`（DeleteEmailsBefore 部分）
  - 作業内容:
    - `DeleteEmailsBefore(reportCutoff, savedAtCutoff time.Time) (deleted int, err error)` を実装する：
      - 通常削除条件（`report_end_date != null && report_end_date < reportCutoff`）と強制削除条件（`savedAtCutoff != zero && saved_at < savedAtCutoff`）でインデックスエントリを評価する（`AC-29`）
      - ファイル削除を先に行い、その後インデックスをアトミック更新する（`AC-30`）
      - `.eml` が既に存在しない場合は非エラーとして扱い、インデックスエントリを除去して継続する（`AC-31`）
      - 個別削除で I/O エラーが発生しても全体を継続し、`errors.Join` で集約したエラーと成功件数を返す（`AC-32a`）
      - `savedAtCutoff` が非ゼロの場合、インデックスベース削除の完了後に `emails/{uidvalidity}/{YYYYMM}` ディレクトリを走査し、`YYYYMM < savedAtCutoff の年月` のディレクトリを `os.RemoveAll` で削除する。エラーは WARN ログに記録して継続する（`AC-32b`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [ ] **3.6** `.eml` GC のテスト実装
  - ファイル: `internal/store/emails_test.go`（GC 部分）
  - 作業内容:
    - 通常削除条件・強制削除条件・両方非対象のケースを個別に確認する（`AC-29`）
    - `savedAtCutoff` がゼロ値のとき強制削除が行われないこと（`AC-29`）
    - `report_end_date` が null のエントリが通常削除の対象外であること（`AC-29`）
    - `DeleteEmailsBefore` 後にファイルとインデックスエントリの両方が除去されていること（`AC-30`）
    - ファイルが既に存在しないエントリを対象とした場合に冪等動作すること（`AC-31`）
    - 削除 0 件で `err = nil` が返ること（`AC-32`）
    - 削除不能ファイルが混在するとき、成功件数と集約エラーが返り、失敗エントリのインデックスが残ること（`AC-32a`）
    - `savedAtCutoff` が非ゼロのとき、カットオフ年月より前の `{uidvalidity}/{YYYYMM}` ディレクトリが丸ごと削除され、インデックス外の孤立 `.eml` も除去されること（`AC-32b`）
    - `savedAtCutoff` がゼロ値のときディレクトリスイープが実行されないこと（`AC-32b`）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

- [ ] **3.7** UIDVALIDITY および recovery API の実装
  - ファイル: `internal/store/recovery.go`
  - 作業内容:
    - `SaveUIDValidity(v uint32) error`：sentinel の `uid_validity` をアトミック保存する（`AC-23`）
    - `LoadUIDValidity() (v uint32, found bool, err error)`：フィールドが存在しない場合は `found = false` を返す（`AC-24`）
    - `SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error`：sentinel の `recovery_required` をアトミック保存する（`AC-33`）
    - `LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error)`：フィールドが存在しない場合は `found = false` を返す（`AC-34`）
    - `ClearRecoveryRequired() error`：sentinel から `recovery_required` をアトミック除去する（`AC-35`）
    - `ApplyRecovery(newUIDValidity uint32) error`：`uid_validity` 更新と `recovery_required` 除去を 1 回の read-modify-write で実行する（`AC-36`）
  - 完了判定: `go build ./internal/store/...` が通ること

- [ ] **3.8** UIDVALIDITY および recovery API のテスト実装
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容:
    - `SaveUIDValidity` → `LoadUIDValidity` ラウンドトリップ（`AC-23`）
    - 新規 sentinel で `LoadUIDValidity` が `found = false`・`err = nil` を返すこと（`AC-24`）
    - `SaveUIDValidity` の再保存で値が上書きされること（`AC-23`）
    - `SaveRecoveryRequired` → `LoadRecoveryRequired` ラウンドトリップ（`AC-33`・`AC-34`）
    - `recovery_required` フィールドのない sentinel で `LoadRecoveryRequired` が `found = false` を返すこと（`AC-34`）
    - `ClearRecoveryRequired` 後に `LoadRecoveryRequired` が `found = false` を返すこと（`AC-35`）
    - `ApplyRecovery(newUID)` 後に `LoadUIDValidity` が `newUID` を返し、`LoadRecoveryRequired` が `found = false` を返すこと（`AC-36`）
  - 完了判定: `go test ./internal/store/...` がすべて通ること

---

### Phase 4: モックと入口統合

- [ ] **4.1** `FakeStore` モックの実装
  - ファイル: `internal/store/testutil/mocks.go`
  - パッケージ名: `storetestutil`
  - 作業内容:
    - `//go:build test` ビルドタグを付与する
    - `Store` インターフェースを実装する `FakeStore` 構造体を定義する（インメモリスライス/マップで動作）
    - `cmd/tlsrpt-digest` のテストから利用できるようにする
  - 完了判定: `go build -tags test ./internal/store/testutil/...` が通ること

- [ ] **4.2** エントリポイント側の store 利用調整
  - ファイル: `cmd/tlsrpt-digest/main.go`・`cmd/tlsrpt-digest/main_test.go`
  - 作業内容:
    - `main.go` にサブコマンドに応じた open モード選択ロジックを追加する（`fetch`/`gc`/`reprocess`/`recover` は `OpenReadWrite`、`summary` は `OpenReadOnly`）
    - `main_test.go` に `FakeStore` を使ったエントリポイントからの store 利用シナリオを追加する
  - 完了判定: `go test -tags test ./cmd/tlsrpt-digest/...` がすべて通ること

- [ ] **4.3** 最終品質チェック
  - 作業内容:
    - `make fmt` を実行してフォーマット済みであることを確認する
    - `make lint` を実行してエラーがないことを確認する
    - `make test` で全テストが通ることを確認する
    - `make deadcode` で未使用コードが報告されないことを確認する
  - 完了判定: すべてのコマンドがエラーなしで完了すること

---

## 3. 実装順序とマイルストーン

| マイルストーン | 完了条件 |
|---|---|
| M1: 基盤 I/O 完成 | Phase 1 が完了し、`Open` で sentinel/JSON/ディレクトリが正しく作成・検証される |
| M2: レポート・インデックス API 完成 | Phase 2 が完了し、`SaveReports`/`GetReportsSince`/`SaveEmail`/`SaveEmailMetas` が動作する |
| M3: GC・復旧 API 完成 | Phase 3 が完了し、全 API が動作する |
| M4: 統合完成 | Phase 4 が完了し、全テスト・lint・deadcode が通る |

---

## 4. テスト戦略

`02_architecture.md` Section 7 に従う。詳細は各フェーズのテストタスクを参照。

- **単体テスト**: 各 API の正常系・境界値・エラー系をカバーする。テンポラリディレクトリ（`t.TempDir()`）を使用する
- **統合テスト**: `store_test.go` 内で `Open → Save* → Load* → Delete*` の一連実行を検証する。reprocess シナリオ（`LoadEmails` → `SaveEmailMetas` → `SaveReports`）の整合性も含む
- **セキュリティテスト**: ファイル `0600`・ディレクトリ `0700` の検証（`AC-37`・`AC-38`）、および緩いパーミッションへの WARN 確認（`AC-39`）を各フェーズのテストに含める
- **パフォーマンステスト**: 1 万件規模での `GetReportsSince` および `DeleteReportsBefore` が 1 秒以内に完了することを 3.4 で確認する

---

## 5. リスク管理

| リスク | 対策 |
|---|---|
| アトミック rename の挙動が OS によって異なる | `os.Rename` は同一ファイルシステム上では POSIX 保証。テストで一時ファイルが残らないことを確認する |
| `.eml` GC で削除順序が逆転すると孤立エントリが生じる | `AC-30`・`AC-31` のテストで順序と冪等動作を明示的に確認する |
| 大量レコード時の性能劣化 | 3.4 のパフォーマンステストで 1 万件を閾値として早期検知する |
| `cmd/tlsrpt-digest` のサブコマンド構造が未確定（タスク 0070 依存） | 4.2 では open モード選択ロジックのスタブを追加し、0070 で完成させる |

---

## 6. 実装チェックリスト

### Phase 1
- [ ] 型定義（1.1）
- [ ] エラー型（1.2）
- [ ] アトミック書き込みヘルパー（1.3）
- [ ] パーミッション管理ヘルパー（1.4）
- [ ] sentinel 管理（1.5）
- [ ] `Store` インターフェースと `Open`（1.6）
- [ ] Phase 1 テスト（1.7）

### Phase 2
- [ ] レポート保存・取得 API（2.1）
- [ ] レポート API テスト（2.2）
- [ ] メール保存 API（2.3）
- [ ] メール保存 API テスト（2.4）

### Phase 3
- [ ] メール読み込み API（3.1）
- [ ] メール読み込み API テスト（3.2）
- [ ] レポート GC（3.3）
- [ ] レポート GC テスト（3.4）
- [ ] `.eml` GC（3.5）
- [ ] `.eml` GC テスト（3.6）
- [ ] UIDVALIDITY/recovery API（3.7）
- [ ] UIDVALIDITY/recovery API テスト（3.8）

### Phase 4
- [ ] `FakeStore` モック（4.1）
- [ ] エントリポイント調整（4.2）
- [ ] 最終品質チェック（4.3）

---

## 7. 受け入れ条件トレーサビリティ

テスト関数名は実装時に確定する。以下は対象テストファイルと検証方法を示す。

`AC-01`（`root_dir` が存在しない場合にディレクトリを作成する）
- テスト: `internal/store/store_test.go::TestOpen_CreatesRootDir`
- 実装: `internal/store/store.go`（Open 関数）

`AC-02`（`emails/` サブディレクトリが自動作成される）
- テスト: `internal/store/store_test.go::TestOpen_CreatesEmailsDir`
- 実装: `internal/store/store.go`

`AC-03`（`tlsrpt.json` が空のレコードセットで新規作成される）
- テスト: `internal/store/store_test.go::TestOpen_CreatesDataFile`
- 実装: `internal/store/store.go`

`AC-04`（既存データが失われない）
- テスト: `internal/store/store_test.go::TestOpen_ExistingDataPreserved`
- 実装: `internal/store/store.go`

`AC-04a`（read-only モードでファイルを新規作成しない）
- テスト: `internal/store/store_test.go::TestOpen_ReadOnly_NoCreate`
- 実装: `internal/store/store.go`（OpenReadOnly 分岐）

`AC-05`（sentinel が存在しない場合に新規作成される）
- テスト: `internal/store/store_test.go::TestOpen_CreatesSentinel`
- 実装: `internal/store/sentinel.go`（initSentinel）

`AC-06`（sentinel IMAP識別子不一致でエラーが返る）
- テスト: `internal/store/store_test.go::TestOpen_SentinelIdentityMismatch`
- 実装: `internal/store/sentinel.go`（initSentinel）
- 検証方法: `errors.AsType[*ErrStoreIdentityMismatch]` で型を確認し、エラーメッセージに期待値・実値・`root_dir` が含まれることを確認

`AC-07`（`tlsrpt.Report` の全フィールドを保存する）
- テスト: `internal/store/reports_test.go::TestSaveReports_AllFieldsPreserved`
- 実装: `internal/store/reports.go`（SaveReports）

`AC-08`（同一 `report-id` の重複保存が UPSERT となる）
- テスト: `internal/store/reports_test.go::TestSaveReports_Upsert`
- 実装: `internal/store/reports.go`（SaveReports）

`AC-08a`（バッチ保存で複数レポートをまとめて保存できる）
- テスト: `internal/store/reports_test.go::TestSaveReports_Batch`
- 実装: `internal/store/reports.go`（SaveReports）

`AC-08b`（バッチ保存で `report_end_date` が最大値で更新される）
- テスト: `internal/store/reports_test.go::TestSaveReports_UpdatesReportEndDate`
- 実装: `internal/store/reports.go`（SaveReports）

`AC-08c`（`SaveEmailMetas` で複数エントリをバッチ登録できる）
- テスト: `internal/store/emails_test.go::TestSaveEmailMetas_BatchInsert`
- 実装: `internal/store/emails.go`（SaveEmailMetas）

`AC-08d`（`SaveEmailMetas` が既存エントリに影響しない）
- テスト: `internal/store/emails_test.go::TestSaveEmailMetas_Idempotent`
- 実装: `internal/store/emails.go`（SaveEmailMetas）

`AC-09`（書き込みがアトミックに行われる）
- テスト: `internal/store/reports_test.go::TestSaveReports_AtomicWrite`、`internal/store/emails_test.go::TestSaveEmailMetas_AtomicWrite`
- 実装: `internal/store/atomicfile.go`

`AC-10`（保存失敗時にエラーが返る）
- テスト: `internal/store/reports_test.go::TestSaveReports_Error`
- 実装: `internal/store/reports.go`

`AC-11`（`date-range.end-datetime >= since` でフィルタされる）
- テスト: `internal/store/reports_test.go::TestGetReportsSince_FilterSemantics`
- 実装: `internal/store/reports.go`（GetReportsSince）

`AC-12`（対象レポートがない場合に空スライスを返す）
- テスト: `internal/store/reports_test.go::TestGetReportsSince_Empty`
- 実装: `internal/store/reports.go`

`AC-13`（ラウンドトリップで全フィールドが一致する）
- テスト: `internal/store/reports_test.go::TestSaveReports_RoundTrip`
- 実装: `internal/store/reports.go`

`AC-14`（メール 1 件につき 1 ファイルが RFC 2822 形式で作成される）
- テスト: `internal/store/emails_test.go::TestSaveEmail_CreatesFile`
- 実装: `internal/store/emails.go`（SaveEmail）

`AC-15`（ファイル名が 10 桁ゼロパディングの `{uid}.eml` 形式）
- テスト: `internal/store/emails_test.go::TestSaveEmail_FileName`
- 実装: `internal/store/emails.go`（SaveEmail）

`AC-16`（パスが `{root_dir}/emails/{uidvalidity}/{YYYYMM}/{uid}.eml` 形式）
- テスト: `internal/store/emails_test.go::TestSaveEmail_PathFormat`
- 実装: `internal/store/emails.go`（SaveEmail）

`AC-17`（`.eml` 書き込みがアトミック rename で行われる）
- テスト: `internal/store/emails_test.go::TestSaveEmail_Atomic`
- 実装: `internal/store/emails.go` / `internal/store/atomicfile.go`

`AC-18`（同一 UID + UIDVALIDITY の再保存が冪等）
- テスト: `internal/store/emails_test.go::TestSaveEmail_Idempotent`
- 実装: `internal/store/emails.go`（SaveEmail）

`AC-19`（保存失敗時にエラーが返る）
- テスト: `internal/store/emails_test.go::TestSaveEmail_Error`
- 実装: `internal/store/emails.go`（SaveEmail）

`AC-20`（`emails/` 以下の全 `.eml` を再帰列挙し UID/UIDVALIDITY を逆算できる）
- テスト: `internal/store/emails_test.go::TestLoadEmails_Enumeration`
- 実装: `internal/store/emails.go`（LoadEmails）

`AC-21`（各エントリで UID・UIDVALIDITY・SentAt・SavedAt・Path が返る）
- テスト: `internal/store/emails_test.go::TestLoadEmails_Fields`
- 実装: `internal/store/emails.go`（LoadEmails）

`AC-22`（読み込み失敗ファイルはスキップして処理を継続する）
- テスト: `internal/store/emails_test.go::TestLoadEmails_SkipsFailedFiles`
- 実装: `internal/store/emails.go`（LoadEmails）

`AC-23`（`SaveUIDValidity` で sentinel にアトミック保存できる）
- テスト: `internal/store/recovery_test.go::TestSaveLoadUIDValidity`
- 実装: `internal/store/recovery.go`（SaveUIDValidity）

`AC-24`（`LoadUIDValidity` が未保存時に `found = false` を返す）
- テスト: `internal/store/recovery_test.go::TestLoadUIDValidity_NotFound`
- 実装: `internal/store/recovery.go`（LoadUIDValidity）

`AC-25`（`DeleteReportsBefore` が条件に合うレコードを削除し削除件数を返す）
- テスト: `internal/store/reports_test.go::TestDeleteReportsBefore_BoundaryValues`
- 実装: `internal/store/reports.go`（DeleteReportsBefore）

`AC-26`（削除対象 0 件でも `err = nil` を返す）
- テスト: `internal/store/reports_test.go::TestDeleteReportsBefore_ZeroDeleted`
- 実装: `internal/store/reports.go`

`AC-27`（削除後の書き込みもアトミック rename で行う）
- テスト: `internal/store/reports_test.go::TestDeleteReportsBefore_AtomicWrite`
- 実装: `internal/store/reports.go` / `internal/store/atomicfile.go`

`AC-28`（削除が冪等に動作する）
- テスト: `internal/store/reports_test.go::TestDeleteReportsBefore_Idempotent`
- 実装: `internal/store/reports.go`

`AC-28a`（書き込み失敗時は `deleted = 0` とエラーを返す）
- テスト: `internal/store/reports_test.go::TestDeleteReportsBefore_WriteError`
- 実装: `internal/store/reports.go`

`AC-29`（`DeleteEmailsBefore` が 2 条件で `.eml` を削除する）
- テスト: `internal/store/emails_test.go::TestDeleteEmailsBefore_Conditions`
- 実装: `internal/store/emails.go`（DeleteEmailsBefore）

`AC-30`（ファイル削除後にインデックスをアトミック更新する）
- テスト: `internal/store/emails_test.go::TestDeleteEmailsBefore_OrderOfOperations`
- 実装: `internal/store/emails.go`

`AC-31`（`.eml` が存在しない場合も冪等動作）
- テスト: `internal/store/emails_test.go::TestDeleteEmailsBefore_MissingFileIdempotent`
- 実装: `internal/store/emails.go`

`AC-32`（削除対象 0 件でも `err = nil` を返す）
- テスト: `internal/store/emails_test.go::TestDeleteEmailsBefore_ZeroDeleted`
- 実装: `internal/store/emails.go`

`AC-32a`（個別 I/O エラーで全体を継続し成功件数と集約エラーを返す）
- テスト: `internal/store/emails_test.go::TestDeleteEmailsBefore_PartialFailure`
- 実装: `internal/store/emails.go`

`AC-33`（`SaveRecoveryRequired` で sentinel にアトミック書き込みできる）
- テスト: `internal/store/recovery_test.go::TestSaveLoadRecoveryRequired`
- 実装: `internal/store/recovery.go`（SaveRecoveryRequired）

`AC-34`（`LoadRecoveryRequired` が未保存時に `found = false` を返す）
- テスト: `internal/store/recovery_test.go::TestLoadRecoveryRequired_NotFound`
- 実装: `internal/store/recovery.go`（LoadRecoveryRequired）

`AC-35`（`ClearRecoveryRequired` で `recovery_required` フィールドを除去できる）
- テスト: `internal/store/recovery_test.go::TestClearRecoveryRequired`
- 実装: `internal/store/recovery.go`（ClearRecoveryRequired）

`AC-36`（`ApplyRecovery` で `uid_validity` 更新と `recovery_required` 除去が一体で実行される）
- テスト: `internal/store/recovery_test.go::TestApplyRecovery`
- 実装: `internal/store/recovery.go`（ApplyRecovery）

`AC-37`（本パッケージが作成するすべてのファイルが `0600`）
- テスト: `internal/store/store_test.go::TestOpen_FilePermissions`
- 実装: `internal/store/atomicfile.go` / `internal/store/emails.go`

`AC-38`（本パッケージが作成するすべてのディレクトリが `0700`）
- テスト: `internal/store/store_test.go::TestOpen_DirPermissions`
- 実装: `internal/store/permission.go` / `internal/store/store.go` / `internal/store/emails.go`

`AC-39`（緩いパーミッションへの WARN 出力・自動修正なし）
- テスト: `internal/store/store_test.go::TestOpen_WarnOnLaxPermissions`
- 実装: `internal/store/permission.go`

---

## 8. 完了条件

- [ ] `make fmt` がエラーなしで完了する
- [ ] `make lint` がエラーなしで完了する
- [ ] `make test` で全テストが通る
- [ ] `make deadcode` で未使用コードが報告されない
- [ ] `01_requirements.md` のすべての受け入れ条件（`AC-01`〜`AC-39`）に対して、少なくとも 1 件のテストが対応する
- [ ] 1 万件規模での `GetReportsSince` および `DeleteReportsBefore` が 1 秒以内に完了する（パフォーマンステスト）

---

## 9. 次のステップ

- `03_implementation_plan.md` のレビューと `approved` への更新後、Phase 1 から実装を開始する
- タスク 0070（エントリポイント）の実装時に 4.2 のサブコマンド統合を完成させる
- `FakeStore`（4.1）はタスク 0050（weekly summary）および 0070 のテストで利用する
