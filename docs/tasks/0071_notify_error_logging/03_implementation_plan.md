# 実装計画書：通知失敗時の警告ログ出力

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-30 |
| レビュー日 | 2026-05-30 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装の全体像

### 1.1 目的

`cmd/tlsrpt-digest` 層の通知エラー処理を統一する。通知メソッド（`LogAlert`・`LogWarning`・`LogSystemError`・`LogSummary`・`Flush`）およびシステムエラー通知ヘルパーの戻り値を `slog.Warn` でログ出力し、通知失敗を理由とした終了コードへの影響を排除する。設計の詳細は [02_architecture.md](02_architecture.md) を参照。

### 1.2 実装原則

- [02_architecture.md](02_architecture.md) の設計に従う。
- `internal/notify` パッケージは変更しない（アーキテクチャ §1.1「最小変更」）。
- 通知に無関係な `slog.Error`（主処理エラー）は変更しない（後述 1.3）。
- 保守性 NFR（`01_requirements.md` §4「共通のヘルパーまたはラッパーで一元化することが望ましい」）について: ログヘルパー（`logAlerts`・`logWarn`）は `notify_helpers.go` に集約済みでこの NFR を満たす。一方、システムエラー通知ヘルパーの戻り値に対する `slog.Warn` は各呼び出し元に直接書く方針を採る。これはヘルパーがコンポーネント名など呼び出し元固有の文脈を持たず、ログ出力の判断をアプリケーション層に委ねるアーキテクチャ §3.3 の責務分離に従うためである。`望ましい` 要件であり AC ではないため、この範囲で達成とみなす。

### 1.3 既存コード調査結果

#### 変更対象の通知エラー処理箇所

調査により、変更すべき箇所と変更してはならない箇所を以下のとおり確定した。

| ファイル | 変更対象（通知エラー） | 変更してはならない（主処理エラー） |
|---|---|---|
| `notify_helpers.go` | `logAlerts` の `slog.Error`（27行）、`logWarn` の `slog.Error`（44行） | なし |
| `boot.go` | `_ = notifySystemError(...)`（217, 230行） | なし |
| `fetch.go` | `notifyFetchSystemError` の戻り値破棄 11 箇所（82, 87, 94, 101, 113, 178, 184, 192, 196, 233, 243行）、`Flush` エラーの `slog.Error`（152行） | 81, 86, 106, 165, 177, 183, 191, 195, 274行の `slog.Error`（recovery-required・IMAP・ストアなどの主処理エラー） |
| `summary.go` | `LogSummary` エラー（71行）、`Flush` エラーの `slog.Error`（74行）、`logSummarySystemError` の戻り値（46行） | 42行の `slog.Error`（ビルドエラーは通知前段の失敗、対象外） |
| `reprocess.go` | `notifyReprocessSystemError` の戻り値破棄（31, 45行）、`Flush` エラーの `slog.Error`（76行） | 30, 35, 44行の `slog.Error`（主処理エラー） |
| `gc.go` | `notifyGCSystemError` の戻り値破棄（33, 45, 53行） | 32, 37行の `slog.Error`（主処理エラー） |

#### 終了コード挙動の扱い

- **summary.go のみ**終了コード挙動を変更する（アーキテクチャ §4.2）。通常パス（`LogSummary`/`Flush` 失敗）は `exitOK` を返すよう変更し、recovery-required 分岐は `exitError`（recovery-required 由来）を維持する。
- **fetch.go・reprocess.go の `Flush` 失敗は `exitError` を維持する**。これらの `Flush` は at-least-once 配信保証の一部であり（フェッチでは MarkSeen より前に Flush し、失敗時はメッセージを未読のまま次回再試行する）、配信失敗は主処理の失敗に相当する。本タスクではログレベルのみ `slog.Warn` に変更し、戻り値・終了コードは変えない（アーキテクチャ §3.2 はこれらをログレベル変更のみと規定）。

