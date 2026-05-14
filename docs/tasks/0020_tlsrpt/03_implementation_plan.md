# 実装計画書：TLSRPTレポートのパース・failure判定

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-14 |
| レビュー日 | 2026-05-14 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 目的

`internal/tlsrpt` パッケージを新規実装し、TLSRPT レポートのバイト列（`.json.gz` または非圧縮 JSON）を受け取り、RFC 8460 仕様に準拠した `*Report` 構造体を返す `ParseGzip()` / `ParseJSON()` 関数と、failure 判定を行う `(*Report).HasFailure()` メソッドを提供する。

設計の詳細は `02_architecture.md` を参照。

### 実装原則

- 外部ライブラリを追加せず、標準ライブラリ（`compress/gzip`・`encoding/json`・`io`）のみを使用する
- `internal/mailparse` の `decodeContent()` が採用するサイズ制限パターンを参考に zip bomb 対策を実装する
- テストは `package tlsrpt_test`（外部テストパッケージ）で記述する
- エラー型の確認には `errors.AsType` を使用し、文字列マッチングは行わない

---

## 2. 実装ステップ

### フェーズ 1: 型定義・エラー型・定数

`02_architecture.md` §3.1・§4.1 の設計に基づいて定義する。

#### 対象ファイル

- `internal/tlsrpt/tlsrpt.go`（新規作成）

#### 作業内容

- [x] `package tlsrpt` 宣言と必要な import の記述
- [x] `maxDecompressedSize` 定数の定義（10 MB = 10 × 1024 × 1024）
- [x] `ErrDecompressedSizeLimitExceeded` 構造体（`Limit`, `Actual int64` フィールド）と `Error() string` の実装
- [x] `ErrMissingRequiredField` 構造体（`Field string` フィールド）と `Error() string` の実装
- [x] `Report`・`DateRange`・`PolicyRecord`・`Policy`・`Summary`・`FailureDetail` 構造体の定義（JSON タグ含む）

#### 完了基準

- `go build ./internal/tlsrpt/...` がエラーなしで通る

#### 所要時間

- 見積: 30 分 / 実績: -

---

### フェーズ 2: ParseGzip() / ParseJSON() の実装

`02_architecture.md` §6.1 の処理フローに従って実装する。

#### 対象ファイル

- `internal/tlsrpt/tlsrpt.go`

#### 作業内容

- [x] `Parse(data []byte) (*Report, error)` 関数のシグネチャ定義
- [x] gzip マジックバイト判定（先頭2バイトが `0x1f, 0x8b` かチェック）
- [x] gzip 展開パスの実装
  - [x] `gzip.NewReader` でリーダーを作成
  - [x] `io.LimitedReader{N: maxDecompressedSize + 1}` でサイズ上限を制御
  - [x] `io.ReadAll` で全バイト読み込み
  - [x] `len(decompressed) > maxDecompressedSize` の場合 `ErrDecompressedSizeLimitExceeded` を返す
  - [x] `gzip.NewReader` のエラーを `fmt.Errorf("tlsrpt: decompress: %w", err)` でラップ
- [x] 非圧縮 JSON パスの実装
  - [x] `len(data) > maxDecompressedSize` のサイズチェック
  - [x] 上限超過時は `ErrDecompressedSizeLimitExceeded` を返す
- [x] `encoding/json.Unmarshal` で RFC 8460 JSON を内部構造体にパース
  - [x] パース失敗時は `fmt.Errorf("tlsrpt: parse json: %w", err)` でラップして返す
- [x] 必須フィールド検証（`OrganizationName`・`ReportID`・`DateRange`・`Policies` の空値チェック）
  - [x] 欠如フィールドがある場合は `ErrMissingRequiredField` を返す
- [x] 検証成功時は `*Report` を返す

#### 完了基準

- `go build ./internal/tlsrpt/...` がエラーなしで通る

#### 所要時間

