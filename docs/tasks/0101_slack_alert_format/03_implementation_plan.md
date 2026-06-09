# 実装計画書：Slack アラート通知フォーマット改善

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-06-08 |
| レビュー日 | 2026-06-09 |
| レビュアー | isseis |
| コメント | - |

関連文書: [01_requirements.md](01_requirements.md) / [02_architecture.md](02_architecture.md)

---

## 1. 実装概要

### 1.1 目的

即時アラート（`failure_session_count > 0`）の Slack 表示を Block Kit ベースへ刷新し、ポリシーごとの自己完結セクション、失敗詳細（公開 4 項目）、元データ識別情報（Report ID・期間）を表示する。設計の詳細は [02_architecture.md](02_architecture.md) を参照し、本書では作業手順・テスト・受け入れ条件の対応付けのみを示す。

### 1.2 実装方針

- 既存の送信経路（`SlackHandler.Flush()` → `send()` → `formatRecords()` → `formatAlerts()`）は変更しない（アーキテクチャ §2.1）。
- 変更は `internal/notify` と `cmd/tlsrpt-digest` の既存パッケージ内に閉じ、新規パッケージ・新規本番ファイルは作らない。テストは既存 `_test.go` の改修を基本とし、未公開関数を同じパッケージから検証するための `internal/notify/format_internal_test.go` は例外として新設する。
- 既存資産（`TruncateText`・`policyTypeStr`・`uniqueOrgCount`・`SpyNotificationSink`・`spyHandler`・`buildCaptureHandler`・`decodeSlackMessage`）を再利用する。
- Go ソースのコメント・識別子・文字列リテラルは英語で記述する。

### 1.3 既存コード調査結果

| 領域 | 既存 | 変更内容 |
|---|---|---|
| `internal/notify/types.go` | `Alert{OrganizationName, PolicyType, FailureCount, DateRange}` | `ReportID string`・`FailureDetails []FailureDetail` を追加し、`FailureDetail` 型（公開 4 項目）を新設（アーキテクチャ §3.1）。 |
| `internal/notify/message.go` | `slackMessage{Text, Attachments}`・`slackAttachment{Color, Fields}`・`slackField` | `slackAttachment` に `Blocks []slackBlock` を追加。`slackBlock`・`slackTextObject` を新設。`Fields` は警告/エラー/サマリーで継続使用（アーキテクチャ §3.2）。 |
| `internal/notify/helpers.go` `LogAlert` | 5 属性（`organization_name`・`policy_type`・`failure_count`・`date_start`・`date_end`）を出力 | `report_id` 文字列属性と `failure_details` グループ（`failed_session_count` 降順・最大 10 件・インデックス付き子グループ）を追加（アーキテクチャ §3.4）。 |
| `internal/notify/format.go` `extractAlert` | 上記 5 属性を `switch` で復元、未知キーは `warnUnknownKey` | `report_id`・`failure_details` グループの復元を追加（挿入順）。 |
| `internal/notify/format.go` `formatAlerts` | `maxAlertFields=9` で `fields` をチャンク分割 | Block Kit 生成へ全面刷新。`maxAlertFields` は削除し、ブロック数・section/context 文字数・値ごとの上限定数を新設（アーキテクチャ §6.2 の定数表）。 |
| `internal/notify/format.go` `truncateMessage` | `Text`（`maxTextRunes=4000`）と `Attachments[].Fields[].Value`（`maxFieldRunes=1000`）のみ切り詰め | `Attachments[].Blocks[]` の `section`/`context` テキスト走査を追加。`slackBlock.Text` は `nil` ガード必須（アーキテクチャ §6.2-4）。 |
| `cmd/tlsrpt-digest/notify_helpers.go` `logAlerts` | `report`→`Alert` を 4 項目で写像 | `report.ReportID` と `policy.FailureDetails` の公開 4 項目（`ResultType`・`FailedSessionCount`・`ReceivingMXHostname`・`FailureReasonCode`）を写像。`SendingMTAIP`・`ReceivingIP`・`AdditionalInformation` は写像しない（アーキテクチャ §5.2）。 |

**変更サイトの一意性**:
- `formatAlerts`・`maxAlertFields` は `internal/notify/format.go` のみに存在（`rg -n "formatAlerts|maxAlertFields" internal/notify --glob '!*_test.go'` で確認済み）。
- `logAlerts` の定義は `cmd/tlsrpt-digest/notify_helpers.go` の 1 箇所。呼び出しは `fetch.go:315`・`reprocess.go:130`・統合テスト・単体テストにあるが、写像ロジックは定義 1 箇所の変更で全呼び出しに反映される。
- 旧見出しリテラル `"Organization / Policy / Failures / Period"` は `formatAlerts` 内のみに存在し、刷新で消滅する。

**再利用するため新規作成しないもの**: `TruncateText`（rune 単位切り詰め、`section` 上限にも流用）、`policyTypeStr`（unknown→`(unknown)`）、`uniqueOrgCount`（概要見出しの組織数）、`SpyNotificationSink.Alerts`（写像検証）、`security_test.go` の許可リスト方式。

**アーキテクチャ §3.5 の既存テスト注記への対応**: `TestExtract_UnknownAttrKeyLogged` は Phase 4 で明示的に改修し、`report_id`・`failure_details` が既知キーとして警告されないことと、従来どおり未知キー `unexpected_field` はキー名のみ警告されることを同じテストで確認する。

