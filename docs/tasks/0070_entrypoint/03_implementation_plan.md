# 実装計画書：エントリポイントとサブコマンド

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-23 |
| レビュー日 | 2026-05-24 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

`cmd/tlsrpt-digest/main.go` を 5 サブコマンド（`fetch` / `summary` / `reprocess` / `gc` / `recover`）による one-shot 実行アーキテクチャへ移行する。
詳細設計は [`02_architecture.md`](02_architecture.md) を参照。

### 1.2 実装原則

- `cmd` レイヤーはオーケストレーションに限定し、IMAP、TLSRPT パース、Slack 送信、ストア永続化の既存実装を再実装しない。
- 書き込み系サブコマンドは `02_architecture.md` §3.3 のプロセス排他ロックを使う。store open mode は `02_architecture.md` §6.5 に従い、通常の書き込み系は `OpenReadWrite`、pending reset を扱う `recover --mode discard-old --yes` と `recover --abort-reset --yes` は `OpenRecoverReset`、`summary` は `OpenReadOnly` を使う。
- 通知は `NotificationSink` facade に閉じ込め、Go ソース内のコメント、識別子、固定メッセージは英語で記述する。日本語は本計画書とユーザー向け文書に限定する。
- 各ステップ完了後に `make test && make lint` が通ることを確認してから次ステップへ進む。
- 新規テストヘルパーは必要なものだけ追加する。`cmd/tlsrpt-digest` 内で未公開型を扱うものは `test_helpers.go` または `test_helpers_<category>.go` に `//go:build test` を付ける。これらに依存する `_test.go` も同じ `//go:build test` を付ける。plain `go test ./...` では production build と tag なしテストのコンパイルを確認し、`go test -tags test ./...` で acceptance test を実行する。既存の cross-package fake は `internal/imap/testutil/mocks.go`、`internal/store/testutil/mocks.go` を拡張して再利用する。

### 1.3 既存コード調査結果

- `cmd/tlsrpt-digest/main.go` には Phase 1 logging、既存 Slack handler 構築、`loadConfig`、`buildIMAPConfig`、`storeOpenMode` の足場がある。これらは `boot.go` へ移管またはサブコマンド dispatch に接続する。
- `cmd/tlsrpt-digest/main_test.go` には Slack handler 非接続、設定読込、store open mode の既存テストがある。重複を避け、サブコマンド parse と boot のテストへ再配置する。
- `internal/imap/testutil.FakeMailFetcher` は `FetchMeta` / `Download` / `MarkSeen` / `Close` の呼び出し記録を持つため、`fetch` テストで再利用する。
- `internal/store/testutil.FakeStore` は `SaveReports`、`SaveEmailMetas`、`SaveEmail`、UIDVALIDITY、recovery-required、GC API を持つため、追加 store API と summary guard のみ拡張する。
- `internal/notify.GenerateSummary` は現状 `(start, end]` 判定なので、`02_architecture.md` §3.1 に従い `[start, end)` へ変更し、既存 `aggregate_test.go` を更新する。
- `internal/notify` には `LogAlert` / `LogSystemError` / `LogSummary` があるが、`LogWarning`、`Warning`、安全化された `SystemErrorKind` は未実装のため追加する。
- `internal/store` には `ApplyRecovery` まで実装済みだが、`ResetForRecovery`、`AbortReset`、`OpenRecoverReset`、summary consistency guard、pending reset の状態管理は未実装である。

---

## 2. 実装ステップ

### フェーズ 1: 共通基盤

#### ステップ 1-1: `internal/notify` 型拡張

**変更ファイル**: `internal/notify/types.go`, `internal/notify/helpers.go`, `internal/notify/format.go`, `internal/notify/message.go`

**見積工数**: 0.5 日
**実績工数**: -

- [x] `internal/notify/types.go` に `WarningKind` 型（許容値 `size_mismatch` / `parse_failure`）と `Warning` 構造体（`Kind WarningKind`, `UID uint32`, `UIDValidity uint32`, `MessageID string`）を追加する
- [x] `internal/notify/types.go` の `SystemError` 構造体を更新する: フィールドを `Kind SystemErrorKind`, `Component string`, `Mailbox string` に変更し、既存の `ErrorType string` と `Message string` を削除する
- [x] `internal/notify/types.go` に `SystemErrorKind` 型と許容値（`lock_held`, `store_identity_mismatch`, `store_permission`, `store_corruption`, `imap_credentials_missing`, `imap_connect_failed`, `imap_auth_failed`, `imap_operation_failed`, `uidvalidity_changed`, `recovery_required`, `reset_incomplete`, `notification_flush_failed`）を定義する。`notification_flush_failed` は `Flush()` 自体が失敗した場合に使用する種別であり、Slack 配送はすでに失敗しているため `slog.Error` による stderr 出力のみで報告する（`Flush()` による Slack 送信は行わない）
- [x] `internal/notify/helpers.go` の `LogSystemError` を新 `SystemError` 構造（`Kind` フィールド）に対応させる
- [x] `internal/notify/helpers.go` に `LogWarning(ctx context.Context, h slog.Handler, warning Warning) error` を追加する（WARN レベルで error webhook バッファへ積む）
- [x] `internal/notify/format.go` に `fetch_warning` レコードの整形処理を追加する（`tls_failure_alert` 集約に混入させず専用レコードとして扱う）
- [x] `internal/notify/message.go` に `fetch_warning` の Slack 表示文（`kind`, `uid`, `uidvalidity`, `message_id`, `run_id` のみ表示）を追加する

**テスト変更**:

- [x] `internal/notify/helpers_test.go`: `LogWarning` が WARN レベルで typed fields のみを出力することを確認するテストを追加する
- [x] `internal/notify/format_test.go`: `fetch_warning` が TLS failure alert に集約されないことを確認するテストを追加する
- [x] `internal/notify/security_test.go`: `LogWarning` / `LogSystemError` が raw error・secret を payload に含めないことを確認するテストを追加する
- [x] `cmd/tlsrpt-digest/main.go` / `main_test.go` の `primeNotifyHandlers` 経由の `LogSystemError` 呼び出しを新 `SystemError{Kind: ...}` 形式へ更新する。ステップ 1-1 完了時点で `make test && make lint` がコンパイルエラーなく通る状態にする

**完了確認**: `make test && make lint` がパスする

---

#### ステップ 1-2: `internal/store` の拡張

**変更ファイル**: `internal/store/types.go`, `internal/store/store.go`, `internal/store/recovery.go`, `internal/store/errors.go`, `internal/store/store_test.go`, `internal/store/recovery_test.go`, `internal/store/testutil/mocks.go`

**見積工数**: 2.0 日
**実績工数**: -

破壊的復旧の不変条件・エラー境界・更新範囲の詳細は `02_architecture.md` §6.4 を参照。

- [x] `internal/store/types.go` に `OpenRecoverReset` モードを追加する
- [x] `internal/store/types.go` に `SummaryConsistencyGuard` インターフェース（`CheckRecoveryRequired(ctx context.Context) (found bool, err error)` / `Close() error`）を定義する
- [x] `internal/store/store.go` の `Store` インターフェースに以下のメソッドを追加する:
  - `ResetForRecovery(currUIDValidity uint32) error`
  - `AbortReset() error`
  - `AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error)`
- [x] `internal/store/store.go` の `Open` に `OpenRecoverReset` 分岐を追加する: pending reset が存在する場合に通常 `OpenReadWrite` を fail closed（エラー返却）させ、`OpenRecoverReset` だけを通過させる
- [x] `internal/store/recovery.go` に `ResetForRecovery(currUIDValidity uint32) error` を実装する:
  - recovery-required 不在、または引数 `currUIDValidity` が sentinel の `curr_uid_validity` と異なる場合はエラーを返す
  - pending reset manifest を作成し旧データを staging へ移動する（commit 前）
  - commit を実行する（sentinel の `uid_validity` を current へ更新・`recovery_required` フィールドを除去・アトミック書き込み）
  - commit 後に staging ディレクトリを削除する（cleanup）
  - 中間クラッシュ後の再実行で「空ストア + current UIDVALIDITY + recovery-required 解消」へ収束することを保証する（AC-41）
- [x] `internal/store/recovery.go` に `AbortReset() error` を実装する:
  - pending reset がない場合、または commit 後の状態では変更せずエラーを返す（AC-43）
  - pending reset（manifest あり・sentinel 未更新）の場合: staging から旧データを元の位置へ戻し manifest を削除する。完了後も recovery-required は残す
  - 再実行で「旧データ保持 + recovery-required 残存」へ収束すること（AC-44）
- [x] `internal/store/recovery.go` に `AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error)` を実装する: guard はプロセス間の同期境界として専用ガードファイル（例: `{root_dir}/.tlsrpt-digest-summary.lock`）に対して `unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB)` で共有ロックを取得する。`CheckRecoveryRequired` は呼び出しごとにセンチネルファイルを再読み込みして recovery-required の有無を確認する（取得時点の状態をキャッシュしない）。writer 側（`SaveRecoveryRequired` / `ApplyRecovery` / `ResetForRecovery` 内の `commitReset`）は同じガードファイルに対して `unix.Flock(fd, unix.LOCK_EX)` で排他ロックを取得してから recovery-required を作成または解除することで、第 2 回 `CheckRecoveryRequired(found=false)` から `LogSummary` / `Flush()` 開始までの間に recovery-required が作成される false negative を防ぐ。`ResetForRecovery` の初期 manifest/staging 作成と `AbortReset` の restore は recovery-required を変更しないため summary guard では囲まず、writer 同士の直列化は cmd 層の store-wide process lock が担当する。`summary` がプロセス排他ロック（`lock.go` の `AcquireExclusive`）を取得しない設計でも fail closed 境界を持つ（`02_architecture.md` §3.3 / §6.7、`docs/dev/developer_guide/process_locking.ja.md` 参照）
- [x] `internal/store/errors.go` に `ErrPendingReset`, `ErrRecoveryRequiredMissing`, `ErrRecoveryUIDValidityMismatch`, `ErrResetNotPending` を追加し、pending reset の fail closed・recovery-required 不在・curr UIDVALIDITY 不一致・abort 不可状態を分類できるようにする
- [x] `internal/store/store_test.go` に以下のテストを追加する:
  - [x] pending reset がある状態で `OpenReadWrite` がエラーを返すこと
  - [x] `OpenRecoverReset` が pending reset を扱える store を返すこと