- 見積: 60 分 / 実績: -

---

### フェーズ 3: ParseGzip() / ParseJSON() のユニットテスト（`AC-01`〜`AC-09`）

#### 対象ファイル

- `internal/tlsrpt/tlsrpt_test.go`（新規作成）

#### 作業内容

- [x] `package tlsrpt_test` として外部テストパッケージを作成
- [x] テスト用ローカルヘルパー関数の実装（テストファイル内）
  - [x] `gzipOf(data []byte) []byte` — バイト列を gzip 圧縮して返す（`compress/gzip` 使用）
  - [x] `minimalValidJSON() []byte` — 最小限の有効 RFC 8460 JSON バイト列（必須フィールドを全て含む）を返す
- [x] `TestParse_GzipValid` — 有効な gzip 圧縮 JSON を入力し、エラーなしで `*Report` が返り、`Report.OrganizationName` など入力 JSON のフィールド値が正しく反映されていることを確認（`AC-01`, `AC-06`）
- [x] `TestParse_PlainJSONValid` — 有効な非圧縮 JSON を入力し、エラーなしで `*Report` が返り、`Report.OrganizationName` など入力 JSON のフィールド値が正しく反映されていることを確認（`AC-02`, `AC-06`）
- [x] `TestParse_InvalidGzip` — 不正な gzip データを入力しエラーが返ることを確認（`AC-03`）
- [x] `TestParse_InvalidJSONAfterDecompress` — 有効な gzip だが展開後 JSON 不正のケースでエラーを確認（`AC-04`）
- [x] `TestParse_SizeLimitExceeded` — gzip・非圧縮両パスでサイズ上限超過時に `ErrDecompressedSizeLimitExceeded` が返ることをサブテストで確認（`AC-05`）
- [x] `TestParse_MissingRequiredField` — 必須フィールド 4 種（`organization-name`・`report-id`・`date-range`・`policies`）を個別に欠如させ、`ErrMissingRequiredField` が返ることをサブテストで確認（`AC-07`）
- [x] `TestParse_PoliciesFields` — `policies` 配列内の各フィールド（`policy-type`・`policy-domain`等）が正しくパースされることを確認（`AC-08`）
- [x] `TestParse_FailureDetails` — `failure-details` フィールドが存在する場合に正しく取得されることを確認（`AC-09`）

#### 完了基準

- `go test -v ./internal/tlsrpt/...` が全テストパス

#### 所要時間

- 見積: 60 分 / 実績: -

---

### フェーズ 4: HasFailure() の実装とテスト（`AC-11`〜`AC-13`）

`02_architecture.md` §6.2 の処理フローに従って実装する。

#### 対象ファイル

- `internal/tlsrpt/tlsrpt.go`
- `internal/tlsrpt/tlsrpt_test.go`

#### 作業内容

- [x] `(*Report).HasFailure() bool` メソッドの実装
  - [x] `r.Policies` を走査し、いずれかの `Summary.TotalFailureSessionCount > 0` の場合 `true` を返す
  - [x] 全て 0 または `Policies` が空の場合は `false` を返す
- [x] `TestHasFailure_AllZero` — 全ポリシーの `TotalFailureSessionCount` が 0 の場合 `false` を確認（`AC-11`）
- [x] `TestHasFailure_AnyNonZero` — いずれかが 1 以上の場合 `true` を確認（`AC-12`）
- [x] `TestHasFailure_EmptyPolicies` — `Policies` が空スライスの場合 `false` を確認（`AC-13`）

#### 完了基準

- `go test -v ./internal/tlsrpt/...` が全テストパス

#### 所要時間

- 見積: 30 分 / 実績: -

---

### フェーズ 5: 統合テスト（`AC-10`）

#### 対象ファイル

- `internal/tlsrpt/tlsrpt_test.go`

#### 作業内容

