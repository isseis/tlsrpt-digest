# 実装計画書：Slack アラート通知フォーマット改善

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-08 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

関連文書: [01_requirements.md](01_requirements.md) / [02_architecture.md](02_architecture.md)

---

## 1. 実装概要

### 1.1 目的

即時アラート（`failure_session_count > 0`）の Slack 表示を Block Kit ベースへ刷新し、ポリシーごとの自己完結セクション、失敗詳細（公開 4 項目）、元データ識別情報（Report ID・期間）を表示する。設計の詳細は [02_architecture.md](02_architecture.md) を参照し、本書では作業手順・テスト・受け入れ条件の対応付けのみを示す。

### 1.2 実装方針

- 既存の送信経路（`SlackHandler.Flush()` → `send()` → `formatRecords()` → `formatAlerts()`）は変更しない（架構 §2.1）。
- 変更は `internal/notify` と `cmd/tlsrpt-digest` の既存パッケージ内に閉じ、新規パッケージ・新規ファイルは作らない。
- 既存資産（`TruncateText`・`policyTypeStr`・`uniqueOrgCount`・`SpyNotificationSink`・`spyHandler`・`buildCaptureHandler`・`decodeSlackMessage`）を再利用する。
- Go ソースのコメント・識別子・文字列リテラルは英語で記述する。

### 1.3 既存コード調査結果

| 領域 | 既存 | 変更内容 |
|---|---|---|
| `internal/notify/types.go` | `Alert{OrganizationName, PolicyType, FailureCount, DateRange}` | `ReportID string`・`FailureDetails []FailureDetail` を追加し、`FailureDetail` 型（公開 4 項目）を新設（架構 §3.1）。 |
| `internal/notify/message.go` | `slackMessage{Text, Attachments}`・`slackAttachment{Color, Fields}`・`slackField` | `slackAttachment` に `Blocks []slackBlock` を追加。`slackBlock`・`slackTextObject` を新設。`Fields` は警告/エラー/サマリーで継続使用（架構 §3.2）。 |
| `internal/notify/helpers.go` `LogAlert` | 5 属性（`organization_name`・`policy_type`・`failure_count`・`date_start`・`date_end`）を出力 | `report_id` 文字列属性と `failure_details` グループ（`failed_session_count` 降順・最大 10 件・インデックス付き子グループ）を追加（架構 §3.4）。 |
| `internal/notify/format.go` `extractAlert` | 上記 5 属性を `switch` で復元、未知キーは `warnUnknownKey` | `report_id`・`failure_details` グループの復元を追加（挿入順）。 |
| `internal/notify/format.go` `formatAlerts` | `maxAlertFields=9` で `fields` をチャンク分割 | Block Kit 生成へ全面刷新。`maxAlertFields` は削除し、ブロック数・section/context 文字数・値ごとの上限定数を新設（架構 §6.2 の定数表）。 |
| `internal/notify/format.go` `truncateMessage` | `Text`（`maxTextRunes=4000`）と `Attachments[].Fields[].Value`（`maxFieldRunes=1000`）のみ切り詰め | `Attachments[].Blocks[]` の `section`/`context` テキスト走査を追加。`slackBlock.Text` は `nil` ガード必須（架構 §6.2-4）。 |
| `cmd/tlsrpt-digest/notify_helpers.go` `logAlerts` | `report`→`Alert` を 4 項目で写像 | `report.ReportID` と `policy.FailureDetails` の公開 4 項目（`ResultType`・`FailedSessionCount`・`ReceivingMXHostname`・`FailureReasonCode`）を写像。`SendingMTAIP`・`ReceivingIP`・`AdditionalInformation` は写像しない（架構 §5.2）。 |

**変更サイトの一意性**:
- `formatAlerts`・`maxAlertFields` は `internal/notify/format.go` のみに存在（`rg -n "formatAlerts|maxAlertFields" internal/notify --glob '!*_test.go'` で確認済み）。
- `logAlerts` の定義は `cmd/tlsrpt-digest/notify_helpers.go` の 1 箇所。呼び出しは `fetch.go:315`・`reprocess.go:130`・統合テスト・単体テストにあるが、写像ロジックは定義 1 箇所の変更で全呼び出しに反映される。
- 旧見出しリテラル `"Organization / Policy / Failures / Period"` は `formatAlerts` 内のみに存在し、刷新で消滅する。

