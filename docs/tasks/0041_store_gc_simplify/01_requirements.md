# 要件定義書：ストア GC の簡略化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-19 |
| レビュー日 | 2026-05-19 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

タスク 0040 の実装後、`.eml` ファイルの GC（`DeleteEmailsBefore`）に以下の設計上の問題が判明した。

**問題 1: GC 基準の不整合**

`DeleteEmailsBefore` は 2 種類の削除基準を持つ：

- 通常削除：`report_end_date < reportCutoff`（送信側が設定する値）
- 強制削除：`saved_at < savedAtCutoff`（ローカル制御の値）

`report_end_date` は TLSRPT レポート送信者が設定する値であり、遠未来日付（例：year 3000）の誤設定や意図的な攻撃に対して脆弱である。GC の目的は「古いレポートデータを削除する」ことであり、データの古さはダウンロード日時（`saved_at`）ではなく先方の送信日時（`INTERNALDATE`）で計測するのが意味的に正しい。

**問題 2: ディレクトリスイープの不整合**

`sweepOrphanedEmailDirs` はパスの `{YYYYMM}`（`sent_at` 由来）と `savedAtCutoff` を比較して孤立ディレクトリを削除するが、`sent_at` と `saved_at` が異なる月になり得るため（例：2025 年 1 月送信・2025 年 6 月ダウンロード）、保持すべきファイルを誤削除するリスクがある。なお `{YYYYMM}` は本タスク（F-000）で `INTERNALDATE` 由来に変更する。

### 1.2 目的

`.eml` の GC 基準を `INTERNALDATE`（IMAP サーバー受信日時）に統一し、送信側データに依存しない堅牢な GC を実現する。あわせて `{YYYYMM}` パスの決定も `INTERNALDATE` に統一することで、設計全体の一貫性を高める。

---

## 2. スコープ

### 対象範囲（In Scope）

- `SentAt` の廃止と `INTERNALDATE`（`InternalDate` フィールド）への置き換え（F-000）
- `DeleteEmailsBefore` の引数・削除ロジックの簡略化
- メールインデックスから `report_end_date` フィールドを削除
- `SaveReports` からメールインデックス更新ブロック全体（`report_end_date` 更新およびプレースホルダー作成）を削除
- `SaveEmailMetas` のプレースホルダー補填ブランチを削除
- `sweepOrphanedEmailDirs` の廃止と正しい空ディレクトリ削除への置き換え
- `Store` インターフェースの更新

### 対象外（Out of Scope）

- `DeleteReportsBefore`：レポートレコードの GC は引き続き `date-range.end-datetime` 基準とする（変更なし）
- date-range バリデーション：エントリポイントの責務であり、タスク 0070 で対応する
- 既存 `tlsrpt.json` との後方互換性維持およびマイグレーション

### 影響を受けるコンポーネント

- **直接変更**：`internal/store/`、`internal/store/testutil/mocks.go`
- **間接的影響**：`cmd/tlsrpt-digest/`（`DeleteEmailsBefore` シグネチャ変更の呼び出し側）

---

## 3. 機能要件

### F-000: `SentAt` の廃止と `INTERNALDATE` への置き換え（本タスクの前提）

`.eml` のパス決定に用いる日時を、送信側制御の `SentAt`（`Date:` ヘッダー）からサーバー制御の `INTERNALDATE` に置き換え、`SentAt` をシステムから廃止する。`INTERNALDATE` は RFC 3501 必須フィールドであり常に存在するため、フォールバック不要で安定したパスを提供する（ADR-0001 セクション 4.2）。

**受け入れ条件**：

- `AC-15`: `EmailMeta` の `SentAt` フィールドを削除し、`InternalDate time.Time` を追加する
- `AC-16`: `LoadedEmail` の `SentAt` フィールドを削除する（送信日時が必要な場合は `LoadedEmail.Message.Header.Get("Date")` で参照可能）
- `AC-17`: `internalEmailIndexEntry` の `SentAt`（JSON: `sent_at`）を削除し、`InternalDate time.Time`（JSON: `internal_date`）を追加する
- `AC-18`: `Store.SaveEmail` のシグネチャを `SaveEmail(uid, uidValidity uint32, internalDate, savedAt time.Time, rawEML []byte) error` に変更する
- `AC-19`: `.eml` のパス（`{YYYYMM}`）の決定に `InternalDate` を使用する。`InternalDate` がゼロ値の場合は `SavedAt` にフォールバックして `slog.Warn` を出力する（ロバストネス原則）
- `AC-20`: `FakeEmailEntry` の `SentAt` を削除し `InternalDate` を追加する。`FakeStore.SaveEmail` のシグネチャを同様に更新する