### 1.4 外部仕様の確認結果

Slack 仕様は 2026-06-08 に公式ドキュメントで確認済み。

| 確認項目 | 確認した事実 | 根拠 |
|---|---|---|
| Incoming Webhooks の Block Kit 対応 | Incoming Webhooks は通常の formatting と layout blocks を使える。Webhook payload 例にも top-level `blocks` が含まれる。 | `https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/` |
| message あたりの block 上限 | message には最大 50 blocks を含められる。 | `https://docs.slack.dev/reference/block-kit/blocks/` |
| `section.text` 上限 | section block の `text` は 1〜3000 characters。 | `https://docs.slack.dev/reference/block-kit/blocks/section-block/` |
| text object 上限 | `plain_text`/`mrkdwn` text object は 1〜3000 characters。`context.elements[].text` は text object なので Slack 側上限は 3000 characters。 | `https://docs.slack.dev/reference/block-kit/composition-objects/text-object/` |
| context element 数 | context block の `elements` は最大 10 個。 | `https://docs.slack.dev/reference/block-kit/blocks/context-block/` |
| attachment 内 `blocks` と色 | legacy secondary attachments は `blocks` を持てる。`color` は左側ボーダー色を `good`/`warning`/`danger` または hex で指定できる。 | `https://docs.slack.dev/legacy/legacy-messaging/legacy-secondary-message-attachments` |

本計画の `maxAlertContextRunes=300` は Slack 上限 3000 より小さいプロジェクト内上限であり、Run ID だけを載せる context を過大にしないための保守的な値である。

### 1.5 新規テストヘルパーの要否

新規の `testutil/` 配下ヘルパーや `test_helpers.go` は作成しない。`internal/notify/format_internal_test.go` は共有ヘルパーではなく、未公開 `extractAlert` の往復を検証する同一パッケージ `_test.go` として新設する。理由は以下。
- 機微情報非複写の写像検証は既存の `cmd/tlsrpt-digest/test_helpers.go`（`//go:build test`）の `SpyNotificationSink.Alerts` で足りる。
- Block Kit ペイロードのデコードは、既存の `format_test.go` 内テストローカル型（`capturedSlackMessage`/`capturedSlackAttachment`）の拡張で足りる（`_test.go` 内のため新規ビルドタグ付きファイルは不要）。

---

## 2. 実装ステップ

> 凡例: `[ ]` 未着手 / `[x]` 完了 / `[-]` 一部完了（注記付き）。各ステップの「完了条件」は当該変更が満たすべき観測可能な状態。

### Phase 1: データ構造の拡張とデータ経路

対象ファイル: `internal/notify/types.go`、`internal/notify/helpers.go`、`internal/notify/format.go`、`cmd/tlsrpt-digest/notify_helpers.go`、`internal/notify/helpers_test.go`、`internal/notify/format_internal_test.go`（新規）、`cmd/tlsrpt-digest/notify_helpers_test.go`

- [ ] **1-1** `types.go`: `Alert` に `ReportID string`・`FailureDetails []FailureDetail` を追加する（アーキテクチャ §3.1 のコード定義に一致させる）。
- [ ] **1-2** `types.go`: `FailureDetail` 型（`ResultType string`・`FailedSessionCount int64`・`ReceivingMXHostname string`・`FailureReasonCode string`）を新設する。IP・`additional-information` に相当するフィールドは設けない。
- [ ] **1-3** `types.go`: `Alert` に `FailureDetailsTotalCount int64`・`FailureDetailsTotalSessions int64` を追加する（`LogAlert` がエンコード前に集計した元の総エントリ数・総失敗セッション数。`formatAlerts` が `Other N entries (M sessions total)` の正確な値に使う）。
- [ ] **1-4** `helpers.go` `LogAlert`: `report_id` 文字列属性を常に追加する。`failure_details` の全エントリから先に `failure_details_total_count int64`（元の総エントリ数）と `failure_details_total_sessions int64`（元の総失敗セッション数）を集計し slog 属性として格納する。その後 `failed_session_count` 降順で最大 10 件に絞り、インデックス名（`"0"`,`"1"`,…）の子グループとして追加する。各子グループのキーは `result_type`・`failed_session_count`・`receiving_mx_hostname`・`failure_reason_code` の 4 つ。事前集計によって、>10 件のレポートでも `Other N entries (M sessions total)` が正確な元データの件数・セッション数を反映できる（アーキテクチャ §3.4 の定数サイズ方針に従いつつ AC-09 の正確な集計を維持する）。
- [ ] **1-5** `format.go` `extractAlert`: `report_id`・`failure_details_total_count`・`failure_details_total_sessions`・`failure_details` グループを復元する。`failure_details` の子グループは `attr.Value.Group()` が返すスライス順で `[]FailureDetail` に戻す（既存 `extractSummary` の `organization_stats` 処理と同方式・format.go:200-208）。スライス順が `LogAlert` の追加順＝降順を保持するため、キーの文字列ソートには依存しない。`failure_details` の子グループ内に想定外キーがあった場合も `warnUnknownKey` で警告する（アーキテクチャ §3.4）。想定外のトップレベル属性は既存どおり警告する。
- [ ] **1-6** `cmd/tlsrpt-digest/notify_helpers.go` `logAlerts`: `Alert` 構築に `ReportID: report.ReportID` を追加し、`policy.FailureDetails` を公開 4 項目のみで `[]notify.FailureDetail` へ写像する。`SendingMTAIP`・`ReceivingIP`・`AdditionalInformation` は参照しない。
- [ ] **1-7** `helpers_test.go` `TestLogAlert_StructuredPayloadOnly`: 既存の存在検証を、他ヘルパーの `security_test.go` と同じ**許可リスト方式**へ強化する。トップレベル許可キーは `organization_name`・`policy_type`・`failure_count`・`date_start`・`date_end`・`report_id`・`failure_details`・`failure_details_total_count`・`failure_details_total_sessions`（合計 9 キー）。`failure_details` グループ内へ再帰し、子グループのキーが `result_type`・`failed_session_count`・`receiving_mx_hostname`・`failure_reason_code` の 4 つのみであることを検証する（AC-13）。
- [ ] **1-8** `format_internal_test.go` `TestLogAlert_FailureDetailsRoundTrip`（新規・`package notify`）: `LogAlert` → `extractAlert` の往復で、`ReportID` と `failure_details` の `failed_session_count` 降順・最大 10 件・順序保持が成り立つことを検証する。`extractAlert` は未公開関数のため、同じ `notify` パッケージの `_test.go` から直接検証する。
- [ ] **1-9** `notify_helpers_test.go` `TestLogAlerts_MapsPublicFailureFields`（新規）: `policy.FailureDetails` に IP・`additional-information` を含む `tlsrpt.Report` を `logAlerts` に渡し、`SpyNotificationSink.Alerts[0]` の `ReportID` と各 `FailureDetail` の公開 4 項目が期待値に一致することを検証する（IP・自由記述は `notify.FailureDetail` 型に存在しないため構造的に非複写）（AC-13）。