**再利用するため新規作成しないもの**: `TruncateText`（rune 単位切り詰め、`section` 上限にも流用）、`policyTypeStr`（unknown→`(unknown)`）、`uniqueOrgCount`（概要見出しの組織数）、`SpyNotificationSink.Alerts`（写像検証）、`security_test.go` の許可リスト方式。

**架構の注記との差異（要確認）**: 架構 §3.5 は `TestExtract_UnknownAttrKeyLogged` を「改修」としているが、本テストが注入する未知キーは `unexpected_field` であり（`format_test.go:532` で確認）、`report_id`・`failure_details` を既知キーへ追加しても本テストの振る舞いは変わらない。本計画では**維持**（変更不要）とし、念のため Phase 4 で再実行による緑確認のみ行う。

### 1.4 新規テストヘルパーの要否

新規の `testutil/` 配下ヘルパーや `test_helpers.go` は作成しない。理由は以下。
- 機微情報非複写の写像検証は既存の `cmd/tlsrpt-digest/test_helpers.go`（`//go:build test`）の `SpyNotificationSink.Alerts` で足りる。
- Block Kit ペイロードのデコードは、既存の `format_test.go` 内テストローカル型（`capturedSlackMessage`/`capturedSlackAttachment`）の拡張で足りる（`_test.go` 内のため新規ビルドタグ付きファイルは不要）。

---

## 2. 実装ステップ

> 凡例: `[ ]` 未着手 / `[x]` 完了 / `[-]` 一部完了（注記付き）。各ステップの「完了条件」は当該変更が満たすべき観測可能な状態。

### Phase 1: データ構造の拡張とデータ経路

対象ファイル: `internal/notify/types.go`、`internal/notify/helpers.go`、`internal/notify/format.go`、`cmd/tlsrpt-digest/notify_helpers.go`

- [ ] `types.go`: `Alert` に `ReportID string`・`FailureDetails []FailureDetail` を追加する（架構 §3.1 のコード定義に一致させる）。
- [ ] `types.go`: `FailureDetail` 型（`ResultType string`・`FailedSessionCount int64`・`ReceivingMXHostname string`・`FailureReasonCode string`）を新設する。IP・`additional-information` に相当するフィールドは設けない。
- [ ] `helpers.go` `LogAlert`: `report_id` 文字列属性を常に追加する。`failure_details` を `failed_session_count` 降順で最大 10 件に絞り、インデックス名（`"0"`,`"1"`,…）の子グループとして追加する。各子グループのキーは `result_type`・`failed_session_count`・`receiving_mx_hostname`・`failure_reason_code` の 4 つ。
- [ ] `format.go` `extractAlert`: `report_id` と `failure_details` グループを復元する。`failure_details` の子グループは `attr.Value.Group()` が返すスライス順で `[]FailureDetail` に戻す（既存 `extractSummary` の `organization_stats` 処理と同方式・format.go:200-208）。スライス順が `LogAlert` の追加順＝降順を保持するため、キーの文字列ソートには依存しない。想定外キーは既存どおり `warnUnknownKey` で警告する（架構 §3.4）。
- [ ] `cmd/tlsrpt-digest/notify_helpers.go` `logAlerts`: `Alert` 構築に `ReportID: report.ReportID` を追加し、`policy.FailureDetails` を公開 4 項目のみで `[]notify.FailureDetail` へ写像する。`SendingMTAIP`・`ReceivingIP`・`AdditionalInformation` は参照しない。

完了条件: `go build ./...` が通り、`formatAlerts` は新フィールドを無視したまま従来どおり `fields` を出力するため既存アラートテストは緑のまま。

### Phase 2: Block Kit 整形

対象ファイル: `internal/notify/message.go`、`internal/notify/format.go`