- [x] `internal/store/recovery_test.go` に以下のテストを追加する:
  - [x] `ResetForRecovery` が recovery-required 不在・curr 不一致時にエラーを返すこと
  - [x] `ResetForRecovery` の中間クラッシュ後の再実行で最終状態へ収束すること（AC-41）
  - [x] `AbortReset` が commit 前の pending reset を取り消し旧データを保持すること（AC-43）
  - [x] `AbortReset` が commit 後・pending reset なしでエラーを返すこと（AC-43）
  - [x] `AbortReset` の再実行が同じ最終状態へ収束すること（AC-44）
  - [x] commit 後の cleanup 未完了が通常データパス（空ストア一貫性）に影響しないこと
  - [x] `OpenRecoverReset` が通常 `OpenReadWrite` で fail closed する pending reset を再開できること
  - [x] summary guard の第 2 回 `CheckRecoveryRequired(found=false)` 直後に writer が recovery-required を作ろうとする並行テストで、summary の `LogSummary` / `Flush()` と recovery-required 書き込みが同時に通過せず、summary が送信しないか writer が送信開始後まで待つこと
- [x] `internal/store/testutil/mocks.go` の `FakeStore` に `ResetForRecovery`・`AbortReset`・`AcquireSummaryConsistencyGuard` を実装する
- [x] `internal/store/testutil/mocks.go` に `FakeSummaryConsistencyGuard` 構造体（`CheckRecoveryRequired` の戻り値を外部から注入可能）を追加する

**完了確認**: `make test && make lint` がパスする。`var _ store.Store = (*storetestutil.FakeStore)(nil)` コンパイルチェックが通ること

---

#### ステップ 1-3: `GenerateSummary` 集計区間修正

**変更ファイル**: `internal/notify/aggregate.go`, `internal/notify/aggregate_test.go`

**見積工数**: 0.25 日
**実績工数**: -

変更理由は `02_architecture.md` §3.1 の Duration 型設計判断「半開区間 `[start, end)`」を参照。

- [x] `internal/notify/aggregate.go` の `inSummaryPeriod` を `(start, end]`（start 除外・end 含む）から `[start, end)`（start 含む・end 除外）へ変更する: `reportEnd.After(start) && (reportEnd.Equal(end) || reportEnd.Before(end))` を `!reportEnd.Before(start) && reportEnd.Before(end)` に変更する
- [x] `internal/notify/aggregate_test.go` の境界値テストを `[start, end)` セマンティクスに合わせて更新する（start と等しい `reportEnd` が含まれ、`end` と等しい `reportEnd` が除外されることを確認する）

**完了確認**: `make test && make lint` がパスする

---

### PR-1 作成ポイント: `internal` API 拡張

**対象ステップ**: 1-1 / 1-2 / 1-3

**推奨タイトル**: `feat(task 0070): extend internal notify/store APIs for entrypoint`

**レビュー観点**: `internal/store` recovery API の不変条件 / `internal/notify` payload 安全性 / `[start, end)` 区間変更

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（https://github.com/isseis/tlsrpt-digest/pull/85）
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（ステップ 1-4 以降は新しいブランチで作業する）

---

#### ステップ 1-4: `duration.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/duration.go`, `cmd/tlsrpt-digest/duration_test.go`

**見積工数**: 0.5 日
**実績工数**: -

- [x] `duration.go` に `Duration` 型（`Days int`）を定義する（`02_architecture.md` §3.1 参照）
- [x] `ParseDuration(s string) (Duration, error)` を実装する: `d` / `w` 単位のみ受け付け、週は日数（`×7`）に正規化する。パース後の値が 0 以下の場合はエラーを返す（AC-07b）
- [x] `(d Duration) Cutoff(now time.Time) time.Time` を実装する: `now` を UTC 日付の開始時刻（`00:00:00 UTC`）に切り捨ててから `d.Days` 日遡る（AC-07c）
- [x] `UTCDayStart(now time.Time) time.Time` を実装する: 「今日の `00:00:00 UTC`」を返す（AC-07d）
- [x] `duration_test.go` に以下のテストを追加する:
  - [x] 正常パース: `1d`（Days=1）・`7d`（Days=7）・`1w`（Days=7）・`4w`（Days=28）・`30d`（Days=30）（AC-07 / AC-07b）
  - [x] エラー: `0d`・`-1d`・`-2w`・`30h`・`abc`・空文字（AC-07b）
  - [x] `Cutoff(now)` の UTC 切り捨て: UTC 02:01:00 に `Days=7` のカットオフが「7 日前の 00:00:00 UTC」になること（「7 日前の 02:01:00」ではないこと）（AC-07c）
  - [x] 週指定（`1w`）でも UTC 日付単位の切り捨てが行われること（AC-07c）
  - [x] `UTCDayStart(now)` が任意の時刻に対して「今日の 00:00:00 UTC」を返すこと（AC-07d）
  - [x] `--window 1w` を 2000-12-10 10:00 UTC に実行したとき `start=2000-12-03 00:00 UTC`・`end=2000-12-10 00:00 UTC` となり重複・欠落がないこと（AC-07d 統合確認）

**完了確認**: `make test && make lint` がパスする

---

#### ステップ 1-5: `lock.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/lock.go`, `cmd/tlsrpt-digest/lock_test.go`

**見積工数**: 0.5 日
**実績工数**: -

OS API 選定の詳細は `02_architecture.md` §3.3 を参照。

- [x] `lock.go` に `LockHandle` インターフェース（`Close() error`）を定義する
- [x] `AcquireExclusive(lockPath string) (LockHandle, error)` を実装する: `golang.org/x/sys/unix` の `unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)` を使い non-blocking 排他ロックを取得する。取得失敗時は即時エラーを返す（待機しない）
- [x] サブコマンド化前の暫定 production path では `main.go` から `acquireStoreWriterLock` を呼び、`cfg.Store.RootDir` を作成してから store-wide process lock を取得・保持する。サブコマンド化後はこの処理を `Bootstrap` の W-2 / W-5 へ移管する
- [x] `lock_test.go` に以下のテストを追加する:
  - [x] 同一パスに対して 2 回目の `AcquireExclusive` が即時失敗すること（AC-10a）
  - [x] `Close()` 後に同パスへのロック再取得が成功すること（解放確認）
  - [x] 存在しない親ディレクトリでは lock file を作成せずエラーを返すこと（W-2 で親ディレクトリを保証する前提の明確化）
- [x] `main_test.go` に `acquireStoreWriterLock` が root dir を作成して lock を保持し、`Close()` 後に再取得できることを検証するテストを追加する

このステップでは lock primitive と現行 `main.go` の暫定 production path への接続だけを実装する。サブコマンド別の writer/read-only 分岐と runner 完了までの lock 保持は、次の `boot.go` / `main.go` 再構成ステップで `Bootstrap` に移す。未使用の store open wrapper には接続しない。

**完了確認**: `make test && make lint` がパスする

---

#### ステップ 1-6: `boot.go` と `main.go` の再構成

**変更ファイル**: `cmd/tlsrpt-digest/main.go`, `cmd/tlsrpt-digest/main_test.go`
**新規ファイル**: `cmd/tlsrpt-digest/boot.go`, `cmd/tlsrpt-digest/boot_test.go`, `cmd/tlsrpt-digest/test_helpers.go`

**見積工数**: 1.5 日
**実績工数**: -

初期化順序（W-1〜W-6）と `summary` 専用フローの詳細は `02_architecture.md` §2.2・§3.4 を参照。

- [x] `boot.go` に以下の型を定義する: `SubcommandName`・`BootContext`・`IMAPCredentials`・`SubcommandRunner`・`NotificationSink`・`BootstrapOptions`（`02_architecture.md` §3.1 参照）。`BootContext` は `LockHandle` と `SummaryConsistencyGuard` を保持し、`Close() error` で runner 完了後のリソース解放を一元化する
- [x] `boot.go` に `notificationSinkImpl` 構造体を実装する: `NotificationSink` インターフェースの具体実装として、内部に `[]*notify.SlackHandler` を保持し、`LogAlert` / `LogWarning` / `LogSystemError` / `LogSummary` / `Flush` / `IsDryRun` を型付きヘルパー経由で委譲する。`SlackHandler` を `cmd` 外部へ直接公開しない
- [x] `boot.go` に `Bootstrap(subcmd SubcommandName, configPath string, runID string, opts BootstrapOptions) (*BootContext, error)` を実装する: 書き込み系サブコマンドは W-1（設定読込）→ W-2（`{root_dir}` 確保・検証）→ W-3（Slack URL 取得・即時 Secret 化）→ W-4（`BuildHandlers` all-or-nothing）→ W-5（プロセスロック取得）→ W-6（`store.Open(mode)`）の順で実行する。`fetch` / `gc` / `reprocess` / `recover` の通常表示・`keep-old`・`discard-old` dry-run は `OpenReadWrite`、`recover --mode discard-old --yes` と `recover --abort-reset --yes` は `OpenRecoverReset` を使う
- [x] W-6 の store open 失敗を分類する: `store.ErrPendingReset` は `LogSystemError(reset_incomplete)` + `Flush()` + 英語の `recover --mode discard-old --yes` 継続または `recover --abort-reset --yes` ロールバック案内 + exit 1、identity mismatch は `store_identity_mismatch`、permission は `store_permission`、corruption は `store_corruption` とする
- [x] `summary` 向け `Bootstrap` は設定読込と `store.Open(OpenReadOnly)`、`AcquireSummaryConsistencyGuard` までに限定し、それらを `BootContext.Store` / `BootContext.SummaryGuard` として渡す。`GenerateSummary`、第 2 回 `CheckRecoveryRequired`、notifier 遅延構築、`LogSummary` は `summary.go` で実行する
- [x] `main.go` を再構成する:
  - [x] `os.Args` からサブコマンド名を確定し、各サブコマンド専用の `flag.FlagSet` で残り引数を解釈する（グローバル `flag.Parse()` を廃止する）
  - [x] サブコマンド未指定・未知サブコマンド・`FlagSet` 解析エラー時は usage を stderr へ出力し exit 2 とする（AC-02）
  - [x] `flag.FlagSet` 解析成功直後に `ulid.Make().String()` で RunID を採番する（`02_architecture.md` §3.6 参照）
  - [x] 既存の `loadConfig`・`setupNotifyHandlers`・`buildIMAPConfig`・`storeOpenMode` を `boot.go` へ移管し、store open は `Bootstrap` 内で lock 取得後に直接行う（`primeNotifyHandlers` はステップ 1-1 で新 `SystemError` 形式へ更新済みのため、この段階で削除する）
  - [x] 各サブコマンド用の `SubcommandRunner` スタブを追加する（`Run` は後続フェーズで実装する）
  - [x] `main` は `Bootstrap` 成功後に `defer boot.Close()` を設定し、`SubcommandRunner.Run` 完了までロックを保持する。`Bootstrap` は初期化途中のエラー時だけ取得済みリソースを閉じ、成功パスでは `LockHandle` を閉じない
