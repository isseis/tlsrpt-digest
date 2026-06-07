# メモ：Slack アラート通知フォーマット改善

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-08 |
| 作成者 | isseis |

---

## 背景

task 0100（Slack 通知インテグレーションテスト）の実行確認として、実 Slack Webhook にアラートを送信した際に、現在のフォーマットに改善余地があることが判明した。

### 実際の送信結果（スクリーンショット）

`docs/tasks/0101_slack_alert_format/screenshots/slack_alert_sample.png` を参照。

確認した通知内容：

```
⚠️ TLS Failures – 1 organizations affected

  [Organization / Policy / Failures / Period]
  Google Inc. | sts | 2 | 2026-02-08 – 2026-02-09

  [Run ID]
  01KTJ1R3E8GVTDJDFJKFPQBHNP
```

---

## 現状フォーマットの実装

`internal/notify/format.go` の `formatAlerts` 関数が Slack の旧 Attachment API（`fields`）を使用。
1 ポリシー = 1 `slackField`（`Short: false`）で、タイトルと値を title/value ペアとして表示。

```go
slackField{
    Title: "Organization / Policy / Failures / Period",
    Value: fmt.Sprintf("%s | %s | %d | %s – %s",
        a.OrganizationName,
        policyTypeStr(a.PolicyType),
        a.FailureCount,
        ...
    ),
    Short: false,
}
```

---

## 問題点

### 問題 1：見出しと値の列位置が合致しない

- `Title` は太字で 1 行表示、`Value` はその下に通常テキストで表示される。
- `/` 区切りのヘッダと `|` 区切りのデータは等幅フォントでないため視覚的に列が揃わない。
- 複数ポリシーが存在する場合、同じ `Title` が繰り返されることになる。

### 問題 2：失敗数の詳細理由が記載されていない

- 現在は `total-failure-session-count` のみ表示。
- RFC 8460 の `failure-details`（`result-type`、`sending-mta-ip`、`receiving-ip`、`failure-reason-code` 等）は通知に含まれていない。
- どのような TLS エラーで失敗しているかが Slack 上では判断できない。

### 問題 3：元データへのリンク・特定情報がない

- `.eml` ファイルや `tlsrpt.json` の対応エントリを特定するための情報（`report-id`、メール受信日時、ポリシードメイン等）が含まれていない。
- アラートを受け取った開発者が元データを探す手がかりがない。

---

## 改善の方向性（未確定、要設計）

以下はメモレベルの案であり、要件定義・アーキテクチャ設計で精査が必要。

- **フォーマット構造の見直し**
  - Slack Block Kit（`section` + `fields`）への移行を検討。
  - ポリシーごとにセクションを分けて視認性を向上。
  - 各ポリシーの `report-id`、送信ドメイン、ポリシードメインを含める。

- **失敗詳細の追加**
  - `failure-details` の主要フィールド（`result-type`、`failure-reason-code`）を折り畳み表示または要約形式で追加。
  - 件数が多い場合は上位 N 件に絞るか、"詳細は N 件" と要約する。

- **元データ特定情報の追加**
  - `report-id` を表示（`tlsrpt.json` の検索キーになる）。
  - メール受信日時またはレポート生成日時を追加。
  - 将来的にはストア上の JSON ファイルへの直接リンク（ファイルパスまたは管理 UI URL）を検討。

---

## スコープ外（このタスクでは扱わない予定）

- 週次サマリーのフォーマット変更。
- システムエラー通知のフォーマット変更。
- Slack Block Kit 移行に伴う Webhook ペイロードサイズ制限への対応（将来の懸念として記録）。
