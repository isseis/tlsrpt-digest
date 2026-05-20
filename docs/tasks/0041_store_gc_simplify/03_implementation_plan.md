# 実装計画書：ストア GC の簡略化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-20 |
| レビュー日 | 2026-05-20 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

- **目的**: `02_architecture.md` に従い、`.eml` GC 基準を `INTERNALDATE` に統一し、`SentAt` 廃止・`report_end_date` 削除・`sweepOrphanedEmailDirs` の廃止と GC 後の空ディレクトリ削除の実装を行う
- **実装原則**: フェーズ順に実施し、各フェーズ末でビルドとテストが通る状態を維持する

---

## 2. 実装フェーズ

`02_architecture.md` Section 8「実装優先度」の Phase 0〜3 に対応する。

---

### Phase 0: 型・インターフェース変更（F-000・F-002 前提部分）

#### 0.1 `types.go` の型変更

- ファイル: `internal/store/types.go`
- [x] `EmailMeta.SentAt` を削除し `InternalDate time.Time` を追加する（`02_architecture.md` Section 3.2 参照）
- [x] `LoadedEmail.SentAt` フィールドを削除する
- [x] `internalEmailIndexEntry.SentAt`（JSON: `sent_at`）を `InternalDate`（JSON: `internal_date`）に変更する
- [x] `internalEmailIndexEntry.ReportEndDate` フィールドを削除する
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 20 分 / 実績工数: -

#### 0.2 `store.go` のインターフェース変更

- ファイル: `internal/store/store.go`
- [x] `Store.SaveEmail` のシグネチャを `SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error` に変更し、コメントを更新する（AC-18）
- [x] `Store.DeleteEmailsBefore` のシグネチャを `DeleteEmailsBefore(cutoff time.Time) (deleted int, err error)` に変更し、コメントを更新する（AC-01）
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 10 分 / 実績工数: -

---

### Phase 1: 実装変更（F-000・F-001・F-002・F-003）

#### 1.1 `emails.go`: `buildEmailPath` の引数変更

- ファイル: `internal/store/emails.go`
- [x] `buildEmailPath` の引数を `sentAt` → `internalDate time.Time` に変更する
- 見積工数: 5 分 / 実績工数: -

#### 1.2 `emails.go`: `SaveEmail` の実装変更（AC-18・AC-19）

- ファイル: `internal/store/emails.go`
- [x] パラメータ名を `sentAt` → `internalDate` に変更する
- [x] ゼロ値フォールバックを削除し、`internalDate.IsZero()` の場合は `fmt.Errorf(...)` でエラーを返す（AC-19）
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 15 分 / 実績工数: -

#### 1.3 `emails.go`: `SaveEmailMetas` のプレースホルダー補填ロジック削除（AC-14）

- ファイル: `internal/store/emails.go`
- [x] `SentAt` の正規化ブロック（`sentAt := meta.SentAt` 以降）を削除し、`meta.InternalDate` を直接使用する
- [x] 既存エントリに対する補填ブランチ（`df.Emails[i].SentAt.IsZero()` チェック）を削除し、既存エントリがある場合は何もせず `continue` するだけにする（AC-14）
- [x] 新規エントリ追加時のフィールドを `SentAt`/`SavedAt` → `InternalDate` のみに変更する（`SavedAt` はインデックスから除去済み）
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 20 分 / 実績工数: -

#### 1.4 `emails.go`: `LoadEmails` から `SentAt` を除去（AC-16）

- ファイル: `internal/store/emails.go`
- [x] `LoadedEmail{}` 生成時の `SentAt:` フィールドを削除する
- [x] `sentAt` 変数の計算（`Date:` ヘッダー解析ブロック）を削除する
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 10 分 / 実績工数: -

#### 1.5 `reports.go`: `SaveReports` からメールインデックス更新ロジックを削除（AC-09）

- ファイル: `internal/store/reports.go`
- [x] `maxEndDate` マップの計算ブロックを削除する
- [x] `emailIdx` マップを使った `report_end_date` 更新ブロック（プレースホルダー作成を含む）を削除する
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 15 分 / 実績工数: -

#### 1.6 `emails.go`: `DeleteEmailsBefore` の新実装（AC-01〜AC-07・AC-12・AC-13）