- [x] `cmd/tlsrpt-digest/test_helpers.go` を新規作成する（`//go:build test` タグ）: `SpyNotificationSink` 構造体（`NotificationSink` を実装し呼び出し記録・エラー注入を提供する）を定義する。`package main` 内部型（`NotificationSink`）を使用するため `testutil/` サブディレクトリではなく同パッケージのこのファイルに配置する（`test_organization.md` Classification B）
- [x] `boot_test.go` を新規作成する（`package main`、`//go:build test` タグ必須）: `test_helpers.go` の `SpyNotificationSink`（`//go:build test`）を参照するため、このファイルも同じビルドタグを付ける。タグなしの plain `go test ./...` でコンパイル対象外となり、`go test -tags test ./...` でのみ実行される。以下のテストを追加する:
  - [x] 設定読込失敗 → stderr 出力のみで exit 1 となること（AC-08）
  - [x] Slack URL が取得直後に `config.Secret` でラップされること（生文字列がログに出ないこと）
  - [x] `gc` / `recover` / `reprocess` サブコマンドで IMAP 認証情報を要求しないこと
  - [x] `BuildHandlers` の all-or-nothing 動作: 一方の URL が不正な場合に全体がエラーになること
  - [x] `summary` が空ストア時に `BuildHandlers` を呼ばず Slack URL 未設定でも exit 0 になること（AC-10c）
  - [x] ロック取得失敗時に `LogSystemError(lock_held)` + `Flush()` が呼ばれ exit 1 となること（AC-10a）
  - [x] fake runner の `Run` 中に同じ lock path へ 2 回目の `AcquireExclusive` を試みると失敗し、`Run` から戻って `BootContext.Close()` が完了した後は再取得できること（AC-10a）
  - [x] ストアオープン失敗（identity mismatch / permission / corruption）が分類別の `SystemErrorKind` でレポートされること（AC-09）
  - [x] pending reset による store open 失敗時に `fetch` / `gc` / `reprocess` が `LogSystemError(reset_incomplete)` + `Flush()` + 英語の継続/ロールバック案内 + exit 1 となること
  - [x] W-2 境界: `{root_dir}` が symlink の場合に exit 1 となること（symlink 境界チェック。`02_architecture.md` §3.4 W-2 参照）
  - [x] W-2 境界: `{root_dir}` のパーミッションが不足している場合に exit 1 となること
- [x] `main_test.go` を更新する:
  - [x] 移管した `buildIMAPConfig`・`loadConfig`・`setupNotifyHandlers` のテストを `boot_test.go` へ移動する
  - [x] `fetch` / `summary` / `reprocess` / `gc` / `recover` の 5 サブコマンドそれぞれが対応する `SubcommandRunner` へ振り分けられることを確認するテストを追加する（AC-01）
  - [x] サブコマンド未指定・未知サブコマンド・不正フラグで usage が stderr に出力され exit 2 となることを確認するテストを追加する（AC-02）
  - [x] `-config` フラグが全サブコマンド（`fetch` / `summary` / `reprocess` / `gc` / `recover`）で受け付けられることを確認するテストを追加する（AC-03）
  - [x] `-config` フラグのデフォルトパスが `./config.toml` であることを確認するテストを追加する（AC-04）

**完了確認**: `make test && make lint` がパスする

---

### PR-2 作成ポイント: CLI 共通基盤

**対象ステップ**: 1-4 / 1-5 / 1-6

**推奨タイトル**: `feat(task 0070): add CLI infrastructure (duration, lock, boot, main)`

**レビュー観点**: 初期化順序 W-1〜W-6 / ロック解放タイミング / `NotificationSink` facade の境界

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（https://github.com/isseis/tlsrpt-digest/pull/86）
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（フェーズ 2 は新しいブランチで作業する）

---

### フェーズ 2: 主機能サブコマンド

#### ステップ 2-1: `fetch.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/fetch.go`, `cmd/tlsrpt-digest/fetch_test.go`

**見積工数**: 2.0 日
**実績工数**: -

at-least-once 保証・ダウンロード対象選定の詳細は `02_architecture.md` §6.1・§6.2 を参照。処理フロー全体は `02_architecture.md` §2.3 を参照。センチネルエラー変数（`var ErrFoo = errors.New("...")`）のメッセージ文字列にはパッケージプレフィックス（例: `"fetch: "`, `"store: "`）を含めない。呼び出し元で `fmt.Errorf("fetch: %w", err)` のようにプレフィックスを付与するため、二重プレフィックスを避けるためである。

- [x] `fetch.go` に `fetchRunner` 構造体と `Run(ctx context.Context, boot *BootContext) (int, error)` を実装する
- [x] `main.go` のスタブを `fetchRunner` で置き換える
- [x] 処理フローを以下の順で実装する:
  1. `LoadRecoveryRequired` で recovery-required 確認。`found=true` → stderr に英語の `recover` 実行案内を出力し、`LogSystemError(recovery_required)` + `Flush()` + exit 1。`LoadRecoveryRequired` 自体が失敗した場合も fail closed とし、IMAP 接続へ進まず `LogSystemError(store_corruption)` + `Flush()` + exit 1（AC-10d / AC-10e）
  2. `TLSRPT_IMAP_USERNAME`（string）・`TLSRPT_IMAP_PASSWORD`（即時 `config.Secret` 化）を取得する（W-6f）。欠落時は `LogSystemError(imap_credentials_missing)` + `Flush()` + exit 1
  3. IMAP client を作成する（`imap.NewClient`）。接続失敗は `LogSystemError(imap_connect_failed)`、認証失敗は `LogSystemError(imap_auth_failed)` に分類し、いずれも `Flush()` + exit 1 とする（AC-10）。成功・失敗いずれのパスでも最終的に `Close()` を呼ぶ
  4. `FetchMeta(since=Duration.Cutoff(now))` でメタ情報を取得する。失敗時は `LogSystemError(imap_operation_failed)` + `Flush()` + exit 1（AC-11）
  5. `LoadUIDValidity` で前回値を取得し現在の UIDVALIDITY と比較する（AC-11a）:
     - `found=false`: 現在の UIDVALIDITY を `SaveUIDValidity` で即時保存してフェッチ継続（AC-11b）
     - `found=true` かつ一致: フェッチ継続（AC-11b-cont）
     - `found=true` かつ不一致: `SaveRecoveryRequired` + `LogSystemError(uidvalidity_changed)` + `Flush()` + exit 1（AC-11c）。`LoadUIDValidity` または `SaveRecoveryRequired` が失敗した場合はメール処理へ進まず stderr 診断 + `LogSystemError(store_corruption)` + `Flush()` + exit 1
  6. ダウンロード対象を選定する（SEEN × ローカル `.eml` の 4 通り。`02_architecture.md` §6.2 テーブル参照）（AC-12）。RFC822.SIZE 不一致はダウンロード判定に影響しない（Exchange 等のサイズ不正確実装への耐性）。不一致を検出した場合は `LogWarning(size_mismatch)` をバッファへ積むのみとし、SEEN × `.eml` の選定結果はそのまま適用する。たとえば SEEN + `.eml` あり（通常のスキップ対象）でも、スキップ自体は SIZE 不一致ではなく選定テーブルによるものである（AC-13 / AC-14）
  7. ダウンロード対象メールを `imap.Download` で取得し `store.SaveEmail` で保存する（アトミック・冪等）（AC-15）
  8. ローカルに `.eml` が存在する全 UID（今回ダウンロード分＋既存分）を `SaveEmailMetas` で一括登録する（AC-15 / AC-15a）
  9. 各メールの添付 `.json.gz` をパースする（AC-16）。パース失敗時は `LogWarning(parse_failure)` をバッファへ積み、当該メールのレポート保存をスキップする（AC-16a）
  10. UNSEEN だったメールで `failure_session_count > 0` の場合は `LogAlert` をバッファへ積む（AC-17）
  11. パース成功レポートを `SaveReports` で一括 UPSERT する（AC-18）
  12. `Flush()` を呼ぶ。失敗時は exit 1（SEEN 付与しない）（AC-18a）
  13. `Flush()` 成功後に `MarkSeen` で UNSEEN だった全メールに SEEN を付与する（AC-19）
  14. `SaveUIDValidity(currentUIDVALIDITY)` を冪等保存する（AC-20a）
