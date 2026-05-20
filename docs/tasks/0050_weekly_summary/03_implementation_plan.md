# 実装計画書：定期サマリ生成・通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-20 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装概要

- **目的**: `internal/store` から取得した TLSRPT レポートを集計し、`internal/notify` 経由で定期サマリを送信する機能を実装する。詳細は `01_requirements.md` を参照。
- **実装原則**: `02_architecture.md` の設計に従う。設計の詳細（データフロー・コンポーネント責務）はそちらを参照すること。
- **スコープ**: `internal/notify/` の変更のみ。`cmd/` 側のサブコマンド統合はタスク 0070 で行う。
- **既存資産の再利用**:
  - `internal/store/testutil.FakeStore`: `GenerateSummary` のテストで `store.Store` のスタブとして利用
  - `internal/notify/testutil.SpyHandler`: 統合テストで通知ロガーのスタブとして利用
  - `format_test.go` の `buildCaptureHandler`: HTTP POST の実 Slack ペイロード検証に利用
  - `helpers_test.go` の `spyHandler`: `slog.Record` 属性の検証に利用

---

## 2. 実装フェーズ

### フェーズ 1: `Summary` 型の変更と既存コードのコンパイルエラー修正

`Summary` 型の変更は既存コードに影響する。型変更後、すべてのコンパイルエラーを一括修正する。

- [ ] **1.1** `types.go` の `Summary` 型を更新する
  - ファイル: `internal/notify/types.go`
  - 作業内容:
    - `OrganizationCount int` フィールドを削除する
    - `OrganizationStats map[string]int64` フィールドを追加する
    - `ReportCount int` を `ReportCount int64` に変更する
  - 確認方法: `go build ./internal/notify/` でコンパイルエラーを確認する（この時点ではエラーが出る）

- [ ] **1.2** `helpers.go` の `LogSummary` を一時的にコンパイルが通る状態にする
  - ファイル: `internal/notify/helpers.go`
  - 作業内容: `organization_count` の `slog.Int64` 行を削除し、`report_count` の `int64(s.ReportCount)` を `s.ReportCount` に変更する。`OrganizationStats` のシリアライズはフェーズ 3 で実装するため、この時点ではプレースホルダとして空のまま残す。

- [ ] **1.3** `format.go` の `extractSummary` / `formatSummary` を一時的にコンパイルが通る状態にする
  - ファイル: `internal/notify/format.go`
  - 作業内容: `organization_count` キーの case 節を削除する。`formatSummary` 内の `s.OrganizationCount` 参照を `len(s.OrganizationStats)` に変更する。実際のフォーマット更新はフェーズ 4 で行う。

- [ ] **1.4** `cmd/tlsrpt-digest/main.go` の `primeNotifyHandlers` を新型に対応させる
  - ファイル: `cmd/tlsrpt-digest/main.go`
  - 作業内容: `notify.Summary{..., OrganizationCount: 0, ReportCount: 0}` の `OrganizationCount: 0` を削除し、`ReportCount: 0` はそのまま残す（`int64` リテラル `0` は `int` と互換）。

- [ ] **1.5** `format_test.go` の既存テストを新型に対応させる
  - ファイル: `internal/notify/format_test.go`
  - 作業内容: `TestFormatSummary_Fields` の `OrganizationCount: 4` を、`OrganizationStats: map[string]int64{"org-a": 10, "org-b": 20, "org-c": 30, "org-d": 40}` に置き換える。`ReportCount: 7` の型は変更不要（`int64` リテラル互換）。

- [ ] **1.6** `make test` が通ることを確認する
  - 作業内容: `make test` を実行し、コンパイルエラーと既存テストの失敗がないことを確認する。

---

### フェーズ 2: `GenerateSummary` の新設

`02_architecture.md` セクション 6.1 の集計フローを実装する。

- [ ] **2.1** `aggregate.go` を新規作成し `GenerateSummary` を実装する
  - ファイル: `internal/notify/aggregate.go`
  - 作業内容:
    - シグネチャ: `func GenerateSummary(ctx context.Context, st store.Store, start, end time.Time, debugLogger *slog.Logger) (Summary, error)`
    - `store.Store` インポートのため `internal/store` および `internal/tlsrpt` への依存が生じる（`02_architecture.md` セクション 2.2 の意図的トレードオフ）
    - フィルタリング条件: `start < report.DateRange.EndDatetime <= end` かつ `report.HasFailure() == false`
    - 混在レポート検出（`HasFailure() == true` かつ `sum(TotalSuccessfulSessionCount) > 0`）時は `debugLogger.Warn(...)` を呼ぶ（AC-11）
    - 対象レポートが 0 件の場合は `Summary{Period: DateRange{Start: start, End: end}, OrganizationStats: map[string]int64{}, ReportCount: 0}` を返す（AC-04）
    - `store.GetAllReports()` エラー時は `fmt.Errorf("GenerateSummary: %w", err)` でラップして返す

