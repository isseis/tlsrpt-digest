# 実装計画書：メール添付ファイルの抽出

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-14 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装概要

### 目的

`internal/mailparse` パッケージを新規作成し、`*mail.Message` から添付ファイルのバイト列とファイル名を抽出する `ExtractAttachments` 関数を実装する。

詳細な設計は [02_architecture.md](02_architecture.md) を参照。

### 実装原則

- プロダクションコードは標準ライブラリのみ使用（外部依存を追加しない）。テストコードでは `stretchr/testify` を使用する（01_requirements.md §5）
- パッケージ外部への公開 API は `ExtractAttachments`・`Attachment`・`ErrSizeLimitExceeded`・`ErrMIMETooDeep` のみ
- Go ソースコード中のコメント・識別子・文字列リテラルは英語で記述する
- `maxBytes` の既定値 1 MB を呼び出し元で設定する作業は本タスクの対象外とし、本タスクでは呼び出し元から渡された上限値に対する挙動を実装・検証する

---

## 2. 実装ステップ

### フェーズ 1: 型・エラー定義

**対象ファイル**: `internal/mailparse/mailparse.go`（新規作成）

- [ ] パッケージ宣言と import 節を記述する
- [ ] `Attachment` 構造体（`Filename string`、`Content []byte`）を定義する
- [ ] `ErrSizeLimitExceeded` 構造体（`Limit int64`、`Actual int64`）と `Error() string` メソッドを定義する
- [ ] `ErrMIMETooDeep` sentinel var を定義する（`errors.New` を使用）
- [ ] `maxMultipartDepth` パッケージ内定数（値: 10）を定義する
- [ ] `ExtractAttachments(msg *mail.Message, maxBytes int64) ([]Attachment, error)` のシグネチャをスタブ実装する（`return nil, nil`）

**成功基準**: `go build ./internal/mailparse/` が通ること

**見積もり工数**: 30 分

**実績工数**: -

---

### フェーズ 2: コア実装（F-001）

**対象ファイル**: `internal/mailparse/mailparse.go`

#### 2-1. Content-Type 解析とトップレベル振り分け

- [ ] `mime.ParseMediaType` で `Content-Type` を解析し、失敗時に `fmt.Errorf("mailparse: parse content-type: %w", err)` を返す（`AC-11`）
- [ ] `mediaType` が `multipart/*` かどうかで処理を振り分ける
- [ ] 非 `multipart` の場合は AC-02 の添付判定を行い、条件を満たさなければ空スライスを返す（AC-05/AC-06）

#### 2-2. multipart 再帰パース

- [ ] `depth` パラメータを持つ内部再帰関数（例: `extractParts`）を定義する
- [ ] `mime/multipart.NewReader` で boundary を用いてパートを分割する（AC-04）
- [ ] boundary 不正などのパースエラーを `fmt.Errorf("mailparse: parse multipart: %w", err)` でラップして返す（AC-11）
- [ ] 各パートの `Content-Type` が `multipart/*` の場合は `depth+1` で再帰する（AC-04）
- [ ] `depth > maxMultipartDepth` の場合は `fmt.Errorf("%w: depth=%d limit=%d", ErrMIMETooDeep, depth, maxMultipartDepth)` を返す（`AC-17`）

#### 2-3. 添付ファイル判定

- [ ] `Content-Disposition` ヘッダを解析して disposition 値（`attachment`/`inline`/省略）を取得する
- [ ] `Content-Disposition: attachment` がある場合は添付とみなす（AC-02）
- [ ] `Content-Disposition` ヘッダが存在せず `Content-Type` に `name` パラメータがある場合は添付とみなす（AC-02）
- [ ] `Content-Disposition: inline` の場合は `name` パラメータの有無にかかわらず添付としない（AC-03）
- [ ] 上記以外は添付としない

#### 2-4. ファイル名抽出

- [ ] `Content-Disposition` の `filename` パラメータを優先的に取得する（AC-08）
- [ ] `filename` がない場合は `Content-Type` の `name` パラメータを使用する（AC-08）
- [ ] どちらにもない場合は `Filename` を空文字列とする（AC-08）
- [ ] `mime.WordDecoder.DecodeHeader` を使い RFC 2047 形式（`=?charset?enc?text?=`）をデコードする（AC-12）
- [ ] RFC 2231 形式（`filename*=UTF-8''...`）は `mime.ParseMediaType` が自動的にデコードするため別途処理不要（AC-09）