完了条件: `go test -tags test ./internal/notify/... ./cmd/tlsrpt-digest/...` が通る。`formatAlerts` は新フィールドを無視したまま従来どおり `fields` を出力するため既存アラートテストは緑のまま。`TestLogAlert_StructuredPayloadOnly`（許可リスト強化）・`TestLogAlert_FailureDetailsRoundTrip`・`TestLogAlerts_MapsPublicFailureFields` が緑。

### PR-1 作成ポイント: data path for report-id and failure-details

**対象ステップ**: 1-1 / 1-2 / 1-3 / 1-4 / 1-5 / 1-6 / 1-7 / 1-8 / 1-9

**推奨タイトル**: `feat(0101): carry report-id and failure-details through the alert data path`

**レビュー観点**: `Alert`/`FailureDetail` 型の公開フィールド設計と機微フィールド非保持 / `LogAlert`→`extractAlert` の slog 往復と `failed_session_count` 降順保持 / `logAlerts` 写像での IP・`additional-information` 非複写 / `LogAlert` で `failure_details_total_count`/`failure_details_total_sessions` が 10 件上限適用*前*の全エントリから集計されていること

- [ ] グリーンゲート（`_context.md` の "Green gate" 参照）がパスしていることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

### Phase 2: Block Kit 整形

対象ファイル: `internal/notify/message.go`、`internal/notify/format.go`

- [ ] **2-1** `message.go`: `slackAttachment` に `Blocks []slackBlock` を追加する（アーキテクチャ §3.2 のコード定義に一致）。`Fields` は残す。
- [ ] **2-2** `message.go`: `slackBlock`・`slackTextObject` を新設する。
- [ ] **2-3** `format.go` `formatAlerts`: `fields` 生成を廃止し、`Attachments[0].Color = "warning"` の単一 attachment に、ポリシーごとの `section` ブロック群＋末尾 `context`（Run ID）を生成する（アーキテクチャ §3.3）。
- [ ] **2-4** `format.go`: 各ポリシー `section.text` を `plain_text` で構築する。組織名・ポリシータイプ・失敗セッション総数・レポート期間（`.UTC()` 整形）・Report ID・失敗詳細を所定の行レイアウトで配置する（アーキテクチャ §3.3 の行テーブル）。
- [ ] **2-5** `format.go`: 失敗詳細を `failed_session_count` 降順に並べ、上位 3 件を詳細表示、4 件以上は残りを `Other N entries (M sessions total)` として要約する。空の場合は失敗詳細行を出力しない。
- [ ] **2-6** `format.go`: 外部由来文字列（組織名・`policy-type`・Report ID・`result-type`・`receiving-mx-hostname`・`failure-reason-code`）を `plain_text` へ入れる前に、`\n`・`\r`・`\t` を含む制御文字を空白へ正規化する。`policy-type` は `policyTypeStr` が文字列のまま出力するため外部入力として扱い、値ごとの上限（ステップ 3-1 で定義する `maxAlertPolicyTypeRunes`）でも切り詰める。本ステップの切り詰め実装はステップ 3-1 の定数定義後に行うこと。セクション内の項目間改行は実装テンプレート側で付加する（アーキテクチャ §3.3・§5.2）。
- [ ] **2-7** `format.go`: `maxAlertFields` 定数とその参照を削除する。

完了条件: `go test -tags test ./internal/notify/... ./cmd/tlsrpt-digest/...` が通る。なお既存アラートテストの多くは生 JSON 本文への部分文字列マッチ（`Contains`）であり、刷新後も同じ文字列が `section.text` 内に現れるため**自動的には赤化しない**。赤化するのは以下の 2 テスト:（1）`TestFormatAlerts_AttachmentFields`（`fields` の `title`/`value` を直接前提とする）、（2）`TestSlackAttachment_FieldsEncoding`（`captureWarnPayload` が `LogAlert` 経由でアラートペイロードを生成し `attachment["fields"]` を検証する）。Phase 4 では、これら 2 テストを `blocks` 構造検証へ書き換え、部分文字列マッチの既存テストも `sectionTexts` 経由の構造検証へ強化する（§2 Phase 4・§3 参照）。