- [x] `fetch_test.go` に以下のテストを追加する（既存 `imaptestutil.FakeMailFetcher`・`storetestutil.FakeStore`・`SpyNotificationSink` を使用）:
  - [x] `FetchMeta` が UIDVALIDITY・UID・RFC822.SIZE・SEEN・Message-ID・INTERNALDATE を取得し、since 引数として `Duration.Cutoff(now)` が渡されること（AC-11）
  - [x] `LoadRecoveryRequired` 失敗 → IMAP 接続を行わず `LogSystemError(store_corruption)` + `Flush()` + exit 1 となること
  - [x] `FetchMeta` 失敗 → `LogSystemError(imap_operation_failed)` + `Flush()` + SEEN 未付与 + exit 1 となること
  - [x] `--since` フラグが `FlagSet` に登録されており、`Duration.Cutoff(now)` として `FetchMeta` へ渡されること（AC-05）
  - [x] `--since` 指定時に `imap.fetch_days` 設定値が無視されること（AC-06）
  - [x] SEEN + `.eml` あり → スキップされること（AC-12）
  - [x] UNSEEN + `.eml` なし → ダウンロード・処理・SEEN 付与が行われること
  - [x] `imap.Download` 失敗 → 当該メールを保存・通知・SEEN 付与せず exit 1 となること
  - [x] `store.SaveEmail` 失敗 → `SaveEmailMetas` / `SaveReports` / `MarkSeen` を呼ばず exit 1 となること
  - [x] UNSEEN + `.eml` あり → ダウンロードせず既存ファイルを処理・SEEN 付与が行われること
  - [x] SEEN + `.eml` なし → ダウンロードされること（SEEN 変更なし）
  - [x] SEEN + `.eml` なし + `failure_session_count > 0` → `LogAlert` が呼ばれないこと（SEEN 済みメールへの再アラート防止）（AC-17）
  - [x] RFC822.SIZE 不一致 + UNSEEN + `.eml` なし → `LogWarning(size_mismatch)` が積まれること（AC-13）
  - [x] RFC822.SIZE 不一致 + SEEN + `.eml` あり → `LogWarning(size_mismatch)` が積まれた上でスキップされること（AC-14）
  - [x] `failure_session_count > 0` + UNSEEN → `LogAlert` が積まれること（AC-17）
  - [x] `failure_session_count == 0` → `LogAlert` が積まれないこと
  - [x] UIDVALIDITY 初回（`found=false`）→ 即時 `SaveUIDValidity` 後にフェッチ継続（AC-11b）
  - [x] `LoadUIDValidity` 失敗 → ダウンロードへ進まず `LogSystemError(store_corruption)` + `Flush()` + exit 1 となること
  - [x] UIDVALIDITY 初回保存（AC-11b の `SaveUIDValidity`）失敗 → メール取得・処理へ進まず exit 1 となること
  - [x] UIDVALIDITY 不一致 → `SaveRecoveryRequired` + `LogSystemError(uidvalidity_changed)` + `Flush()` + exit 1（AC-11c）
  - [x] UIDVALIDITY 不一致時の `SaveRecoveryRequired` 失敗 → ダウンロードへ進まず stderr 診断 + `LogSystemError(store_corruption)` + `Flush()` + exit 1 となること
  - [x] recovery-required 残存 → `fetch` が即座に停止すること（AC-10d / AC-10e / AC-11d）
  - [x] パース失敗 → `LogWarning(parse_failure)` + レポート保存スキップ + SEEN 付与は継続（AC-16a / AC-20）
  - [x] `Flush()` 失敗 → SEEN が付与されないこと（AC-18a）
  - [x] `SaveEmailMetas` と `SaveReports` が全メール処理後にそれぞれ 1 回ずつ呼ばれること（AC-15 / AC-18）
  - [x] `SaveEmailMetas` 失敗 → `SaveReports` / `MarkSeen` を呼ばず exit 1 となること
  - [x] `SaveReports` 失敗 → `Flush()` / `MarkSeen` を呼ばず exit 1 となること
  - [x] 正常完了 → exit 0、UIDVALIDITY 不一致 / `Flush()` 失敗 / recovery-required 残存 → exit 1 となること（AC-21）
  - [x] IMAP 接続失敗 → `LogSystemError(imap_connect_failed)` + `Flush()` + exit 1（AC-10）
  - [x] IMAP 認証失敗 → `LogSystemError(imap_auth_failed)` + `Flush()` + exit 1（AC-10）
  - [x] IMAP client が成功・失敗のどちらのパスでも `Close()` されること（AC-10）
  - [x] 1 件のメール処理失敗（パース失敗）が他のメールの処理・SEEN 付与に影響しないこと（AC-20）
  - [x] 全メール処理完了後に `SaveUIDValidity(currentUIDVALIDITY)` が 1 回呼ばれること（AC-20a）
  - [x] `MarkSeen` 失敗 → exit 1 となり、`SaveUIDValidity(currentUIDVALIDITY)` を呼ばないこと
  - [x] 最終 `SaveUIDValidity(currentUIDVALIDITY)` 失敗 → exit 1 となること
  - [-] ロック取得失敗 → `LogSystemError(lock_held)` + `Flush()` + exit 1（AC-10a）: lock acquisition happens in Bootstrap (boot.go), not in fetchRunner.Run; covered by bootstrap-level tests

**完了確認**: `make test && make lint` がパスする

---

### PR-3 作成ポイント: `fetch` サブコマンド

**対象ステップ**: 2-1

**推奨タイトル**: `feat(task 0070): implement fetch subcommand with at-least-once guarantee`

**レビュー観点**: `Flush()` → `MarkSeen` 順序 / UIDVALIDITY 比較と fail-closed / SEEN × `.eml` 4 象限

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（ステップ 2-2 は新しいブランチで作業する）

---

#### ステップ 2-2: `summary.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/summary.go`, `cmd/tlsrpt-digest/summary_test.go`

**見積工数**: 1.0 日
**実績工数**: -

空ストア時の詳細シーケンスは `02_architecture.md` §6.7 を参照。

- [x] `summary.go` に `summaryRunner` 構造体と `Run(ctx context.Context, boot *BootContext) (int, error)` を実装する
- [x] `main.go` のスタブを `summaryRunner` で置き換える
- [x] 処理フローを以下の順で実装する（`02_architecture.md` §6.7 参照）:
  1. `Bootstrap` が `OpenReadOnly` で開いた `boot.Store` と `boot.SummaryGuard` を使用する。`summary.go` では store を再オープンしない（AC-10c）
  2. `boot.SummaryGuard` の `CheckRecoveryRequired` を呼ぶ。`found=true` → W-3・W-4 で notifier を構築してから `LogSystemError(recovery_required)` + `Flush()` + exit 1（AC-27a）
  3. `notify.GenerateSummary(ctx, store, start=Duration.Cutoff(now), end=UTCDayStart(now), debugLogger)` を呼ぶ（AC-27）
  4a. Summary が空の場合: `slog.Info("no reports to summarize")` を出力し exit 0（AC-10c）
  4b. Summary が非空の場合: W-3・W-4 で notifier を構築する
  5. `LogSummary` + `Flush()` + exit 0（AC-28）
  > **設計注記**: 当初計画では `CheckRecoveryRequired` を集計前・送信直前の 2 回呼ぶ 2 フェーズ方式を予定していたが、ロック設計の分析（`docs/dev/developer_guide/process_locking.md` §4）により、shared lock 保持中は sentinel の書き込みが物理的にブロックされるため第 2 回チェックは不要と結論づけ、集計前の 1 回のみとした。
- [x] `summary_test.go` に以下のテストを追加する（`storetestutil.FakeStore`・`FakeSummaryConsistencyGuard`・`SpyNotificationSink` を使用）:
  - [x] `--window 7d` 指定時に `Duration.Cutoff(now)` が `start` として `GenerateSummary` へ渡されること（AC-07a / AC-07c）
  - [x] `--window` 未指定 + 設定値ありで設定値が `start` として使われること（AC-07a）
  - [x] `GenerateSummary` に渡される `end` が `UTCDayStart(now)` であること（AC-07d）
  - [x] `notify.GenerateSummary` 失敗 → notifier を構築せず exit 1 となること
  - [x] 集計対象期間（開始・終了日時）がメッセージ（`notify.Summary.Period`）に含まれること（AC-28）
  - [x] recovery-required 残存（第 1 回確認）→ 集計・送信せず exit 1（AC-27a）
  - [x] `CheckRecoveryRequired` がエラーを返した場合 → 集計・送信を行わず exit 1 となること
  - [x] 空集計 + recovery-required なし → `slog.Info` で `no reports to summarize` が出力され notifier 未構築・Slack URL 未設定でも exit 0（AC-10c）
  - [x] 非空集計 + Slack URL 未設定 → `BuildHandlers` 失敗で exit 1（空集計との対比）
  - [x] `notify.GenerateSummary` を呼び、集計ロジックを `summary.go` で再実装しないこと
  - [x] 空集計 / 正常送信 → exit 0、recovery-required 残存 / `Flush()` 失敗 → exit 1 となること（AC-29）

**完了確認**: `make test && make lint` がパスする

---

### PR-4 作成ポイント: `summary` サブコマンド

**対象ステップ**: 2-2

**推奨タイトル**: `feat(task 0070): implement summary subcommand with consistency guard`

**レビュー観点**: `SummaryConsistencyGuard` による集計前 recovery-required チェック / 空ストア時の notifier 遅延構築

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（https://github.com/isseis/tlsrpt-digest/pull/89）
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（フェーズ 3 は新しいブランチで作業する）

---

### フェーズ 3: 運用サブコマンド

#### ステップ 3-1: `gc.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/gc.go`, `cmd/tlsrpt-digest/gc_test.go`

**見積工数**: 0.75 日
**実績工数**: -

カットオフ計算と各 API の呼び出し方針は `02_architecture.md` §3.2 の `gc.go` 行を参照。