- [ ] **2.2** `aggregate_test.go` を新規作成し `GenerateSummary` のテストを実装する
  - ファイル: `internal/notify/aggregate_test.go`
  - パッケージ: `package notify_test`
  - テスト対象 AC: AC-01, AC-02, AC-03, AC-04, AC-11
  - 作業内容（各テスト関数）:
    - `TestGenerateSummary_FiltersByPeriod`: `EndDatetime` が `start` 以下のレポートは除外し、`end` 以下のレポートは含まれることを確認（半開区間 AC-01）
    - `TestGenerateSummary_StartBoundaryExclusion`: `EndDatetime == start` のレポートが結果に含まれないことを確認（半開区間の境界値）
    - `TestGenerateSummary_EndBoundaryInclusion`: `EndDatetime == end` のレポートが結果に含まれることを確認（半開区間の境界値）
    - `TestGenerateSummary_ExcludesFailureReports`: `HasFailure() == true` のレポートが `OrganizationStats` に含まれないことを確認（AC-01）
    - `TestGenerateSummary_SumsSuccessfulSessions`: 同一組織の複数レポートの `TotalSuccessfulSessionCount` が合算されることを確認（AC-02）
    - `TestGenerateSummary_PeriodInSummary`: 返却された `Summary.Period` が渡した `start`・`end` と一致することを確認（AC-03）
    - `TestGenerateSummary_EmptyPeriod`: 対象期間にレポートが 0 件の場合、`OrganizationStats` が空で `ReportCount` が 0 の `Summary` が返ることを確認（AC-04）
    - `TestGenerateSummary_MixedReportWarning`: `HasFailure() == true` かつ成功セッションあり（`TotalSuccessfulSessionCount > 0`）のレポートを検出したとき、`debugLogger` に警告が記録されることを確認（AC-11）
    - `TestGenerateSummary_MixedReportNotInStats`: 混在レポートの成功セッションが `OrganizationStats` に加算されないことを確認（AC-11 副作用）
    - `TestGenerateSummary_StoreError`: `GetAllReports()` がエラーを返した場合、`GenerateSummary` がそのエラーをラップして返すことを確認。`storetestutil.FakeStore` を埋め込み `GetAllReports` をオーバーライドするローカル型 `errStoreWrapper` を使う。
  - 既存ヘルパーの利用: `storetestutil.NewFakeStore()` を使って `store.Store` のスタブを作成する

- [ ] **2.3** `make test` が通ることを確認する

---

### フェーズ 3: `LogSummary` の更新

`02_architecture.md` セクション 6.3 のシリアライズ仕様を実装する。

- [ ] **3.1** `helpers.go` の `LogSummary` を `OrganizationStats` 対応に更新する
  - ファイル: `internal/notify/helpers.go`
  - 作業内容:
    - `slog.Int64("organization_count", ...)` 行を削除する
    - `slices.Sorted(maps.Keys(s.OrganizationStats))` でキーをアルファベット昇順にソートする
    - ソート済みキーから `[]any` の属性リストを構築し、`slog.Group("organization_stats", attrs...)` として追加する
    - `slog.Int64("report_count", s.ReportCount)` を正しい型で追加する

- [ ] **3.2** `helpers_test.go` に `LogSummary` のシリアライズテストを追加する
  - ファイル: `internal/notify/helpers_test.go`
  - テスト対象 AC: AC-08 の前提（ログ記録の対称性）
  - 作業内容（各テスト関数）:
    - `TestLogSummary_OrganizationStats_Serialized`: `OrganizationStats` に複数の組織を設定した `Summary` をログ記録したとき、`slog.Record` の属性に `organization_stats` グループが含まれ、組織名と成功セッション数が正しいことを `spyHandler` で確認する
    - `TestLogSummary_OrganizationStats_SortedKeys`: 複数の組織名が属性グループ内でアルファベット昇順に並んでいることを確認する（決定論的シリアライズ）
    - `TestLogSummary_EmptyOrganizationStats`: `OrganizationStats` が空のとき、ログ記録時にパニックが起きないことを確認する

- [ ] **3.3** `make test` が通ることを確認する

---

### フェーズ 4: `extractSummary` と `formatSummary` の更新

`02_architecture.md` セクション 6.2 および 6.3 を参照。

- [ ] **4.1** `format.go` の `extractSummary` を `organization_stats` グループ対応に更新する
  - ファイル: `internal/notify/format.go`
  - 作業内容:
    - `organization_count` の case 節（フェーズ 1.3 で削除済み）を確認する
    - `organization_stats` キーの case 節を追加する: `attr.Value.Kind() == slog.KindGroup` のとき、グループ内の各属性を `OrganizationStats` マップに復元する