> **PR-2 開発上の注意**: Phase 2 完了時点でテストが赤になり、Phase 4（ステップ 4-3・4-10）で修正されるまで緑に戻らない。PR-2 のグリーンゲート（`make test && make lint`）は Phase 2〜4 のすべてが完了して初めて確認できる。フィーチャーブランチへの中間 push は Phase 4 の全テスト修正が終わるまで行わないこと。

### Phase 3: サイズ制限と切り詰め

対象ファイル: `internal/notify/format.go`

- [ ] **3-1** `format.go`: §1.4 の公式 Slack 仕様確認結果に基づき、`maxAlertBlocksPerMessage=50`・`maxAlertSectionRunes=3000`・`maxAlertContextRunes=300`・`maxAlertOrganizationRunes=120`・`maxAlertPolicyTypeRunes=80`・`maxAlertReportIDRunes=160`・`maxAlertResultTypeRunes=80`・`maxAlertMXHostnameRunes=120`・`maxAlertReasonCodeRunes=80` を定義する。`policy-type` は外部 JSON 由来のため制御文字正規化と切り詰めの対象に含める（Phase 2 の正規化タスクと一貫）。
- [ ] **3-2** `format.go` `formatAlerts`: 外部由来値を section 組み立て前に値ごとの上限で切り詰める（`TruncateText` を流用）。対象は組織名・`policy-type`・Report ID・`result-type`・`receiving-mx-hostname`・`failure-reason-code`。期間・失敗数・静的ラベルは切り詰めない。
- [ ] **3-3** `format.go` `formatAlerts`: ブロック数を `maxAlertBlocksPerMessage` 以内に収める。常に Run ID `context` の 1 ブロックを末尾に確保する。ポリシー数が 49 件以下の場合は overflow summary なしで全ポリシーを表示する（50 ブロック以内）。49 件を超える場合のみ overflow summary `section` の 1 ブロックを追加確保し、上位 48 件を詳細表示した後 overflow summary `section`（`plain_text`）に `N additional policies omitted; organizations: X; failed sessions: Y` を表示する（アーキテクチャ §6.2-3）。overflow summary は overflow が必要な場合にのみ追加し、49 件以下のときに不要なブロックを予約して AC-03 を早期違反しない。
- [ ] **3-4** `format.go` `truncateMessage`: `Attachments[].Blocks[]` を走査し、`section.text`（`maxAlertSectionRunes`）と `context.elements[].text`（`maxAlertContextRunes`）を `TruncateText` で切り詰める。`slackBlock.Text` が `nil`（`divider` 等）の場合はスキップし、`Elements` は長さチェックの上で走査する（アーキテクチャ §6.2-4）。

完了条件: 過大入力でも単一 `slackMessage` のブロック数が 50 以内、各 `section.text` が 3000 rune 以内に収まる。

### Phase 4: テスト更新・追加

対象ファイル: `internal/notify/format_test.go`、`internal/notify/format_internal_test.go`、`internal/notify/helpers_test.go`、`internal/notify/message_test.go`、`internal/notify/security_test.go`、`internal/notify/handler_test.go`、`cmd/tlsrpt-digest/notify_helpers_test.go`、`cmd/tlsrpt-digest/slack_notify_env_test.go`、`cmd/tlsrpt-digest/slack_notify_integration_test.go`

**テストインフラ更新（前提）**
- [ ] **4-1** `format_test.go`: `capturedSlackAttachment` に `Blocks []capturedSlackBlock` を追加し、`capturedSlackBlock`/`capturedSlackTextObject` 型と、section/context テキストを取り出す `sectionTexts(msg capturedSlackMessage) []string` を追加する。`Fields`・`flattenSlackFields`・`flattenFields` はサマリー/警告テストが使用するため残す。

**既存アラートテストの改修（旧 `fields` 前提・部分文字列マッチ → `blocks` 構造検証）**

> 注意: 以下のうち `TestFormatAlerts_Fields`／`_NoTruncation`／`_RunID`／`_NoPolicyFound`／`_PolicyTypeUnknown`／`_Color` は生 JSON 本文への `Contains` 検証であり、刷新後も同じ文字列が `section.text` 内に出現するため**自動的には赤化しない**。これらは「壊れた blocks 実装でも緑になりうる」弱いテストなので、`sectionTexts(msg)` で取り出した特定 `section`/`context` のテキストを対象とする構造検証へ書き換え、誤レイアウトで確実に赤化するよう強化する。