- [x] `gc.go` に `gcRunner` 構造体と `Run(ctx context.Context, boot *BootContext) (int, error)` を実装する
- [x] `main.go` のスタブを `gcRunner` で置き換える
- [x] 処理フローを以下の順で実装する:
  1. `LoadRecoveryRequired` で recovery-required 確認。`found=true` → stderr に英語の `recover` 実行案内を出力し、削除処理を行わず exit 1。`LoadRecoveryRequired` 自体が失敗した場合も fail closed とし、削除処理を行わず `LogSystemError(store_corruption)` + `Flush()` + exit 1（AC-29a）
  2. `--before` の `Duration.Cutoff(now)` を `DeleteReportsBefore(cutoff)` に渡す。失敗時は `LogSystemError(store_permission)` + `Flush()` + exit 1（AC-32 / AC-33）
  3. `--max-email-age` の `Duration.Cutoff(now)` を `DeleteEmailsBefore(cutoff)` に渡す。失敗時は `LogSystemError(store_permission)` + `Flush()` + exit 1（AC-32b / AC-33）。`DeleteEmailsBefore` の実装では、`.eml` ファイル削除後に親ディレクトリが空になった場合に限りディレクトリを削除する（事前に `os.ReadDir` で空確認。空でない場合は削除せず、エラーにもしない）
  4. JSON レコードと `.eml` それぞれの削除件数を `slog.Info` で出力する（AC-33）
- [x] `gc_test.go` に以下のテストを追加する（`storetestutil.FakeStore` を使用）:
  - [x] `--before` フラグが `FlagSet` に登録されており、`ParseDuration` でパースされること（AC-30）
  - [x] `--before` 指定時に `DeleteReportsBefore` が AC-07c に従った UTC 日付単位切り捨て済みカットオフで呼ばれること（AC-32）
  - [x] `--before` 未指定で設定値（`store.retention_days`）が使われること（AC-31）
  - [x] `--max-email-age` フラグが `FlagSet` に登録されており、`ParseDuration` でパースされること（AC-32a）
  - [x] `--max-email-age` 指定時に `DeleteEmailsBefore` が AC-07c に従ったカットオフで呼ばれること（AC-32b）
  - [x] `--max-email-age` 未指定で設定値（`store.max_email_age_days`）が使われること（AC-32a）
  - [x] JSON レコードと `.eml` それぞれの削除件数が INFO ログに出力されること（AC-33）
  - [x] recovery-required 残存 → stderr に英語の `recover` 実行案内を出力し、削除処理を行わず exit 1（AC-29a）
  - [x] `LoadRecoveryRequired` 失敗 → 削除処理を行わず `LogSystemError(store_corruption)` + `Flush()` + exit 1 となること
  - [x] `DeleteReportsBefore` 失敗 → `DeleteEmailsBefore` を呼ばず、成功 INFO 削除件数ログを出力せず、`LogSystemError(store_permission)` + `Flush()` + exit 1 となること（AC-33）
  - [x] `DeleteEmailsBefore` 失敗 → 成功 INFO 削除件数ログを出力せず、`LogSystemError(store_permission)` + `Flush()` + exit 1 となること（AC-33）
  - [x] `gc` を同じカットオフで 2 回連続実行しても 2 回目の削除件数が 0 件となること（冪等性確認）
  - [x] 正常完了 → exit 0、recovery-required 残存 / `DeleteReportsBefore` 失敗 → exit 1 となること（AC-34）

**完了確認**: `make test && make lint` がパスする

---

#### ステップ 3-2: `reprocess.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/reprocess.go`, `cmd/tlsrpt-digest/reprocess_test.go`

**見積工数**: 1.0 日
**実績工数**: -

`SaveEmailMetas` と `SaveReports` の呼び出し順序の根拠は `02_architecture.md` §6.6 を参照。

- [x] `reprocess.go` に `reprocessRunner` 構造体と `Run(ctx context.Context, boot *BootContext) (int, error)` を実装する
- [x] `main.go` のスタブを `reprocessRunner` で置き換える
- [x] 処理フローを以下の順で実装する:
  1. `LoadRecoveryRequired` で recovery-required 確認。`found=true` → stderr に英語の `recover` 実行案内を出力し、`.eml` 読み込みとストア書き込みを行わず exit 1。`LoadRecoveryRequired` 自体が失敗した場合も fail closed とし、`LoadEmails` へ進まず `LogSystemError(store_corruption)` + `Flush()` + exit 1（AC-21a）
  2. `LoadEmails` で `{root_dir}/emails/` 以下の `.eml` を再帰的に列挙する。列挙全体の失敗はコマンド全体を中断して exit 1、ファイル単位の読み込み失敗は記録して残りを継続する（AC-22 / AC-25）
  3. `SaveEmailMetas` でバッチ登録する（AC-23a）
  4. 各メールの添付 `.json.gz` をパースする。読み込み・パース失敗はスキップし記録して継続する（`--notify` 指定時のみ `LogWarning` 通知）（AC-25）
  5. パース成功レポートを `SaveReports` で一括 UPSERT する（AC-23）。失敗時はコマンド全体を中断して exit 1（AC-25）
  6. `--notify` 指定時のみ `Flush()` を呼ぶ。失敗時は exit 1（AC-24a）
- [x] `reprocess_test.go` に以下のテストを追加する（`testdata/` の実 `.eml` と `storetestutil.FakeStore` を使用）:
  - [x] recovery-required 残存 → stderr に英語の `recover` 実行案内を出力し、`LoadEmails` もストア書き込みも行わず exit 1（AC-21a）
  - [x] `LoadRecoveryRequired` 失敗 → `LoadEmails` もストア書き込みも行わず `LogSystemError(store_corruption)` + `Flush()` + exit 1（AC-21a）
  - [x] `--notify` なしで `LogAlert` が呼ばれないこと（AC-24）
  - [x] `--notify` あり + TLS failure → `LogAlert` が呼ばれること（AC-24）
  - [x] `--notify` あり + ファイル単位パース失敗 → `LogWarning` が呼ばれ残りのファイル処理が継続すること（AC-25）
  - [x] ファイル単位読み込み失敗 → 当該ファイルを記録してスキップし、残りのファイル処理が継続すること（AC-25）
  - [x] ストア書き込み失敗（`SaveReports`）→ コマンド全体中断・exit 1（AC-25）
  - [x] ストア書き込み失敗（`SaveEmailMetas`）→ パースと `SaveReports` を行わずコマンド全体中断・exit 1（AC-25）
  - [x] 正常完了 → exit 0、recovery-required 残存 / `SaveReports` 失敗 / `Flush()` 失敗 → exit 1 となること（AC-26）
  - [x] `SaveEmailMetas` が `SaveReports` より前に呼ばれること（AC-23a / AC-23）
  - [x] 重複実行しても結果が変わらないこと（冪等性）

**完了確認**: `make test && make lint` がパスする

---

### PR-5a 作成ポイント: `gc` + `reprocess` サブコマンド

**対象ステップ**: 3-1 / 3-2

**推奨タイトル**: `feat(task 0070): implement gc and reprocess subcommands`

**レビュー観点**: カットオフ計算 / recovery-required ガード / `SaveEmailMetas` → `SaveReports` 呼び出し順序

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（https://github.com/isseis/tlsrpt-digest/pull/91）
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（ステップ 3-3 は新しいブランチで作業する）

---

#### ステップ 3-3: `recover.go` の作成

**新規ファイル**: `cmd/tlsrpt-digest/recover.go`, `cmd/tlsrpt-digest/recover_test.go`

**見積工数**: 1.0 日
**実績工数**: -

オペレータ向け表示内容・モード別挙動・エラー境界は `02_architecture.md` §6.4 を参照。

- [ ] `recover.go` に `recoverRunner` 構造体と `Run(ctx context.Context, boot *BootContext) (int, error)` を実装する
- [ ] `main.go` のスタブを `recoverRunner` で置き換える
- [ ] `recover` の `FlagSet` 解析結果から `BootstrapOptions.StoreOpenMode` を決定する: 通常表示・`keep-old`・`discard-old` dry-run は `OpenReadWrite`、`discard-old --yes` と `--abort-reset --yes` は `OpenRecoverReset` を指定する
- [ ] 通常表示・`keep-old`・`discard-old` dry-run で `store.ErrPendingReset` を受け取った場合は、`ApplyRecovery` / `ResetForRecovery` を呼ばず、pending reset 状態と `discard-old --yes` 継続・`abort-reset --yes` ロールバックの選択肢を英語で表示して exit 1 とする（AC-45）
- [ ] 実行前に stdout へ英語のオペレータ向け情報を表示する: previous UIDVALIDITY・current UIDVALIDITY・mailbox 識別子・local data path・選択 mode・pending reset 状態（AC-36 / AC-45）
- [ ] `keep-old` モード: 英語の旧エポックデータ混入リスク警告を表示してから `ApplyRecovery(currUIDValidity)` を呼ぶ（AC-37）
- [ ] `discard-old --yes` モード: stdout の英語メッセージに、レポート store と `.eml` store を空状態へ置き換えること、sentinel の `uid_validity` を current へ更新すること、`initialized_at` と mailbox 識別子を保持することを含めてから `ResetForRecovery(currUIDValidity)` を呼ぶ（AC-38）
- [ ] `discard-old`（`--yes` なし）: 英語の実行予定内容を表示するのみで破壊的変更を行わず exit 1 とする（AC-39）
- [ ] `--abort-reset --yes` フラグの組み合わせ: `BootstrapOptions.StoreOpenMode=OpenRecoverReset` で開いた store に対して `AbortReset()` を呼ぶ（AC-42 / AC-43）
- [ ] `--abort-reset` 単独・`--yes` 単独は英語のエラーメッセージを出力して exit 1 とする（AC-42）
- [ ] recovery-required 不在 → 英語の説明付きで exit 1（変更しない）（AC-40）
- [ ] pending reset 検出時に利用可能な選択肢（`discard-old --yes` の継続・`abort-reset --yes` のロールバック）を stdout の英語メッセージへ追加表示する（AC-45）
- [ ] `recover_test.go` に以下のテストを追加する（`storetestutil.FakeStore` を使用）:
  - [ ] `--mode` フラグが `FlagSet` に登録されており、`keep-old` / `discard-old` のいずれかが受け付けられること（AC-35）
  - [ ] `keep-old` で `ApplyRecovery` が呼ばれること。stdout に previous/current UIDVALIDITY・mailbox・mode・旧エポックデータ混入警告の英語メッセージが表示されること（AC-36 / AC-37 / AC-45）
  - [ ] pending reset 中の `keep-old` は `ApplyRecovery` を呼ばず、継続/ロールバック選択肢を英語で表示して exit 1 となること（AC-45）
  - [ ] `ApplyRecovery` 失敗 → recovery-required 状態を残し exit 1 となること（AC-41）
  - [ ] `discard-old --yes` で `ResetForRecovery` が呼ばれること（AC-38）
  - [ ] `ResetForRecovery` 失敗 → pending reset / recovery-required を保持して exit 1 となること（AC-41）
  - [ ] `discard-old`（`--yes` なし）が破壊的変更を行わず exit 1 となること。stdout の英語メッセージに、レポート store と `.eml` store が空状態へ置き換わること・sentinel の `uid_validity` が current へ更新されることが含まれること（`02_architecture.md` §6.4 参照）（AC-39）
  - [ ] `--abort-reset --yes` で `AbortReset` が呼ばれること（AC-42 / AC-43）
  - [ ] `AbortReset` 失敗 → recovery-required を保持して exit 1 となること（AC-43 / AC-44）
  - [ ] `--abort-reset` 単独・`--yes` 単独 → exit 1（破壊的変更なし）（AC-42）
  - [ ] pending reset 検出時に、通常の `recover` 状態表示・`--mode keep-old`・`discard-old`（`--yes` なし）の各パスで破壊的変更を行わず、`discard-old --yes` と `abort-reset --yes` の選択肢が stdout に英語で表示されること（AC-45）
  - [ ] recovery-required 不在 → 説明付きで exit 1（AC-40）

