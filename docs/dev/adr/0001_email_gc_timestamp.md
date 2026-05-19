# ADR-0001: `.eml` ファイルの GC 判定に使用する日時の選択

| 項目 | 内容 |
|---|---|
| 番号 | ADR-0001 |
| ステータス | 採択 |
| 決定日 | 2026-05-19 |
| 関連タスク | 0040_store, 0041_store_gc_simplify |

---

## 背景

`internal/store` は TLSRPT レポートを処理したメール原本を `.eml` ファイルとして保存し、定期的に GC（ガーベジコレクション）で削除する。GC の判定に使える日時として、システム内には以下の 4 種類がある。

| 日時 | 取得元 | 制御主体 |
|---|---|---|
| **送信日時**（SentAt） | メールの `Date:` ヘッダー | 送信側（外部） |
| **IMAP 受信日時**（INTERNALDATE） | IMAP サーバー | メールサーバー（外部） |
| **ダウンロード日時**（SavedAt） | ファイルの inode change time（ctime） | 本システム（ローカル） |
| **レポート期間終了日**（report_end_date） | TLSRPT レポート内の `date-range.end-datetime` | 送信側（外部） |

---

## 議論した問題

### 問題 1: GC 基準の不整合

タスク 0040 の当初設計では、`DeleteEmailsBefore` が 2 種類の削除基準を持っていた。

- **通常削除**：`report_end_date < reportCutoff`
- **強制削除**：`saved_at < savedAtCutoff`

`report_end_date` は TLSRPT レポート送信者が設定する値であり、誤設定や意図的な攻撃（例：year 3000 を設定する）によって遠未来日付になり得る。この場合、通常削除が機能せず `.eml` ファイルが蓄積し続ける。

一方、`saved_at`（ctime）は本システムが記録するローカル制御の値であり、外部から改ざんできない。

### 問題 2: ディレクトリスイープの不整合

孤立 `.eml` ファイルを清掃するため、`sweepOrphanedEmailDirs` がディレクトリ名（`SentAt` 由来の `YYYYMM`）と `savedAtCutoff` の年月を比較してディレクトリ丸ごと削除していた。

しかし `SentAt` と `SavedAt` は異なる月になり得る。例えば、2025 年 1 月送信・2025 年 6 月ダウンロードのメールは `emails/.../202501/` に置かれるが、`savedAtCutoff = 2025-06-01` のスイープは `202501 < 202506` と判定して削除してしまう。`saved_at` の観点では「最近保存したファイル」であり、削除は不適切である。

### 問題 3: date-range バリデーションの配置

遠未来 `report_end_date` 対策として、`internal/tlsrpt` の `Parse` 関数に `end-datetime` の上限チェックを追加する案を検討した。

---

## 検討した選択肢

### 選択肢 A: 両基準を維持しスイープのみ修正する

`sweepOrphanedEmailDirs` をサバイビングインデックスと照合することで誤削除を防ぐ（実際に一時的に実装した）。

**却下理由**: 根本原因（送信側制御の値を GC 判定に使うこと）を解消しておらず、コードの複雑性が残る。`report_end_date` による攻撃ベクターも残存する。

### 選択肢 B: `{YYYYMM}` ディレクトリ名を `SentAt` から `SavedAt` に変更する

パスを `{savedAt.YYYYMM}` ベースにすればスイープの比較が一貫する。

**却下理由**: `{YYYYMM}` を `SentAt` 由来とすることは既存要件（0040 §6.3）で定められており、「古いファイルの手動削除を容易にする」目的のためである。破壊的変更であり、変更コストが大きい。また根本的な問題（送信側制御の値）は `SentAt` にも同様に存在する。

### 選択肢 C: `.eml` GC を `saved_at` 一本化し、スイープを廃止する（採択）

- `DeleteEmailsBefore(savedAtCutoff time.Time)` に一本化する（`reportCutoff` パラメータを削除）
- `report_end_date` をメールインデックスから削除する
- `sweepOrphanedEmailDirs` を廃止する

孤立 `.eml` の清掃は `reprocess` サブコマンド（タスク 0070）が全 `.eml` を再帰走査するため、スイープがなくても自然に回収される。

### 選択肢 D: `internal/tlsrpt.Parse()` に date-range バリデーションを追加する

パース時点で `end-datetime <= now + 48h` などを検証する。

**却下理由**: `internal/tlsrpt` は RFC 8460 JSON を忠実に Go 構造体へ変換する責務を持つ。「この date-range を処理するかどうか」はアプリケーションレベルの判断であり、エントリポイントの責務である（タスク 0070 で対応予定）。パーサーにビジネスロジックを持ち込むと責務が混在し、`now time.Time` を注入する必要も生じてテスト容易性が低下する。

---

## 決定

**選択肢 C を採択する**（タスク 0041_store_gc_simplify として実装）。

| 日時 | `.eml` GC での役割 | レポート GC での役割 |
|---|---|---|
| `SentAt` | ファイルパスの `{YYYYMM}` 決定のみ（変更なし） | 使用しない |
| INTERNALDATE | 使用しない | 使用しない |
| `SavedAt` | **削除判定の唯一の基準** | 使用しない |
| `report_end_date` | **使用しない**（メールインデックスから削除） | 削除判定の基準（変更なし） |

`report_end_date` は引き続きレポートレコード（`tlsrpt.json` の `reports` 配列）の GC（`DeleteReportsBefore`）に使用する。レポートレコードには `saved_at` 相当の日時がなく、「対象期間が終わったデータを集計対象から外す」という意味論が正しいためである。

エントリポイント（タスク 0070）でのレポートレコード保持期間に上限を設けることで、遠未来 `end-datetime` の攻撃に対してシステム全体として対処する。

date-range バリデーションはエントリポイントの責務とし、`internal/tlsrpt` には追加しない。

---

## 結果として生じるトレードオフ

| 得られるもの | 失うもの |
|---|---|
| GC 判定が外部攻撃に対して堅牢になる | 孤立 `.eml` が `reprocess` 実行まで残る場合がある |
| `DeleteEmailsBefore` のロジックが単純化される | スイープによる孤立ファイルの自動清掃がなくなる |
| `SaveReports` がメールインデックスを変更しなくなり責務が明確化される | — |
| `sweepOrphanedEmailDirs` が消え誤削除リスクがなくなる | — |