- [ ] **4-2** `format_test.go` `TestFormatAlerts_Fields`: 組織・ポリシー・失敗数・期間の検証を、`sectionTexts` で取得した該当ポリシー `section` テキストに対する検証へ強化する（テスト名も `TestFormatAlerts_PolicySection` 等へ見直す）。
- [ ] **4-3** `format_test.go` `TestFormatAlerts_AttachmentFields`: `fields` の `title`/`value` を直接前提とし赤化する。`blocks` の `section`/`text` 構造検証へ書き換える。
- [ ] **4-4** `format_test.go` `TestFormatAlerts_NoTruncation`: 切り詰め対象を `section`/`context` テキストへ変え、`sectionTexts` 経由で長文が上限内に収まることを検証する。
- [ ] **4-5** `format_test.go` `TestFormatAlerts_RunID`: Run ID を末尾 `context` ブロックの `elements[].text` から取得して検証する。
- [ ] **4-6** `format_test.go` `TestFormatAlerts_NoPolicyFound`: 出力先を該当 `section` テキストへ更新する（`policyTypeStr` の挙動は不変）。
- [ ] **4-7** `format_test.go` `TestFormatAlerts_PolicyTypeUnknown`: 同上。
- [ ] **4-8** `format_test.go` `TestFormatAlerts_Color`: `attachment.color = "warning"` の検証をブロック構成変更後も成立するよう確認する（維持見込み）。
- [ ] **4-9** `format_test.go` `TestExtract_UnknownAttrKeyLogged`: `report_id`・`failure_details` が既知キーとして警告されないことと、未知トップレベルキー `unexpected_field` はキー名のみ警告されることを検証する。また以下の 2 つの不正ケースを同テストで検証する（アーキテクチャ §3.4・AC-13）。（a）`failure_details` の有効な子グループ内に想定外キーを注入した場合、キー名のみが警告され属性値がログ出力されない。（b）`failure_details["0"]` が非グループ値（例: 文字列）の場合でも `Flush` がパニックせず、当該エントリをスキップして残りのアラートが正常送信される（`extractAlert` は `Value.Group()` を呼ぶ前に `KindGroup` チェックを行う実装を要求する）。
- [ ] **4-10** `message_test.go` `TestSlackAttachment_FieldsEncoding`: ヘルパー `captureWarnPayload` がアラートを生成するため、`attachment.blocks`（`section`/`text`）検証へ書き換える。名称と実体が乖離した `captureWarnPayload` を `captureAlertPayload` へ改名する。
- [ ] **4-11** `message_test.go` `TestSlackMessage_JSONShape`: `text`・`attachments` の存在検証は維持。`captureWarnPayload` 改名に追従する。

**新規 AC テスト（`internal/notify/format_test.go`）**
- [ ] **4-12** `TestFormatAlerts_PolicySection`: 各ポリシーの組織名・ポリシータイプ・失敗セッション総数・レポート期間（UTC）が当該 `section` に表示される（AC-02、AC-12）。
- [ ] **4-13** `TestFormatAlerts_AllPoliciesIncluded`: 複数組織・複数ポリシーで全ポリシーが個別 `section` として含まれる（AC-03 通常系）。
- [ ] **4-14** `TestFormatAlerts_NoDuplicateHeaders`: 2 ポリシーで、旧見出し文字列が出力に存在せず、各ポリシーが独立 `section` として提示される（AC-04）。
- [ ] **4-15** `TestFormatAlerts_FailureDetails_Basic`: `failure-details` 存在時、各エントリの `result-type`・`failed-session-count` が表示される（AC-05）。
- [ ] **4-16** `TestFormatAlerts_FailureDetails_MXHostname`: `receiving-mx-hostname` の有無で表示/非表示が切り替わる（AC-06）。
- [ ] **4-17** `TestFormatAlerts_FailureDetails_ReasonCode`: `failure-reason-code` の有無で表示/非表示が切り替わる（AC-07）。
- [ ] **4-18** `TestFormatAlerts_FailureDetails_AllWhenLE3`: エントリ 3 件以下は全件詳細表示（AC-08）。
- [ ] **4-19** `TestFormatAlerts_FailureDetails_SummaryWhenGT3`: エントリ 4 件以上は上位 3 件＋`Other N entries (M sessions total)` 要約（AC-09）。
- [ ] **4-20** `TestFormatAlerts_FailureDetails_Empty`: `failure-details` 空でもエラー・不自然な空欄なく成立し、識別情報のみの `section` になる（AC-10）。
- [ ] **4-21** `TestFormatAlerts_ReportID`: Report ID が `section` に表示される（AC-11）。
- [ ] **4-22** `TestFormatAlerts_NormalizesControlChars`: 外部由来値に `\n`/`\r`/`\t` を含めても、`plain_text` 出力に偽の行が差し込まれず制御文字が空白化される（AC-13 表示無害化）。
- [ ] **4-23** `TestFormatAlerts_ValueTruncation`: 上限超の組織名・`policy-type`・Report ID・`result-type`・`receiving-mx-hostname`・`failure-reason-code` のそれぞれが値ごとの上限で切り詰められ、必須ラベルと識別情報が残ることを検証する（AC-14）。各 failure-detail フィールドの過大値が 3 件並んでも section が 3000 rune 以内に収まることも確認する。
- [ ] **4-24** `TestFormatAlerts_OverflowSummary`: ブロック上限を超える件数の失敗ポリシーで、単一メッセージのブロック数が 50 以内に収まり、overflow summary `section` に省略ポリシー数・対象組織数・合計失敗セッション数が表示される（AC-03 overflow・AC-14）。
- [ ] **4-25** `format_test.go` `TestTruncateMessage_Blocks`: `section.text` が 3000 rune 超で切り詰められ、`Text==nil` の `divider` ブロックでパニックしない（AC-14）。