- [x] `TestParse_RealReport` 統合テストの実装
  - [x] `../../testdata/tlsrpt_google.eml` を `os.ReadFile` で読み込む（`mailparse_test.go` の統合テストパターンに倣う）
  - [x] `net/mail.ReadMessage()` でパース
  - [x] `mailparse.ExtractAttachments()` で全添付ファイルを抽出
  - [x] 全添付ファイルの中から拡張子が `.json.gz` または `.json` のものをフィルタする
  - [x] 対象添付ファイルが 1 件以上あることを `require.NotEmpty` で確認（空の場合はテストデータの前提が崩れているため即失敗させる）
  - [x] 各対象添付ファイルに `ParseGzip()` または `ParseJSON()` を呼び出す（拡張子で判定）
  - [x] エラーなしで `*Report` が返ることを確認（`AC-10`）
  - [x] `Report.OrganizationName` が空でないことを確認

#### 完了基準

- `go test -v -run TestParse_RealReport ./internal/tlsrpt/...` がパス

#### 所要時間

- 見積: 30 分 / 実績: -

---

### フェーズ 6: 品質確認

#### 作業内容

- [x] `make fmt` を実行しフォーマットエラーがないことを確認
- [x] `make lint` を実行し lint エラーがないことを確認
- [x] `make test` で全テストがパスすることを確認

#### 所要時間

- 見積: 15 分 / 実績: -

---

## 3. 実装順序とマイルストーン

| マイルストーン | 対象フェーズ | 成果物 |
|---|---|---|
| M1: 型定義完了 | フェーズ 1 | `tlsrpt.go` の型・エラー型・定数が定義済み |
| M2: パース実装完了 | フェーズ 2・3 | `ParseGzip()` / `ParseJSON()` 実装 + `AC-01`〜`AC-09` のテストがパス |
| M3: failure 判定完了 | フェーズ 4 | `HasFailure()` 実装 + `AC-11`〜`AC-13` のテストがパス |
| M4: 統合テスト完了 | フェーズ 5 | `AC-10` の統合テストがパス |
| M5: 品質確認完了 | フェーズ 6 | lint・fmt・全テストがパス |

**総見積時間**: 約 3.5 時間

---

## 4. テスト戦略

### 単体テスト

- テストファイル: `internal/tlsrpt/tlsrpt_test.go`（`package tlsrpt_test`）
- gzip 圧縮データはテスト内でインメモリ生成する（`compress/gzip` 使用、外部ファイル不要）
- 必須フィールド欠如テストはサブテスト（`t.Run`）を使い、各フィールドを個別に検証する
- `ErrDecompressedSizeLimitExceeded` の確認は `errors.AsType` を使用する

### 統合テスト

- `testdata/tlsrpt_google.eml` を実際のテストデータとして使用する
- `mailparse.ExtractAttachments()` との連携を確認することで `AC-10` を検証する

### セキュリティテスト

- zip bomb テスト: `maxDecompressedSize + 1` バイトのゼロバイト列を gzip 圧縮し、`ParseGzip()` が `ErrDecompressedSizeLimitExceeded` を返すことを確認する（`AC-05`）

### 後方互換性テスト

N/A — `internal/tlsrpt` は新規パッケージであり、既存の呼び出し元が存在しない。後方互換性の考慮は不要。

### テストヘルパーファイルの方針

本タスクではテストヘルパーファイルを追加しない。以下の理由による：

- テスト内ローカルヘルパー（`gzipOf`, `minimalValidJSON`）で十分
- 非公開 API へのアクセスが不要なため Classification B（`test_helpers.go`）は不要
- 他パッケージから `tlsrpt` テストヘルパーを再利用するユースケースがないため Classification A（`testutil/`）も不要

---

## 5. リスク管理

### 技術リスク

