# 要件定義書：TLSRPTレポートのパース・failure判定

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

TLSRPT レポートメールには .json.gz 形式の添付ファイルが含まれる。
このタスクでは `internal/tlsrpt` パッケージを実装し、添付ファイルの展開・RFC 8460 JSON のパース・failure_session_count の評価を担う。

### 1.2 目的

1. **主目的**: .json.gz 添付ファイルを展開し、RFC 8460 準拠の JSON を Go の構造体に変換する
2. **副次的目的**: failure_session_count を評価して即時アラートの必要性を判断する

---

## 2. スコープ

### 対象範囲（In Scope）

- .json.gz バイト列の gzip 展開
- RFC 8460 JSON のパース（`TLSRPTReport` 構造体への変換）
- failure_session_count の集計と評価
- パース失敗時のエラーハンドリング

### 対象外（Out of Scope）

- メールの取得（`internal/imap` が担当）
- 通知の送信（`internal/notify` が担当）
- データの蓄積（`internal/store` が担当）

### 影響を受けるコンポーネント

- **直接変更**: `internal/tlsrpt/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（パース結果の利用側）

---

## 3. 機能要件

### F-001: .json.gz の展開

gzip 圧縮された JSON バイト列を展開する。

**受け入れ条件（Acceptance Criteria）**:

1. 有効な .json.gz バイト列を入力すると、展開された JSON バイト列を返す
2. 不正な gzip データを入力するとエラーを返す
3. 展開後のデータが有効な JSON でない場合もエラーを返す

### F-002: RFC 8460 JSON のパース

展開した JSON を RFC 8460 仕様に従ってパースし、Go 構造体に変換する。

**受け入れ条件（Acceptance Criteria）**:

1. 有効な RFC 8460 JSON を正しく `TLSRPTReport` 構造体に変換する
2. 必須フィールド（`organization-name`、`date-range`、`policies`）が欠如している場合はエラーを返す
3. `policies` 配列内の各ポリシーレコードが正しくパースされる
4. `failure-details` フィールドが存在する場合、正しく取得できる
5. `testdata/` 内の実際のレポートファイルを正しくパースできる

### F-003: failure_session_count の評価

レポート内のすべてのポリシーレコードにわたる failure_session_count を評価する。

**受け入れ条件（Acceptance Criteria）**:

1. すべてのポリシーレコードの `failure-session-count` の合計が 0 の場合、`HasFailure()` は `false` を返す
2. いずれかのポリシーレコードの `failure-session-count` が 1 以上の場合、`HasFailure()` は `true` を返す
3. `policies` が空の場合、`HasFailure()` は `false` を返す

---

## 4. 非機能要件

### パフォーマンス

- 1 件のレポート（通常数 KB〜数百 KB）のパースは 1 秒以内に完了すること

### セキュリティ

- gzip 展開時の zip bomb 攻撃に対して展開サイズの上限を設ける

### 保守性

- RFC 8460 の主要フィールドをカバーする明確な構造体定義

---

## 5. 制約

- 使用言語は Go とする（Go 1.23 以上）
- RFC 8460 の JSON フィールド名はケバブケース（`failure-session-count` 等）のため、構造体タグで対応する
- テストには `stretchr/testify` を使用する

---

## 6. テスト方針

### 単体テスト

- 有効な .json.gz データのパーステスト
- 不正データ（壊れた gzip、不正 JSON、必須フィールド欠如）のエラーテスト
- `HasFailure()` の境界値テスト（0件、1件、複数件）

### 統合テスト

- `testdata/` 内の実際のレポートファイルを使ったエンドツーエンドパーステスト
