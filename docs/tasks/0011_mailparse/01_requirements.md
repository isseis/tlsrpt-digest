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
- `Content-Transfer-Encoding: base64` のデコード
- `Content-Disposition: attachment` および `Content-Type` の `name` パラメータからのファイル名取得
- ファイル名の RFC 2231 エンコード（`filename*=UTF-8''...` 形式）のデコード
- ファイル名の RFC 2047 エンコード（`=?charset?encoding?text?=` 形式）のデコード
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

- **AC-01**: `*mail.Message` を受け取り、`[]Attachment` を返す。各 `Attachment` はファイル名（`Filename string`）と内容（`Content []byte`）を持つ
- **AC-02**: 以下のいずれかを満たすパートを添付ファイルとみなす：`Content-Disposition: attachment` がある、または `Content-Disposition` ヘッダが存在せず `Content-Type` に `name` パラメータがある
- **AC-03**: `Content-Disposition: inline` のパートは `Content-Type` に `name` パラメータがある場合でも添付ファイルとして扱わない
- **AC-04**: `multipart/*` 形式のメールに含まれる全添付ファイルを再帰的に抽出する
- **AC-05**: トップレベルが非 `multipart` のメール（例：`Content-Type: application/gzip; name="report.json.gz"` 単体）は、AC-02 の条件を満たす場合に限り 1 件の添付ファイルとして扱う
- **AC-06**: トップレベルが非 `multipart` かつ AC-02 の条件を満たさないメール（例：プレーンテキスト）は、空のスライスを返す（エラーにしない）
- **AC-07**: `Content-Transfer-Encoding: base64` でエンコードされた添付ファイルを正しくデコードする
- **AC-08**: ファイル名は `Content-Disposition` の `filename` パラメータを優先し、なければ `Content-Type` の `name` パラメータを使用する。どちらにもない場合は `Filename` を空文字列とする
- **AC-09**: ファイル名が RFC 2231 形式（`filename*=UTF-8''...`）でエンコードされている場合、正しくデコードする
- **AC-10**: 添付ファイルが存在しない場合、空のスライスを返す（エラーにしない）
- **AC-11**: `Content-Type` ヘッダが解析不能、または `multipart/*` の boundary が不正な場合はエラーを返す
- **AC-12**: ファイル名が RFC 2047 形式（`=?charset?encoding?text?=`）でエンコードされている場合、正しくデコードする
- **AC-13**: `Content-Transfer-Encoding` が `base64` 以外のパートは、添付ファイルとして識別されても内容のデコードを行わずスキップする（エラーにしない）

### F-002: 抽出サイズの上限

メモリ枯渇を防ぐため、1 メッセージから抽出する添付ファイルの総バイト数に上限を設ける。

**受け入れ条件（Acceptance Criteria）**:

- **AC-14**: 全添付ファイルの**デコード後**バイト列の合計サイズが上限（デフォルト 1 MB）を超えた場合、エラーを返す。上限チェックはデコードしながら逐次的に行い、超過時点で処理を中断する
- **AC-15**: 上限値は呼び出し元から指定可能とする（0 以下の場合は上限なし）
- **AC-16**: 上限超過の場合は専用のエラー型（`ErrSizeLimitExceeded`）を返す

---

## 4. 非機能要件

### パフォーマンス

- 通常の TLSRPT レポートメール（添付ファイル数十 KB 程度）のパースは 100 ms 以内に完了すること

### セキュリティ

- F-002 のサイズ上限により、巨大な添付ファイルによるメモリ枯渇を防ぐ
- 本パッケージは RFC 2231・RFC 2047 デコード後のファイル名をそのまま返し、パス区切り文字の除去等のサニタイズは行わない（パストラバーサル対策は利用側の責任とする）

### 保守性

- 外部ライブラリへの依存を追加せず、標準ライブラリのみで実装する

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- プロダクションコードは標準ライブラリのみで実装する（外部ライブラリへの依存を追加しない）
- テストコードでは `stretchr/testify` を使用する

---

## 6. テスト方針

### 単体テスト

- `multipart/mixed` メールからの添付ファイル抽出テスト
- ネストした `multipart/*` 構造からの抽出テスト
- `Content-Disposition` なし・`Content-Type name` パラメータのみのパート抽出テスト
- `Content-Disposition: inline` パートが除外されるテスト
- トップレベルが非 `multipart` でかつ添付条件を満たすメールのテスト（AC-05）
- プレーンテキストメールに対する空スライス返却テスト（AC-06）
- base64 エンコードされた添付ファイルのデコードテスト
- RFC 2231 エンコードされたファイル名のデコードテスト
- RFC 2047 エンコードされたファイル名のデコードテスト（AC-12）
- `base64` 以外の `Content-Transfer-Encoding` を持つパートがスキップされるテスト（AC-13）
- サイズ上限超過時の `ErrSizeLimitExceeded` テスト
- 不正な MIME boundary・解析不能 `Content-Type` に対するエラーテスト

### 統合テスト

- `testdata/` に格納した実際の TLSRPT レポートメール（`.eml`）を使った抽出テスト