**完了確認**: `make test && make lint` がパスする

---

### PR-5b 作成ポイント: `recover` サブコマンド

**対象ステップ**: 3-3

**推奨タイトル**: `feat(task 0070): implement recover subcommand`

**レビュー観点**: `--mode` / `--yes` / `--abort-reset` のフラグ組み合わせ / `OpenRecoverReset` 選択 / 破壊的操作の fail-safe

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（フェーズ 4 は新しいブランチで作業する）

---

### フェーズ 4: 仕上げ

#### ステップ 4-1: セキュリティテスト・統合テスト

**新規ファイル**: `cmd/tlsrpt-digest/security_test.go`
**変更ファイル**: `cmd/tlsrpt-digest/main_test.go`

**見積工数**: 0.75 日
**実績工数**: -

セキュリティテスト要件の詳細は `02_architecture.md` §7.3 および `docs/dev/developer_guide/notification_security.md` §5 を参照。

- [ ] `security_test.go` に以下のテストを追加する（`package main`、このファイルは `//go:build test` タグ付きのヘルパーに依存しないため `//go:build test` タグは不要）:
  - [ ] `NotificationSink` の公開メソッド集合を reflection で検査し、`*notify.SlackHandler` / `slog.Handler` / `*slog.Logger` を返すメソッドが存在しないことを確認する
  - [ ] `LogWarning` と `LogSystemError` の payload に raw error 文字列・ローカルファイルパス・IMAP パスワード・Slack Webhook URL が含まれないことを確認する
  - [ ] `BootContext` をログ出力した場合に `config.Secret` フィールドが `[REDACTED]` になることを確認する
  - [ ] IMAP デバッグ用 `io.Writer` が Slack ハンドラと型システムレベルで分離されていることを確認する（将来 IMAP デバッグを有効化する際の検証ポイント）
  - [ ] `slog.Default().Error(...)` を呼んでも Slack ハンドラのバッファへレコードが追加されないことを確認する
  - [ ] `go doc` または compile-time API チェックで、`internal/notify` が通知用 `*slog.Logger` を取得できる exported symbol を公開していないことを確認する
- [ ] `main_test.go` に以下の統合テストを追加する:
  - [ ] 有効な TOML 設定と環境変数を与えたとき、W-1〜W-6 の順序で `BootContext{Config, Store, Notifier, LockHandle}` が構築されることをスパイで確認する
  - [ ] `testdata/` の実 `.eml` を用いた `reprocess` のラウンドトリップ
  - [ ] `fetch` 実行中に `summary` が並走できること（書き込み系どうしはロックで直列化）

**完了確認**: `make test && make lint` がパスする

---

#### ステップ 4-2: ドキュメント整備

**変更ファイル**: `README.md`
**新規ファイル**: `docs/tasks/0070_entrypoint/notes/operational_examples.md`

**見積工数**: 0.5 日
**実績工数**: -

- [ ] `docs/tasks/0070_entrypoint/notes/operational_examples.md` を作成する: systemd timer 設定例・cron 例・`run_id` による重複通知判別手順・`discard-old --yes` 実行前の外部スケジューラ停止手順・クラッシュ時の復旧フローチャート（`recover` で状態確認 → `discard-old --yes` 再実行 または `abort-reset --yes` でロールバック）を記載する
- [ ] `README.md` を更新する: 各サブコマンドの使用例・重複通知への対処（`run_id` による判別方法）・外部スケジューラ設定例（systemd timer / cron）を追加する

**完了確認**: `README.md` と `notes/operational_examples.md` に上記項目が全て含まれることをセルフチェックし、Markdown lint がある場合はパスする

---

### PR-6 作成ポイント: セキュリティテスト・統合テスト・ドキュメント

**対象ステップ**: 4-1 / 4-2

**推奨タイトル**: `feat(task 0070): add security tests, integration tests, and documentation`

**レビュー観点**: `NotificationSink` API 境界の reflection チェック / `reprocess` ラウンドトリップ / README 運用例の網羅性

- [ ] `go test ./...` と `go test -tags test ./...` が両方グリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた

---

## 3. 実装順序とマイルストーン

### 3.1 マイルストーン

| マイルストーン | 内容 | 成果物 |
|---|---|---|
| M1 | フェーズ 1 完了 | `notify` 型拡張 / `store` 拡張 / `duration` / `lock` / `boot` / `main` 再構成が完成し `make test` がグリーン |
| M2 | フェーズ 2 完了 | `fetch` / `summary` が実際のワークフローを処理できる状態 |
| M3 | フェーズ 3 完了 | 全 5 サブコマンドが動作する状態 |
| M4 | フェーズ 4 完了 | セキュリティテスト・統合テスト・ドキュメントが揃い、リリース可能な状態 |

**総見積**: 約 13 日（レビュー対応・バッファ 1 日を含む）

### 3.2 PR 構成

`runplan` で実装する際は、以下の各 PR 作成ポイントでいったん作業を中断し、PR を作成してマージを待ってから次のブランチで続行する。

| PR | 対象ステップ | 内容 | リスク | レビュー観点 |
|---|---|---|---|---|
| PR-1 | 1-1 / 1-2 / 1-3 | `internal` API 拡張（notify・store・aggregate） | 高（store recovery 不変条件） | 不変条件・再実行性・payload 安全性 |
| PR-2 | 1-4 / 1-5 / 1-6 | CLI 共通基盤（duration・lock・boot・main） | 中 | 初期化順序・ロック解放 |
| PR-3 | 2-1 | `fetch` サブコマンド | 高（at-least-once） | Flush→SEEN 順序・fail-closed |
| PR-4 | 2-2 | `summary` サブコマンド | 中 | 2 回 guard・遅延構築 |
| PR-5a | 3-1 / 3-2 | `gc` + `reprocess` サブコマンド | 低 | カットオフ・呼び出し順序 |
| PR-5b | 3-3 | `recover` サブコマンド | 高（状態機械） | フラグ組み合わせ・破壊的操作 |
| PR-6 | 4-1 / 4-2 | セキュリティテスト・統合テスト・ドキュメント | 低 | 網羅性 |

---

## 4. テスト戦略

### 4.1 単体テスト目標

| ファイル | 主な検証対象 AC |
|---|---|
| `duration_test.go` | AC-07 / AC-07b / AC-07c / AC-07d |
| `lock_test.go` | AC-10a（排他・解放） |
| `main_test.go` | AC-01 / AC-02 / AC-03 / AC-04 |
| `boot_test.go` | AC-08 / AC-09 / AC-10a |
| `fetch_test.go` | AC-05 / AC-06 / AC-10 / AC-10d / AC-10e / AC-11〜AC-21（IMAP・store・通知境界の失敗パスを含む） |
| `summary_test.go` | AC-07a / AC-10c / AC-27〜AC-29（`GenerateSummary` と guard の失敗パスを含む） |
| `reprocess_test.go` | AC-21a〜AC-26 |
| `gc_test.go` | AC-29a〜AC-34 |
| `recover_test.go` | AC-35〜AC-45 |
| `internal/store/recovery_test.go` | AC-38 / AC-41 / AC-43 / AC-44 |

### 4.2 テストダブル利用方針

- **`FakeMailFetcher`**: 既存 `internal/imap/testutil/mocks.go` を再利用する
- **`FakeStore`**: 既存 `internal/store/testutil/mocks.go` を再利用する。ステップ 1-2 で `ResetForRecovery`・`AbortReset`・`AcquireSummaryConsistencyGuard` を追加する
- **`FakeSummaryConsistencyGuard`**: `internal/store/testutil/mocks.go` に新規追加する（`CheckRecoveryRequired` の戻り値を注入可能にする）。`SummaryConsistencyGuard` は `internal/store` の公開インターフェースであるため `testutil/mocks.go`（Classification A）へ配置する
- **`SpyHandler`**: 既存 `internal/notify/testutil/mocks.go` を再利用する
- **`SpyNotificationSink`**: `cmd/tlsrpt-digest/test_helpers.go`（`//go:build test`、`package main`）に定義する。`NotificationSink` は `package main` 内のインターフェースのため `testutil/` サブディレクトリではなく同パッケージに配置する

