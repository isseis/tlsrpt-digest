# 実装計画書：Slack アラート通知フォーマット改善

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-06-08 |
| レビュー日 | 2026-06-09 |
| 最終更新日 | 2026-06-09 |
| レビュアー | isseis |
| コメント | 実 Slack 表示確認後の最終実装に合わせ、Block Kit sections 案から warning attachment fields 案へ更新 |

関連文書: [01_requirements.md](01_requirements.md) / [02_architecture.md](02_architecture.md)

---

## 1. 実装概要

### 1.1 目的

即時アラート（`failure_session_count > 0`）の Slack 表示を、旧来の黄色い warning attachment に近い見た目へ改善する。表示内容にはポリシーごとの概要、Report ID、失敗詳細（公開 4 項目）、Run ID を含める。

最終実装では Block Kit sections を主表示に使わない。Slack クライアントや表示面によって `attachment.blocks` が表示されないことがあり、top-level `text` に全文を置くと通常クライアントでは本文が重複するためである。詳細は [02_architecture.md](02_architecture.md) §3.4 を参照する。

### 1.2 実装方針

- 既存の送信経路（`SlackHandler.Flush()` → `send()` → `formatRecords()` → `formatAlerts()`）は変更しない。
- 変更は `internal/notify` と `cmd/tlsrpt-digest` の既存パッケージ内に閉じる。
- `Alert` へ Report ID と FailureDetails を追加し、`logAlerts` で TLSRPT レポートから公開情報のみを写像する。
- Slack payload は `Text` にタイトルだけを置き、`Attachments[].Fields` に主表示を置く。
- Block Kit 用の `slackBlock` / `Blocks` は YAGNI 原則に従って持たず、即時アラートの payload surface を `text` + `attachments[].fields` に絞る。
- Go ソースのコメント・識別子・文字列リテラルは英語で記述する。

### 1.3 既存コード調査結果

| 領域 | 既存 | 最終変更内容 |
|---|---|---|
| `internal/notify/types.go` | `Alert{OrganizationName, PolicyType, FailureCount, DateRange}` | `ReportID string`、`FailureDetails []FailureDetail`、集計用 count/session fields を追加 |
| `internal/notify/message.go` | `slackMessage{Text, Attachments}`、`slackAttachment{Color, Fields}` | `Blocks` / `slackBlock` 型は持たない |
| `internal/notify/helpers.go` | `LogAlert` が基本 5 属性を出力 | Report ID、FailureDetails、FailureDetails の総件数・総セッション数を slog 属性へ追加 |
| `internal/notify/format.go` `extractAlert` | 基本 5 属性を復元 | 新属性を復元し、未知キーは値を出さず DebugLogger にキー名のみ警告 |
| `internal/notify/format.go` `formatAlerts` | `maxAlertFields` で fields を生成 | warning attachment fields を生成。top-level `text` はタイトルのみ |
| `internal/notify/format.go` `truncateMessage` | `Text` と field values を切り詰め | `Text` と field values を切り詰め |
| `cmd/tlsrpt-digest/notify_helpers.go` | TLSRPT report から基本 4 項目を写像 | `report.ReportID` と `policy.FailureDetails` の公開 4 項目のみを写像。IP と自由記述は写像しない |

---

## 2. 実装ステップ

> 凡例: `[ ]` 未着手 / `[x]` 完了。

### Phase 1: データ構造の拡張とデータ経路

対象ファイル: `internal/notify/types.go`、`internal/notify/helpers.go`、`internal/notify/format.go`、`cmd/tlsrpt-digest/notify_helpers.go`、関連テスト

- [x] **1-1** `Alert` に `ReportID string` と `FailureDetails []FailureDetail` を追加する。
- [x] **1-2** `FailureDetail` 型を新設し、`ResultType`、`FailedSessionCount`、`ReceivingMXHostname`、`FailureReasonCode` の公開 4 項目だけを持たせる。
- [x] **1-3** FailureDetails の元総件数・元総失敗セッション数を保持する集計 fields を追加する。
- [x] **1-4** `LogAlert` で `report_id`、`failure_details`、集計値を slog 属性へエンコードする。FailureDetails は `failed_session_count` 降順で最大 10 件に制限する。
- [x] **1-5** `extractAlert` で新属性を復元し、想定外キーはキー名のみ DebugLogger へ警告する。
- [x] **1-6** `logAlerts` で `report.ReportID` と `policy.FailureDetails` の公開 4 項目だけを `Alert` へ写像する。
- [x] **1-7** `LogAlert` の構造化属性テストを許可リスト方式へ強化し、機微情報用 field が存在しないことを検証する。
- [x] **1-8** `LogAlert` → `extractAlert` の round-trip テストを追加し、順序保持と 10 件上限を検証する。
- [x] **1-9** `logAlerts` の写像境界テストを追加し、IP と `additional-information` が通知型へ入らないことを検証する。