- ファイル: `internal/store/emails.go`
- [x] `sweepOrphanedEmailDirs` を削除する（AC-12）
- [x] `DeleteEmailsBefore` のシグネチャを `(cutoff time.Time)` に変更する（AC-01）
- [x] `cutoff.IsZero()` の場合は `0, nil` を即時返す（AC-02）
- [x] 削除条件を `entry.InternalDate.Before(cutoff)` のみとする（AC-03）
- [x] パス再構築を `entry.InternalDate` から行う（`sentAt`/`savedAt` フォールバックを削除）
- [x] ファイル削除 → インデックスアトミック更新の順を維持する（AC-06）
- [x] ファイル不在（`os.IsNotExist`）は非エラーとして削除件数に含む（AC-04・AC-07）
- [x] 個別 I/O エラーは `errors.Join` で集約し継続する（AC-05）
- [x] インデックス更新失敗時は `deleted` と集約エラーを返す（AC-07）
- [x] インデックス更新後に GC 済みエントリの `{uidvalidity}/{YYYYMM}` ディレクトリが空なら削除し、`{uidvalidity}` ディレクトリも空なら削除する。失敗は `slog.Warn` のみ（AC-13）
- 完了判定: `go build ./internal/store/...` が通ること
- 見積工数: 60 分 / 実績工数: -

---

### Phase 2: `testutil/mocks.go` の更新（AC-20）

- ファイル: `internal/store/testutil/mocks.go`
- [x] `FakeEmailEntry.SentAt` を削除し `InternalDate time.Time` を追加する
- [x] `FakeEmailEntry.ReportEndDate` を削除する
- [x] `FakeStore.SaveEmail` のシグネチャを `SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error` に変更し、`internalDate.IsZero()` の場合はエラーを返す（AC-19 と同じ動作）
- [x] `FakeStore.SaveEmailMetas` から補填ブランチを削除し、`SentAt` 参照を `InternalDate` に変更する
- [x] `FakeStore.SaveReports` からメールインデックス更新ロジックを削除する（AC-09 と同じ）
- [x] `FakeStore.DeleteEmailsBefore` を新シグネチャ `(cutoff time.Time)` に変更し、`InternalDate.Before(cutoff)` を削除条件とする（AC-01・AC-03）
- [x] `FakeStore.LoadEmails` の `LoadedEmail{}` 生成から `SentAt:` フィールドを削除する
- 完了判定: `go build -tags test ./internal/store/testutil/...` が通ること
- 見積工数: 40 分 / 実績工数: -

---

### Phase 3: テストの更新・追加・削除

#### 3.1 `emails_test.go` の整理

- ファイル: `internal/store/emails_test.go`
- 見積工数: 90 分 / 実績工数: -
- [x] `saveEMLWithMeta` ヘルパーを `sentAt`/`SavedAt` → `internalDate`/`SavedAt` に更新する（`SaveEmail` 呼び出しと `EmailMeta` フィールドを変更）
- [x] 削除するテスト（旧ロジックに依存するもの）:
  - `TestSaveEmail_ZeroSentAtFallback`（フォールバック廃止）
  - `TestSaveEmailMetas_MinimalEntryRescue`（プレースホルダー補填廃止）
  - `TestSaveEmailMetas_OrphanRescue`（同上）
  - `TestSaveEmailMetas_ZeroSentAtNormalization`（SentAt廃止）
  - `TestDeleteEmailsBefore_NullReportEndDate`（report_end_date廃止）
  - `TestDeleteEmailsBefore_Sweep`（sweepOrphanedEmailDirs廃止）
  - `TestDeleteEmailsBefore_SweepNotCalledWhenZero`（同上）
  - `TestDeleteEmailsBefore_PlaceholderEntryNotOrphaned`（プレースホルダー廃止）
  - `TestLoadEmails_SentAtFallback`（LoadedEmail.SentAt廃止）
- [x] 更新するテスト（引数名・フィールド名変更）:
  - `TestSaveEmail_CreatesFile` 〜 `TestSaveEmail_ReadOnly`（`sentAt` 引数 → `internalDate`）
  - `TestSaveEmailMetas_BatchInsert`、`TestSaveEmailMetas_Idempotent`、`TestSaveEmailMetas_AtomicWrite`、`TestSaveEmailMetas_WriteError`、`TestSaveEmailMetas_ReadOnly`（`EmailMeta.SentAt` → `InternalDate`）
  - `TestLoadEmails_Fields`（`LoadedEmail.SentAt` フィールドの検証を削除し、`LoadedEmail` が `SentAt` を持たないことをコンパイルで担保）
  - `TestDeleteEmailsBefore_MissingFileIdempotent`、`TestDeleteEmailsBefore_ZeroDeleted`、`TestDeleteEmailsBefore_PartialFailure`（新シグネチャに対応）