### 4.3 テストデータ

- `reprocess` ラウンドトリップテストには既存 `testdata/` の `.eml` ファイルを再利用する
- `fetch` メール処理テストには `FakeMailFetcher` で人工データを渡す（`testdata/` の追加は不要）

### 4.4 後方互換テスト

- フェーズ移行時に常に `make test` がグリーンであること
- `Store` インターフェース変更後、`var _ store.Store = (*storetestutil.FakeStore)(nil)` コンパイルチェックが継続して通ること

---

## 5. リスク管理

| リスク | 影響度 | 緩和策 |
|---|---|---|
| `Store` インターフェース拡張による既存テストへの影響 | 中 | ステップ 1-2 で `FakeStore` を同時更新しコンパイルエラーで即座に検出する |
| `notify.SystemError` 型変更（`ErrorType`→`Kind`）による既存コードへの影響 | 中 | ステップ 1-1 で `primeNotifyHandlers` 呼び出しも同時更新し、各ステップ完了時のコンパイル可能性を維持する |
| `GenerateSummary` の区間変更（`(start, end]` → `[start, end)`）による既存動作への影響 | 低 | ステップ 1-3 でテストを先に更新し、境界値テストで変更前後の動作差異を明確にする |
| `unix.Flock` の OS 互換性（Linux 固定） | 低 | 要件定義書の対象 OS は Linux のみ。必要に応じて `//go:build linux` タグを付与する |
| `recover.go` の複数フェーズ破壊的操作の複雑性 | 高 | `internal/store.ResetForRecovery`・`AbortReset` にロジックを閉じ込め、`recover.go` は API 呼び出しのみに留める |

---

## 6. 実装チェックリスト

### PR-1: `internal` API 拡張（ステップ 1-1 / 1-2 / 1-3）

- [ ] ステップ 1-1: `internal/notify` 型拡張完了
- [ ] ステップ 1-2: `internal/store` 拡張と単体テスト完了
- [ ] ステップ 1-3: `GenerateSummary` 区間修正と既存テスト更新完了
- [ ] PR-1 作成・マージ完了

### PR-2: CLI 共通基盤（ステップ 1-4 / 1-5 / 1-6）

- [ ] ステップ 1-4: `duration.go` と単体テスト完了
- [ ] ステップ 1-5: `lock.go` と単体テスト完了
- [ ] ステップ 1-6: `boot.go` と `main.go` 再構成・単体テスト完了
- [ ] PR-2 作成・マージ完了

### PR-3: `fetch` サブコマンド（ステップ 2-1）

- [ ] ステップ 2-1: `fetch.go` と単体テスト完了
- [ ] PR-3 作成・マージ完了

### PR-4: `summary` サブコマンド（ステップ 2-2）

- [ ] ステップ 2-2: `summary.go` と単体テスト完了
- [ ] PR-4 作成・マージ完了

### PR-5a: `gc` + `reprocess` サブコマンド（ステップ 3-1 / 3-2）

- [ ] ステップ 3-1: `gc.go` と単体テスト完了
- [ ] ステップ 3-2: `reprocess.go` と単体テスト完了
- [ ] PR-5a 作成・マージ完了

### PR-5b: `recover` サブコマンド（ステップ 3-3）

- [ ] ステップ 3-3: `recover.go` と単体テスト完了
- [ ] PR-5b 作成・マージ完了

### PR-6: セキュリティテスト・統合テスト・ドキュメント（ステップ 4-1 / 4-2）

- [ ] ステップ 4-1: セキュリティテスト・統合テスト完了
- [ ] ステップ 4-2: ドキュメント整備完了
- [ ] PR-6 作成・マージ完了

---

## 7. 完了基準

### 7.1 機能的完成基準

- 全 5 サブコマンド（`fetch` / `summary` / `reprocess` / `gc` / `recover`）が動作し、`01_requirements.md` の全 AC（AC-01〜AC-45）に対応するテストが存在しパスすること

### 7.2 品質基準

- `make test && make lint` が全フェーズ完了後にグリーンであること
- `var _ store.Store = (*storetestutil.FakeStore)(nil)` コンパイルチェックが継続して通ること
- セキュリティテスト（`security_test.go`）の全項目がパスすること

### 7.3 セキュリティ検証

- `02_architecture.md` §7.3 に記載されたセキュリティ制約が実装されていることをテストで確認する:
  - `config.Secret` ラップが `os.Getenv` 直後に行われること
  - `NotificationSink` から `*notify.SlackHandler` が取得できないこと
  - Slack payload に raw error・credentials・file path が含まれないこと

### 7.4 ドキュメント完成基準

- `README.md` の各サブコマンド使用例が追記済みであること
- `docs/tasks/0070_entrypoint/notes/operational_examples.md` が作成済みであること

---

## 8. 受け入れ条件検証

### F-001 サブコマンドとコマンドライン引数

**AC-01**: 5 サブコマンドの受付
- テスト: `cmd/tlsrpt-digest/main_test.go::TestSubcommandDispatch`
- 実装: `cmd/tlsrpt-digest/main.go`（サブコマンド振り分け）

**AC-02**: サブコマンド省略・不正フラグで usage 出力 + exit 2
- テスト: `cmd/tlsrpt-digest/main_test.go::TestInvalidSubcommand`
- 実装: `cmd/tlsrpt-digest/main.go`（`FlagSet` の `ContinueOnError` + usage 出力）

**AC-03**: `-config` フラグで設定ファイルパス指定（全サブコマンド）
- テスト: `cmd/tlsrpt-digest/main_test.go::TestConfigFlag`
- 実装: 全サブコマンドの `FlagSet` への `-config` 登録

**AC-04**: デフォルトパス `./config.toml`
- テスト: `cmd/tlsrpt-digest/main_test.go::TestConfigDefaultPath`
- 実装: `cmd/tlsrpt-digest/main.go`（`FlagSet` デフォルト値）

**AC-05**: `fetch --since <duration>` フラグ
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetchSinceFlag`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`FlagSet` への `--since` 登録）

**AC-06**: `--since` が `imap.fetch_days` を上書きする
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetchSinceOverridesConfig`
- 実装: `cmd/tlsrpt-digest/fetch.go`（Duration フラグ解決の優先順位）

**AC-07**: duration の `d`/`w` 単位共通パーサー
- テスト: `cmd/tlsrpt-digest/duration_test.go::TestParseDuration`
- 実装: `cmd/tlsrpt-digest/duration.go::ParseDuration`

**AC-07a**: `summary --window` フラグ
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummaryWindowFlag`
- 実装: `cmd/tlsrpt-digest/summary.go`（`FlagSet` への `--window` 登録）

**AC-07b**: duration ≤ 0 でエラー
- テスト: `cmd/tlsrpt-digest/duration_test.go::TestParseDurationInvalid`
- 実装: `cmd/tlsrpt-digest/duration.go::ParseDuration`

**AC-07c**: UTC 日付単位切り捨てカットオフ
- テスト: `cmd/tlsrpt-digest/duration_test.go::TestCutoff`
- 実装: `cmd/tlsrpt-digest/duration.go::Duration.Cutoff`

**AC-07d**: summary 集計窓終端 = 今日の 00:00:00 UTC
- テスト: `cmd/tlsrpt-digest/duration_test.go::TestUTCDayStart`
- 実装: `cmd/tlsrpt-digest/duration.go::UTCDayStart`

### F-002 コンポーネントの初期化

**AC-08**: 設定読込失敗 → exit 1
- テスト: `cmd/tlsrpt-digest/boot_test.go::TestBootstrap_ConfigLoadFail`
- 実装: `cmd/tlsrpt-digest/boot.go::Bootstrap`（W-1）

**AC-09**: ストア初期化失敗 → exit 1
- テスト: `cmd/tlsrpt-digest/boot_test.go::TestBootstrap_StoreOpenFail`, `TestBootstrap_PendingResetFailClosed`
- 実装: `cmd/tlsrpt-digest/boot.go::Bootstrap`（W-6。`ErrPendingReset` は `reset_incomplete` として通知）

**AC-10**: `fetch` IMAP 接続失敗 → exit 1
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_IMAPConnectFail`
- 実装: `cmd/tlsrpt-digest/fetch.go`（IMAP client 生成失敗時の `LogSystemError`）

**AC-10a**: 書き込み系サブコマンドのプロセス排他ロック
- テスト: `cmd/tlsrpt-digest/lock_test.go::TestAcquireExclusive_Concurrent`, `boot_test.go::TestBootstrap_LockFail`, `main_test.go::TestLockHeldDuringRunner`
- 実装: `cmd/tlsrpt-digest/lock.go::AcquireExclusive`, `boot.go`（W-5）, `main.go`（runner 完了後に `BootContext.Close()`）

**AC-10c**: `summary` はロック不要・read-only・空ストア正常終了
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummary_EmptyStore`
- 実装: `cmd/tlsrpt-digest/summary.go`（`OpenReadOnly` 使用・空集計 exit 0）

### F-003 `fetch` サブコマンド

**AC-10d / AC-10e**: `fetch` 起動直後の recovery-required 確認
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_RecoveryRequiredStops`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 1 の `LoadRecoveryRequired`）

**AC-11**: IMAP メタ情報取得（UID / RFC822.SIZE / SEEN / Message-ID / INTERNALDATE / UIDVALIDITY）
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_MetaFetch`, `TestFetch_MetaFetchFail`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`FetchMeta` 呼び出し、失敗時は `SystemErrorKind=imap_operation_failed`）

**AC-11a**: `LoadUIDValidity` で前回値取得・比較
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_UIDValidity_FirstRun`, `TestFetch_UIDValidity_LoadFail`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 3）

