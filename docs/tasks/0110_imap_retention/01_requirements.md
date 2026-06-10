# 要件定義書：IMAP メールボックスの古いメール自動削除

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-10 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

現状、`fetch` サブコマンドは IMAP メールボックスから TLSRPT レポートメールを取得してローカルストア（`./store`）に `.eml` として保存し、IMAP 側は `\Seen` を付与するのみである。そのため **IMAP サーバー上に元メールが無制限に蓄積し続ける**。

ストレージは 2 層に分かれている。

- **IMAP メールボックス**（リモート、サーバー上の元メール）: 削除手段は**現状なし**（本タスクの対象）。
- **ローカルストア**（`./store` のレポート JSON と `.eml`）: 既存の `gc` サブコマンドが `store.retention_days` / `store.max_email_age_days` で削除済み。

なお `retention_days` という設定名は既に `[store]` セクションで「ローカルのレポート JSON 保持期間」として使用されているため、IMAP 側の保持期間は別スコープ `[imap]` に `retention_days` として新設し、名前の衝突を避ける。

### 1.2 目的

1. **主目的**: IMAP メールボックス上の保持期間を超えた古いメールを `gc` サブコマンドで自動削除し、サーバー上のメール蓄積を抑止する。
2. **副次的目的**: リモート削除という不可逆操作を、設定上の不変条件と dry-run により安全に運用できるようにする。

---

## 2. スコープ

### 対象範囲（In Scope）

- `[imap] retention_days` 設定の新設（デフォルト・バリデーション・不変条件）。
- `gc` サブコマンドへの IMAP 古いメール削除ステップの追加。
- `gc` サブコマンドへの `--dry-run` 対応の追加（ローカル削除・IMAP 削除の双方を no-op 化）。
- `MailFetcher` インターフェースへの削除メソッド追加と、対象 UID のみを削除する IMAP 実装（UID EXPUNGE）。
- テスト用モック（`FakeMailFetcher` 等）の更新。
- 設定例（`config.toml`）・README・関連ドキュメントの更新。

### 対象外（Out of Scope）

- `fetch` サブコマンドの挙動変更（`\Seen` 付与・dry-run 等は現状維持。本タスクでは一切変更しない）。
- 削除対象を `\Seen` 済みやローカル保存済みに限定する絞り込み（本タスクは期間のみ＝age-based で判定する）。
- IMAP 保持期間を上書きする CLI フラグ（例 `--imap-retention`）。
- IMAP サーバーのゴミ箱（Trash）挙動の制御（削除後の復旧可否はサーバー実装に依存する）。
- 既存メールに対するマイグレーション処理。

### 影響を受けるコンポーネント

- **直接変更**: `internal/config/`、`internal/imap/`（`imap.go` / `client.go`）、`internal/imap/testutil/mocks.go`、`cmd/tlsrpt-digest/gc.go`、`cmd/tlsrpt-digest/main.go`。
- **間接的影響**: `config.toml`、README・設定ドキュメント。

---

## 3. 機能要件

### F-001: IMAP 保持期間設定 `imap.retention_days`