**機微情報非混入（`internal/notify/security_test.go`）**
- [ ] **4-26** `TestAlertPayload_NoSensitiveData`（新規）: 公開 4 項目に最大長・記号入りの値を持つアラートを Block Kit で送信し、生成ペイロード本文に IP・`additional-information` 由来文字列・Webhook URL が含まれないことを検証する（AC-13）。
- [ ] **4-27** 既存の secret 非混入・Webhook URL 非ログ・Flush エラー secret 非混入・Debug/Slack 分離・通知 logger 非公開の回帰テストは削除・弱体化しない（実行して緑を確認）。

**集約・overflow の単一 POST（`internal/notify/handler_test.go`）**
- [ ] **4-28** `TestFlush_MultipleAlerts_SinglePost`（既存・`handler_test.go:345`）を拡張する。小規模な複数アラートが単一 POST に集約される既存検証は維持し、overflow が必要な大量アラートでも overflow summary を含む単一 POST として送信されることを追加検証する（AC-03、AC-14）。新規の集約テストは作らない（重複防止）。

**統合テスト（`cmd/tlsrpt-digest/slack_notify_integration_test.go`）**
- [ ] **4-29** `TestSlackNotify_FailureAlert_Integration`: 実 Webhook 送信による smoke/transport/目視確認として維持する。JSON 構造の厳密検証は追加せず（`internal/notify` 側に置く）、新表示が目視確認できる範囲に留める。`//go:build test && slack_notify` と環境変数スキップ（`loadSlackNotifyTestEnv`）は変更しない。

**統合テスト環境ヘルパー（`cmd/tlsrpt-digest/slack_notify_env_test.go`）**
- [ ] **4-30** `slack_notify_env_test.go` はすでに存在し、`missingSlackNotifyEnv(env map[string]string) []string` 純粋関数と `TestSlackNotify_EnvRequirements`（欠落・空文字・有効 URL・nil の各ケース）が実装済みである。本タスクでは変更不要であることを確認する（`go test -tags test ./cmd/tlsrpt-digest/...` で `TestSlackNotify_EnvRequirements` が緑であること）。

**完了ゲート**
- [ ] **4-31** `make fmt && make test && make lint` が緑。
- [ ] **4-32** `go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...` でビルドタグ付き統合テストファイルがコンパイルされる（型・シグネチャ不整合の早期検出）。

### PR-2 作成ポイント: Block Kit alert rendering

**対象ステップ**: 2-1〜2-7 / 3-1〜3-4 / 4-1〜4-32

**推奨タイトル**: `feat(0101): render TLS failure alerts with Block Kit sections`

**レビュー観点**: ポリシー単位 `section` の自己完結性と旧 `fields` 撤廃 / `plain_text` 無害化（制御文字正規化・メンション抑制） / ステップ 3-3：overflow 時は policy section 最大 48・通常時は最大 49 という 48/49 件分岐と block 数 ≤ 50 の invariant；`FailureDetailsTotalCount`/`FailureDetailsTotalSessions` が PR-1 の事前集計値（>10 件でも正確な元総数）を参照していること / ステップ 3-4：`divider` 等 `Text==nil` ブロックでパニックしない nil ガードの実装 / AC 別テストの網羅と許可リスト・機微情報テストの維持

- [ ] グリーンゲート（`_context.md` の "Green gate" 参照）がパスしていることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

## 3. 実装順序とマイルストーン

緑ゲート（`make test && make lint`）は **PR 境界**で担保する。フェーズ内の中間状態で一時的にテストが赤になることは許容するが、PR は緑で出す。

### 3.1 マイルストーン

| マイルストーン | 含むステップ | 内容 | 緑ゲート時の状態 |
|---|---|---|---|
| PR-1 | 1-1〜1-9 | データ構造・slog 往復・写像。`formatAlerts` は未変更で従来 `fields` を出力 | 既存アラートテストは緑のまま。追加: `TestLogAlert_StructuredPayloadOnly` 強化（1-7）、`TestLogAlert_FailureDetailsRoundTrip`（1-8）、`TestLogAlerts_MapsPublicFailureFields`（1-9）（`TestSlackNotify_EnvRequirements` は既存・変更不要） |
| PR-2 | 2-1〜2-7 / 3-1〜3-4 / 4-1〜4-32 | Block Kit 整形・切り詰め・overflow と、それに伴う全テスト更新/追加 | `TestFormatAlerts_AttachmentFields`・`TestSlackAttachment_FieldsEncoding` は刷新で赤化するため同一 PR で更新。部分文字列マッチの既存テストは赤化しないが、誤レイアウトを検出できるよう同一 PR で構造検証へ強化してから緑で出す |

### 3.2 PR 構成

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | 1-1〜1-9 | `Alert`/`FailureDetail` 型拡張、`LogAlert`/`extractAlert` slog 往復、`logAlerts` 写像、往復・写像境界テスト |
| PR-2 | 2-1〜2-7 / 3-1〜3-4 / 4-1〜4-32 | Block Kit 整形、切り詰め・overflow summary、全テスト更新/追加 |

`formatAlerts` の刷新（Phase 2）と既存アラートテストの構造検証化（Phase 4）は不可分のため、PR-2 にまとめる。部分文字列マッチのテストは刷新後も偶然緑になりうるが、それは「壊れた blocks 実装を見逃す」弱いテストであり、PR-2 内で `sectionTexts` 経由の構造検証へ強化する（§2 Phase 4 の注意書き参照）。Phase 1 は単独で緑を保てるため PR-1 として独立させ、レビュー単位を小さくする。

---

## 4. テスト戦略