| リスク | 影響 | 対策 |
|---|---|---|
| `io.LimitedReader` によるサイズ検出の誤実装 | zip bomb 対策が機能しない | `N: maxDecompressedSize + 1` で読み込み後に `len > maxDecompressedSize` を確認する二重チェックを採用する |
| `testdata/tlsrpt_google.eml` に `.json.gz` 添付が含まれていない | `AC-10` のテストが意図せずスキップされる | テスト内で `require.NotEmpty(t, attachments)` により添付数 > 0 を強制する |
| `DateRange` の空値チェックが難しい（ゼロ値の `time.Time` は空でないため） | 必須フィールド検証漏れ | `DateRange` は `time.Time` のゼロ値（`IsZero()`）ではなく JSON 上の欠如（フィールドの非存在）を検出するためポインタ型でのパースを検討する。または JSON タグ `omitempty` なしでのデコード後 `StartDatetime.IsZero() && EndDatetime.IsZero()` で判定する |

### スケジュールリスク

- 実装対象は単一ファイル（`tlsrpt.go`）であり、実装量が少ないためリスクは低い

---

## 6. 実装チェックリスト

### フェーズ 1
- [x] `maxDecompressedSize` 定数定義済み
- [x] `ErrDecompressedSizeLimitExceeded` 定義済み
- [x] `ErrMissingRequiredField` 定義済み
- [x] 全構造体（`Report`・`DateRange`・`PolicyRecord`・`Policy`・`Summary`・`FailureDetail`）定義済み

### フェーズ 2・3
- [x] `ParseGzip()` / `ParseJSON()` 実装済み
- [x] `AC-01`〜`AC-09` テスト実装済み・パス

### フェーズ 4
- [x] `HasFailure()` 実装済み
- [x] `AC-11`〜`AC-13` テスト実装済み・パス

### フェーズ 5
- [x] `AC-10` 統合テスト実装済み・パス

### フェーズ 6
- [x] `make fmt` エラーなし
- [x] `make lint` エラーなし
- [x] `make test` 全テストパス

---

## 7. 受け入れ条件の検証

`AC-01`: 有効な gzip JSON → `*Report` が返り JSON フィールド値が反映されている
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_GzipValid`
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseGzip()` — gzip 展開パス
- 検証方法: `gzipOf(minimalValidJSON())` を `ParseGzip()` に渡し、エラーなし・`Report.OrganizationName` が期待値と一致することを `assert` で確認

`AC-02`: 有効な非圧縮 JSON → `*Report` が返り JSON フィールド値が反映されている
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_PlainJSONValid`
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseJSON()` — 非圧縮パス
- 検証方法: `minimalValidJSON()` を `ParseJSON()` に渡し、エラーなし・`Report.OrganizationName` が期待値と一致することを `assert` で確認

`AC-03`: 不正 gzip データ → エラー返却
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_InvalidGzip`
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseGzip()` — gzip.NewReader エラー処理
- 検証方法: 不正バイト列（gzip マジックバイトで始まるが壊れたデータ）を入力し、エラーが返ることを `require.Error` で確認

`AC-04`: 展開後 JSON 不正 → エラー返却
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_InvalidJSONAfterDecompress`
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseGzip()` — json.Unmarshal エラー処理
- 検証方法: `gzipOf([]byte("not json"))` を入力し、エラーが返ることを `require.Error` で確認

`AC-05`: サイズ上限超過 → `ErrDecompressedSizeLimitExceeded`
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_SizeLimitExceeded`（gzip・非圧縮サブテスト）
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseGzip()` / `ParseJSON()` — サイズチェック
- 検証方法: `maxDecompressedSize + 1` バイトのデータを各パスで渡し、`errors.AsType[*ErrDecompressedSizeLimitExceeded]` が成功することを確認

