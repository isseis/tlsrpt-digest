# 実装計画書：TLSRPTレポートのパース・failure判定

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

`internal/tlsrpt` パッケージを新規実装し、TLSRPT レポートのバイト列（`.json.gz` または非圧縮 JSON）を受け取り、RFC 8460 仕様に準拠した `*Report` 構造体を返す `Parse()` 関数と、failure 判定を行う `(*Report).HasFailure()` メソッドを提供する。

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

- [ ] `package tlsrpt` 宣言と必要な import の記述
- [ ] `maxDecompressedSize` 定数の定義（10 MB = 10 × 1024 × 1024）
- [ ] `ErrDecompressedSizeLimitExceeded` 構造体（`Limit`, `Actual int64` フィールド）と `Error() string` の実装
- [ ] `ErrMissingRequiredField` 構造体（`Field string` フィールド）と `Error() string` の実装
- [ ] `Report`・`DateRange`・`PolicyRecord`・`Policy`・`Summary`・`FailureDetail` 構造体の定義（JSON タグ含む）

#### 完了基準

- `go build ./internal/tlsrpt/...` がエラーなしで通る

---

### フェーズ 2: Parse() の実装

`02_architecture.md` §6.1 の処理フローに従って実装する。

#### 対象ファイル

- `internal/tlsrpt/tlsrpt.go`

#### 作業内容

- [ ] `Parse(data []byte) (*Report, error)` 関数のシグネチャ定義
- [ ] gzip マジックバイト判定（先頭2バイトが `0x1f, 0x8b` かチェック）
- [ ] gzip 展開パスの実装
  - [ ] `gzip.NewReader` でリーダーを作成
  - [ ] `io.LimitedReader{N: maxDecompressedSize + 1}` でサイズ上限を制御
  - [ ] `io.ReadAll` で全バイト読み込み
  - [ ] `len(decompressed) > maxDecompressedSize` の場合 `ErrDecompressedSizeLimitExceeded` を返す
  - [ ] `gzip.NewReader` のエラーを `fmt.Errorf("tlsrpt: decompress: %w", err)` でラップ
- [ ] 非圧縮 JSON パスの実装
  - [ ] `len(data) > maxDecompressedSize` のサイズチェック
  - [ ] 上限超過時は `ErrDecompressedSizeLimitExceeded` を返す
- [ ] `encoding/json.Unmarshal` で RFC 8460 JSON を内部構造体にパース
  - [ ] パース失敗時は `fmt.Errorf("tlsrpt: parse json: %w", err)` でラップして返す
- [ ] 必須フィールド検証（`OrganizationName`・`ReportID`・`DateRange`・`Policies` の空値チェック）
  - [ ] 欠如フィールドがある場合は `ErrMissingRequiredField` を返す
- [ ] 検証成功時は `*Report` を返す

#### 完了基準

- `go build ./internal/tlsrpt/...` がエラーなしで通る

---

### フェーズ 3: Parse() のユニットテスト（`AC-01`〜`AC-09`）

#### 対象ファイル

- `internal/tlsrpt/tlsrpt_test.go`（新規作成）

#### 作業内容

- [ ] `package tlsrpt_test` として外部テストパッケージを作成
- [ ] テスト用ローカルヘルパー関数の実装（テストファイル内）
  - [ ] `gzipOf(data []byte) []byte` — バイト列を gzip 圧縮して返す（`compress/gzip` 使用）
  - [ ] `minimalValidJSON() []byte` — 最小限の有効 RFC 8460 JSON バイト列（必須フィールドを全て含む）を返す
- [ ] `TestParse_GzipValid` — 有効な gzip 圧縮 JSON を入力し `*Report` が返ることを確認（`AC-01`, `AC-06`）
- [ ] `TestParse_PlainJSONValid` — 有効な非圧縮 JSON を入力し `*Report` が返ることを確認（`AC-02`, `AC-06`）
- [ ] `TestParse_InvalidGzip` — 不正な gzip データを入力しエラーが返ることを確認（`AC-03`）
- [ ] `TestParse_InvalidJSONAfterDecompress` — 有効な gzip だが展開後 JSON 不正のケースでエラーを確認（`AC-04`）
- [ ] `TestParse_SizeLimitExceeded` — gzip・非圧縮両パスでサイズ上限超過時に `ErrDecompressedSizeLimitExceeded` が返ることをサブテストで確認（`AC-05`）
- [ ] `TestParse_MissingRequiredField` — 必須フィールド 4 種（`organization-name`・`report-id`・`date-range`・`policies`）を個別に欠如させ、`ErrMissingRequiredField` が返ることをサブテストで確認（`AC-07`）
- [ ] `TestParse_PoliciesFields` — `policies` 配列内の各フィールド（`policy-type`・`policy-domain`等）が正しくパースされることを確認（`AC-08`）
- [ ] `TestParse_FailureDetails` — `failure-details` フィールドが存在する場合に正しく取得されることを確認（`AC-09`）

#### 完了基準

- `go test -v ./internal/tlsrpt/...` が全テストパス

---

### フェーズ 4: HasFailure() の実装とテスト（`AC-11`〜`AC-13`）

`02_architecture.md` §6.2 の処理フローに従って実装する。