- [ ] **4.2** `format.go` の `formatSummary` を attachment チャンク分割・`text` フィールド配置に更新する
  - ファイル: `internal/notify/format.go`
  - 作業内容（`02_architecture.md` セクション 6.2 の Slack メッセージ構造に従う）:
    - `text` フィールド: 集計期間・レポート総数・組織総数を文字列フォーマットで設定する（AC-05, AC-07）
    - `OrganizationStats` をアルファベット昇順にソートし、9 組織ずつ attachment にチャンク分割する（AC-06, AC-10）
    - `OrganizationStats` が空の場合: Run ID のみを含む 1 つの attachment を生成する（AC-04 対応）
    - 最後の attachment の末尾に Run ID フィールド（1 フィールド）を追加する
    - `formatAlerts` の実装パターンを参考にする

- [ ] **4.3** `format_test.go` に `formatSummary` のテストを追加・更新する
  - ファイル: `internal/notify/format_test.go`
  - テスト対象 AC: AC-05, AC-06, AC-07, AC-10
  - 作業内容（各テスト関数）:
    - フェーズ 1.5 で更新した `TestFormatSummary_Fields` が引き続きパスすることを確認する
    - `TestFormatSummary_PeriodInText`: `Summary.Period` が `text` フィールドに含まれることを確認する（AC-05）
    - `TestFormatSummary_OrgStatsInAttachment`: 各組織の成功セッション数が attachment フィールドに含まれることを確認する（AC-06）
    - `TestFormatSummary_ReportCountInText`: `ReportCount` が `text` フィールドに含まれることを確認する（AC-07）
    - `TestFormatSummary_SingleAttachmentUpTo9Orgs`: 組織数が 1〜9 の場合、attachment が 1 つで Run ID が含まれることを確認する（AC-10）
    - `TestFormatSummary_ChunkingOver9Orgs`: 組織数が 10 以上の場合、attachment が 2 つ以上に分割され、Run ID が最後の attachment のみに含まれることを確認する（AC-10）
    - `TestFormatSummary_EmptyOrganizationStats`: `OrganizationStats` が空のとき、Run ID を含む attachment が 1 つ生成されることを確認する（AC-04）
    - `TestExtractSummary_OrganizationStats_Roundtrip`: `LogSummary` でシリアライズした `slog.Record` を `extractSummary` で復元したとき、`OrganizationStats` が元の値と一致することを確認する

- [ ] **4.4** `make test` が通ることを確認する

---

### フェーズ 5: 統合テスト

`GenerateSummary` → `LogSummary` → `Flush` の一連フローをエンドツーエンドで検証する。

- [ ] **5.1** 統合テストを `helpers_test.go` に追加する
  - ファイル: `internal/notify/helpers_test.go`
  - テスト対象 AC: AC-08, AC-09
  - 作業内容（各テスト関数）:
    - `TestSummaryFlow_E2E`: `storetestutil.FakeStore` にサンプルレポートを格納し、`GenerateSummary` → `LogSummary` → `Flush` を順に呼び出す。`buildCaptureHandler` で実際の Slack HTTP ペイロードを取得し、組織名・成功セッション数・集計期間・Run ID が含まれることを確認する（AC-08）
    - `TestSummaryFlow_E2E_NoReports`: レポート 0 件で `GenerateSummary` → `LogSummary` → `Flush` を実行し、Run ID を含む Slack ペイロードが送信されることを確認する（AC-04 + AC-08）
    - `TestSummaryFlow_FlushError`: `Flush` が HTTP 403 を受け取ったときにエラーを返すことを確認する（AC-09）

- [ ] **5.2** セキュリティ検証テストを `security_test.go` に追加する
  - ファイル: `internal/notify/security_test.go`
  - 作業内容:
    - `TestSummary_NoSensitiveFields`: `LogSummary` で記録した `slog.Record` に Webhook URL やパスワードが含まれないことを確認する（`02_architecture.md` セクション 5.2 の原則 1 に対応）
    - `TestMixedReportWarn_NotInNotifyLogger`: `GenerateSummary` の混在レポート警告（AC-11）が `SpyHandler`（通知ロガー）側に流れず、`debugLogger` 側のみに出力されることを確認する

- [ ] **5.3** `make test` と `make lint` が通ることを確認する

- [ ] **5.4** `make deadcode` で不要なコードがないことを確認する

---

## 3. 受け入れ条件トレーサビリティ

`01_requirements.md` の各受け入れ条件とテストの対応を記録する。実装完了後にファイルパスと行番号を記入すること。