### F-001: `DeleteEmailsBefore` の簡略化

`.eml` の GC 基準を `internal_date < cutoff` のみとする。`report_end_date` による削除を廃止する。

**受け入れ条件**：

- `AC-01`: シグネチャを `DeleteEmailsBefore(cutoff time.Time) (deleted int, err error)` とする（`reportCutoff` および `savedAtCutoff` パラメータを削除し、`internal_date` と比較する）
- `AC-02`: `cutoff` がゼロ値の場合、削除を行わず `deleted = 0`、`err = nil` を返す
- `AC-03`: `internal_date < cutoff` を満たすエントリの `.eml` ファイルを削除し、インデックスエントリを除去する
- `AC-04`: 削除対象の `.eml` が既に存在しない場合は非エラーとして扱い、インデックスエントリを除去して処理を継続する（冪等動作）
- `AC-05`: 個別の `.eml` 削除で I/O エラーが発生しても全体を中断せず、成功件数と `errors.Join` で集約したエラーを返す
- `AC-06`: ファイル削除を先に行い、その後インデックスをアトミック更新する（クラッシュ時の孤立 `.eml` を次回実行で自己回復可能にするため）
- `AC-07`: 削除件数は物理ファイルの削除に成功した件数を返す。ファイルが既に存在しない場合（AC-04）も成功とみなしてカウントに含む。インデックス更新失敗時は失敗前の削除件数を返し、保存エラーを集約して返す

### F-002: メールインデックスから `report_end_date` を削除

**受け入れ条件**：

- `AC-08`: `internalEmailIndexEntry` から `report_end_date` フィールドを削除する
- `AC-09`: `SaveReports` からメールインデックス更新ブロック全体を削除する。具体的には `report_end_date` の更新と、エントリが存在しない場合のプレースホルダー作成の両方を削除し、`SaveReports` はレポートデータのみを更新する
- `AC-14`: `SaveEmailMetas` から既存エントリへのプレースホルダー補填ブランチ（`RawEML == nil` ガード付き `InternalDate`/`SavedAt` 補填）を削除する。AC-09 により `SaveReports` がプレースホルダーを作成しなくなるため不要となる

### F-003: `sweepOrphanedEmailDirs` の廃止と空ディレクトリ削除

**受け入れ条件**：

- `AC-12`: `sweepOrphanedEmailDirs` を削除する
- `AC-13`: `DeleteEmailsBefore` はインデックスをアトミック更新した後、GC 対象の `.eml` ファイルが属していた `{uidvalidity}/{YYYYMM}` ディレクトリが空になった場合そのディレクトリを削除し、さらに `{uidvalidity}` ディレクトリも空になった場合そのディレクトリを削除する。ディレクトリ削除の失敗は `slog.Warn` を出力して継続し、関数の戻り値エラーには含めない

---

## 4. 非機能要件

### 保守性

- `Store` インターフェースの `DeleteEmailsBefore` シグネチャ変更に伴い、`FakeStore`（`internal/store/testutil/mocks.go`）も同様に更新すること

---

## 5. テスト方針

- `InternalDate` を用いたパス決定の確認（AC-19）
- `InternalDate` がゼロ値のとき `SavedAt` にフォールバックして WARN ログが出力されることの確認（AC-19）
- `DeleteEmailsBefore` の新シグネチャでの正常系・境界値・エラー系テスト（AC-01〜AC-07）
- ファイルが既に存在しない場合も成功カウントに含まれることの確認（AC-04・AC-07）
- `SaveReports` 呼び出し後にメールインデックスが更新されないことの確認（AC-09）
- `SaveEmailMetas` が既存エントリを補填しないことの確認（AC-14）
- GC 後に空になった `{uidvalidity}/{YYYYMM}` および `{uidvalidity}` ディレクトリが削除されることの確認（AC-13）
- ディレクトリ削除失敗時でも `DeleteEmailsBefore` がエラーを返さないことの確認（AC-13）