#### 対象ファイル

- `internal/tlsrpt/tlsrpt.go`
- `internal/tlsrpt/tlsrpt_test.go`

#### 作業内容

- [ ] `(*Report).HasFailure() bool` メソッドの実装
  - [ ] `r.Policies` を走査し、いずれかの `Summary.TotalFailureSessionCount > 0` の場合 `true` を返す
  - [ ] 全て 0 または `Policies` が空の場合は `false` を返す
- [ ] `TestHasFailure_AllZero` — 全ポリシーの `TotalFailureSessionCount` が 0 の場合 `false` を確認（`AC-11`）
- [ ] `TestHasFailure_AnyNonZero` — いずれかが 1 以上の場合 `true` を確認（`AC-12`）
- [ ] `TestHasFailure_EmptyPolicies` — `Policies` が空スライスの場合 `false` を確認（`AC-13`）

#### 完了基準

- `go test -v ./internal/tlsrpt/...` が全テストパス

---

### フェーズ 5: 統合テスト（`AC-10`）

#### 対象ファイル

- `internal/tlsrpt/tlsrpt_test.go`

#### 作業内容

- [ ] `TestParse_RealReport` 統合テストの実装
  - [ ] `../../testdata/tlsrpt_google.eml` を `os.Open` で開く
  - [ ] `net/mail.ReadMessage()` でパース
  - [ ] `mailparse.ExtractAttachments()` で全添付ファイルを抽出
  - [ ] 添付ファイル数が 1 件以上であることを `require` で確認
  - [ ] ファイル名が `.json.gz` または `.json` で終わる添付ファイルを対象に `tlsrpt.Parse()` を呼び出す
  - [ ] エラーなしで `*Report` が返ることを確認（`AC-10`）
  - [ ] `Report.OrganizationName` が空でないことを確認

#### 完了基準

- `go test -v -run TestParse_RealReport ./internal/tlsrpt/...` がパス

---

### フェーズ 6: 品質確認

#### 作業内容

- [ ] `make fmt` を実行しフォーマットエラーがないことを確認
- [ ] `make lint` を実行し lint エラーがないことを確認
- [ ] `make test` で全テストがパスすることを確認

---

## 3. 実装順序とマイルストーン

| マイルストーン | 対象フェーズ | 成果物 |
|---|---|---|
| M1: 型定義完了 | フェーズ 1 | `tlsrpt.go` の型・エラー型・定数が定義済み |
| M2: パース実装完了 | フェーズ 2・3 | `Parse()` 実装 + `AC-01`〜`AC-09` のテストがパス |
| M3: failure 判定完了 | フェーズ 4 | `HasFailure()` 実装 + `AC-11`〜`AC-13` のテストがパス |
| M4: 統合テスト完了 | フェーズ 5 | `AC-10` の統合テストがパス |
| M5: 品質確認完了 | フェーズ 6 | lint・fmt・全テストがパス |

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

- zip bomb テスト: `maxDecompressedSize + 1` バイトのゼロバイト列を gzip 圧縮し、`Parse()` が `ErrDecompressedSizeLimitExceeded` を返すことを確認する（`AC-05`）

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
- [ ] `maxDecompressedSize` 定数定義済み
- [ ] `ErrDecompressedSizeLimitExceeded` 定義済み
- [ ] `ErrMissingRequiredField` 定義済み
- [ ] 全構造体（`Report`・`DateRange`・`PolicyRecord`・`Policy`・`Summary`・`FailureDetail`）定義済み

### フェーズ 2・3
- [ ] `Parse()` 実装済み
- [ ] `AC-01`〜`AC-09` テスト実装済み・パス

### フェーズ 4
- [ ] `HasFailure()` 実装済み
- [ ] `AC-11`〜`AC-13` テスト実装済み・パス

### フェーズ 5
- [ ] `AC-10` 統合テスト実装済み・パス

### フェーズ 6
- [ ] `make fmt` エラーなし
- [ ] `make lint` エラーなし
- [ ] `make test` 全テストパス

---

## 7. 受け入れ条件の検証

| AC | 検証テスト | テストファイル |
|---|---|---|
| `AC-01` | `TestParse_GzipValid` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-02` | `TestParse_PlainJSONValid` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-03` | `TestParse_InvalidGzip` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-04` | `TestParse_InvalidJSONAfterDecompress` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-05` | `TestParse_SizeLimitExceeded`（gzip・非圧縮サブテスト） | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-06` | `TestParse_GzipValid`, `TestParse_PlainJSONValid` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-07` | `TestParse_MissingRequiredField`（各フィールドのサブテスト） | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-08` | `TestParse_PoliciesFields` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-09` | `TestParse_FailureDetails` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-10` | `TestParse_RealReport` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-11` | `TestHasFailure_AllZero` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-12` | `TestHasFailure_AnyNonZero` | `internal/tlsrpt/tlsrpt_test.go` |
| `AC-13` | `TestHasFailure_EmptyPolicies` | `internal/tlsrpt/tlsrpt_test.go` |

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

- `internal/tlsrpt` の実装完了後、`cmd/tlsrpt-digest/main.go` から `Parse()` を呼び出す統合を別タスク（タスク 0070 エントリポイント）で実施する