#### 再利用できる既存資産

- **`SpyNotificationSink`**（[cmd/tlsrpt-digest/test_helpers.go](../../../cmd/tlsrpt-digest/test_helpers.go)）: `LogError`・`FlushError` フィールドで各メソッド／`Flush` のエラーを注入できる。新規モックは不要。
- **slog 出力キャプチャの既存 idiom**（[cmd/tlsrpt-digest/gc_test.go:180-182](../../../cmd/tlsrpt-digest/gc_test.go)）: `bytes.Buffer` + `slog.NewTextHandler` + `slog.SetDefault` + `t.Cleanup` で復元するパターンが既にある。これを再利用する。アーキテクチャ §7.1 が言及する「カスタム `slog.Handler`」は新規実装せず、この標準ライブラリベースの既存パターンを共通ヘルパー関数に切り出して使う。
- **`summaryTestBed`**（[cmd/tlsrpt-digest/summary_test.go:22-](../../../cmd/tlsrpt-digest/summary_test.go)）: `guard.RecoveryRequiredFound`・`notif.FlushError` で recovery-required／Flush 失敗シナリオを構築済み。

#### 挙動変更により更新が必要な既存テスト

summary.go の終了コード変更に伴い、以下の既存テストは期待値の更新が必要。

| テスト | 現状の期待 | 変更後の期待 |
|---|---|---|
| `TestSummary_FlushFailureExits1`（summary_test.go:280） | `exitError` | `exitOK` + `slog.Warn` 出力 |
| `TestSummary_RecoveryRequiredFirstCheckFlushFailure`（同:183） | `err` に "notify recovery required" を含み返る | `err == nil`／`exitError` 維持 + `slog.Warn` 出力 |
| `TestSummary_ExitCodes` の "flush failure exits 1" ケース（同:317-323） | `exitError` | `exitOK` |

fetch・reprocess・gc の既存テスト（`TestReprocess_FlushFailure_ExitError` 等）は終了コード挙動が変わらないため、原則として既存のまま通る。

---

## 2. 実装フェーズ

### Phase 1: テストヘルパーの整備

- [x] **1.1** slog 出力キャプチャヘルパーの追加
  - ファイル: `cmd/tlsrpt-digest/slog_capture_test.go`（新規、先頭に `//go:build test`）
  - 作業内容: gc_test.go:180-182 の idiom を共通化する関数を追加する。`*testing.T` を受け取り `*bytes.Buffer` を返し、`slog.SetDefault` で `slog.NewTextHandler` を設定し、`t.Cleanup` で元のロガーを復元する。テストはキャプチャ結果を文字列として検査し、`level=WARN`／`level=ERROR` と `error=` フィールドの有無を確認できるようにする。
  - 配置根拠: このヘルパーは `*testing.T`／`t.Cleanup` を使うため `testing` を import する。リポジトリの既存慣習では `*testing.T` を使うヘルパー（`newSummaryTestBed` 等）は `_test.go` に置かれており（`test_helpers.go` は `testing` を import しない `SpyNotificationSink` のみ）、この慣習に合わせて `_test.go` に配置する。同一 `package main` 内のため他テストファイルから利用できる。[test_organization.md](../../dev/developer_guide/test_organization.md) の Classification B（パッケージ内テストヘルパー）に該当し、`//go:build test` タグを付す。
  - 完了基準: ヘルパーが `make test` でコンパイルされ、Phase 3–5 のテストから利用できる。

### Phase 2: ログヘルパーの修正（notify_helpers.go）

- [x] **2.1** `logAlerts`・`logWarn` のログレベル変更
  - ファイル: `cmd/tlsrpt-digest/notify_helpers.go`
  - 作業内容: 27行・44行の `slog.Error` を `slog.Warn` に変更する。メッセージ文字列・`"error"` フィールドは変更しない。
  - 完了基準: `make lint`／`make test` が通る。