- [x] 追加するテスト:
  - `TestSaveEmail_ZeroInternalDate_Error`：`internalDate` がゼロ値のときエラーが返ることを確認（AC-19）
  - `TestSaveEmailMetas_NoPlaceholderUpdate`：既存エントリがある場合に `InternalDate` を上書きしないことを確認（AC-14）
  - `TestDeleteEmailsBefore_ZeroCutoff`：`cutoff` がゼロ値のとき削除件数 0・エラーなしを確認（AC-02）
  - `TestDeleteEmailsBefore_Conditions`（書き直し）：`internal_date < cutoff` の条件で削除、ファイル不在もカウント、`>= cutoff` のエントリを保持（AC-03・AC-04・AC-06・AC-07）
  - `TestDeleteEmailsBefore_EmptyDirCleanup`：GC 後の空 `{uidvalidity}/{YYYYMM}` および `{uidvalidity}` ディレクトリが削除されることを確認（AC-13）
  - `TestDeleteEmailsBefore_DirCleanupWarn`：ディレクトリ削除失敗時にエラーを返さず `slog.Warn` が出力されることを確認（AC-13）

#### 3.2 `reports_test.go` の整理

- ファイル: `internal/store/reports_test.go`
- 見積工数: 20 分 / 実績工数: -
- [x] 削除するテスト:
  - `TestSaveReports_UpdatesReportEndDate`（report_end_date廃止）
- [x] 追加するテスト:
  - `TestSaveReports_DoesNotUpdateEmailIndex`：`SaveReports` 後にメールインデックスが空のままであることを確認（AC-09）

#### 3.3 品質確認

- 見積工数: 10 分 / 実績工数: -
- [x] `make fmt` を実行してフォーマット済みであることを確認する
- [x] `make test` で全テストが通ることを確認する
- [x] `make lint` でエラーがないことを確認する
- [x] `make deadcode` で未使用コードが報告されないことを確認する

---

## 3. 実装順序とマイルストーン

| マイルストーン | 完了条件 | 見積工数 |
|---|---|---|
| M0: 型・インターフェース確定 | Phase 0 完了。`go build ./internal/store/...` が通る | 30 分 |
| M1: 実装変更完了 | Phase 1 完了。`go build ./internal/store/...` が通る | 125 分 |
| M2: モック更新完了 | Phase 2 完了。`go build -tags test ./...` が通る | 40 分 |
| M3: テスト完了 | Phase 3 完了。`make test` が通る | 120 分 |
| **合計** | | **315 分（約 5.5 時間）** |

---

## 4. テスト戦略

### 4.1 単体テスト

`02_architecture.md` Section 7.1 参照。主な検証点：

| 対象 | 追加・変更するテスト | 確認内容 |
|---|---|---|
| `SaveEmail` | `TestSaveEmail_ZeroInternalDate_Error`（追加） | ゼロ値でエラー（AC-19） |
| `SaveEmail` | 既存テスト群（更新） | `internalDate` からパスが正しく決定される（AC-18・AC-19） |
| `SaveEmailMetas` | `TestSaveEmailMetas_NoPlaceholderUpdate`（追加） | 既存エントリを補填しない（AC-14） |
| `SaveEmailMetas` | 既存テスト群（更新） | `InternalDate` フィールドで動作（AC-15・AC-17） |
| `SaveReports` | `TestSaveReports_DoesNotUpdateEmailIndex`（追加） | メールインデックスを変更しない（AC-09） |
| `DeleteEmailsBefore` | `TestDeleteEmailsBefore_ZeroCutoff`（追加） | ゼロ値で削除なし（AC-02） |
| `DeleteEmailsBefore` | `TestDeleteEmailsBefore_Conditions`（書き直し） | `internal_date < cutoff` で削除（AC-03） |
| `DeleteEmailsBefore` | `TestDeleteEmailsBefore_EmptyDirCleanup`（追加） | 空ディレクトリを削除（AC-13） |
| `DeleteEmailsBefore` | `TestDeleteEmailsBefore_DirCleanupWarn`（追加） | 失敗時は WARN のみ（AC-13） |

### 4.2 統合テスト