- **単体テスト**: `internal/notify/format_test.go` に AC 別テストを集約（アーキテクチャ §7.1）。`failure-details` 0/1〜3/4 件以上、`receiving-mx-hostname`・`failure-reason-code` の有無、値ごと/overflow の切り詰めを境界値として網羅する。
- **slog 往復・許可リスト**: `helpers_test.go` と同じ `notify` パッケージの `format_internal_test.go` で順序保持と属性キー網羅（アーキテクチャ §3.4・§7.2）。
- **セキュリティテスト**: `security_test.go` で Block Kit ペイロードの機微情報非混入と既存回帰の維持（アーキテクチャ §7.2）。
- **写像境界テスト**: `cmd/tlsrpt-digest/notify_helpers_test.go` で公開 4 項目のみ写像（アーキテクチャ §5.2）。
- **統合テスト**: 実 Webhook 送信は smoke/目視のみ。JSON 構造検証は持ち込まない（アーキテクチャ §7.3）。
- **後方互換**: 警告・サマリー・システムエラーの `fields` 整形は不変であり、`flattenSlackFields` を使う既存テスト（`TestSummaryFlow_Integration` 他）が緑のまま維持されることで担保する。

---

## 5. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| `failure_details` を slog グループで往復する際の順序崩れ | 表示順が `failed_session_count` 降順と不一致 | `extractAlert` は挿入順復元（アーキテクチャ §3.4）。同じ `notify` パッケージの `TestLogAlert_FailureDetailsRoundTrip`（ステップ 1-8）で順序を検証。 |
| `failure_details` の上限処理で表示対象の順序が崩れる | AC-09 の上位 3 件と残件要約が、上位 10 件の降順というアーキテクチャ §3.4 の前提から外れる | `LogAlert` は `failed_session_count` 降順で最大 10 件を保持し、`TestLogAlert_FailureDetailsRoundTrip`（ステップ 1-8）と `TestFormatAlerts_FailureDetails_SummaryWhenGT3`（ステップ 4-19）で順序と 4 件以上の要約を検証。 |
| `truncateMessage` の blocks 走査で `nil` ポインタ参照 | 送信時パニック | `Text != nil` ガードを実装し、`TestTruncateMessage_Blocks`（ステップ 4-25）の `divider` ケースで検証。 |
| overflow 時にブロック数が 50 を超える | Slack が 400 を返し送信失敗（AC-14 違反） | Run ID・overflow summary の 2 ブロック予約（アーキテクチャ §6.2-3）。`TestFormatAlerts_OverflowSummary`（ステップ 4-24）でブロック数 ≤ 50 を検証。 |
| 部分文字列マッチの弱いテストが、誤った blocks 実装でも偶然緑になる | 誤レイアウトを CI が見逃す | 該当テストを `sectionTexts` 経由の構造検証へ強化（§2 Phase 4 注意書き）。新規 AC テストでレイアウトを明示検証。 |
| 旧 `fields` 前提テストの取りこぼし | PR-2 に赤テストが残る | §3.5 由来の改修対象テストを Phase 4 に網羅列挙し、完了ゲート（ステップ 4-31）で `make test` を確認。 |
| Slack 仕様の解釈差異が実装中に見つかる | 定数・payload shape の再設計で PR-2 が遅れる | §1.4 の公式仕様確認結果を PR-2 開始前に再確認し、差異があれば実装前にアーキテクチャ追補を 0.5 日分のバッファとして扱う。 |
| PR-1/PR-2 の境界をまたぐテスト更新が増える | PR-1 単独緑の前提が崩れレビューが大きくなる | PR-1 はデータ経路と round-trip に限定し、Block Kit 構造検証は PR-2 に閉じる。境界調整が必要な場合は PR-2 側へ寄せ、PR-1 には 0.5 日分の再分割バッファを置く。 |

---

## 6. 実装チェックリスト

- [ ] PR-1 マージ済み（対象ステップ: 1-1〜1-9）
- [ ] PR-2 マージ済み（対象ステップ: 2-1〜2-7 / 3-1〜3-4 / 4-1〜4-32）
- [ ] 完了ゲート: `make fmt && make test && make lint` 緑、ビルドタグ付き統合テストのコンパイル確認

---

## 7. 受け入れ条件の検証

各 AC を、実行可能テスト（`test`）／静的チェック（`static`）／目視（`manual`）で検証する。`path::TestName` は新規または改修後のテストを指す。