#### 2-5. base64 デコード

- [ ] `Content-Transfer-Encoding: base64` のパートは `encoding/base64` でデコードする（AC-07）
- [ ] base64 以外の `Content-Transfer-Encoding` を持つパートはスキップする（エラーなし）（AC-13）
- [ ] base64 デコードに失敗したパートはスキップする（エラーを返さない）（AC-07）

**成功基準**: `go build ./internal/mailparse/` が通ること

**見積もり工数**: 2 時間

**実績工数**: -

---

### フェーズ 3: サイズ制限・深度制限（F-002/F-003）

**対象ファイル**: `internal/mailparse/mailparse.go`

#### 3-1. 累積サイズチェック（AC-14/AC-15/AC-16）

- [ ] `maxBytes <= 0` の場合はサイズ制限なしとする（AC-15）
- [ ] base64 デコードをストリーミングで行い、デコード済みチャンクを読み出すたびに累積サイズを更新する（AC-14）
- [ ] 累積サイズが `maxBytes` を超えた時点で即座に `&ErrSizeLimitExceeded{Limit: maxBytes, Actual: actual}` を返す（AC-14/AC-16）

#### 3-2. MIME ネスト深度制限（AC-17）

- [ ] `extractParts` の再帰呼び出しごとに深度カウンタを更新する
- [ ] `depth > maxMultipartDepth` になった時点で `fmt.Errorf("%w: depth=%d limit=%d", ErrMIMETooDeep, depth, maxMultipartDepth)` を返す