- [x] **2.2** ログヘルパーのテスト追加（AC-01）
  - ファイル: `cmd/tlsrpt-digest/notify_helpers_test.go`（既存があれば追記、なければ新規 `//go:build test`）
  - 作業内容: `SpyNotificationSink{LogError: ...}` で `logAlerts`・`logWarn` を呼び、Phase 1 のキャプチャヘルパーで `slog.Warn`（`level=WARN`）かつ `error` フィールドを含むログが出力されることを検証する。

### Phase 3: Bootstrap の修正（boot.go、AC-03）

- [ ] **3.1** `notifySystemError` 戻り値のログ出力
  - ファイル: `cmd/tlsrpt-digest/boot.go`
  - 作業内容: 217行・230行の `_ = notifySystemError(...)` を、戻り値を受け取り非 nil の場合に `slog.Warn`（`"error"` フィールド付き）で出力する形に変更する。後続の `return nil, fmt.Errorf(...)`（主処理エラーによる中断）は維持する。
  - 完了基準: Bootstrap の戻り値・終了挙動が変わらないこと。

- [ ] **3.2** Bootstrap 通知失敗のテスト追加（AC-03）
  - ファイル: `cmd/tlsrpt-digest/boot_test.go`
  - 作業内容: ロック取得失敗またはストアオープン失敗を誘発し、かつ `BuildNotifier` が `SpyNotificationSink{FlushError: ...}` を返すケースを追加する。Phase 1 のキャプチャヘルパーで `slog.Warn` + `error` フィールドが出力されること、Bootstrap が元の主処理エラーで非 nil を返すことを検証する（アーキテクチャ §6.1）。

### Phase 4: summary.go の修正（AC-01・AC-02）

- [ ] **4.1** 通常サマリ送信パスの挙動変更
  - ファイル: `cmd/tlsrpt-digest/summary.go`
  - 作業内容: 71行 `LogSummary` エラーを `return exitError` する代わりに `slog.Warn` で出力して継続する。74-75行 `Flush` エラーの `slog.Error` + `return exitError` を `slog.Warn` + 継続（`exitOK`）に変更する（アーキテクチャ §4.2(a)・§6.3.1）。

- [ ] **4.2** recovery-required 分岐の挙動変更
  - ファイル: `cmd/tlsrpt-digest/summary.go`
  - 作業内容: 45-46行の `logSummarySystemError` の戻り値を `return exitError, fmt.Errorf(...)` する代わりに、非 nil 時は `slog.Warn`（`"error"` フィールド付き）で出力し、`return exitError, nil`（recovery-required 由来）に変更する（アーキテクチャ §4.2(b)・§6.3.2）。

- [ ] **4.3** summary.go の既存テスト更新と追加（AC-01・AC-02）
  - ファイル: `cmd/tlsrpt-digest/summary_test.go`
  - 作業内容:
    - `TestSummary_FlushFailureExits1` を `exitOK` 期待に更新し、テスト名を挙動に合わせて改名する（例: `TestSummary_FlushFailureLogsWarnAndExitsOK`）。キャプチャヘルパーで `slog.Warn` 出力を検証する。
    - `TestSummary_RecoveryRequiredFirstCheckFlushFailure` を更新し、`err == nil`・`exitError` 維持・`slog.Warn` 出力を検証する。
    - `TestSummary_ExitCodes` の "flush failure exits 1" ケースを `exitOK` に修正する。
    - `LogSummary` 失敗時に `exitOK` + `slog.Warn` となるテストを追加する（`SpyNotificationSink{LogError: ...}`）。
    - ハッピーパス検証として、エラーを注入しない（`LogError`／`FlushError` ともに nil）ケースでキャプチャバッファに `level=WARN` 行が現れないことを確認するアサーションを追加する（アーキテクチャ §2.2 の「エラーなし → 処理継続」分岐。`slog.Warn` が無条件発火する回帰を検出する）。

