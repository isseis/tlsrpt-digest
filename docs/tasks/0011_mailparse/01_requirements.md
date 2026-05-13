# 要件定義書：メール添付ファイルの抽出

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-13 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

`internal/imap` の `Download` メソッドは `*mail.Message`（`net/mail` パッケージ）を返す。
`*mail.Message` のボディは MIME マルチパート形式の生ストリームであり、`.json.gz` 添付ファイルのバイト列を取り出すには MIME パース処理が必要となる。

一方、`internal/tlsrpt` は `.json.gz` バイト列を入力として受け取る設計である。
このギャップを埋める処理をどちらのパッケージにも帰属させず、専用の `internal/mailparse` パッケージとして切り出す。

### 1.2 目的

1. **主目的**: `*mail.Message` から添付ファイルのバイト列とファイル名を取り出す
2. **副次的目的**: IMAP・TLSRPT の両パッケージから MIME パース処理を分離し、単一責任の原則を維持する

---

## 2. スコープ

### 対象範囲（In Scope）

- `multipart/mixed` および `multipart/related` などのネストした MIME 構造の解析
- `Content-Transfer-Encoding`（base64・quoted-printable・7bit・8bit）のデコード
- `Content-Disposition: attachment` および `Content-Type` の `name` パラメータからのファイル名取得
- ファイル名の RFC 2231 エンコード（`filename*=UTF-8''...` 形式）のデコード
- 添付ファイルのバイト列と対応するファイル名を返す

### 対象外（Out of Scope）

- IMAP による メッセージダウンロード（`internal/imap` が担当）
- `.json.gz` の gzip 展開・JSON パース（`internal/tlsrpt` が担当）
- ファイル名によるフィルタリング（呼び出し元が担当）

### 影響を受けるコンポーネント

- **直接変更**: `internal/mailparse/`（新規作成）
- **間接的影響**: `cmd/tlsrpt-digest/`（添付ファイル抽出の利用側）

---

## 3. 機能要件

### F-001: 添付ファイルの抽出

`*mail.Message` から全添付ファイルのバイト列とファイル名を取り出す。

**受け入れ条件（Acceptance Criteria）**:

1. `*mail.Message` を受け取り、`[]Attachment` を返す。各 `Attachment` はファイル名（`Filename string`）と内容（`Content []byte`）を持つ
2. `multipart/mixed` 形式のメールに含まれる添付ファイルをすべて抽出できる
3. ネストした `multipart/*` 構造（例：`multipart/related` 内に `multipart/mixed`）でも正しく抽出できる
4. `Content-Transfer-Encoding: base64` でエンコードされた添付ファイルを正しくデコードする
5. `Content-Disposition: attachment; filename="..."` からファイル名を取得する
6. `Content-Disposition` が存在しない場合、`Content-Type` の `name` パラメータをファイル名として使用する
7. ファイル名が RFC 2231 形式（`filename*=UTF-8''...`）でエンコードされている場合、正しくデコードする
8. 添付ファイルが存在しない場合、空のスライスを返す（エラーにしない）
9. MIME パースに失敗した場合はエラーを返す

### F-002: 抽出サイズの上限

メモリ枯渇を防ぐため、1 メッセージから抽出する添付ファイルの総バイト数に上限を設ける。

**受け入れ条件（Acceptance Criteria）**:

1. 全添付ファイルの合計サイズが上限（デフォルト 50 MB）を超えた場合、エラーを返す
2. 上限値は呼び出し元から指定可能とする（0 以下の場合は上限なし）
3. 上限超過の場合は専用のエラー型（`ErrSizeLimitExceeded`）を返す

---

## 4. 非機能要件

### パフォーマンス

- 通常の TLSRPT レポートメール（添付ファイル数十 KB 程度）のパースは 100 ms 以内に完了すること

### セキュリティ

- F-002 のサイズ上限により、巨大な添付ファイルによるメモリ枯渇を防ぐ
- ファイル名のデコード結果をパスとして使用しない（パストラバーサル対策は利用側の責任とするが、本パッケージはファイル名をそのまま返すのみで保存処理を行わない）

### 保守性

- `net/mail` および標準ライブラリの `mime/multipart`・`mime` のみに依存し、外部ライブラリへの依存を追加しない

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- 外部ライブラリは使用せず、標準ライブラリ（`mime/multipart`・`mime`・`net/mail`）のみで実装する
- テストには `stretchr/testify` を使用する

---

## 6. テスト方針

### 単体テスト

- `multipart/mixed` メールからの添付ファイル抽出テスト
- ネストした `multipart/*` 構造からの抽出テスト
- base64 エンコードされた添付ファイルのデコードテスト
- RFC 2231 エンコードされたファイル名のデコードテスト
- 添付ファイルなしメールに対する空スライス返却テスト
- サイズ上限超過時の `ErrSizeLimitExceeded` テスト
- 不正な MIME 構造に対するエラーテスト

### 統合テスト

- `testdata/` に格納した実際の TLSRPT レポートメール（`.eml`）を使った抽出テスト