- [ ] `message.go`: `slackAttachment` に `Blocks []slackBlock` を追加する（架構 §3.2 のコード定義に一致）。`Fields` は残す。
- [ ] `message.go`: `slackBlock`・`slackTextObject` を新設する。
- [ ] `format.go` `formatAlerts`: `fields` 生成を廃止し、`Attachments[0].Color = "warning"` の単一 attachment に、ポリシーごとの `section` ブロック群＋末尾 `context`（Run ID）を生成する（架構 §3.3）。
- [ ] `format.go`: 各ポリシー `section.text` を `plain_text` で構築する。組織名・ポリシータイプ・失敗セッション総数・レポート期間（`.UTC()` 整形）・Report ID・失敗詳細を所定の行レイアウトで配置する（架構 §3.3 の行テーブル）。
- [ ] `format.go`: 失敗詳細を `failed_session_count` 降順に並べ、上位 3 件を詳細表示、4 件以上は残りを「他 N 件（合計 M セッション）」として要約する。空の場合は失敗詳細行を出力しない。
- [ ] `format.go`: 外部由来文字列（組織名・Report ID・`result-type`・`receiving-mx-hostname`・`failure-reason-code`）を `plain_text` へ入れる前に、`\n`・`\r`・`\t` を含む制御文字を空白へ正規化する。セクション内の項目間改行は実装テンプレート側で付加する（架構 §3.3・§5.2）。
- [ ] `format.go`: `maxAlertFields` 定数とその参照を削除する。

完了条件: `go build ./...` が通る。なお既存アラートテストの多くは生 JSON 本文への部分文字列マッチ（`Contains`）であり、刷新後も同じ文字列が `section.text` 内に現れるため**自動的には赤化しない**。`TestFormatAlerts_AttachmentFields` のみ `fields` の `title`/`value` を直接前提とするため赤化する。Phase 4 では、これら部分文字列テストを「壊れた blocks 実装では緑にならない」構造検証（`sectionTexts` 経由）へ強化する（§2 Phase 4・§3 参照）。

### Phase 3: サイズ制限と切り詰め

対象ファイル: `internal/notify/format.go`

- [ ] `format.go`: 架構 §6.2 の定数表に基づき `maxAlertBlocksPerMessage=50`・`maxAlertSectionRunes=3000`・`maxAlertContextRunes=300`・`maxAlertOrganizationRunes=120`・`maxAlertReportIDRunes=160`・`maxAlertResultTypeRunes=80`・`maxAlertMXHostnameRunes=120`・`maxAlertReasonCodeRunes=80` を定義する。
- [ ] `format.go` `formatAlerts`: 外部由来値を section 組み立て前に値ごとの上限で切り詰める（`TruncateText` を流用）。期間・ポリシータイプ・失敗数・静的ラベルは切り詰めない。
- [ ] `format.go` `formatAlerts`: ブロック数を `maxAlertBlocksPerMessage` 以内に収める。Run ID `context` と overflow summary `section` の 2 ブロックを予約し、上限超過時は詳細表示するポリシーを打ち切り、overflow summary `section`（`plain_text`）に「詳細表示できなかったポリシー数」「対象組織数」「合計失敗セッション数」を表示する（架構 §6.2-3）。
- [ ] `format.go` `truncateMessage`: `Attachments[].Blocks[]` を走査し、`section.text`（`maxAlertSectionRunes`）と `context.elements[].text`（`maxAlertContextRunes`）を `TruncateText` で切り詰める。`slackBlock.Text` が `nil`（`divider` 等）の場合はスキップし、`Elements` は長さチェックの上で走査する（架構 §6.2-4）。

完了条件: 過大入力でも単一 `slackMessage` のブロック数が 50 以内、各 `section.text` が 3000 rune 以内に収まる。

### Phase 4: テスト更新・追加

対象ファイル: `internal/notify/format_test.go`、`internal/notify/helpers_test.go`、`internal/notify/message_test.go`、`internal/notify/security_test.go`、`internal/notify/handler_test.go`、`cmd/tlsrpt-digest/notify_helpers_test.go`、`cmd/tlsrpt-digest/slack_notify_integration_test.go`

**テストインフラ更新（前提）**
- [ ] `format_test.go`: `capturedSlackAttachment` に `Blocks []capturedSlackBlock` を追加し、`capturedSlackBlock`/`capturedSlackTextObject` 型と、section/context テキストを取り出す `sectionTexts(msg capturedSlackMessage) []string` を追加する。`Fields`・`flattenSlackFields`・`flattenFields` はサマリー/警告テストが使用するため残す。

**既存アラートテストの改修（旧 `fields` 前提・部分文字列マッチ → `blocks` 構造検証）**

> 注意: 以下のうち `TestFormatAlerts_Fields`／`_NoTruncation`／`_RunID`／`_NoPolicyFound`／`_PolicyTypeUnknown`／`_Color` は生 JSON 本文への `Contains` 検証であり、刷新後も同じ文字列が `section.text` 内に出現するため**自動的には赤化しない**。これらは「壊れた blocks 実装でも緑になりうる」弱いテストなので、`sectionTexts(msg)` で取り出した特定 `section`/`context` のテキストを対象とする構造検証へ書き換え、誤レイアウトで確実に赤化するよう強化する。