詳細なサイズチェックの設計は [02_architecture.md §6.2](02_architecture.md#62-サイズ上限チェックac-14ac-15) を参照。

**成功基準**: `go build ./internal/mailparse/` が通ること

**見積もり工数**: 30 分

**実績工数**: -

---

### フェーズ 4: テスト

**対象ファイル**: `internal/mailparse/mailparse_test.go`（新規作成）

テストヘルパーファイルは不要。`net/mail.ReadMessage(strings.NewReader(rawEmail))` でテスト用の `*mail.Message` を生成できるため、`test_helpers.go` や `testutil/` サブディレクトリは作成しない。

#### 4-1. 単体テスト（テーブル駆動）

各 AC に対して最低 1 つのテストケースを `TestExtractAttachments` のテーブル駆動テストとして実装する。

- [ ] `Content-Disposition: attachment` を持つパートが抽出される（AC-01/AC-02）
- [ ] `Content-Disposition` なし・`Content-Type name` のみのパートが抽出される（AC-02）
- [ ] `Content-Disposition: inline` かつ `name` ありのパートが除外される（AC-03）
- [ ] `multipart/mixed` から複数の添付ファイルが抽出される（AC-01/AC-04）
- [ ] ネストした `multipart/*` 構造から再帰的に抽出される（AC-04）
- [ ] トップレベルが非 `multipart` かつ添付条件を満たすメールで 1 件が返る（AC-05）
- [ ] プレーンテキストメール（添付条件を満たさない）で空スライスが返る（AC-06）
- [ ] `Content-Transfer-Encoding: base64` の添付ファイルが正しくデコードされる（AC-07）
- [ ] base64 デコード失敗のパートがスキップされ、エラーが返らない（AC-07）
- [ ] ファイル名が `Content-Disposition` の `filename` パラメータから取得される（AC-08）
- [ ] ファイル名が `Content-Type` の `name` パラメータから取得される（`filename` なしの場合）（AC-08）
- [ ] `filename` も `name` もない場合に `Filename` が空文字列になる（AC-08）
- [ ] RFC 2231 形式（`filename*=UTF-8''...`）のファイル名がデコードされる（AC-09）
- [ ] 添付ファイルなしのメールで空スライスが返る（AC-10）
- [ ] 解析不能な `Content-Type` でエラーが返る（AC-11）
- [ ] 不正 boundary でエラーが返る（AC-11）
- [ ] RFC 2047 形式（`=?UTF-8?B?...?=`）のファイル名がデコードされる（AC-12）
- [ ] `Content-Transfer-Encoding: quoted-printable` のパートがスキップされる（エラーなし）（AC-13）
- [ ] 累積サイズが `maxBytes` を超えた時点で `ErrSizeLimitExceeded` が返る（AC-14）
- [ ] `maxBytes = 0` でサイズ制限なしとなる（AC-15）
- [ ] `maxBytes < 0` でサイズ制限なしとなる（AC-15）
- [ ] `errors.AsType[*ErrSizeLimitExceeded]` で `ErrSizeLimitExceeded` が取得できる（AC-16）
- [ ] `ErrSizeLimitExceeded` に正しい `Limit` と `Actual` 値が入る（AC-16）
- [ ] ネスト深度 > 10 のメールで `ErrMIMETooDeep` を含むエラーが返る（AC-17）
- [ ] `errors.Is(err, ErrMIMETooDeep)` が `true` になる（AC-17）

#### 4-2. 統合テスト

**4-2a: ローカル動作確認（`testdata/private/tlsrpt_google.eml` 使用）**

- [ ] `TestExtractAttachments_Integration` を `testdata/private/tlsrpt_google.eml` を読み込んで `ExtractAttachments` に渡し、`.json.gz` バイト列が正しく抽出する形で実装し、ローカルで確認する。
- `testdata/private/tlsrpt_google.eml` は実際のメールのため git には追加しない（`testdata/private/` は `.gitignore` 対象）

**4-2b: 恒久テスト用加工済みデータの作成**

- [ ] `testdata/private/tlsrpt_google.eml` を元に個人情報・機密情報を除去した `testdata/tlsrpt_google.eml` を作成する（加工内容は §4 テスト戦略を参照）
- [ ] `TestExtractAttachments_Integration` を `testdata/tlsrpt_google.eml` を読み込む形に書き換える。
- [ ] `testdata/tlsrpt_google.eml` を git に追加する

#### 4-2c: CI での統合テスト実行確認

- [ ] `testdata/tlsrpt_google.eml` を git に追加した PR の CI (`test` ジョブ) で `TestExtractAttachments_Integration` が通ることを確認する

なお `.github/workflows/ci.yml` の `test` ジョブは `make test`（`go test -v -tags test ./...`）を実行しており、ワークフローの変更は不要。`testdata/private/` は `.gitignore` 対象だが `testdata/tlsrpt_google.eml` はその対象外のため、PR の `check-changes` ジョブがコード変更と判定してテストが実行される。

#### 4-3. セキュリティテスト

- [ ] `maxBytes = 1` などの低い上限値で `ErrSizeLimitExceeded` が返ることを確認する（F-002）
- [ ] RFC 2231 のパストラバーサル的なファイル名（`../etc/passwd`）が本パッケージでサニタイズされずそのまま返ることを確認する（呼び出し元への責務の明確化）
- [ ] 深度 11 の `multipart/*` ネストで `ErrMIMETooDeep` が返ることを確認する（F-003/AC-17）

**成功基準**: `make test` がすべて通ること

**見積もり工数**: 2 時間

**実績工数**: -

---

## 3. 実装順序とマイルストーン

| マイルストーン | 成果物 | 目安工数 |
|---|---|---|
| M1: 型定義完了 | `mailparse.go`（スタブ状態） | 0.5 時間 |
| M2: コア実装完了 | `mailparse.go`（フェーズ 2 まで） | 2.5 時間 |
| M3: セキュリティ制限完了 | `mailparse.go`（フェーズ 3 まで） | 3 時間 |
| M4: テスト完了・全 AC 検証済み | `mailparse_test.go`、`make test` 通過 | 5 時間 |

---

## 4. テスト戦略

### 単体テスト方針

- テーブル駆動テスト（`[]struct{ name, input, want }`）を `TestExtractAttachments` に集約する
- インライン MIME メッセージ文字列から `net/mail.ReadMessage` で `*mail.Message` を生成する
- `github.com/stretchr/testify/require` でアサーションを行う

### 統合テスト方針

テストデータは **2 段階** で管理する。

**フェーズ 4-2a（動作確認用）**: `testdata/private/tlsrpt_google.eml` を使って実装が正しく動くことをローカルで確認する。このファイルは実際のメールのため **git リポジトリには追加しない**（`.gitignore` の `testdata/private/` ディレクトリで除外）。

**フェーズ 4-2b（恒久テスト用）**: 動作確認後、`testdata/private/tlsrpt_google.eml` から個人情報・機密情報を除去した加工済みファイルを `testdata/tlsrpt_google.eml` として作成し、こちらを git に追加する。テストコードはこの加工済みファイルを使用する。

加工の内容：
- メールアドレスを `@example.com` ドメインに置換
- メールヘッダ中の実際のメールサーバー名・ホスト名を `mail.example.com` 等に置換
- IP アドレスを `192.0.2.x`（TEST-NET-1）に置換
- 添付ファイル名のドメイン部分を `example.com` に置換
- base64 エンコードされた添付ファイルの中身は **変更しない**（gzip/JSON 構造を維持するため）

統合テストの確認内容：
- 返された `[]Attachment` のファイル名が期待値と一致すること
- 返された `Content` が非空で、gzip ヘッダ（`\x1f\x8b`）で始まること
- ビルドタグは不要（`make test` で実行）

### テストヘルパーファイル

新規のテストヘルパーファイルは作成しない。

- `*mail.Message` の生成は `net/mail.ReadMessage(strings.NewReader(...))` で十分
- `internal/mailparse` はインターフェースへの依存がなく、モックや `testutil/` サブディレクトリは不要

---

## 5. リスク管理

| リスク | 発生確率 | 影響度 | 軽減策 |
|---|---|---|---|
| RFC 2047 と RFC 2231 の混在ファイル名の挙動が不定 | 低 | 中 | `mime.WordDecoder` で RFC 2047 を試み、失敗時はそのまま使用する。実 `.eml` で動作を確認する |
| `mime/multipart` の `NextPart` が EOF 以外でエラーを返す条件の見落とし | 低 | 中 | `io.EOF` のみをループ終了とし、それ以外はエラーとして伝搬する |
| base64 のストリーミングデコード中のサイズチェックで `io.Reader` ラッパーが複雑になる | 中 | 低 | `io.LimitReader` + 手動読み取りループで実装し、超過時に `ErrSizeLimitExceeded` を返す |
| `maxBytes` の既定値 1 MB を本パッケージ側で持ってしまい API 責務が曖昧になる | 中 | 中 | 本タスクでは `maxBytes` 引数の挙動のみ実装し、運用上の既定値設定は `cmd/tlsrpt-digest` 組み込みタスクで明示する |

---

## 6. 実装チェックリスト

### フェーズ 1
- [ ] `internal/mailparse/mailparse.go` を新規作成
- [ ] `Attachment`・`ErrSizeLimitExceeded`・`ErrMIMETooDeep` を定義
- [ ] `ExtractAttachments` スタブを定義
- [ ] `go build` が通ること

### フェーズ 2
- [ ] Content-Type 解析・トップレベル振り分けを実装
- [ ] multipart 再帰パースを実装
- [ ] 添付ファイル判定ロジックを実装
- [ ] ファイル名抽出（RFC 2047/2231 対応）を実装
- [ ] base64 デコードを実装
- [ ] `go build` が通ること

### フェーズ 3
- [ ] 累積サイズチェック（ストリーミング）を実装
- [ ] MIME ネスト深度制限を実装
- [ ] `maxBytes <= 0` で無制限となることを実装
- [ ] `go build` が通ること

### フェーズ 4
- [ ] 全 AC に対応する単体テストを実装
- 任意: `testdata/private/tlsrpt_google.eml` でローカル動作確認（4-2a）
- [ ] 加工済み `testdata/tlsrpt_google.eml` を作成して git に追加（4-2b）
- [ ] 統合テストを加工済みファイルに切り替え
- [ ] セキュリティテストを実装
- [ ] `make test` がすべて通ること
- [ ] `make lint` がすべて通ること
- [ ] `make fmt` 適用後に差分がないこと

---

## 7. 受け入れ条件検証

| AC | 検証テスト | 実装箇所（予定） |
|---|---|---|
| **AC-01**: `[]Attachment` を返す。各 `Attachment` は `Filename`・`Content` を持つ | `TestExtractAttachments/multipart_mixed_attachment` | `mailparse.go: ExtractAttachments` |
| **AC-02**: `Content-Disposition: attachment` または `Content-Disposition` なし + `Content-Type name` で添付とみなす | `TestExtractAttachments/content_disposition_attachment`<br>`TestExtractAttachments/no_disposition_with_name` | `mailparse.go: isAttachment` |
| **AC-03**: `Content-Disposition: inline` は `name` ありでも除外 | `TestExtractAttachments/inline_with_name_skipped` | `mailparse.go: isAttachment` |
| **AC-04**: `multipart/*` を再帰的に抽出 | `TestExtractAttachments/nested_multipart` | `mailparse.go: extractParts` |
| **AC-05**: トップレベル非 multipart かつ添付条件を満たす場合に 1 件返す | `TestExtractAttachments/toplevel_non_multipart_attachment` | `mailparse.go: ExtractAttachments` |
| **AC-06**: トップレベル非 multipart かつ条件を満たさない場合に空スライスを返す | `TestExtractAttachments/plaintext_returns_empty` | `mailparse.go: ExtractAttachments` |
| **AC-07**: base64 のデコード、デコード失敗のスキップ | `TestExtractAttachments/base64_decoded`<br>`TestExtractAttachments/base64_decode_failure_skipped` | `mailparse.go: decodeBase64Streaming` |
| **AC-08**: ファイル名解決の優先順位 (`filename` > `name` > 空文字) | `TestExtractAttachments/filename_from_disposition`<br>`TestExtractAttachments/filename_from_content_type`<br>`TestExtractAttachments/filename_empty` | `mailparse.go: resolveFilename` |
| **AC-09**: RFC 2231 ファイル名のデコード | `TestExtractAttachments/rfc2231_filename` | `mailparse.go: resolveFilename` |
| **AC-10**: 添付ファイルなしで空スライスを返す | `TestExtractAttachments/no_attachments_empty_slice` | `mailparse.go: ExtractAttachments` |
| **AC-11**: 解析不能 `Content-Type`・不正 boundary でエラーを返す | `TestExtractAttachments/invalid_content_type_error`<br>`TestExtractAttachments/invalid_boundary_error` | `mailparse.go: ExtractAttachments` |
| **AC-12**: RFC 2047 ファイル名のデコード | `TestExtractAttachments/rfc2047_filename` | `mailparse.go: resolveFilename` |
| **AC-13**: base64 以外の `Content-Transfer-Encoding` をスキップ | `TestExtractAttachments/non_base64_encoding_skipped` | `mailparse.go: extractParts` |
| **AC-14**: サイズ超過時点で処理を中断し `ErrSizeLimitExceeded` を返す | `TestExtractAttachments/size_limit_exceeded` | `mailparse.go: decodeBase64Streaming` |
| **AC-15**: `maxBytes <= 0` で上限なし | `TestExtractAttachments/zero_max_bytes_no_limit`<br>`TestExtractAttachments/negative_max_bytes_no_limit` | `mailparse.go: decodeBase64Streaming` |
| **AC-16**: `ErrSizeLimitExceeded` に `Limit`・`Actual` が設定される | `TestExtractAttachments/size_limit_exceeded_error_fields` | `mailparse.go: decodeBase64Streaming` |
| **AC-17**: ネスト深度 > 10 で `ErrMIMETooDeep` を含むエラーを返す | `TestExtractAttachments/nesting_too_deep` | `mailparse.go: extractParts` |

### 統合テスト

| シナリオ | テスト名 | 検証内容 |
|---|---|---|
| 実際の TLSRPT レポートメール（加工済み） | `TestExtractAttachments_Integration` | `testdata/tlsrpt_google.eml` から `.json.gz` が抽出できる |

---

## 8. 成功基準

### 機能完全性

- [ ] 全 17 件の受け入れ条件（AC-01 〜 AC-17）を検証するテストが存在し、全テストが通ること

### 品質指標

- [ ] `make test` がすべて通ること（単体テスト・統合テスト含む）
- [ ] `make lint` がすべて通ること
- [ ] `make fmt` 適用後に差分がないこと

### セキュリティ検証

- [ ] 累積サイズ上限超過テストが通ること（F-002）
- [ ] ネスト深度上限超過テストが通ること（F-003/AC-17）

### ドキュメント完全性

- [ ] 任意手順を除く本実装計画書の全チェックボックスが完了状態になること

---

## 9. 次のステップ

実装完了後に実施すること：

1. `cmd/tlsrpt-digest` に `ExtractAttachments` の呼び出しを組み込む（別タスクとして計画）
   - このタスクで `maxBytes` の運用上の既定値 1 MB を設定し、F-002 の既定値要件を満たす
2. 新しい送信元の TLSRPT メールが届いた場合は `testdata/private/` に保存し、加工済みデータを `testdata/` に追加する
3. `03_implementation_plan.md` の受け入れ条件検証表に実装箇所の最終的な行番号を記入する
