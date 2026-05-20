# 要件定義書：定期サマリ生成・通知

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-12 |
| レビュー日 | 2026-05-20 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

failure のない TLSRPT レポートは即時通知せず、定期サマリとしてまとめて通知する（集計期間は呼び出し側で指定可能、デフォルトは 7 日）。
このタスクでは `internal/store` から取得したデータを集計し、`internal/notify` 経由で定期サマリを送信する機能を実装する。

### 1.2 目的

1. **主目的**: 指定された集計期間内の正常レポートを集計して定期サマリ通知を送信する
2. **副次的目的**: システムが正常動作していることを定期的に管理者に伝える

---

## 2. スコープ

### 対象範囲（In Scope）

- `internal/store` から全レポートを取得（`GetAllReports()` を使用）し、集計期間・failure によるフィルタリングはアプリケーション層で実施する
- 組織別の集計（ポリシー別集計は将来タスク）
- 定期サマリメッセージのフォーマット
- `Notifier` インターフェース経由での通知送信

### 対象外（Out of Scope）

- 実行スケジューリング（外部スケジューラー cron/systemd timer が担当）
- failure 検出時の即時アラート（タスク 0030 で担当）
- ポリシー別の success_session_count 集計（将来タスク）
- レポートなし時の動作の設定化（タスク 0070 で担当）

### 影響を受けるコンポーネント

- **直接変更**: `internal/notify/`（以下の3ファイルを変更する）
  - `types.go`: `Summary` 型の `OrganizationCount` を削除し、`OrganizationStats map[string]int64` を追加する
  - `helpers.go`: `LogSummary` を更新し、`OrganizationStats` を `slog.Group("organization_stats", ...)` としてまとめて出力する（組織名は動的キーであるため、個別の slog 属性ではなくグループとして出力することで `extractSummary` のパース処理と整合させる）
  - `format.go`: `extractSummary` と `formatSummary` を更新し、`OrganizationStats`（組織別成功セッション数）を処理・表示する
- **読み取りのみ**: `internal/store/`（`GetAllReports` を利用。store 層はフィルタリングを担わない）

---

## 3. 機能要件

### F-001: 定期サマリの集計

呼び出し元から渡された集計期間内に蓄積されたレポートデータを集計する。集計期間の指定方法（CLI フラグ／TOML 設定）はタスク 0070 で担う。本タスクは集計期間を引数で受け取る API を提供する。

レポート取得は `store.GetAllReports()` を使用する。開始・終了日時によるフィルタリングおよび failure 除外（`report.HasFailure() == true`）はすべてアプリケーション層で実施する。store 層はフィルタリングを担わない。

集計期間は半開区間 `start < DateRange.EndDatetime <= end` とする。これにより、連続する集計期間で境界上のレポートが重複カウントされない。

`HasFailure() == true` のレポートは集計から除外するが、除外されたレポートに成功セッションが含まれる場合（`sum(policy.Summary.TotalSuccessfulSessionCount) > 0`）は、成功セッションが集計に含まれないことを示す警告を出力する。これにより、混在レポート（同一レポート内に失敗と成功が共存するケース）を検出できる。

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: 集計期間（開始日時・終了日時）を引数で受け取り、`start < DateRange.EndDatetime <= end` を満たし、かつ `HasFailure() == false` のレポートを組織名別にグループ化して集計できる
- `AC-02`: 各組織について `TotalSuccessfulSessionCount` の合計を算出できる
- `AC-03`: 集計対象期間（開始日時・終了日時）がサマリに反映される
- `AC-04`: 対象期間にレポートが存在しない場合、「レポートなし」として常に通知する（設定化はタスク 0070 で担う）
- `AC-11`: `HasFailure() == true` かつ `sum(policy.Summary.TotalSuccessfulSessionCount) > 0` のレポートを検出した場合、組織名・集計期間・成功セッション数を含む警告を `slog.Warn` で出力する

### F-002: 定期サマリのメッセージフォーマット

集計結果を定期サマリメッセージとしてフォーマットする。

Slack Incoming Webhook は 1 attachment あたり最大 10 フィールドまでという制約がある。組織数が多い場合にこの制限を超える可能性があるため、`formatSummary` は `formatAlerts` と同様に attachment をチャンクして複数に分割する実装とする。

メッセージ構成は以下の通りとする:
- 集計期間（AC-05）・レポート総数（AC-07）・組織総数は、Slack メッセージの `text` フィールドに配置する（attachment フィールドとしてカウントされない）
- 各 attachment には組織フィールドのみを配置し、1 attachment あたり最大 9 フィールドとする
- Run ID フィールドは最後の attachment にのみ追加する（1 フィールド消費）
- これにより各 attachment のフィールド数は最大 9（通常組織フィールド）または 10（最後の attachment: 組織フィールド 9 + Run ID 1）以内に収まる

**受け入れ条件（Acceptance Criteria）**:

- `AC-05`: サマリメッセージにレポート対象期間（開始〜終了）が含まれる
- `AC-06`: 組織別の `TotalSuccessfulSessionCount` 合計が含まれる（ポリシー別の内訳は含まない）
- `AC-07`: 処理したレポート総数が含まれる
- `AC-10`: 組織数が 9 を超える場合、組織フィールドは複数の attachment に分割される（1 attachment あたり最大 9 組織フィールド。Run ID は最後の attachment にのみ付与。集計期間・レポート総数・組織総数は `text` フィールドに配置するため attachment のフィールドカウントに含まれない）

### F-003: 定期サマリの送信

`Notifier` インターフェース経由で定期サマリを送信する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-08`: 定期サマリが正しく Notifier に渡される
- `AC-09`: 送信失敗時はエラーを返す

---

## 4. 非機能要件

### パフォーマンス

- 定期サマリの集計・フォーマット処理は、想定累積上限（タスク 0040 の 1 万件）規模で 1 秒以内に完了すること

### 保守性

- 即時アラートと定期サマリでメッセージフォーマット処理を分離する

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- 通知には `internal/notify` の `slog.Handler` ベース実装を使用し、型付きイベントヘルパー経由で送信する
- `notify.Summary` の `OrganizationStats` フィールドは `map[string]int64`（キー: 組織名、値: 成功セッション数合計）とする。`OrganizationCount` は `len(OrganizationStats)` で導出するため削除する
- メッセージフォーマット時、`OrganizationStats` は組織名のアルファベット昇順でソートして出力する（マップのイテレーション順は非決定的なため）
- テストには スパイハンドラ（`internal/notify` のテスト用ハンドラ）と `FakeStore` を使用する

---

## 6. テスト方針

### 単体テスト

- 集計ロジックの単体テスト（複数レポート、組織別）
- メッセージフォーマットの単体テスト
- レポートなし時の動作テスト

### 統合テスト

- スパイハンドラを使った定期サマリ送信フローのエンドツーエンドテスト（複数の集計期間値で動作確認）