- [ ] `format_test.go` `TestFormatAlerts_Fields`: 組織・ポリシー・失敗数・期間の検証を、`sectionTexts` で取得した該当ポリシー `section` テキストに対する検証へ強化する（テスト名も `TestFormatAlerts_PolicySection` 等へ見直す）。
- [ ] `format_test.go` `TestFormatAlerts_AttachmentFields`: `fields` の `title`/`value` を直接前提とし赤化する。`blocks` の `section`/`text` 構造検証へ書き換える。
- [ ] `format_test.go` `TestFormatAlerts_NoTruncation`: 切り詰め対象を `section`/`context` テキストへ変え、`sectionTexts` 経由で長文が上限内に収まることを検証する。
- [ ] `format_test.go` `TestFormatAlerts_RunID`: Run ID を末尾 `context` ブロックの `elements[].text` から取得して検証する。
- [ ] `format_test.go` `TestFormatAlerts_NoPolicyFound`: 出力先を該当 `section` テキストへ更新する（`policyTypeStr` の挙動は不変）。
- [ ] `format_test.go` `TestFormatAlerts_PolicyTypeUnknown`: 同上。
- [ ] `format_test.go` `TestFormatAlerts_Color`: `attachment.color = "warning"` の検証をブロック構成変更後も成立するよう確認する（維持見込み）。
- [ ] `message_test.go` `TestSlackAttachment_FieldsEncoding`: ヘルパー `captureWarnPayload` がアラートを生成するため、`attachment.blocks`（`section`/`text`）検証へ書き換える。名称と実体が乖離した `captureWarnPayload` を `captureAlertPayload` へ改名する。
- [ ] `message_test.go` `TestSlackMessage_JSONShape`: `text`・`attachments` の存在検証は維持。`captureWarnPayload` 改名に追従する。

**新規 AC テスト（`internal/notify/format_test.go`）**
- [ ] `TestFormatAlerts_PolicySection`: 各ポリシーの組織名・ポリシータイプ・失敗セッション総数・レポート期間（UTC）が当該 `section` に表示される（AC-02、AC-12）。
- [ ] `TestFormatAlerts_AllPoliciesIncluded`: 複数組織・複数ポリシーで全ポリシーが個別 `section` として含まれる（AC-03 通常系）。
- [ ] `TestFormatAlerts_NoDuplicateHeaders`: 2 ポリシーで、旧見出し文字列が出力に存在せず、各ポリシーが独立 `section` として提示される（AC-04）。
- [ ] `TestFormatAlerts_FailureDetails_Basic`: `failure-details` 存在時、各エントリの `result-type`・`failed-session-count` が表示される（AC-05）。
- [ ] `TestFormatAlerts_FailureDetails_MXHostname`: `receiving-mx-hostname` の有無で表示/非表示が切り替わる（AC-06）。
- [ ] `TestFormatAlerts_FailureDetails_ReasonCode`: `failure-reason-code` の有無で表示/非表示が切り替わる（AC-07）。
- [ ] `TestFormatAlerts_FailureDetails_AllWhenLE3`: エントリ 3 件以下は全件詳細表示（AC-08）。
- [ ] `TestFormatAlerts_FailureDetails_SummaryWhenGT3`: エントリ 4 件以上は上位 3 件＋「他 N 件（合計 M セッション）」要約（AC-09）。
- [ ] `TestFormatAlerts_FailureDetails_Empty`: `failure-details` 空でもエラー・不自然な空欄なく成立し、識別情報のみの `section` になる（AC-10）。
- [ ] `TestFormatAlerts_ReportID`: Report ID が `section` に表示される（AC-11）。
- [ ] `TestFormatAlerts_NormalizesControlChars`: 外部由来値に `\n`/`\r`/`\t` を含めても、`plain_text` 出力に偽の行が差し込まれず制御文字が空白化される（AC-13 表示無害化）。
- [ ] `TestFormatAlerts_ValueTruncation`: 上限超の組織名・Report ID が値ごとの上限で切り詰められ、必須ラベルと識別情報が残る（AC-14）。
- [ ] `TestFormatAlerts_OverflowSummary`: ブロック上限を超える件数の失敗ポリシーで、単一メッセージのブロック数が 50 以内に収まり、overflow summary `section` に省略ポリシー数・対象組織数・合計失敗セッション数が表示される（AC-03 overflow・AC-14）。
- [ ] `format_test.go` `TestTruncateMessage_Blocks`: `section.text` が 3000 rune 超で切り詰められ、`Text==nil` の `divider` ブロックでパニックしない（AC-14）。