**AC-11b**: `found=false` のとき即時 `SaveUIDValidity`
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_UIDValidity_FirstSave`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 3 の `found=false` 分岐）

**AC-11b-cont**: `found=true` かつ一致でフェッチ継続
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_UIDValidity_Match`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 3 の一致分岐）

**AC-11c / AC-11d**: UIDVALIDITY 不一致 → recovery-required 記録 + fail closed
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_UIDValidity_Mismatch`, `TestFetch_UIDValidity_SaveRecoveryRequiredFail`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 3 の不一致分岐）

**AC-12**: SEEN + `.eml` ありでスキップ
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_Skip_SeenWithLocalFile`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ダウンロード対象選定テーブル）

**AC-13 / AC-14**: RFC822.SIZE 不一致 WARN・処理継続
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_SizeMismatchWarn`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`LogWarning(size_mismatch)` バッファリング）

**AC-15 / AC-15a**: `.eml` 保存と `SaveEmailMetas` 一括登録
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_SaveEmailMetas`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 5 の `SaveEmail`→`SaveEmailMetas`）

**AC-16 / AC-16a**: パース・パース失敗時の WARN + スキップ + SEEN 継続
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_ParseFailWarn`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`LogWarning(parse_failure)` バッファリング）

**AC-17**: failure > 0 → `LogAlert`
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_AlertOnFailure`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`NotificationSink.LogAlert` 呼び出し）

**AC-18**: `SaveReports` 一括 UPSERT
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_SaveReports`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 5）

**AC-18a**: `Flush()` → SEEN の順序（at-least-once）
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_AtLeastOnceOrder`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 12〜13 の順序）

**AC-19**: `Flush()` 成功後に SEEN 付与
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_SeenAfterFlush`
- 実装: `cmd/tlsrpt-digest/fetch.go`（`MarkSeen` 呼び出し順序）

**AC-20**: 1 件失敗が他に影響しない
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_PerEmailFailureIsolation`
- 実装: `cmd/tlsrpt-digest/fetch.go`（メール単位エラー継続処理）

**AC-20a**: ステップ 5 完了後に `SaveUIDValidity` 冪等保存
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_SaveUIDValidity_AfterProcess`
- 実装: `cmd/tlsrpt-digest/fetch.go`（ステップ 14）

**AC-21**: exit 0 / exit 1
- テスト: `cmd/tlsrpt-digest/fetch_test.go::TestFetch_ExitCodes`（正常完了 → exit 0、UIDVALIDITY 不一致 / Flush 失敗 / recovery-required 残存 → exit 1 を列挙）
- 実装: `cmd/tlsrpt-digest/fetch.go`

### F-004 `reprocess` サブコマンド

**AC-21a**: recovery-required 確認 → 停止
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_RecoveryRequiredStops`, `TestReprocess_LoadRecoveryRequiredFail`
- 実装: `cmd/tlsrpt-digest/reprocess.go`

**AC-22**: `LoadEmails` で `.eml` を列挙
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_LoadEmails`
- 実装: `cmd/tlsrpt-digest/reprocess.go`

**AC-23a / AC-23**: `SaveEmailMetas` → `SaveReports` の順序
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_CallOrder`
- 実装: `cmd/tlsrpt-digest/reprocess.go`（`02_architecture.md` §6.6 参照）

**AC-24 / AC-24a**: `--notify` フラグと `Flush()` 呼び出し
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_NotifyFlag`
- 実装: `cmd/tlsrpt-digest/reprocess.go`

**AC-25**: 失敗種別による挙動の違い（スキップ継続 vs. コマンド中断）
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_FailureHandling`, `TestReprocess_FileReadFailureContinues`, `TestReprocess_SaveEmailMetasFail`
- 実装: `cmd/tlsrpt-digest/reprocess.go`

**AC-26**: exit 0 / exit 1
- テスト: `cmd/tlsrpt-digest/reprocess_test.go::TestReprocess_ExitCodes`（正常完了 → exit 0、recovery-required 残存 / SaveReports 失敗 / Flush 失敗 → exit 1 を列挙）
- 実装: `cmd/tlsrpt-digest/reprocess.go`

### F-005 `summary` サブコマンド

**AC-27**: `[start, end)` 区間で集計
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummary_Window`, `internal/notify/aggregate_test.go`（境界値テスト更新）
- 実装: `cmd/tlsrpt-digest/summary.go` + `internal/notify/aggregate.go`（`inSummaryPeriod` 修正）

**AC-27a**: recovery-required → 集計・送信なし exit 1
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummary_RecoveryRequired`
- 実装: `cmd/tlsrpt-digest/summary.go`（第 1 回・第 2 回 `CheckRecoveryRequired`）

**AC-28**: 送信メッセージに集計対象期間を含める
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummary_PeriodInMessage`
- 実装: `cmd/tlsrpt-digest/summary.go`（`LogSummary` へ渡す `notify.Summary.Period`）

**AC-29**: exit 0 / exit 1
- テスト: `cmd/tlsrpt-digest/summary_test.go::TestSummary_ExitCodes`（空集計 / 正常送信 → exit 0、recovery-required 残存 / Flush 失敗 → exit 1 を列挙）
- 実装: `cmd/tlsrpt-digest/summary.go`

### F-006 `gc` サブコマンド

**AC-29a**: recovery-required → 削除なし exit 1
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_RecoveryRequiredStops`, `TestGC_LoadRecoveryRequiredFail`
- 実装: `cmd/tlsrpt-digest/gc.go`

**AC-30**: `--before` フラグ
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_BeforeFlag`
- 実装: `cmd/tlsrpt-digest/gc.go`（`FlagSet` への `--before` 登録）

**AC-31**: `--before` 省略時のデフォルト設定値
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_BeforeDefault`
- 実装: `cmd/tlsrpt-digest/gc.go`（`store.retention_days` フォールバック）

**AC-32**: `DeleteReportsBefore` へ UTC 切り捨て済みカットオフを渡す
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_ReportsCutoff`
- 実装: `cmd/tlsrpt-digest/gc.go`（`Duration.Cutoff(now)` → `DeleteReportsBefore`）

**AC-32a / AC-32b**: `--max-email-age` フラグと `DeleteEmailsBefore` 呼び出し
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_EmailsCutoff`
- 実装: `cmd/tlsrpt-digest/gc.go`（`Duration.Cutoff(now)` → `DeleteEmailsBefore`）

**AC-33**: 削除件数 INFO ログ
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_DeleteCountLog`, `TestGC_DeleteReportsFailureNotifies`, `TestGC_DeleteEmailsFailureNotifies`
- 実装: `cmd/tlsrpt-digest/gc.go`（成功時は `slog.Info` 出力、削除失敗時のみ `LogSystemError` + `Flush()`）

**AC-34**: exit 0 / exit 1
- テスト: `cmd/tlsrpt-digest/gc_test.go::TestGC_ExitCodes`（正常完了 → exit 0、recovery-required 残存 / DeleteReportsBefore 失敗 → exit 1 を列挙）
- 実装: `cmd/tlsrpt-digest/gc.go`

### F-007 `recover` サブコマンド

**AC-35**: `--mode` フラグ
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_ModeFlag`
- 実装: `cmd/tlsrpt-digest/recover.go`（`FlagSet` への `--mode` 登録）

**AC-36**: recovery-required 内容の stdout 表示
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_DisplayInfo`
- 実装: `cmd/tlsrpt-digest/recover.go`（stdout 出力ロジック）

**AC-37**: `keep-old` で `ApplyRecovery` 呼び出し
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_KeepOld`
- 実装: `cmd/tlsrpt-digest/recover.go`

**AC-38**: `discard-old --yes` で `ResetForRecovery` 呼び出し
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_DiscardOldWithYes`
- 実装: `cmd/tlsrpt-digest/recover.go` + `internal/store/recovery.go`

**AC-39**: `discard-old`（`--yes` なし）は変更なし exit 1
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_DiscardOldWithoutYes`
- 実装: `cmd/tlsrpt-digest/recover.go`

**AC-40**: recovery-required 不在 → 説明付き exit 1
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_NoRecoveryRequired`
- 実装: `cmd/tlsrpt-digest/recover.go`

**AC-41**: 中途半端な状態を残さない
- テスト: `internal/store/recovery_test.go::TestResetForRecovery_CrashRecovery`
- 実装: `internal/store/recovery.go::ResetForRecovery`

**AC-42**: `--abort-reset`/`--yes` 単独はエラー
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_AbortReset_FlagCombination`
- 実装: `cmd/tlsrpt-digest/recover.go`（フラグ組み合わせ検証）

**AC-43**: `--abort-reset --yes` は commit 前 pending reset でのみ有効
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_AbortReset_CommitBoundary`, `internal/store/recovery_test.go::TestAbortReset_Preconditions`
- 実装: `cmd/tlsrpt-digest/recover.go` + `internal/store/recovery.go::AbortReset`

**AC-44**: `--abort-reset --yes` の再実行冪等性
- テスト: `internal/store/recovery_test.go::TestAbortReset_Idempotent`
- 実装: `internal/store/recovery.go::AbortReset`

**AC-45**: pending reset 検出時の選択肢表示
- テスト: `cmd/tlsrpt-digest/recover_test.go::TestRecover_PendingReset_DisplayOptions`, `TestRecover_PendingReset_DryRunPaths`
- 実装: `cmd/tlsrpt-digest/recover.go`（通常表示・`keep-old`・dry-run は pending reset で変更せず案内のみ、`discard-old --yes` / `abort-reset --yes` のみ `OpenRecoverReset` で継続・ロールバックする）

---

## 9. 次のステップ

実装計画書が `approved` になったら、`02_architecture.md` §8 の順序（フェーズ 1 → 2 → 3 → 4）に従い実装を開始する。各フェーズ完了後に `make test && make lint` が通ることを確認してから次フェーズへ進む。