### Phase 5: fetch.go・reprocess.go・gc.go の修正（AC-01）

- [ ] **5.1** fetch.go のログレベル変更
  - ファイル: `cmd/tlsrpt-digest/fetch.go`
  - 作業内容: (a) 11 箇所の `_ = notifyFetchSystemError(...)`（1.3 の該当行）を、戻り値を受け取り非 nil 時に `slog.Warn` で出力する形に変更する。(b) 152行 `Flush` エラーの `slog.Error` を `slog.Warn` に変更する。**153行の `return exitError, fmt.Errorf(...)` は維持する**（at-least-once 保証、1.3 参照）。1.3 に挙げた主処理エラーの `slog.Error` は変更しない。

- [ ] **5.2** reprocess.go のログレベル変更
  - ファイル: `cmd/tlsrpt-digest/reprocess.go`
  - 作業内容: 31, 45行の `_ = notifyReprocessSystemError(...)` を戻り値受け取り + `slog.Warn` に変更する。76行 `Flush` エラーの `slog.Error` を `slog.Warn` に変更し、**77行の `return exitError` は維持する**。30, 35, 44行の主処理エラーは変更しない。

- [ ] **5.3** gc.go のログレベル変更
  - ファイル: `cmd/tlsrpt-digest/gc.go`
  - 作業内容: 33, 45, 53行の `_ = notifyGCSystemError(...)` を戻り値受け取り + `slog.Warn` に変更する。32, 37行の主処理エラーの `slog.Error` は変更しない。

- [ ] **5.4** fetch・reprocess・gc の通知失敗テスト追加（AC-01）
  - ファイル: `cmd/tlsrpt-digest/fetch_test.go`、`reprocess_test.go`、`gc_test.go`
  - 作業内容: 各サブコマンドでシステムエラー通知ヘルパーまたは `Flush` が失敗するケースを `SpyNotificationSink{LogError/FlushError: ...}` で構築し、キャプチャヘルパーで `slog.Warn` + `error` フィールドが出力されることを検証する。fetch・reprocess の `Flush` 失敗ケースでは終了コードが従来どおり `exitError` のままであることも確認する。

### Phase 6: セキュリティテスト（AC-01 補強）

- [ ] **6.1** センシティブ情報非漏洩のテスト
  - ファイル: `cmd/tlsrpt-digest/summary_test.go`（または通知エラー処理を集約的に検証できる箇所）
  - 作業内容: `SpyNotificationSink.FlushError` に Slack webhook URL を含む文字列、および IMAP パスワード相当の文字列を設定し、キャプチャした `slog.Warn` 出力にそれらが現れないことを確認する（アーキテクチャ §5・§7.1）。なお実体の URL リダクションは `internal/notify` 側で担保されており、本テストは `cmd` 層での防御的多層化の確認である。

---

## 3. 受け入れ条件トレーサビリティ

`AC-01`: 各通知メソッド／ヘルパーの非 nil エラーを `slog.Warn`（`"error"` フィールド付き）で出力する
- Test: `notify_helpers_test.go`（Phase 2.2）、`boot_test.go`（Phase 3.2）、`summary_test.go`（Phase 4.3）、`fetch_test.go`／`reprocess_test.go`／`gc_test.go`（Phase 5.4）
- 実装: `notify_helpers.go`、`boot.go`、`summary.go`、`fetch.go`、`reprocess.go`、`gc.go`
- 検証方法: `SpyNotificationSink{LogError/FlushError}` でエラーを注入し、Phase 1 のキャプチャヘルパーで出力文字列に `level=WARN` と `error=` フィールドが含まれることを確認する。