**AC-01**: 集計期間（半開区間）と `HasFailure() == false` フィルタリングで組織別集計ができる
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_FiltersByPeriod`
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_StartBoundaryExclusion`
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_EndBoundaryInclusion`
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_ExcludesFailureReports`
- 実装: `internal/notify/aggregate.go`（実装後に行番号を記入）

**AC-02**: 各組織の `TotalSuccessfulSessionCount` 合計を算出できる
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_SumsSuccessfulSessions`
- 実装: `internal/notify/aggregate.go`

**AC-03**: 集計対象期間がサマリに反映される
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_PeriodInSummary`
- 実装: `internal/notify/aggregate.go`

**AC-04**: 対象期間にレポートが存在しない場合も常に通知する
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_EmptyPeriod`
- テスト: `internal/notify/format_test.go::TestFormatSummary_EmptyOrganizationStats`
- テスト: `internal/notify/helpers_test.go::TestSummaryFlow_E2E_NoReports`
- 実装: `internal/notify/aggregate.go`、`internal/notify/format.go`

**AC-05**: サマリメッセージにレポート対象期間（開始〜終了）が含まれる
- テスト: `internal/notify/format_test.go::TestFormatSummary_PeriodInText`
- 実装: `internal/notify/format.go`（`formatSummary`）

**AC-06**: 組織別の `TotalSuccessfulSessionCount` 合計が含まれる
- テスト: `internal/notify/format_test.go::TestFormatSummary_OrgStatsInAttachment`
- 実装: `internal/notify/format.go`（`formatSummary`）

**AC-07**: 処理したレポート総数が含まれる
- テスト: `internal/notify/format_test.go::TestFormatSummary_ReportCountInText`
- 実装: `internal/notify/format.go`（`formatSummary`）

**AC-08**: 定期サマリが正しく Notifier に渡される
- テスト: `internal/notify/helpers_test.go::TestSummaryFlow_E2E`
- テスト: `internal/notify/format_test.go::TestExtractSummary_OrganizationStats_Roundtrip`
- 実装: `internal/notify/helpers.go`（`LogSummary`）、`internal/notify/format.go`（`extractSummary`）

**AC-09**: 送信失敗時はエラーを返す
- テスト: `internal/notify/helpers_test.go::TestSummaryFlow_FlushError`
- 実装: 既存の `SlackHandler.Flush` のリトライ・エラー処理（変更なし）

**AC-10**: 組織数 > 9 の場合、attachment がチャンク分割される
- テスト: `internal/notify/format_test.go::TestFormatSummary_SingleAttachmentUpTo9Orgs`
- テスト: `internal/notify/format_test.go::TestFormatSummary_ChunkingOver9Orgs`
- 実装: `internal/notify/format.go`（`formatSummary`）

**AC-11**: 混在レポート（failure あり + 成功セッションあり）を検出したとき `debugLogger` に警告を出力する
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_MixedReportWarning`
- テスト: `internal/notify/aggregate_test.go::TestGenerateSummary_MixedReportNotInStats`
- テスト: `internal/notify/security_test.go::TestMixedReportWarn_NotInNotifyLogger`
- 実装: `internal/notify/aggregate.go`

---

## 4. リスク管理

| リスク | 対策 |
|---|---|
| `slog.Group` に空の属性リストを渡したとき `extractSummary` が正しく動作しない | フェーズ 3.2 の `TestLogSummary_EmptyOrganizationStats` と `TestExtractSummary_OrganizationStats_Roundtrip` で空ケースを明示的にテストする |
| `Summary` 型変更による既存テストの連鎖的な破損 | フェーズ 1 で全コンパイルエラーを修正し、`make test` をフェーズごとに実行して回帰を即検出する |
| `internal/notify` が `internal/store` に依存することによる循環インポート | `02_architecture.md` セクション 2.2 に記載の通り現時点では循環なし。`make build` でビルドが通ることを確認する |

---

## 5. 完了条件

- [ ] `make lint` がエラーなく完了する
- [ ] `make test` がすべて成功する
- [ ] `01_requirements.md` の全受け入れ条件（AC-01〜AC-11）に対応するテストが存在する
- [ ] `make deadcode` で不要なコードが報告されない
- [ ] セクション 3 の受け入れ条件トレーサビリティ表に実装ファイルの行番号が記入されている

---

## 6. 次のステップ

- **タスク 0070**: `cmd/tlsrpt-digest` に `summary` サブコマンドを追加し、`GenerateSummary` → `LogSummary` → `Flush` を実際の設定（集計期間・TOML 設定）で呼び出す統合を実装する。
- **将来タスク（未定）**: ポリシー別の集計対応（現在は `OrganizationStats` が組織単位の合計のみ）。`Summary` 型の変更が必要になる想定。