| AC | 区分 | 実装対象 | 検証方法 |
|---|---|---|---|
| AC-01 | test | `formatAlerts` の概要見出し、`uniqueOrgCount` | `internal/notify/format_test.go::TestFormatAlerts_TitleOrgCount`（維持）・`::TestFormatAlerts_TitleOrgCountDedup`（維持）で概要見出しの組織数を検証（グリーンゲートはステップ 4-31 で確認） |
| AC-02 | test | `formatAlerts` のポリシー別 `section.text` | `internal/notify/format_test.go::TestFormatAlerts_PolicySection`（ステップ 4-12）で組織名・ポリシータイプ・失敗数・期間を同一 section 内に表示することを検証 |
| AC-03 | test | `formatAlerts` の複数ポリシー section、`SlackHandler.Flush` の単一 POST | `internal/notify/format_test.go::TestFormatAlerts_AllPoliciesIncluded`（ステップ 4-13、通常系）・`::TestFormatAlerts_OverflowSummary`（ステップ 4-24、overflow 時の要約）・`internal/notify/handler_test.go::TestFlush_MultipleAlerts_SinglePost`（ステップ 4-28、単一 POST 集約）で検証 |
| AC-04 | test | `formatAlerts` の旧 `fields` 見出し削除と section 分離 | `internal/notify/format_test.go::TestFormatAlerts_NoDuplicateHeaders`（ステップ 4-14）で旧見出し非出力と独立 section を検証 |
| AC-05 | test | `formatAlerts` の failure-details 基本表示 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_Basic`（ステップ 4-15）で `result-type`・`failed-session-count` 表示を検証 |
| AC-06 | test | `formatAlerts` の `receiving-mx-hostname` 表示条件 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_MXHostname`（ステップ 4-16）で有無の組合せを検証 |
| AC-07 | test | `formatAlerts` の `failure-reason-code` 表示条件 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_ReasonCode`（ステップ 4-17）で有無の組合せを検証 |
| AC-08 | test | `formatAlerts` の 3 件以下 detail 展開 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_AllWhenLE3`（ステップ 4-18）で 3 件以下は全件詳細表示されることを検証 |
| AC-09 | test | `LogAlert` の事前集計＋最大 10 件保持、`extractAlert` の順序復元、`formatAlerts` の 4 件以上要約 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_SummaryWhenGT3`（ステップ 4-19）と `internal/notify/format_internal_test.go::TestLogAlert_FailureDetailsRoundTrip`（ステップ 1-8）で上位 3 件＋元データに基づく正確な残件数・残セッション数の要約を検証（>10 件のケースを含む） |
| AC-10 | test | `formatAlerts` の空 failure-details 処理 | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_Empty`（ステップ 4-20）で空配列でも識別情報 section が成立することを検証 |
| AC-11 | test | `LogAlert`/`extractAlert` の `report_id` 往復、`formatAlerts` の Report ID 表示 | `internal/notify/format_test.go::TestFormatAlerts_ReportID`（ステップ 4-21）と `internal/notify/format_internal_test.go::TestLogAlert_FailureDetailsRoundTrip`（ステップ 1-8）で検証 |
| AC-12 | test | `formatAlerts` の期間 UTC 表示 | `internal/notify/format_test.go::TestFormatAlerts_PolicySection`（ステップ 4-12）で期間が UTC で表示されることを併せて検証 |
| AC-13 | test | `LogAlert` 許可リスト、`logAlerts` 写像境界、Block Kit payload、制御文字正規化 | `internal/notify/helpers_test.go::TestLogAlert_StructuredPayloadOnly`（ステップ 1-7、許可リスト＋グループ再帰）、`internal/notify/security_test.go::TestAlertPayload_NoSensitiveData`（ステップ 4-26）、`internal/notify/format_test.go::TestFormatAlerts_NormalizesControlChars`（ステップ 4-22）、`cmd/tlsrpt-digest/notify_helpers_test.go::TestLogAlerts_MapsPublicFailureFields`（ステップ 1-9） |
| AC-13 | static | `notify.FailureDetail` 型の機微フィールド非保持 | `rg -n "SendingMTAIP|ReceivingIP|AdditionalInformation" internal/notify/types.go` 期待: マッチ 0 件（`rg` の Rust 正規表現では `|` をエスケープせず交替として用いる） |
| AC-14 | test | `formatAlerts` の値ごと切り詰めと overflow、`truncateMessage` の blocks 対応 | `internal/notify/format_test.go::TestFormatAlerts_ValueTruncation`（ステップ 4-23）・`::TestFormatAlerts_OverflowSummary`（ステップ 4-24）・`::TestTruncateMessage_Blocks`（ステップ 4-25） |

---

## 8. 横断確認チェックリスト

`make lint`／`make test` で検出できない事項のみを対象とする。

- [ ] `rg -n "maxAlertFields" internal/notify/` 期待: マッチ 0 件（削除済みであること。テスト・コメント含む残存参照がない）。
- [ ] `rg -n "captureWarnPayload" internal/notify/` 期待: マッチ 0 件（`captureAlertPayload` へ改名済み。改名漏れがない）。
- [ ] `rg -n "flattenSlackFields" internal/notify/` 期待: サマリー/警告テストでの使用のみが残り、アラートテストでの使用が消えていること（用途の取り違えがない）。

---

## 9. 完了基準・成功基準

- **機能完了**: 全 AC（AC-01〜AC-14）が §7 の `test`／`static` 検証で緑。
- **品質**: `make fmt && make test && make lint` が緑。`go test -tags test,slack_notify -run '^$' ./cmd/tlsrpt-digest/...` がコンパイル成功。
- **セキュリティ**: AC-13 の許可リスト・機微情報非混入・制御文字正規化テストが緑で、既存の secret 回帰テストが削除・弱体化されていない。
- **ドキュメント**: PR-1/PR-2 の説明に §7 の AC 検証結果、§1.4 の Slack 仕様確認根拠、未更新のタスク文書参照がないことの確認結果を記載する。
- 警告・サマリー・システムエラーの既存 `fields` 整形テストが緑のまま（後方互換）。

---

## 10. 次のステップ

- PR-1（Phase 1、ステップ 1-1〜1-9）から実装に着手する。
- 実装中は各ステップのチェックボックスをリアルタイムに更新する。
- PR-1・PR-2 をそれぞれ緑で提出し、§7 の AC 検証結果を PR 説明に添える。