`AC-02`: 通知失敗はプロセスの終了コードに影響しない（summary.go の通常パスを `exitOK` に変更）
- Test: `summary_test.go`（Phase 4.3。`TestSummary_FlushFailureExits1` の改名版、`LogSummary` 失敗の新規テスト、`TestSummary_ExitCodes`、`TestSummary_RecoveryRequiredFirstCheckFlushFailure`）
- 実装: `summary.go`（Phase 4.1・4.2）
- 検証方法: `Flush`／`LogSummary` 失敗時に戻り値の終了コードが `exitOK` であること、recovery-required 分岐では `exitError` が維持され `err == nil` となることを `assert.Equal` で確認する。

`AC-03`: `boot.go` の `notifySystemError` の戻り値を `slog.Warn` で出力する
- Test: `boot_test.go`（Phase 3.2）
- 実装: `boot.go`（Phase 3.1）
- 検証方法: ロック取得失敗等を誘発し `Flush` を失敗させたうえで、キャプチャヘルパーで `slog.Warn` + `error=` フィールドが出力されること、Bootstrap が主処理エラーで非 nil を返すことを確認する。

---

## 4. 実装順序とマイルストーン

| マイルストーン | 内容 | 含むフェーズ |
|---|---|---|
| M1 | テスト基盤整備 | Phase 1 |
| M2 | 影響の小さい箇所の統一 | Phase 2, 3 |
| M3 | 終了コード挙動変更（最も影響大） | Phase 4 |
| M4 | 残りサブコマンドの統一 | Phase 5 |
| M5 | セキュリティ検証 | Phase 6 |

実装はアーキテクチャ §8 の優先順位に従い、summary.go（挙動変更を含む）を単独で検証してから fetch/reprocess/gc のパターン変更へ進む。

---

## 5. テスト戦略

- **単体テスト**: 各 AC につき最低 1 つの具体的テストを Phase 2–6 で追加・更新する。slog 出力は Phase 1 のキャプチャヘルパーで検証する（詳細はアーキテクチャ §7.1）。
- **既存テストの回帰**: summary.go の挙動変更で 3 件の既存テストを更新する（1.3 参照）。fetch・reprocess・gc は終了コード挙動が不変のため既存テストはそのまま通ることを `make test` で確認する。
- **セキュリティテスト**: Phase 6 で webhook URL・IMAP パスワードの非漏洩を確認する。

---

## 6. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| 主処理エラーの `slog.Error` を誤って変更する | 主処理失敗が警告に格下げされ検知漏れ | 1.3 の変更対象表で対象行を明示。Phase 5 で対象行を限定 |
| fetch・reprocess の `Flush` 失敗の終了コードを誤って変更する | at-least-once 配信保証の破壊（メッセージ重複既読化） | Phase 5.1・5.2 で `return exitError` 維持を明記。5.4 で終了コードを検証 |
| summary.go の既存テスト更新漏れ | `make test` 失敗 | 1.3 の更新対象テスト表で対象を網羅 |

---

## 7. 実装チェックリスト

- [x] Phase 1: slog キャプチャヘルパー追加
- [x] Phase 2: notify_helpers.go 修正 + テスト
- [ ] Phase 3: boot.go 修正 + テスト
- [ ] Phase 4: summary.go 修正 + テスト更新・追加
- [ ] Phase 5: fetch.go・reprocess.go・gc.go 修正 + テスト
- [ ] Phase 6: セキュリティテスト

---

## 8. 完了基準

- [ ] `make fmt` 実行済み
- [ ] `make lint` がエラーなく完了する
- [ ] `make test` が全テストで通過する
- [ ] `01_requirements.md` の全 AC（AC-01・AC-02・AC-03）にテストが存在する
- [ ] `make deadcode` が未使用コードを報告しない
- [ ] 通知に無関係な主処理エラーの `slog.Error` が変更されていないことを差分レビューで確認する

---

## 9. 次のステップ

- 実装完了後、`02_architecture.md` および本計画のステータスを踏まえて PR を作成する。
- 通知エラーの統一パターンは将来のメール通知などの追加時にも適用する（アーキテクチャ §9）。