- fetch サイクル（`SaveEmail` → `SaveEmailMetas` → `SaveReports`）後に GC を実行し、`internal_date < cutoff` のエントリのみが削除されることを確認する

### 4.3 セキュリティテスト

本タスクにセキュリティ固有のテスト要件はない（N/A）

---

## 5. リスク管理

| リスク | 対策 |
|---|---|
| `SentAt` 参照の見落とし | `grep -rn "SentAt\|sent_at\|sentAt"` で残存参照を確認する |
| `report_end_date` 参照の見落とし | `grep -rn "report_end_date\|ReportEndDate"` で残存参照を確認する |
| `saveEMLWithMeta` の更新漏れ | ヘルパー更新後に依存テストが全件コンパイル通ることで確認 |
| `FakeStore` とインターフェースの乖離 | `cmd/tlsrpt-digest/main_test.go` の `var _ store.Store = (*storetestutil.FakeStore)(nil)` でコンパイル時に検出 |


---

## 6. 実装チェックリスト

### Phase 0 チェックリスト

- [x] `EmailMeta.SentAt` 削除・`InternalDate` 追加（types.go）
- [x] `LoadedEmail.SentAt` 削除（types.go）
- [x] `internalEmailIndexEntry.SentAt` → `InternalDate`（types.go）
- [x] `internalEmailIndexEntry.ReportEndDate` 削除（types.go）
- [x] `Store.SaveEmail` シグネチャ変更（store.go）
- [x] `Store.DeleteEmailsBefore` シグネチャ変更（store.go）

### Phase 1 チェックリスト

- [x] `buildEmailPath` 引数変更（emails.go）
- [x] `SaveEmail` ゼロ値エラー実装（emails.go）
- [x] `SaveEmailMetas` 補填ブランチ削除（emails.go）
- [x] `LoadEmails` から SentAt 除去（emails.go）
- [x] `SaveReports` インデックス更新ロジック削除（reports.go）
- [x] `sweepOrphanedEmailDirs` 削除（emails.go）
- [x] `DeleteEmailsBefore` 新実装（emails.go）

### Phase 2 チェックリスト

- [x] `FakeEmailEntry` フィールド更新（testutil/mocks.go）
- [x] `FakeStore.SaveEmail` 更新（testutil/mocks.go）
- [x] `FakeStore.SaveEmailMetas` 更新（testutil/mocks.go）
- [x] `FakeStore.SaveReports` 更新（testutil/mocks.go）
- [x] `FakeStore.DeleteEmailsBefore` 更新（testutil/mocks.go）
- [x] `FakeStore.LoadEmails` 更新（testutil/mocks.go）

### Phase 3 チェックリスト

- [x] `saveEMLWithMeta` ヘルパー更新
- [x] 削除対象テスト 9 件を削除
- [x] 既存テスト群を新シグネチャ・フィールドに更新
- [x] 新規テスト 6 件を追加
- [x] `TestSaveReports_UpdatesReportEndDate` 削除
- [x] `TestSaveReports_DoesNotUpdateEmailIndex` 追加
- [x] `make fmt && make test && make lint && make deadcode` が全て通ること

---

## 7. 受け入れ条件の対応表

**AC-01**: `DeleteEmailsBefore(cutoff time.Time)` シグネチャ
- 実装: `internal/store/store.go`、`internal/store/emails.go`
- テスト: `TestDeleteEmailsBefore_Conditions`、`TestDeleteEmailsBefore_ZeroCutoff`

**AC-02**: `cutoff` がゼロ値のとき削除なし
- 実装: `internal/store/emails.go`（先頭のゼロ値チェック）
- テスト: `TestDeleteEmailsBefore_ZeroCutoff`

**AC-03**: `internal_date < cutoff` を満たすエントリを削除
- 実装: `internal/store/emails.go`（削除ループ）
- テスト: `TestDeleteEmailsBefore_Conditions`

**AC-04**: ファイル不在は非エラー・インデックスエントリ除去
- 実装: `internal/store/emails.go`（`os.IsNotExist` の扱い）
- テスト: `TestDeleteEmailsBefore_MissingFileIdempotent`

**AC-05**: 個別 I/O エラーを集約して継続
- 実装: `internal/store/emails.go`（`errors.Join`）
- テスト: `TestDeleteEmailsBefore_PartialFailure`

**AC-06**: ファイル削除 → インデックス更新の順
- 実装: `internal/store/emails.go`（処理順序）
- テスト: `TestDeleteEmailsBefore_Conditions`（削除後のインデックス状態確認）