IMAP メールボックス上の保持期間を制御する設定を `[imap]` セクションに新設する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-01`: `IMAPConfig` に `RetentionDays int` を、`rawIMAPConfig` に `RetentionDays *int`（TOML キー `retention_days`）を追加する。
- `AC-02`: `retention_days` 未指定時のデフォルトは `30` とする。
- `AC-03`: `retention_days = 0` のとき IMAP 削除機能を無効化する（不変条件チェックの対象外とし、削除を一切行わない）。
- `AC-04`: `retention_days < 0` は設定エラーとし、config ロード時に起動を拒否する。
- `AC-05`: `retention_days > 0` のとき、`retention_days >= max(imap.fetch_days, summary.window_days)` を満たさなければ設定エラーとし、config ロード時に起動を拒否する（古すぎるカットオフによる、まだ取得・集計され得るメールの誤削除を防止する）。
- `AC-06`: バリデーションエラーは `errors.Is` で判別可能な専用エラー型として定義する。

### F-002: `gc` サブコマンドによる IMAP 古いメールの削除

`gc` 実行時に、`INTERNALDATE` が保持期間を超えた IMAP メールを削除する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-07`: `imap.retention_days > 0` かつ非 dry-run のとき、`gc` は `INTERNALDATE`（日付截断）が `now - retention_days` より古い IMAP メールを削除する。
- `AC-08`: 削除は検索でヒットした対象 UID のみを対象とし（`\Deleted` 付与 + UID EXPUNGE）、他クライアントが `\Deleted` を付与したメールを巻き込んで削除しない。
- `AC-09`: `imap.retention_days = 0`（無効）のとき、IMAP へ接続せずローカル GC のみを実行する（IMAP 認証情報が無くても `gc` は成功する）。
- `AC-10`: dry-run のとき、IMAP・ローカルとも削除を行わず、削除予定の件数・対象をログ出力する。
- `AC-11`: IMAP 削除が有効（`retention_days > 0`・非 dry-run）であるのに IMAP 認証情報が欠落している場合、`SystemErrorKindIMAPCredentialsMissing` を通知してエラー終了する（`fetch` と同一方針）。
- `AC-12`: 接続先サーバーが UIDPLUS（UID EXPUNGE）に非対応の場合、`\Deleted` を付与せず警告ログのみを出力し、削除件数 0 として継続する（無差別 EXPUNGE は行わない）。
- `AC-13`: IMAP 操作で発生したエラーは既存の `gc` 通知パターン（`notifyGCSystemError` / `gcNotifyKind`）で通知する。
- `AC-14`: IMAP からの削除件数をローカル削除件数とあわせてログ出力する。

### F-003: `MailFetcher` インターフェースの削除メソッド

期間指定で古いメールを削除する操作を `MailFetcher` に追加する。

**受け入れ条件（Acceptance Criteria）**:

- `AC-15`: `MailFetcher` に `DeleteOlderThan(ctx context.Context, cutoff time.Time) (deleted int, err error)` を追加する。実装は read-write SELECT → `UID SEARCH BEFORE cutoff` → `\Deleted` 付与 → 対象 UID のみ UID EXPUNGE を行う。
- `AC-16`: `cutoff` がゼロ値の場合、削除を行わず `deleted = 0`、`err = nil` を返す。
- `AC-17`: `FakeMailFetcher`（`internal/imap/testutil/mocks.go`）に `DeleteOlderThan` を追加し、呼び出し（`cutoff` 値）を記録できるようにする。

---

## 4. 非機能要件

### セキュリティ / 安全性

- リモート削除は不可逆（復旧可否はサーバーのゴミ箱挙動に依存）であるため、無差別 EXPUNGE は禁止し、対象 UID のみの UID EXPUNGE を用いる（AC-08・AC-12）。
- デフォルト有効（30 日）であることに伴う以下の挙動変更を README に明記する。
  - アップグレード後、既存環境は IMAP 上のメールを 30 日で自動削除し始める。
  - `gc` がデフォルトで IMAP 認証情報を必要とするようになる（無効化は `imap.retention_days = 0`）。

### 保守性

- `MailFetcher` の拡張に伴い、テスト用モックも同様に更新する（AC-17）。

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）。
- 既存の `fetch` の挙動・既存設定（`store.retention_days` 等）の意味は変更しない。
- 既存の `gc` の通知・fail-closed（recovery-required 時の停止）パターンを踏襲する。

---

## 6. テスト方針

### 単体テスト

- config: デフォルト 30（AC-02）、`0` で無効（AC-03）、負値でエラー（AC-04）、不変条件違反でエラー（AC-05）、`fetch_days` / `window_days` 境界値、エラー型を `errors.Is` で検証（AC-06）。
- imap: `DeleteOlderThan` が正しい `BEFORE` 条件で検索し対象 UID のみ EXPUNGE すること（AC-15）、`cutoff` ゼロ値で 0 件返却（AC-16）、UIDPLUS 非対応時に削除せず警告のみ（AC-12）。`fakeSession` を使用。
- testutil: `FakeMailFetcher.DeleteOlderThan` の呼び出し記録（AC-17）。

### 統合 / サブコマンドテスト

- gc: 有効時に正しいカットオフで削除を呼ぶ（AC-07）、`retention_days = 0` で IMAP 未接続・ローカルのみ実行（AC-09）、dry-run で削除しない（AC-10）、認証情報欠落でエラー（AC-11）、IMAP エラー通知（AC-13）、削除件数ログ（AC-14）。`SpyNotifier` 等の既存パターンを流用。
- main: `gc` + `--dry-run` が許可されること（AC-10 の前提）。