`AC-06`: 有効 RFC 8460 JSON → `tlsrpt.Report` 構造体へ正確に変換
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_GzipValid`, `TestParse_PlainJSONValid`
- 実装: `internal/tlsrpt/tlsrpt.go` の `parseJSON()` — json.Unmarshal と構造体定義
- 検証方法: 各フィールドを含む JSON を入力し、返された `Report` の各フィールド値が期待値と一致することを `assert.Equal` で確認

`AC-07`: 必須フィールド欠如 → `ErrMissingRequiredField`
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_MissingRequiredField`（各フィールドのサブテスト）
- 実装: `internal/tlsrpt/tlsrpt.go` の `parseJSON()` — 必須フィールド検証ブロック
- 検証方法: 4 種の必須フィールドを個別に除いた JSON を入力し、`errors.AsType[*ErrMissingRequiredField]` が成功し `Field` が正しいフィールド名であることを確認

`AC-08`: `policies` 配列の各フィールドが正しくパースされる
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_PoliciesFields`
- 実装: `internal/tlsrpt/tlsrpt.go` の `PolicyRecord`・`Policy`・`Summary` 構造体定義と JSON タグ
- 検証方法: `policies` に複数のレコードを含む JSON を入力し、`Report.Policies[0].Policy.PolicyType` 等を `assert.Equal` で確認

`AC-09`: `failure-details` フィールドが正しく取得される
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_FailureDetails`
- 実装: `internal/tlsrpt/tlsrpt.go` の `FailureDetail` 構造体定義と JSON タグ
- 検証方法: `failure-details` を含む JSON を入力し、`Report.Policies[0].FailureDetails` の各フィールドを `assert.Equal` で確認

`AC-10`: 実際のレポートファイルを正しくパースできる
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestParse_RealReport`
- 実装: `internal/tlsrpt/tlsrpt.go` の `ParseGzip()` / `ParseJSON()` 全体
- 検証方法: `testdata/tlsrpt_google.eml` から `.json.gz` 添付を抽出・`ParseGzip()` / `ParseJSON()` で解析し、エラーなし・`Report.OrganizationName` が空でないことを確認

`AC-11`: 全 `total-failure-session-count` が 0 → `HasFailure()` は `false`
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestHasFailure_AllZero`
- 実装: `internal/tlsrpt/tlsrpt.go` の `(*Report).HasFailure()`
- 検証方法: 全ポリシーの `TotalFailureSessionCount` を 0 にした `Report` を構築し、`HasFailure()` が `false` を返すことを `assert.False` で確認

`AC-12`: いずれかの `total-failure-session-count` が 1 以上 → `HasFailure()` は `true`
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestHasFailure_AnyNonZero`
- 実装: `internal/tlsrpt/tlsrpt.go` の `(*Report).HasFailure()`
- 検証方法: 複数ポリシーのうち 1 件だけ `TotalFailureSessionCount` を 1 にした `Report` を構築し、`HasFailure()` が `true` を返すことを `assert.True` で確認

`AC-13`: `Policies` が空 → `HasFailure()` は `false`
- テスト: `internal/tlsrpt/tlsrpt_test.go::TestHasFailure_EmptyPolicies`
- 実装: `internal/tlsrpt/tlsrpt.go` の `(*Report).HasFailure()`
- 検証方法: `Policies` が `nil` または空スライスの `Report` を構築し、`HasFailure()` が `false` を返すことを `assert.False` で確認

---

## 8. 成功基準

### 機能完全性

- `AC-01`〜`AC-13` の全テストがパスすること

### 品質基準

- `make lint` がエラーを報告しないこと
- `make test` で全テストがパスすること
- `make fmt` でフォーマット差分が発生しないこと

### セキュリティ要件

- zip bomb テストが `ErrDecompressedSizeLimitExceeded` を返すことで展開サイズ上限が機能していることを確認

### ドキュメント整合性

- 実装型定義が `02_architecture.md` §3.1 と一致すること

---

## 9. 次のステップ

- `internal/tlsrpt` の実装完了後、`cmd/tlsrpt-digest/main.go` から `ParseGzip()` / `ParseJSON()` を呼び出す統合を別タスク（タスク 0070 エントリポイント）で実施する