完了条件: `go test -tags test ./internal/notify/... ./cmd/tlsrpt-digest/...` が通る。

### PR-1 作成ポイント: data path for report-id and failure-details

**対象ステップ**: 1-1〜1-9

**推奨タイトル**: `feat(0101): carry report-id and failure-details through the alert data path`

**レビュー観点**: `Alert`/`FailureDetail` 型の公開フィールド設計、IP・`additional-information` 非保持、`LogAlert`→`extractAlert` の slog 往復、FailureDetails の降順保持と上限処理。

- [x] `make test && make lint` が通っていることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた

### Phase 2: Slack attachment fields 整形

対象ファイル: `internal/notify/message.go`、`internal/notify/format.go`

- [x] **2-1** `slackAttachment` に `Fallback string` を追加する。
- [x] **2-2** `slackMessage.Text` は `⚠️ TLS Failures – N organizations affected` のタイトルだけにする。
- [x] **2-3** `formatAlerts` で `Attachments[0].Color = "warning"` の単一 attachment を生成する。
- [x] **2-4** `Attachments[0].Fields` に、ポリシー概要、Report ID、Failure Details、Run ID を配置する。
- [x] **2-5** ポリシー概要 field は旧来の見た目に合わせ、title を `Organization / Policy / Failures / Period`、value を `org | policy | failures | start – end` とする。
- [x] **2-6** Failure Details は `failed_session_count` 降順で上位 3 件を表示し、4 件以上は残り件数と合計 sessions を要約する。
- [x] **2-7** `Attachments[0].Fallback` に詳細本文を構築し、fields が表示されないクライアント向けの情報を保持する。
- [x] **2-8** top-level `Text` に詳細本文を入れず、通常 Slack 画面で本文が重複しないことを担保する。
- [x] **2-9** 外部由来文字列の制御文字を空白へ正規化し、Markdown 装飾に依存しない field / fallback 表示にする。
- [x] **2-10** `slackBlock` / `Blocks` は YAGNI 原則に従って削除する。

完了条件: `make test-slack-notify` の実 Slack 表示で、タイトルの下に黄色い attachment が表示され、本文が重複しない。

### Phase 3: サイズ制限と切り詰め

対象ファイル: `internal/notify/format.go`

- [x] **3-1** attachment fields に表示するポリシー数の上限を定義する。
- [x] **3-2** 上限を超えるポリシーは `Additional Policies` field に、省略ポリシー数・組織数・失敗セッション数として要約する。
- [x] **3-3** 組織名、policy type、Report ID、result type、MX hostname、reason code を値ごとの上限で切り詰める。
- [x] **3-4** `truncateMessage` で top-level `Text`、attachment `Fallback`、field title/value を切り詰める。

完了条件: 過大入力でも単一 Slack message として送信でき、Slack 側の文字数制限に対して保守的に収まる。

### Phase 4: テスト更新・追加

対象ファイル: `internal/notify/*_test.go`、`cmd/tlsrpt-digest/*_test.go`

- [x] **4-1** テスト用 Slack payload 型に `Fallback` を追加し、未使用の `Blocks` 検証は削除する。
- [x] **4-2** アラート表示テストを fields/fallback 構造検証へ更新する。
- [x] **4-3** top-level `Text` がタイトルのみで、詳細本文を含まないことを検証する。
- [x] **4-4** attachment fields に旧来の `Organization / Policy / Failures / Period` 表示、Report ID、Failure Details、Run ID が含まれることを検証する。
- [x] **4-5** attachment fallback に詳細本文が含まれることを検証する。
- [x] **4-6** Failure Details の 0 件、1〜3 件、4 件以上の表示を検証する。
- [x] **4-7** `receiving-mx-hostname` と `failure-reason-code` の有無による表示/非表示を検証する。
- [x] **4-8** 制御文字正規化と値ごとの切り詰めを検証する。
- [x] **4-9** overflow summary を検証する。
- [x] **4-10** `truncateMessage` が fallback と fields を切り詰めることを検証する。
- [x] **4-11** IP、`additional-information`、Webhook URL、secret が通知 payload / DebugLogger に混入しないことを検証する。
- [x] **4-12** warning、system error、summary の既存 fields 表示が変わらないことを既存テストで確認する。
- [x] **4-13** `make test-slack-notify` 用の統合テストがビルドできることを確認する。

完了ゲート:

- [x] `make test` が通る
- [x] `make lint` が通る
- [x] `go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...` が通る

### PR-2 作成ポイント: Slack alert attachment rendering

**対象ステップ**: 2-1〜2-10 / 3-1〜3-4 / 4-1〜4-13

**推奨タイトル**: `fix(0101): restore Slack alert attachment rendering`

