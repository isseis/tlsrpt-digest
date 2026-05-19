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

`report_end_date` は TLSRPT レポート送信者が設定する値であり、遠未来日付（例：year 3000）の誤設定や意図的な攻撃に対して脆弱である。一方 `saved_at`（ダウンロード日時）は本システムが記録するローカル制御の値であり、信頼性が高い。

**問題 2: ディレクトリスイープの不整合**

`sweepOrphanedEmailDirs` はパスの `{YYYYMM}`（`sent_at` 由来）と `savedAtCutoff` を比較して孤立ディレクトリを削除するが、`sent_at` と `saved_at` が異なる月になり得るため（例：2025 年 1 月送信・2025 年 6 月ダウンロード）、保持すべきファイルを誤削除するリスクがある。

### 1.2 目的

`.eml` の GC 基準をローカル制御可能な `saved_at` のみに統一し、送信側データに依存しない堅牢な GC を実現する。

---

## 2. スコープ

### 対象範囲（In Scope）

- `DeleteEmailsBefore` の引数・削除ロジックの簡略化
- メールインデックスから `report_end_date` フィールドを削除
- `SaveReports` からメールインデックス更新ロジックを削除
- `sweepOrphanedEmailDirs` の廃止
- `Store` インターフェースの更新

### 対象外（Out of Scope）

- `DeleteReportsBefore`：レポートレコードの GC は引き続き `date-range.end-datetime` 基準とする（変更なし）
- date-range バリデーション：エントリポイントの責務であり、タスク 0070 で対応する
- データファイルのスキーマバージョン変更：後述のとおり後方互換の変更のため不要

### 影響を受けるコンポーネント

- **直接変更**：`internal/store/`
- **間接的影響**：`cmd/tlsrpt-digest/`（`DeleteEmailsBefore` シグネチャ変更の呼び出し側）

---

## 3. 機能要件

### F-001: `DeleteEmailsBefore` の簡略化

`.eml` の GC 基準を `saved_at < savedAtCutoff` のみとする。`report_end_date` による通常削除を廃止する。

**受け入れ条件**：

- `AC-01`: シグネチャを `DeleteEmailsBefore(savedAtCutoff time.Time) (deleted int, err error)` に変更する（`reportCutoff` パラメータを削除）
- `AC-02`: `savedAtCutoff` がゼロ値の場合、削除を行わず `deleted = 0`、`err = nil` を返す
- `AC-03`: `saved_at != zero && saved_at < savedAtCutoff` を満たすエントリの `.eml` ファイルを削除し、インデックスエントリを除去する。`saved_at` がゼロのエントリ（`SaveEmailMetas` 未実行のプレースホルダー）は削除対象外とする
- `AC-04`: 削除対象の `.eml` が既に存在しない場合は非エラーとして扱い、インデックスエントリを除去して処理を継続する（冪等動作）
- `AC-05`: 個別の `.eml` 削除で I/O エラーが発生しても全体を中断せず、成功件数と `errors.Join` で集約したエラーを返す
- `AC-06`: ファイル削除を先に行い、その後インデックスをアトミック更新する（クラッシュ時の孤立 `.eml` を次回実行で自己回復可能にするため）
- `AC-07`: 削除件数は物理ファイルの削除に成功した件数（ファイル不在の場合も含む）を返す。インデックス更新失敗時は失敗前の削除件数を返し、保存エラーを集約して返す

### F-002: メールインデックスから `report_end_date` を削除

**受け入れ条件**：

- `AC-08`: `internalEmailIndexEntry` から `report_end_date` フィールドを削除する
- `AC-09`: `SaveReports` はメールインデックスを更新しない（`report_end_date` 更新ロジックを削除する）
- `AC-10`: 既存の `tlsrpt.json` に `report_end_date` フィールドが含まれていても、読み込み時に無視される（`encoding/json` の標準動作により後方互換性を保つ）
- `AC-11`: `DataFileVersion` は変更しない。フィールド削除は後方互換の変更であり、既存ファイルの読み込みに影響しないため、バージョン変更・マイグレーション処理は不要とする

### F-003: `sweepOrphanedEmailDirs` の廃止

**受け入れ条件**：

- `AC-12`: `sweepOrphanedEmailDirs` を削除する
- `AC-13`: `DeleteEmailsBefore` はディレクトリ単位のスイープを行わない

---

## 4. 非機能要件

### 後方互換性

- 既存の `tlsrpt.json`（`report_end_date` フィールドを含む）を持つ環境でも、データ損失なく動作すること

### 保守性

- `Store` インターフェースの `DeleteEmailsBefore` シグネチャ変更に伴い、タスク 0040 Phase 4 で実装予定の `FakeStore` も同様に更新すること

---

## 5. テスト方針

- `DeleteEmailsBefore` の新シグネチャでの正常系・境界値・エラー系テスト
- `saved_at` がゼロのプレースホルダーエントリが削除されないことの確認
- `SaveReports` 呼び出し後にメールインデックスが更新されないことの確認
- 既存の `tlsrpt.json`（`report_end_date` フィールド付き）を読み込んでもエラーにならないことの確認
- `DeleteEmailsBefore` がスイープを行わないことの確認