**slog 往復・許可リスト（`internal/notify/helpers_test.go`）**
- [ ] `TestLogAlert_StructuredPayloadOnly`: 既存の存在検証を、他ヘルパーの `security_test.go` と同じ**許可リスト方式**へ強化する。トップレベル許可キーは `organization_name`・`policy_type`・`failure_count`・`date_start`・`date_end`・`report_id`・`failure_details`。`failure_details` グループ内へ再帰し、子グループのキーが `result_type`・`failed_session_count`・`receiving_mx_hostname`・`failure_reason_code` の 4 つのみであることを検証する（AC-13）。
- [ ] `helpers_test.go` `TestLogAlert_FailureDetailsRoundTrip`（新規）: `LogAlert` → `extractAlert` 相当の往復で、`failed_session_count` 降順・最大 10 件・順序保持が成り立つことを検証する。

**機微情報非混入（`internal/notify/security_test.go`）**
- [ ] `TestAlertPayload_NoSensitiveData`（新規）: 公開 4 項目に最大長・記号入りの値を持つアラートを Block Kit で送信し、生成ペイロード本文に IP・`additional-information` 由来文字列・Webhook URL が含まれないことを検証する（AC-13）。
- [ ] 既存の secret 非混入・Webhook URL 非ログ・Flush エラー secret 非混入・Debug/Slack 分離・通知 logger 非公開の回帰テストは削除・弱体化しない（実行して緑を確認）。

**集約・overflow の単一 POST（`internal/notify/handler_test.go`）**
- [ ] `TestFlush_MultipleAlerts_SinglePost`（既存・`handler_test.go:345`）を拡張する。小規模な複数アラートが単一 POST に集約される既存検証は維持し、overflow が必要な大量アラートでも overflow summary を含む単一 POST として送信されることを追加検証する（AC-03、AC-14）。新規の集約テストは作らない（重複防止）。

**写像境界の機微情報非複写（`cmd/tlsrpt-digest/notify_helpers_test.go`）**
- [ ] `TestLogAlerts_MapsPublicFailureFields`（新規）: `policy.FailureDetails` に IP・`additional-information` を含む `tlsrpt.Report` を `logAlerts` に渡し、`SpyNotificationSink.Alerts[0]` の `ReportID` と各 `FailureDetail` の公開 4 項目が期待値に一致することを検証する（IP・自由記述は `notify.FailureDetail` 型に存在しないため構造的に非複写）（AC-13）。

**統合テスト（`cmd/tlsrpt-digest/slack_notify_integration_test.go`）**
- [ ] `TestSlackNotify_FailureAlert_Integration`: 実 Webhook 送信による smoke/transport/目視確認として維持する。JSON 構造の厳密検証は追加せず（`internal/notify` 側に置く）、新表示が目視確認できる範囲に留める。`//go:build test && slack_notify` と環境変数スキップ（`loadSlackNotifyTestEnv`）は変更しない。

**完了ゲート**
- [ ] `make fmt && make test && make lint` が緑。
- [ ] `go test -tags 'test slack_notify' -run '^$' ./cmd/tlsrpt-digest/...` でビルドタグ付き統合テストファイルがコンパイルされる（型・シグネチャ不整合の早期検出）。

---

## 3. 実装順序とマイルストーン

緑ゲート（`make test && make lint`）は **PR 境界**で担保する。フェーズ内の中間状態で一時的にテストが赤になることは許容するが、PR は緑で出す。

| マイルストーン | 含むフェーズ | 内容 | 緑ゲート時の状態 |
|---|---|---|---|
| PR-1 | Phase 1 | データ構造・slog 往復・写像。`formatAlerts` は未変更で従来 `fields` を出力 | 既存アラートテストは緑のまま。追加: `TestLogAlert_StructuredPayloadOnly` 強化、`TestLogAlert_FailureDetailsRoundTrip`、`TestLogAlerts_MapsPublicFailureFields` |
| PR-2 | Phase 2・Phase 3・Phase 4 | Block Kit 整形・切り詰め・overflow と、それに伴う全テスト更新/追加 | `TestFormatAlerts_AttachmentFields` は刷新で赤化するため同一 PR で更新。部分文字列マッチの既存テストは赤化しないが、誤レイアウトを検出できるよう同一 PR で構造検証へ強化してから緑で出す |