**レビュー観点**: 黄色い warning attachment の見た目、top-level `Text` と attachment fields の本文重複回避、FailureDetails の表示・要約、機微情報非混入、既存通知形式の回帰なし。

- [x] `make test && make lint` が通っていることを確認した
- [x] `go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...` が通っていることを確認した
- [x] コミットを作成した
- [ ] PR がマージされた

---

## 3. 実装順序とマイルストーン

| マイルストーン | 含むステップ | 内容 | テスト状態 |
|---|---|---|---|
| PR-1 | 1-1〜1-9 | データ構造・slog 往復・TLSRPT から Alert への公開情報写像 | `make test && make lint` 合格 |
| PR-2 | 2-1〜2-10 / 3-1〜3-4 / 4-1〜4-13 | warning attachment fields 表示、切り詰め、本文非重複、テスト更新 | `make test && make lint`、slack_notify ビルドタグのコンパイル確認が合格 |

PR-2 は当初 Block Kit sections で実装したが、実 Slack 表示で本文が消えるケースが確認されたため、最終的に warning attachment fields へ修正した。この修正は表示仕様に関わるため、同じ PR-2 の範囲として扱う。

---

## 4. テスト戦略

- **単体テスト**: `internal/notify/format_test.go` で Slack payload の fields shape、本文非重複、FailureDetails 表示、overflow、切り詰めを検証する。
- **slog 往復・許可リスト**: `helpers_test.go` と `format_internal_test.go` で属性キー、順序保持、上限処理を検証する。
- **セキュリティテスト**: `security_test.go` で IP、自由記述、Webhook URL、secret が payload / DebugLogger に混入しないことを検証する。
- **写像境界テスト**: `cmd/tlsrpt-digest/notify_helpers_test.go` で TLSRPT レポートから公開 4 項目だけが通知型へ写像されることを検証する。
- **統合テスト**: 実 Webhook 送信は smoke / 目視確認とする。JSON 構造の厳密検証は `internal/notify` 側に置く。
- **後方互換**: 警告・サマリー・システムエラーの `fields` 整形は不変であり、既存テストで担保する。

---

## 5. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| `attachment.blocks` が Slack 表示面で本文非表示になる | サブジェクトだけが表示され、障害対応に必要な情報が見えない | 即時アラートの主表示を `attachment.fields` に戻す |
| top-level `text` に全文を入れる | 通常 Slack 画面で本文が attachment と重複する | top-level `text` はタイトルだけにし、詳細は fields に置く |
| `failure_details` の順序崩れ | 上位失敗が正しく表示されない | `LogAlert` で降順に整列し、round-trip テストで検証する |
| FailureDetails の過大入力 | Slack payload が肥大化する | slog 上限、表示上限、overflow summary、`truncateMessage` で制御する |
| 外部由来文字列による偽装表示 | 改行注入や偽の行見出しが起きる | 制御文字正規化、Markdown 装飾に依存しない表示、値ごとの切り詰めで抑制する |
| 機微情報の混入 | IP や自由記述が通知・ログに出る | `FailureDetail` 型に機微 field を持たせず、許可リストテストで検証する |
| 既存通知種別の回帰 | warning / system error / summary の見た目が変わる | 変更を alert path に閉じ、既存通知テストを維持する |

---

## 6. 実装チェックリスト

- [x] PR-1 マージ済み（対象ステップ: 1-1〜1-9）
- [x] PR-2 実装済み（対象ステップ: 2-1〜2-10 / 3-1〜3-4 / 4-1〜4-13）
- [x] 完了ゲート: `make test` が通る
- [x] 完了ゲート: `make lint` が通る
- [x] 完了ゲート: `go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...` が通る
- [ ] PR-2 マージ済み

---

## 7. 受け入れ条件の検証

| AC | 検証方法 |
|---|---|
| AC-01 | top-level `Text` のタイトル検証 |
| AC-02 | attachment fields の policy summary 検証 |
| AC-03 | 複数ポリシーと overflow summary の検証 |
| AC-04 | 旧来の field title/value 表示として、各 policy summary が読みやすくまとまることを検証 |
| AC-05〜AC-09 | Failure Details の基本表示、任意項目、有無、3 件以下、4 件以上要約を検証 |
| AC-10 | Failure Details 空でも policy summary / Report ID / Run ID が表示されることを検証 |
| AC-11 | Report ID field の検証 |
| AC-12 | UTC 期間表示の検証 |
| AC-13 | 型境界、制御文字正規化、Markdown 装飾に依存しない表示、機微情報非混入テストで検証 |
| AC-14 | 値ごとの切り詰め、field truncation、overflow summary で検証 |

---

## 8. 実行済み確認

最終実装で以下を確認済み。

```sh
make test
make lint
go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...
```