**AC-07**: 削除件数（ファイル不在も含む）・インデックス更新失敗時の挙動
- 実装: `internal/store/emails.go`
- テスト: `TestDeleteEmailsBefore_Conditions`、`TestDeleteEmailsBefore_MissingFileIdempotent`

**AC-08**: `internalEmailIndexEntry` から `report_end_date` を削除
- 実装: `internal/store/types.go`（フィールド削除）
- テスト: フィールド削除後に `go build ./internal/store/...` が通ることで確認（残存参照はコンパイルエラーとなる）

**AC-09**: `SaveReports` がメールインデックスを変更しない
- 実装: `internal/store/reports.go`
- テスト: `TestSaveReports_DoesNotUpdateEmailIndex`

**AC-10**: 既存 JSON の `report_end_date` を無視して読み込める
- 実装: 変更不要。Go の `encoding/json` は未知フィールドを標準で無視する
- テスト: 不要（標準ライブラリの動作であり、本タスクのスコープ外）

**AC-11**: `DataFileVersion` 変更なし
- 実装: `internal/store/types.go` の `DataFileVersion = 1` 定数を変更しないことで満たす
- テスト: 不要（定数の変更がないことはコードレビューで確認）

**AC-12**: `sweepOrphanedEmailDirs` を削除
- 実装: `internal/store/emails.go`（関数を削除）
- テスト: 関数削除後に `go build ./internal/store/...` が通ること（残存参照はコンパイルエラーとなる）および `make deadcode` で未使用コードが報告されないことで確認

**AC-13**: GC 後の空ディレクトリを削除（失敗は WARN のみ）
- 実装: `internal/store/emails.go`（`DeleteEmailsBefore` 末尾の空ディレクトリ削除）
- テスト: `TestDeleteEmailsBefore_EmptyDirCleanup`、`TestDeleteEmailsBefore_DirCleanupWarn`

**AC-14**: `SaveEmailMetas` の補填ブランチを削除
- 実装: `internal/store/emails.go`
- テスト: `TestSaveEmailMetas_NoPlaceholderUpdate`

**AC-15**: `EmailMeta.SentAt` 削除・`InternalDate` 追加
- 実装: `internal/store/types.go`
- テスト: `TestSaveEmailMetas_BatchInsert`（更新版）で `EmailMeta.InternalDate` を使用することで確認

**AC-16**: `LoadedEmail.SentAt` 削除
- 実装: `internal/store/types.go`
- テスト: `TestLoadEmails_Fields`（更新版）で `SentAt` フィールドの参照を削除し、コンパイルで残存参照を検出

**AC-17**: `internalEmailIndexEntry.SentAt`（JSON: `sent_at`）→ `InternalDate`（JSON: `internal_date`）
- 実装: `internal/store/types.go`
- テスト: `TestSaveEmailMetas_BatchInsert`（更新版）でインデックス永続化後の JSON フィールド名を確認

**AC-18**: `Store.SaveEmail` シグネチャ変更
- 実装: `internal/store/store.go`、`internal/store/emails.go`
- テスト: `TestSaveEmail_CreatesFile`（更新版）等でコンパイルと動作を確認

**AC-19**: `InternalDate` がゼロ値のときエラー
- 実装: `internal/store/emails.go`
- テスト: `TestSaveEmail_ZeroInternalDate_Error`

**AC-20**: `FakeEmailEntry.SentAt` 削除・`InternalDate` 追加、`FakeStore.SaveEmail` シグネチャ変更
- 実装: `internal/store/testutil/mocks.go`
- テスト: `cmd/tlsrpt-digest/main_test.go` の `var _ store.Store = (*storetestutil.FakeStore)(nil)` によるコンパイル確認

---

## 8. 成功基準

- `make fmt && make test && make lint` がすべてエラーなしで完了すること
- 全受け入れ条件（AC-01〜AC-20）に対して実装またはコンパイル確認が存在し、テストが必要な AC はすべて通ること
- `make deadcode` で未使用コードが報告されないこと

---

## 9. 次のステップ

- `02_architecture.md` Section 2.3 のシーケンス図を AC-03 と整合させる（ゼロ値ガードの削除を反映）
- タスク 0070: `cmd/tlsrpt-digest` のエントリポイントを `Store.SaveEmail`（新シグネチャ）・`Store.DeleteEmailsBefore`（新シグネチャ）に接続する