`formatAlerts` の刷新（Phase 2）と既存アラートテストの構造検証化（Phase 4）は不可分のため、PR-2 にまとめる。部分文字列マッチのテストは刷新後も偶然緑になりうるが、それは「壊れた blocks 実装を見逃す」弱いテストであり、PR-2 内で `sectionTexts` 経由の構造検証へ強化する（§2 Phase 4 の注意書き参照）。Phase 1 は単独で緑を保てるため PR-1 として独立させ、レビュー単位を小さくする。

### PR-1 作成ポイント: data path for report-id and failure-details

- **対象ステップ**: Phase 1 全タスク＋上表の PR-1 追加テスト。
- **推奨タイトル**: `feat(0101): carry report-id and failure-details through the alert data path`
- **レビュー観点**: `Alert`/`FailureDetail` 型、`LogAlert`/`extractAlert` の往復と順序保持、`logAlerts` 写像での機微情報非複写。

### PR-2 作成ポイント: Block Kit alert rendering

- **対象ステップ**: Phase 2・Phase 3・Phase 4 全タスク。
- **推奨タイトル**: `feat(0101): render TLS failure alerts with Block Kit sections`
- **レビュー観点**: ポリシー単位 `section`、`plain_text` 無害化と制御文字正規化、値ごと/section/ブロック数の切り詰めと overflow summary、AC 別テストの網羅。

---

## 4. テスト戦略

- **単体テスト**: `internal/notify/format_test.go` に AC 別テストを集約（架構 §7.1）。`failure-details` 0/1〜3/4 件以上、`receiving-mx-hostname`・`failure-reason-code` の有無、値ごと/overflow の切り詰めを境界値として網羅する。
- **slog 往復・許可リスト**: `helpers_test.go` で順序保持と属性キー網羅（架構 §3.4・§7.2）。
- **セキュリティテスト**: `security_test.go` で Block Kit ペイロードの機微情報非混入と既存回帰の維持（架構 §7.2）。
- **写像境界テスト**: `cmd/tlsrpt-digest/notify_helpers_test.go` で公開 4 項目のみ写像（架構 §5.2）。
- **統合テスト**: 実 Webhook 送信は smoke/目視のみ。JSON 構造検証は持ち込まない（架構 §7.3）。
- **後方互換**: 警告・サマリー・システムエラーの `fields` 整形は不変であり、`flattenSlackFields` を使う既存テスト（`TestSummaryFlow_Integration` 他）が緑のまま維持されることで担保する。

---

## 5. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| `failure_details` を slog グループで往復する際の順序崩れ | 表示順が `failed_session_count` 降順と不一致 | `extractAlert` は挿入順復元（架構 §3.4）。`TestLogAlert_FailureDetailsRoundTrip` で順序を検証。 |
| `truncateMessage` の blocks 走査で `nil` ポインタ参照 | 送信時パニック | `Text != nil` ガードを実装し、`TestTruncateMessage_Blocks` の `divider` ケースで検証。 |
| overflow 時にブロック数が 50 を超える | Slack が 400 を返し送信失敗（AC-14 違反） | Run ID・overflow summary の 2 ブロック予約（架構 §6.2-3）。`TestFormatAlerts_OverflowSummary` でブロック数 ≤ 50 を検証。 |
| 部分文字列マッチの弱いテストが、誤った blocks 実装でも偶然緑になる | 誤レイアウトを CI が見逃す | 該当テストを `sectionTexts` 経由の構造検証へ強化（§2 Phase 4 注意書き）。新規 AC テストでレイアウトを明示検証。 |
| 旧 `fields` 前提テストの取りこぼし | PR-2 に赤テストが残る | §3.5 由来の改修対象テストを Phase 4 に網羅列挙し、完了ゲートで `make test` を確認。 |

---

## 6. 実装チェックリスト

- [ ] Phase 1: `Alert`/`FailureDetail` 拡張、`LogAlert`/`extractAlert` 往復、`logAlerts` 写像
- [ ] Phase 2: `slackBlock` 型、`formatAlerts` 刷新、`plain_text` 無害化、`maxAlertFields` 削除
- [ ] Phase 3: 値ごと/section/ブロック数の上限、overflow summary、`truncateMessage` blocks 対応
- [ ] Phase 4: 既存テスト改修、AC 別テスト、許可リスト強化、セキュリティ/写像/統合テスト
- [ ] 完了ゲート: `make fmt && make test && make lint` 緑、ビルドタグ付き統合テストのコンパイル確認

---

## 7. 受け入れ条件の検証

各 AC を、実行可能テスト（`test`）／静的チェック（`static`）／目視（`manual`）で検証する。`path::TestName` は新規または改修後のテストを指す。

| AC | 区分 | 検証方法 |
|---|---|---|
| AC-01 | test | `internal/notify/format_test.go::TestFormatAlerts_TitleOrgCount`（維持）・`::TestFormatAlerts_TitleOrgCountDedup`（維持）で概要見出しの組織数を検証 |
| AC-02 | test | `internal/notify/format_test.go::TestFormatAlerts_PolicySection` で組織名・ポリシータイプ・失敗数・期間を検証 |
| AC-03 | test | `internal/notify/format_test.go::TestFormatAlerts_AllPoliciesIncluded`（通常系）・`::TestFormatAlerts_OverflowSummary`（overflow 時の要約）・`internal/notify/handler_test.go::TestFlush_MultipleAlerts_SinglePost`（単一 POST 集約）で検証 |
| AC-04 | test | `internal/notify/format_test.go::TestFormatAlerts_NoDuplicateHeaders` で旧見出し非出力と独立 section を検証 |
| AC-05 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_Basic` |
| AC-06 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_MXHostname`（有無の組合せ） |
| AC-07 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_ReasonCode`（有無の組合せ） |
| AC-08 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_AllWhenLE3` |
| AC-09 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_SummaryWhenGT3` |
| AC-10 | test | `internal/notify/format_test.go::TestFormatAlerts_FailureDetails_Empty` |
| AC-11 | test | `internal/notify/format_test.go::TestFormatAlerts_ReportID` |
| AC-12 | test | `internal/notify/format_test.go::TestFormatAlerts_PolicySection`（期間が UTC で表示されることを併せて検証） |
| AC-13 | test | `internal/notify/helpers_test.go::TestLogAlert_StructuredPayloadOnly`（許可リスト＋グループ再帰）、`internal/notify/security_test.go::TestAlertPayload_NoSensitiveData`、`internal/notify/format_test.go::TestFormatAlerts_NormalizesControlChars`、`cmd/tlsrpt-digest/notify_helpers_test.go::TestLogAlerts_MapsPublicFailureFields` |
| AC-13 | static | `rg -n "SendingMTAIP|ReceivingIP|AdditionalInformation" internal/notify/types.go` 期待: マッチ 0 件（`notify.FailureDetail` が機微フィールドを型として持たないことの確認。`rg` の Rust 正規表現では `|` をエスケープせず交替として用いる） |
| AC-14 | test | `internal/notify/format_test.go::TestFormatAlerts_ValueTruncation`・`::TestFormatAlerts_OverflowSummary`・`::TestTruncateMessage_Blocks` |

---

## 8. 横断確認チェックリスト

`make lint`／`make test` で検出できない事項のみを対象とする。

- [ ] `rg -n "maxAlertFields" internal/notify/` 期待: マッチ 0 件（削除済みであること。テスト・コメント含む残存参照がない）。
- [ ] `rg -n "captureWarnPayload" internal/notify/` 期待: マッチ 0 件（`captureAlertPayload` へ改名済み。改名漏れがない）。
- [ ] `rg -n "flattenSlackFields" internal/notify/` 期待: サマリー/警告テストでの使用のみが残り、アラートテストでの使用が消えていること（用途の取り違えがない）。

---

## 9. 完了基準

- 全 AC（AC-01〜AC-14）が §7 の `test`／`static` 検証で緑。
- `make fmt && make test && make lint` が緑。`go test -tags 'test slack_notify' -run '^$' ./cmd/tlsrpt-digest/...` がコンパイル成功。
- 警告・サマリー・システムエラーの既存 `fields` 整形テストが緑のまま（後方互換）。
- 既存のセキュリティ回帰テストが削除・弱体化なく緑。

---

## 10. 次のステップ

- 本計画が `approved` になり次第、PR-1（Phase 1）から実装に着手する。
- 実装中は各ステップのチェックボックスをリアルタイムに更新する。
- PR-1・PR-2 をそれぞれ緑で提出し、§7 の AC 検証結果を PR 説明に添える。
