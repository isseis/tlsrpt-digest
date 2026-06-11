# 実装計画書：IMAP メールボックスの古いメール自動削除

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-06-10 |
| レビュー日 | 2026-06-10 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

本実装は `02_architecture.md` で承認された設計に従い、以下を実現する。

1. `[imap]` セクションに `retention_days` 設定を新設し、IMAP メールボックス上の保持期間（デフォルト無効）を制御できるようにする（AC-01〜AC-06）。
2. `MailFetcher` インターフェースに `DeleteOlderThan` / `SearchOlderThan` を追加し、`imapClient` で UID EXPUNGE による対象限定削除を実装する（AC-08, AC-12, AC-15〜AC-17）。
3. `internal/store` に `CountReportsBefore` / `CountEmailsBefore` を追加し、dry-run での削除予定件数のログ出力を可能にする（AC-10）。
4. `gc` サブコマンドに IMAP 古いメール削除ステップ・認証情報チェック・dry-run 分岐・エラー分類・統合ログを追加する（AC-03, AC-07, AC-09〜AC-11, AC-13, AC-14）。
5. `gc --dry-run` を許可し、`main.go` のヘルプ文言を更新する（AC-10）。
6. README（日本語・英語）に設定例・運用上の注意・有効化手順を追記する。

### 1.2 実装方針

`02_architecture.md` §1.1 の設計原則（オプトイン・フェイルセーフ、対象限定削除、既存パターンの踏襲、dry-run は読み取り専用）に従う。各コンポーネントの詳細設計は次のとおりアーキテクチャ設計書を参照し、本書では重複させない。

- config の値の意味と検証規則: §3.1
- `MailFetcher` 拡張とインターフェース定義: §3.2
- `imapClient` の UID EXPUNGE 実装方針（UIDPLUS 確認順序・検索条件・read-write SELECT）: §3.3
- `Store` のカウントメソッド: §3.4
- `gcRunner` の処理ステップ: §3.5、全体フロー: §6.1、`DeleteOlderThan` 内部フロー: §6.2
- dry-run の副作用契約: §3.6
- テストダブル拡張方針: §3.7
- エラー分類: §4.2

### 1.3 既存コード調査結果

#### `internal/config/types.go`
- **現状**: `IMAPConfig` / `rawIMAPConfig` に `RetentionDays` フィールドが存在しない。
- **変更が必要**: `IMAPConfig.RetentionDays int` と `rawIMAPConfig.RetentionDays *int`（TOML キー `retention_days`）を追加する（AC-01）。

#### `internal/config/defaults.go`
- **現状**: `applyDefaults` は `IMAPConfig` の各フィールドに `intDefault` 等を適用している。`defaultIMAPRetentionDays` のような定数は存在しない。
- **変更が必要**: `RetentionDays: intDefault(raw.IMAP.RetentionDays, 0)` を追加する（AC-02）。デフォルト値 `0` は他の `default*` 定数と異なり意味上「無効」を表すため、定数化せずリテラル `0` を直接使用し、コメントで「0 = disabled (opt-in)」と明記する。

#### `internal/config/validate.go`
- **現状**: `validate(cfg *Config)` は `IMAP.Host` / `Port` / `FetchDays` / `Summary.WindowDays` / `Store.RetentionDays` / `Store.MaxEmailAgeDays` / `MaxMessageBytes` / `TLSCACert` / `AllowedHost` を検証する。`IMAP.RetentionDays` の検証は存在しない。
- **変更が必要**: `IMAP.RetentionDays < 0` で `ErrInvalidIMAPRetentionDays`（AC-04）、`IMAP.RetentionDays > 0` かつ `IMAP.RetentionDays < max(IMAP.FetchDays, Summary.WindowDays)` で `ErrIMAPRetentionTooShort`（AC-05）を返す検証を追加する。`RetentionDays == 0` は両チェックの対象外とする（AC-03）。

#### `internal/config/errors.go`
- **現状**: `ErrInvalidFetchDays` 等の sentinel エラーが定義されているが、IMAP retention 用のエラーは存在しない。
- **変更が必要**: `ErrInvalidIMAPRetentionDays` と `ErrIMAPRetentionTooShort` を「Field validation errors」ブロックに追加する（AC-06）。

#### `internal/config/config_test.go`
- **現状**: `TestLoad_AllFields` は `[imap]` の全フィールドを個別に assert しているが `retention_days` を含まない。`TestLoad_Default_FetchDays14` 等のデフォルト値専用テストが存在するが `IMAP.RetentionDays` 用は無い。`TestLoad_DaysValidation` は `window_days` / `retention_days`（store）/ `max_email_age_days` の「`0` と負値はエラー、`1` は許可」というテーブルテストだが、`imap.retention_days` は `0` が有効値であるため、このテーブルは流用できない（新規テスト関数が必要）。
- **変更が必要**:
  - `TestLoad_AllFields` に `retention_days = N` を追加し `cfg.IMAP.RetentionDays` を assert する。
  - `TestLoad_Default_IMAPRetentionDays0`（新規）を追加する（AC-02）。
  - `TestLoad_IMAPRetentionDaysValidation`（新規）を追加し、負値で `ErrInvalidIMAPRetentionDays`、`0` で成功することを検証する（AC-03, AC-04）。
  - `TestLoad_IMAPRetentionTooShort`（新規）を追加し、`retention_days` が `max(fetch_days, window_days)` 未満でエラー、等しい場合は成功することを検証する（AC-05）。

#### `internal/config/load_file_test.go`
- **現状**: `TestLoadFile_*` は `cfg.IMAP.Host` / `Port` / `Mailbox` / `FetchDays` / `Store.RetentionDays` 個別の assert のみで、`IMAPConfig` 構造体全体を比較するテーブルテストは存在しない。`retention_days`（IMAP 側）への参照は無い。
- **変更不要**: `IMAPConfig.RetentionDays` のデフォルト値 `0` は既存のテストデータ（`retention_days` 未指定の TOML）でも成立し、既存 assert と矛盾しない。コンパイルエラーも生じない（フィールド追加のみで既存比較は個別フィールド単位）。

#### `internal/imap/imap.go`
- **現状**: `MailFetcher` インターフェースは `FetchMeta` / `Download` / `MarkSeen` / `Close` のみ。
- **変更が必要**: `DeleteOlderThan(ctx context.Context, cutoff time.Time) (deleted int, err error)` と `SearchOlderThan(ctx context.Context, cutoff time.Time) ([]uint32, error)` を追加する（§3.2 のドキュメントコメントをそのまま使用）（AC-15, AC-16）。

#### `internal/imap/client.go`
- **現状**:
  - `imapSession` インターフェースは `Login` / `Logout` / `Close` / `Select` / `UidSearch` / `UidFetch` / `UidStore` のみで、capability 照会・UID EXPUNGE のメソッドが無い。
  - `dialTLS` は `imapclient.DialTLS` をそのまま `imapSession` として返す（`*imapclient.Client` は `imapSession` の現行メソッド集合を満たす）。
  - `*imapclient.Client` には `Capability() (map[string]bool, error)` と `Support(cap string) (bool, error)`（`client/cmd_any.go`）が実装済みであることをモジュールキャッシュで確認済み。UID EXPUNGE（RFC 4315）は go-imap v1.2.1 本体には存在しない。
- **変更が必要**:
  - `imapSession` に `Support(name string) (bool, error)` と `UidExpunge(seqset *goimap.SeqSet) error`（または `go-imap-uidplus` の実シグネチャに合わせた形）を追加する（AC-08, AC-12, AC-15）。
  - `dialTLS` が返す具象型を `*imapclient.Client` の素の戻り値から、`UidExpunge` を `go-imap-uidplus` 経由で実装するラッパー型（例: `uidplusSession`）でラップするよう変更する。`Support` は `*imapclient.Client` の既存メソッドに委譲する。
  - `imapClient` に `DeleteOlderThan` / `SearchOlderThan` を実装する（§3.3, §6.2 のフローに従う）。両メソッドは `UID SEARCH BEFORE <cutoff の日付>` を発行する共通ヘルパー（例: `searchUIDsBefore`）を共有する。
  - `DeleteOlderThan` は `cutoff.IsZero()` で `(0, nil)`（AC-16）、`Support("UIDPLUS")` が `false` の場合 `slog.Warn` を出力して `(0, nil)`（AC-12）、read-write SELECT が read-only に倒れた場合 `ErrMailboxReadOnly` を返す。検索結果が空なら `(0, nil)`。ヒットがあれば `UID STORE +FLAGS (\Deleted)` → `UID EXPUNGE <対象 UID>` の順に実行する（AC-08）。
  - `SearchOlderThan` は EXAMINE（読み取り専用 SELECT）+ `UID SEARCH BEFORE` のみを行い、`cutoff.IsZero()` の場合は `([], nil)` を返す（dry-run 経路で `retention_days = 0` のときは呼び出されないため必須ではないが、`DeleteOlderThan` との対称性のため同じガードを置く）。

#### `internal/imap/client_test.go`
- **現状**: `fakeSession` は `Login` / `Logout` / `Close` / `Select` / `UidSearch` / `UidFetch` / `UidStore` のみを実装し、`imapSession` を満たす。`Support` / `UidExpunge` が無いため、インターフェース拡張後はコンパイルエラーになる。
- **変更が必要**:
  - `fakeSession` に `supportResult map[string]bool`（または `supportFn`）と `UidExpunge` の呼び出し記録用フィールド（例: `uidExpungeCalls []*goimap.SeqSet`、`uidExpungeErr error`）、対応する `Support` / `UidExpunge` メソッドを追加する。
  - 新規テスト（テスト関数名は Phase 2 のチェックボックスに記載）を追加し、AC-08, AC-12, AC-15, AC-16 を検証する。

#### `internal/imap/testutil/mocks.go`
- **現状**: `FakeMailFetcher` は `FetchMeta` / `Download` / `MarkSeen` / `Close` のみを実装する。`DeleteOlderThan` / `SearchOlderThan` が無いため、`MailFetcher` 拡張後はコンパイルエラーになる。
- **変更が必要**: `DeleteOlderThanCalls []time.Time` / `DeleteOlderThanResult int` / `DeleteOlderThanErr error`、`SearchOlderThanCalls []time.Time` / `SearchOlderThanResult []uint32` / `SearchOlderThanErr error` を追加し、対応するメソッドを実装する（AC-17）。

#### `internal/imap/testutil/mocks_test.go`
- **現状**: `FakeMailFetcher` の各メソッドの呼び出し記録を検証するテストが存在する（既存メソッド分）。
- **変更が必要**: `DeleteOlderThan` / `SearchOlderThan` の呼び出し記録（`cutoff` 値）を検証するテストケースを追加する（AC-17）。

#### `internal/imap/client_integration_test.go`
- **現状**: `loadIntegrationConfig` / `loadSMTPTestConfig` / `requireFixedUserEnv` / `requireSMTPEnv` / `testMailboxName` 等のヘルパーが整備済み。`CreateMailbox` / `DeleteMailbox`（`testutil/helpers.go`）も利用可能。UIDPLUS の CAPABILITY 確認テストは存在しない。
- **変更が必要**: greenmail の UIDPLUS capability を確認するテストと、`DeleteOlderThan` / `SearchOlderThan` の統合テストを追加する（AC-07, AC-08, AC-12）。greenmail が UIDPLUS 非対応の場合は AC-12 のフォールバック経路のみ検証する（Phase 2 の完了条件で確認結果を記録する）。

#### `internal/store/store.go`
- **現状**: `Store` インターフェースに `DeleteReportsBefore` / `DeleteEmailsBefore` はあるが、件数のみを返す読み取り専用メソッドは無い。
- **変更が必要**: `CountReportsBefore(cutoff time.Time) (int, error)` と `CountEmailsBefore(cutoff time.Time) (int, error)` を追加する（§3.4）（AC-10）。

#### `internal/store/reports.go`
- **現状**: `DeleteReportsBefore`（95-124 行目）は `s.readOnly` で `ErrReadOnly` を返した後 `loadDataFile` し、`r.DateRange.EndDatetime.Before(cutoff)` で削除対象を判定する。
- **変更が必要**: `CountReportsBefore` を追加する。`DeleteReportsBefore` と同じ述語（`EndDatetime.Before(cutoff)`）でカウントするが、`s.readOnly` チェックと `saveDataFile` 呼び出しは行わない（読み取り専用ストアでも動作する）。

#### `internal/store/emails.go`
- **現状**: `DeleteEmailsBefore`（249-311 行目）は `s.readOnly` で `ErrReadOnly`、`cutoff.IsZero()` で `(0, nil)` を返した後、`entry.InternalDate.Before(cutoff)` で削除対象を判定し、ファイル削除とディレクトリクリーンアップを行う。
- **変更が必要**: `CountEmailsBefore` を追加する。`cutoff.IsZero()` で `(0, nil)`、`entry.InternalDate.Before(cutoff)` で件数のみカウントし、`s.readOnly` チェック・ファイル削除・ディレクトリクリーンアップは行わない。

#### `internal/store/reports_test.go` / `internal/store/emails_test.go`
- **現状**: `TestDeleteReportsBefore*` / `TestDeleteEmailsBefore*` が削除件数・残存レコードを検証している。`CountReportsBefore` / `CountEmailsBefore` のテストは存在しない。
- **変更が必要**: `CountReportsBefore` / `CountEmailsBefore` が `DeleteReportsBefore` / `DeleteEmailsBefore` と同一件数を返すこと、読み取り専用ストア（`OpenReadOnly`）でも動作することを検証する新規テストを追加する（AC-10）。

#### `internal/store/testutil/mocks.go`
- **現状**: `FakeStore` は `DeleteReportsBefore` / `DeleteEmailsBefore` を実装するが `CountReportsBefore` / `CountEmailsBefore` は無い。`Store` インターフェース拡張後はコンパイルエラーになる。
- **変更が必要**: `CountReportsBefore` / `CountEmailsBefore` を追加する。`f.Reports` / `f.Emails` を走査して `DeleteReportsBefore` / `DeleteEmailsBefore` と同じ述語でカウントするのみとし、状態を変更しない。呼び出し回数フィールドは既存の `Delete*BeforeCallCount` と区別するため追加しない（カウントは副作用を持たないため呼び出し記録の必要性が低い。dry-run のテストでは戻り値で十分検証できる）。

#### `cmd/tlsrpt-digest/gc.go`
- **現状**: `gcRunner{ now func() time.Time }` のみ。`Run` は recovery チェック → `DeleteReportsBefore` → `DeleteEmailsBefore` → ログ出力の 4 ステップ。IMAP 関連のフィールド・処理は無い。
- **変更が必要**: §3.5 と §6.1 のフローに従い、`gcRunner` に `newMailFetcher` / `credentials` フィールドを追加し、認証情報チェック・IMAP 削除ステップ・dry-run 分岐・統合ログを実装する（詳細は Phase 4）。

#### `cmd/tlsrpt-digest/gc_test.go`
- **現状**: 既存 18 テストはすべて `cfg.IMAP.RetentionDays` をゼロ値（`0`）のまま使用しており（`makeGCBoot` のデフォルト `cfg` および各テストの `cfg` 構築箇所を確認済み）、IMAP 削除ステップは AC-09 によりスキップされる。`gcRunner{now: ...}` のリテラル構築（`newMailFetcher` / `credentials` フィールドなし）は、フィールド追加後も Go のゼロ値ルールにより `nil` になりコンパイルエラーにはならないが、`retention_days = 0` の経路では参照されないため動作上も問題ない。
- **変更不要（既存 18 テスト）**: 既存テストへの修正は不要。新規テストのみ追加する（Phase 4）。

#### `cmd/tlsrpt-digest/fetch.go`
- **現状**: `fetchRunner` は `newMailFetcher func(cfg imap.Config) (imap.MailFetcher, error)` と `credentials func() (string, config.Secret)` を持ち、`newFetchRunner()` で `imap.NewIMAPClient` と環境変数読み取りクロージャを設定する。`classifyIMAPClientError` と `dryRunUIDSampleMax`（375 行目）/ `logFetchDryRunSummary` のサンプリングパターンが定義されている。
- **変更不要**: `gcRunner` は同じ `newMailFetcher` / `credentials` パターンと `classifyIMAPClientError`、`dryRunUIDSampleMax` 定数を再利用する（新規定数は追加しない）。

#### `cmd/tlsrpt-digest/boot.go`
- **現状**: `buildIMAPConfig(cfg *config.Config, creds IMAPCredentials) imap.Config` は `Host` / `Port` / `Mailbox` / `TLSCACert` / `MaxMessageBytes` / `Username` / `Password` のみを設定する。`RetentionDays` は `imap.Config` に存在しない。
- **変更不要**: `imap.Config` に `RetentionDays` を追加する必要は無い。`gcRunner` はカットオフを `cfg.IMAP.RetentionDays`（`config.Config`）から直接計算し（`Duration{Days: cfg.IMAP.RetentionDays}.Cutoff(now)`）、`imap.Config` には渡さない。`buildIMAPConfig` はそのまま再利用できる。

#### `cmd/tlsrpt-digest/main.go`
- **現状**:
  - 196 行目: `if opts.DryRun && subcmd != subcommandFetch && subcmd != subcommandSummary { return errDryRunNotSupported }`
  - 145 行目: `--dry-run` のフラグ説明文は fetch 専用の文言（"connect to IMAP and check UIDVALIDITY..."）。
  - 241-247 行目: `printDetailedHelp` の `fetch:` / `summary:` セクションにのみ `-n, --dry-run` の説明があり、`gc:` セクション（252 行目以降）には無い。
- **変更が必要**:
  - 196 行目の条件に `subcommandGC` を追加する（AC-10）。
  - 145 行目のフラグ説明文を、fetch/summary/gc で共通に通用する一般的な文言に更新する（gc の dry-run 効果を含める）。
  - `printDetailedHelp` の `gc:` セクションに `-n, --dry-run` の説明を追加する。

#### `cmd/tlsrpt-digest/main_test.go`
- **現状**:
  - `TestParseCLI_DryRunSupportedSubcommands`（155-163 行目）は `{subcommandFetch, subcommandSummary}` をループし `--dry-run` が受理されることを検証する。
  - `TestParseCLI_DryRunUnsupportedSubcommands`（165-172 行目）は `{subcommandGC, subcommandReprocess, subcommandRecover}` をループし `errDryRunNotSupported` を検証する。
  - `TestRunCLI_DryRunUnsupportedSubcommandExits2`（174-181 行目）は同じ 3 つのサブコマンドで `exitUsage` を検証する。
- **変更が必要**:
  - `TestParseCLI_DryRunSupportedSubcommands` のループ対象に `subcommandGC` を追加する。
  - `TestParseCLI_DryRunUnsupportedSubcommands` と `TestRunCLI_DryRunUnsupportedSubcommandExits2` のループ対象から `subcommandGC` を削除し、`{subcommandReprocess, subcommandRecover}` のみとする。

#### `go.mod`
- **現状**: `go.mod` の `require` ブロックに `github.com/emersion/go-imap-uidplus` は無く、リポジトリは現時点でこのパッケージに依存していない。実際の API シグネチャ（`UidExpunge` の引数・戻り値・`SupportUidPlus` の有無）は未確認である。
- **変更が必要**: `go get github.com/emersion/go-imap-uidplus` で依存を追加する。Phase 2 の最初のタスクとして `go doc` で実際の API シグネチャを確認し、`client.go` のラッパー実装をそれに合わせる（§3.3 の A.1 参照）。
- **フォールバック（`go-imap-uidplus` が取得不能、または API がコンストラクタ・`UidExpunge` シグネチャの想定と大きく異なる場合）**: 新規外部依存を追加せず、`internal/imap/client.go` 内で RFC 4315 の `UID EXPUNGE` コマンドを `*imapclient.Client.Execute` で直接送信する。`go-imap` v1.2.1 の `client` パッケージには `func (c *Client) Execute(cmdr imap.Commander, h responses.Handler) (*imap.StatusResp, error)` というこの実行経路がすでに存在し、ルートパッケージの `imap.Commander` インターフェースと `imap.Command` 構造体（`commands/expunge.go` がその実装例）を使って任意のコマンドを送信できることを確認済み。フォールバック実装は以下の形になる。

  ```go
  // uidExpungeCommand implements imap.Commander for the RFC 4315 UID EXPUNGE
  // command, used when github.com/emersion/go-imap-uidplus is unavailable.
  type uidExpungeCommand struct {
      seqset *goimap.SeqSet
  }

  func (cmd *uidExpungeCommand) Command() *goimap.Command {
      return &goimap.Command{Name: "UID EXPUNGE", Arguments: []interface{}{cmd.seqset}}
  }

  func (s *uidplusSession) UidExpunge(seqset *goimap.SeqSet) error {
      _, err := s.Client.Execute(&uidExpungeCommand{seqset: seqset}, nil)
      return err
  }
  ```

  この場合 `uidplusSession` は `uidplus *uidplus.Client` フィールドを持たず、`*imapclient.Client` のみを埋め込む。`go.mod` への新規依存追加は不要。Phase 2 の依存関係タスクで `go get github.com/emersion/go-imap-uidplus` が失敗した場合、このフォールバックに切り替えたことを「フェーズ完了の確認」に記録する。

#### `README.md` / `README.ja.md`
- **現状**: 「Configuration File (TOML)」/「設定ファイル (TOML)」の「All Configuration Items」/「全設定項目」に `[imap]` セクションのテーブルがあるが `retention_days` は無い。`gc` サブコマンドの説明（README.md 203-209 行目、README.ja.md 202-209 行目付近）に dry-run の記載が無い。`config.toml`（リポジトリルート）は `.gitignore` 対象（`.gitignore:54`）のためコミット対象外であり、本タスクでの編集対象に含めない。
- **変更が必要**: `[imap]` の設定例に `retention_days` を追加し、オプトイン手順・有効化時の挙動（IMAP 認証情報が必須になること）・TLSRPT 受信専用メールボックスの推奨・Gmail の「完全に削除する」設定が前提条件であること・`fetch --since` を `retention_days` 以下にする注意を記載する（§3.1, §3.3, §5.1, §5.3）。`gc` サブコマンドの説明に `--dry-run` を追加する。CLAUDE.md の翻訳ワークフローに従い、`README.ja.md` を先に編集し、`/mktrans` で `README.md` に反映する。

---

## 2. 実装ステップ

### Phase 1: config 拡張（AC-01〜AC-06）

#### 変更ファイル: `internal/config/types.go`

- [x] `IMAPConfig` 構造体の `MaxMessageBytes int64` フィールドの直後に以下を追加する。

  ```go
  // RetentionDays is the IMAP message retention period in days.
  // 0 disables IMAP deletion (opt-in, default).
  RetentionDays int
  ```

- [x] `rawIMAPConfig` 構造体の `MaxMessageBytes *int64 \`toml:"max_message_bytes"\`` の直後に以下を追加する。

  ```go
  RetentionDays *int `toml:"retention_days"`
  ```

#### 変更ファイル: `internal/config/defaults.go`

- [x] `applyDefaults` 内の `IMAPConfig{...}` リテラルの `MaxMessageBytes: int64Default(...)` の直後に以下のフィールドを追加する。リテラル `0` をそのまま使用し、`defaultIMAPRetentionDays` のような新規定数は追加しない（`defaultStoreRetention = 30` などの既存定数とは異なり、無効値であることをコード上で明示するため）。

  ```go
  RetentionDays: intDefault(raw.IMAP.RetentionDays, 0), // 0 = IMAP deletion disabled (opt-in)
  ```

#### 変更ファイル: `internal/config/errors.go`

- [x] "Field validation errors" の `var (...)` ブロック末尾に以下の 2 つの sentinel エラーを追加する（AC-06）。

  ```go
  ErrInvalidIMAPRetentionDays = errors.New("imap.retention_days must be >= 0")
  ErrIMAPRetentionTooShort    = errors.New("imap.retention_days must be >= max(imap.fetch_days, summary.window_days) when enabled")
  ```

#### 変更ファイル: `internal/config/validate.go`

- [x] `validate` 関数内の `cfg.IMAP.MaxMessageBytes < 0` のチェック（`ErrInvalidMaxMessageBytes` を返すブロック）の直後、`validateTLSCACert` の呼び出しより前に、以下の不変条件チェックを追加する。Go 1.21+ 組み込みの `max` を使用する（CLAUDE.md「Modern Go Idioms」準拠）。

  ```go
  if cfg.IMAP.RetentionDays < 0 {
      return fmt.Errorf("config: %w: %d", ErrInvalidIMAPRetentionDays, cfg.IMAP.RetentionDays)
  }
  if cfg.IMAP.RetentionDays > 0 && cfg.IMAP.RetentionDays < max(cfg.IMAP.FetchDays, cfg.Summary.WindowDays) {
      return fmt.Errorf("config: %w: %d", ErrIMAPRetentionTooShort, cfg.IMAP.RetentionDays)
  }
  ```

  `cfg.IMAP.RetentionDays == 0` はどちらの条件にも該当せず素通りする（AC-03）。`validate` の引数・呼び出し元は変更不要（既存のチェック群と同じ `*Config` を参照する）。

#### 変更ファイル: `internal/config/config_test.go`

- [x] `TestLoad_AllFields`（92-126 行目）の `[imap]` ブロックに `retention_days = 30` を追加し（既存の `fetch_days = 3` と `[summary] window_days = 5` に対し `30 >= max(3,5)=5` を満たす）、アサーションに `assert.Equal(t, 30, cfg.IMAP.RetentionDays)` を追加する。
- [x] `TestLoad_Default_IMAPRetentionDays0`（新規）を追加する。`config.Load([]byte(baseConfigTOML))` をロードし、`assert.Equal(t, 0, cfg.IMAP.RetentionDays)` を検証する（AC-02、`TestLoad_Default_FetchDays14` と同じ形式）。
- [x] `TestLoad_IMAPRetentionDaysValidation`（新規）を追加する。`baseConfigTOML`（`fetch_days` 未指定 = 14、`window_days` 未指定 = 7）に `[imap] retention_days = N` を追加したテーブル駆動テストとし、以下を検証する。
  - `N = -1` → `errors.Is(err, config.ErrInvalidIMAPRetentionDays)`（AC-04）。
  - `N = 0` → `require.NoError(t, err)`（AC-03、明示的な無効化として許可）。
- [x] `TestLoad_IMAPRetentionTooShort`（新規）を追加する。テーブル駆動テストとし、以下のケースを検証する（AC-05）。
  - `fetch_days` 未指定（デフォルト 14）、`window_days` 未指定（デフォルト 7）、`retention_days = 13` → `errors.Is(err, config.ErrIMAPRetentionTooShort)`（`max(14,7)=14` 未満）。
  - 同条件で `retention_days = 14` → `require.NoError(t, err)`（境界値、`max(14,7)=14` と等しい場合は許可）。
  - `[imap] fetch_days = 5`、`[summary] window_days = 20`、`retention_days = 19` → `errors.Is(err, config.ErrIMAPRetentionTooShort)`（`window_days` が支配的になるケース、`max(5,20)=20` 未満）。
  - 同条件で `retention_days = 20` → `require.NoError(t, err)`（境界値）。

**フェーズ完了の確認**:
- [x] `make fmt` を実行し差分がないこと
- [x] `make test` が通過すること
- [x] `make lint` が通過すること

### PR-1 作成ポイント: imap.retention_days config support

**対象ステップ**: Phase 1
**推奨タイトル**: `feat(0110): add imap.retention_days config field with validation`
**レビュー観点**:
- デフォルト `0`（無効）が既存環境に影響しないこと（AC-02・AC-03）。
- 負値が `ErrInvalidIMAPRetentionDays` で拒否されること（AC-04）。
- `retention_days > 0` の不変条件が `fetch_days` と `summary.window_days` の両方を考慮し、境界値（等しい場合）で許可されること（AC-05）。
- 新規エラー型が `errors.Is` で判別可能であること（AC-06）。

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のステップ用のブランチへ切り替えた

---

### Phase 2: internal/imap 拡張（AC-08, AC-12, AC-15〜AC-17）

#### 依存関係の追加と API 確認: `go.mod` / `go.sum`

- [x] `go get github.com/emersion/go-imap-uidplus@latest` を実行し、`go.mod` の `require` ブロックに依存を追加する（現状は未追加であることをローカルモジュールキャッシュ不在で確認済み）。取得に失敗した場合（モジュールが存在しない等）、§1「既存コード調査結果」の `go.mod` フォールバックに切り替え、本タスクのチェック項目をフォールバック実施として記録する。
  - 取得成功（`v0.0.0-20200503180755-e75854c361e9`）。フォールバックは不要だった。
- [x] `go get` が成功した場合、`go doc github.com/emersion/go-imap-uidplus` および `go doc github.com/emersion/go-imap-uidplus.Client`（パッケージ名・型名は実際の `go doc` 出力に従って読み替える）を実行し、コンストラクタのシグネチャと `UidExpunge` のシグネチャを確認する。確認結果を本ファイルの本タスク完了時に追記する（Phase 2 完了条件を参照）。
  - 確認結果: `func NewClient(c *client.Client) *Client`、`func (c *Client) UidExpunge(seqSet *imap.SeqSet, ch chan uint32) error`（`Capability = "UIDPLUS"` も定義済み）。想定シグネチャ（引数1個）と異なり `ch chan uint32` を取るため、`uidplusSession.UidExpunge` 内で `nil` を渡すラッパーとした。
- [x] 以降のタスクは次のシグネチャを想定して記述している。`go doc` の結果がこれと異なる場合（API 自体が想定と大きく異なる場合を含む）、`internal/imap/client.go` の `uidplusSession` 型定義と `dialTLS` の実装を実際のシグネチャ、または§1の `go.mod` フォールバックに合わせて修正する。
  - `func uidplus.NewClient(c *imapclient.Client) *uidplus.Client`
  - `func (c *uidplus.Client) UidExpunge(seqSet *goimap.SeqSet, ch chan uint32) error`（実際のシグネチャ。上記「依存関係の追加と API 確認」で確認済み）
  - 上記の通り `UidExpunge` の実シグネチャに合わせて `uidplusSession.UidExpunge(seqset) error { return s.uidplus.UidExpunge(seqset, nil) }` とした。`uidplus.Capability`（`"UIDPLUS"`）を `DeleteOlderThan` の capability 照会に再利用し、独自定数は追加していない。
- [x] `go doc github.com/emersion/go-imap.DeletedFlag` で `goimap.DeletedFlag`（`"\Deleted"`）が go-imap v1.2.1 の `imap` パッケージに定義済みであることを確認する（`SeenFlag` と同じ定数グループ、`message.go`、確認済み）。

#### 変更ファイル: `internal/imap/imap.go`

- [x] `MailFetcher` インターフェースに、`02_architecture.md` §3.2 のコード片（ドキュメントコメント含む）をそのまま追加し、`DeleteOlderThan` と `SearchOlderThan` を末尾に定義する。

  ```go
  // DeleteOlderThan deletes messages whose INTERNALDATE (truncated to date) is
  // before cutoff. If cutoff is zero, it does nothing and returns (0, nil)
  // (AC-16). If the server does not support UIDPLUS, it logs a warning and
  // returns (0, nil) without setting any flags (AC-12).
  DeleteOlderThan(ctx context.Context, cutoff time.Time) (deleted int, err error)

  // SearchOlderThan returns the UIDs of messages whose INTERNALDATE (truncated
  // to date) is before cutoff, using a read-only (EXAMINE) selection. It does
  // not modify mailbox state. Used to preview deletion candidates in dry-run
  // (AC-10).
  SearchOlderThan(ctx context.Context, cutoff time.Time) ([]uint32, error)
  ```

#### 変更ファイル: `internal/imap/client.go`

- [x] `imapSession` インターフェース（28-36 行目）に以下の 2 メソッドを追加する。

  ```go
  Support(name string) (bool, error)
  UidExpunge(seqset *goimap.SeqSet) error
  ```

- [x] `dialTLS`（38-42 行目）が返す値を、go-imap-uidplus でラップした型に変更する。`*imapclient.Client` は `Support(string) (bool, error)` を既に実装しているため（`client/cmd_any.go`、確認済み）、`UidExpunge` のみを委譲する新規型 `uidplusSession` を定義する。

  ```go
  // uidplusSession wraps *imapclient.Client to add RFC 4315 UID EXPUNGE support
  // via go-imap-uidplus, satisfying imapSession.
  type uidplusSession struct {
      *imapclient.Client
      uidplus *uidplus.Client
  }

  func (s *uidplusSession) UidExpunge(seqset *goimap.SeqSet) error {
      return s.uidplus.UidExpunge(seqset)
  }

  var dialTLS dialTLSFunc = func(addr string, tlsConfig *tls.Config) (imapSession, error) {
      c, err := imapclient.DialTLS(addr, tlsConfig)
      if err != nil {
          return nil, err
      }
      return &uidplusSession{Client: c, uidplus: uidplus.NewClient(c)}, nil
  }
  ```

  `uidplus` は `github.com/emersion/go-imap-uidplus` のインポートエイリアス（`go doc` で確認した実際のパッケージ名に合わせる）。

- [x] `imapClient` に、SELECT と UID SEARCH BEFORE をそれぞれ単独で実行するヘルパーを追加する（`MarkSeen`（266-289 行目）の SELECT 部分と `truncateToDate`（291-293 行目）を再利用し、`DeleteOlderThan` / `SearchOlderThan` で共有する）。

  ```go
  // selectMailbox selects the configured mailbox (read-write or read-only) and
  // records lastSelectReadOnly for Close().
  func (c *imapClient) selectMailbox(readOnly bool) (*goimap.MailboxStatus, error) {
      status, err := c.session.Select(c.cfg.Mailbox, readOnly)
      if err != nil {
          return nil, fmt.Errorf("imap: select mailbox %s: %w", c.cfg.Mailbox, err)
      }
      c.lastSelectReadOnly = status.ReadOnly
      return status, nil
  }

  // uidSearchBefore returns UIDs whose INTERNALDATE is before cutoff.date
  // (date-truncated, RFC 3501 BEFORE semantics). The mailbox must already be
  // selected.
  func (c *imapClient) uidSearchBefore(cutoff time.Time) ([]uint32, error) {
      criteria := goimap.NewSearchCriteria()
      criteria.Before = truncateToDate(cutoff)
      uids, err := c.session.UidSearch(criteria)
      if err != nil {
          return nil, fmt.Errorf("imap: uid search before %s: %w", criteria.Before.Format("2006-01-02"), err)
      }
      return uids, nil
  }
  ```

- [x] `DeleteOlderThan` を実装する（処理順序は `02_architecture.md` §6.2 のフロー図に従う: cutoff ゼロ値 → UIDPLUS 対応確認 → read-write SELECT → read-only チェック → UID SEARCH BEFORE → 0 件チェック → `\Deleted` 付与 → UID EXPUNGE）。

  ```go
  const uidplusCapability = "UIDPLUS"

  func (c *imapClient) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
      if err := ctx.Err(); err != nil {
          return 0, fmt.Errorf("imap: delete older than: %w", err)
      }
      if cutoff.IsZero() {
          return 0, nil
      }

      supported, err := c.session.Support(uidplusCapability)
      if err != nil {
          return 0, fmt.Errorf("imap: delete older than: check %s support: %w", uidplusCapability, err)
      }
      if !supported {
          slog.Warn("imap: server does not support UIDPLUS; skipping delete", "mailbox", c.cfg.Mailbox)
          return 0, nil
      }

      status, err := c.selectMailbox(false)
      if err != nil {
          return 0, fmt.Errorf("imap: delete older than: %w", err)
      }
      if status.ReadOnly {
          return 0, ErrMailboxReadOnly
      }

      uids, err := c.uidSearchBefore(cutoff)
      if err != nil {
          return 0, fmt.Errorf("imap: delete older than: %w", err)
      }
      if len(uids) == 0 {
          return 0, nil
      }

      seqSet := uidsToSeqSet(uids)
      storeItem := goimap.FormatFlagsOp(goimap.AddFlags, false)
      if err := c.session.UidStore(seqSet, storeItem, []any{goimap.DeletedFlag}, nil); err != nil {
          return 0, fmt.Errorf("imap: delete older than: mark deleted: %w", err)
      }
      if err := c.session.UidExpunge(seqSet); err != nil {
          return 0, fmt.Errorf("imap: delete older than: uid expunge: %w", err)
      }
      return len(uids), nil
  }
  ```

- [x] `SearchOlderThan` を実装する（EXAMINE のみ、状態変更なし）。

  ```go
  func (c *imapClient) SearchOlderThan(ctx context.Context, cutoff time.Time) ([]uint32, error) {
      if err := ctx.Err(); err != nil {
          return nil, fmt.Errorf("imap: search older than: %w", err)
      }
      if cutoff.IsZero() {
          return []uint32{}, nil
      }

      if _, err := c.selectMailbox(true); err != nil {
          return nil, fmt.Errorf("imap: search older than: %w", err)
      }
      uids, err := c.uidSearchBefore(cutoff)
      if err != nil {
          return nil, fmt.Errorf("imap: search older than: %w", err)
      }
      return uids, nil
  }
  ```

- [x] `import` ブロックに `uidplus "github.com/emersion/go-imap-uidplus"`（実際のパッケージ名に合わせる）を追加する。`log/slog` は既存の import に含まれているため追加不要。

#### 変更ファイル: `internal/imap/client_test.go`

- [x] `fakeSession` 構造体（172-179 行目）に以下のフィールドを追加する。

  ```go
  supportResult       map[string]bool
  supportErr          error
  uidSearchCriteria   *goimap.SearchCriteria
  uidStoreCalls       []fakeUidStoreCall
  uidExpungeCalls     []*goimap.SeqSet
  uidExpungeErr       error
  selectReadOnlyCalls []bool
  ```

  あわせて呼び出し記録用の型を追加する。

  ```go
  type fakeUidStoreCall struct {
      seqset *goimap.SeqSet
      item   goimap.StoreItem
      flags  any
  }
  ```

- [x] `Select`（185-193 行目）に `selectReadOnlyCalls` への記録を追加する: `f.selectReadOnlyCalls = append(f.selectReadOnlyCalls, readOnly)`（第 2 引数を `_` から `readOnly` に変更する）。
- [x] `UidSearch`（196-201 行目）に `f.uidSearchCriteria = criteria` を追加する（第 1 引数を `_` から `criteria` に変更する）。
- [x] `UidStore`（211-213 行目）の本体を、呼び出しを記録するように変更する。

  ```go
  func (f *fakeSession) UidStore(seqset *goimap.SeqSet, item goimap.StoreItem, flags any, _ chan *goimap.Message) error {
      f.uidStoreCalls = append(f.uidStoreCalls, fakeUidStoreCall{seqset: seqset, item: item, flags: flags})
      return nil
  }
  ```

- [x] `//revive:enable:var-naming`（215 行目）の直前に以下のメソッドを追加する。

  ```go
  func (f *fakeSession) Support(name string) (bool, error) {
      if f.supportErr != nil {
          return false, f.supportErr
      }
      return f.supportResult[name], nil
  }

  func (f *fakeSession) UidExpunge(seqset *goimap.SeqSet) error {
      f.uidExpungeCalls = append(f.uidExpungeCalls, seqset)
      return f.uidExpungeErr
  }
  ```

- [x] 以下の新規テストを追加する。すべて `&imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}` のパターン（既存の `TestClose_SendsIMAPCloseOnlyAfterEXAMINE` 等と同様）で `fakeSession` を構築する。
  - `TestImapClient_DeleteOlderThan_ZeroCutoff`: `cutoff = time.Time{}` で呼び出し、`(0, nil)` を返すこと、および `s.supportErr = errors.New("must not be called")` を設定しても発生しないこと（capability 照会前に early return することの確認、AC-16）。
  - `TestImapClient_DeleteOlderThan_UIDPLUSUnsupported`: `s.supportResult = map[string]bool{"UIDPLUS": false}` のとき `(0, nil)` を返し、`len(s.uidStoreCalls) == 0` かつ `len(s.uidExpungeCalls) == 0` であること、`slog.Warn` が出力されること（`bytes.Buffer` + `slog.SetDefault(slog.New(slog.NewTextHandler(...)))` + `t.Cleanup` で捕捉し `level=WARN` を含むことを検証、AC-12）。
  - `TestImapClient_DeleteOlderThan_Success`: `s.supportResult = map[string]bool{"UIDPLUS": true}`、`s.selectMailboxStatus = &goimap.MailboxStatus{Name: "INBOX", ReadOnly: false}`、`s.uidSearchResult = []uint32{5, 6}` のとき `(2, nil)` を返すこと、`s.selectReadOnlyCalls` の最後の値が `false`（read-write SELECT）であること、`s.uidSearchCriteria.Before` が `truncateToDate(cutoff)` と一致すること、`s.uidStoreCalls` に 1 件の呼び出しがあり `flags` が `[]any{goimap.DeletedFlag}` であること、`s.uidExpungeCalls` に 1 件の呼び出しがあり対象 UID が `{5, 6}` のみであること（AC-08, AC-15）。
  - `TestImapClient_DeleteOlderThan_EmptySearch`: `s.uidSearchResult = nil` のとき `(0, nil)` を返し、`len(s.uidStoreCalls) == 0` かつ `len(s.uidExpungeCalls) == 0` であること。
  - `TestImapClient_DeleteOlderThan_ReadOnly`: `s.supportResult = map[string]bool{"UIDPLUS": true}`、`s.selectMailboxStatus = &goimap.MailboxStatus{Name: "INBOX", ReadOnly: true}` のとき `errors.Is(err, ErrMailboxReadOnly)` であること。
  - `TestImapClient_DeleteOlderThan_SupportError`: `s.supportErr = errors.New("capability error")` のとき、`%w` でラップされたエラーが返り `errors.Is(err, s.supportErr)` が真であること。
  - `TestImapClient_SearchOlderThan_ZeroCutoff`: `cutoff = time.Time{}` で `[]uint32{}` を返し、`s.selectErr = errors.New("must not be called")` を設定しても発生しないこと（AC-16 と同じ early-return パターンを `SearchOlderThan` にも適用）。
  - `TestImapClient_SearchOlderThan_UsesExamine`: `s.uidSearchResult = []uint32{7}` のとき `[]uint32{7}` を返すこと、`s.selectReadOnlyCalls` の最後の値が `true`（EXAMINE）であること、`len(s.uidStoreCalls) == 0` かつ `len(s.uidExpungeCalls) == 0` であること（状態変更なし、AC-15 の `SearchOlderThan` 仕様）。

#### 変更ファイル: `internal/imap/testutil/mocks.go`

- [x] `FakeMailFetcher` 構造体（14-27 行目）に以下のフィールドを追加する。

  ```go
  DeleteOlderThanResult int
  DeleteOlderThanErr    error
  DeleteOlderThanCalls  []time.Time

  SearchOlderThanResult []uint32
  SearchOlderThanErr    error
  SearchOlderThanCalls  []time.Time
  ```

- [x] `Close` メソッド（55-58 行目）の前に、`MarkSeen`（49-53 行目）と同じパターンで以下のメソッドを追加する（AC-17）。

  ```go
  // DeleteOlderThan implements imap.MailFetcher.
  func (f *FakeMailFetcher) DeleteOlderThan(_ context.Context, cutoff time.Time) (int, error) {
      f.DeleteOlderThanCalls = append(f.DeleteOlderThanCalls, cutoff)
      if f.DeleteOlderThanErr != nil {
          return 0, f.DeleteOlderThanErr
      }
      return f.DeleteOlderThanResult, nil
  }

  // SearchOlderThan implements imap.MailFetcher.
  func (f *FakeMailFetcher) SearchOlderThan(_ context.Context, cutoff time.Time) ([]uint32, error) {
      f.SearchOlderThanCalls = append(f.SearchOlderThanCalls, cutoff)
      if f.SearchOlderThanErr != nil {
          return nil, f.SearchOlderThanErr
      }
      return f.SearchOlderThanResult, nil
  }
  ```

#### 変更ファイル: `internal/imap/testutil/mocks_test.go`

- [x] `TestFakeMailFetcherDeleteOlderThan`（新規）を追加する。`TestFakeMailFetcherMarkSeen`（45-58 行目）と同じ形式で、`DeleteOlderThanCalls` に呼び出し時の `cutoff` が記録されること、`DeleteOlderThanResult` / `DeleteOlderThanErr` がそれぞれ返却されることを検証する。
- [x] `TestFakeMailFetcherSearchOlderThan`（新規）を追加する。同様に `SearchOlderThanCalls` への記録と結果・エラーの返却を検証する。

#### 変更ファイル: `internal/imap/client_integration_test.go`

- [x] `TestIntegration_DeleteOlderThan`（新規）を追加する。`loadSMTPTestConfig(t)` を使う（`TestIntegration_FetchMeta`/`_Download`/`_MarkSeen` と同じパターンで、テストごとに一意な受信者アドレス＝一意な INBOX を割り当てるため、greenmail 上の他テストのメールボックスとは独立しており削除操作が他テストに影響しない）。
  1. `injectTestMail` で 1 通のテストメールを送信する（`messageID := testMessageID()`）。
  2. 1つ目のセッション（`client1`、取得直後に `t.Cleanup(func() { _ = client1.Close() })` を登録）で `client1.FetchMeta(ctx, time.Now().AddDate(-1, 0, 0))` を呼び、`TestIntegration_Download` と同じ `normalizeMessageID` 比較で注入したメールの UID（`wantUID`）を取得する（`require.NotZero(t, wantUID)`）。
  3. 同じ `client1` で `DeleteOlderThan(ctx, cutoff)` を呼ぶ（`cutoff = time.Now().AddDate(0, 0, 1)` とすることで、注入したメールの INTERNALDATE（当日）が `truncateToDate(cutoff)`（翌日 00:00）より前になるようにする）。
  4. 2つ目のセッション（`client2`、取得直後に `t.Cleanup(func() { _ = client2.Close() })` を登録）で `client2.SearchOlderThan(ctx, cutoff)` を呼び、結果を `gotUIDs` とする。
  5. 次のいずれかを確認する。
     - `deleted > 0`（UIDPLUS 対応）の場合: `require.NotContains(t, gotUIDs, wantUID)`（削除済み）。
     - `deleted == 0`（UIDPLUS 非対応、AC-12 のフォールバック）の場合: `require.Contains(t, gotUIDs, wantUID)`（削除されていない）。
  6. いずれの場合も `t.Logf("greenmail UIDPLUS support: deleted=%d (>0 means UIDPLUS supported)", deleted)` で結果を記録し、テスト出力から greenmail の UIDPLUS 対応状況を確認できるようにする。
- [x] `TestIntegration_SearchOlderThan_ReadOnly`（新規）を追加する。`loadSMTPTestConfig(t)` で一意な INBOX を割り当て、テストメール注入後、同一セッションで `SearchOlderThan(ctx, cutoff)` を 2 回連続で呼び、両方とも同じ UID 集合を返すこと（EXAMINE のみで状態変更が無く、結果が冪等であること）を検証する。
- [x] 上記 2 テストの実行結果（greenmail が UIDPLUS を広告するか、`deleted` が 0 より大きいか）を、本ファイルの「フェーズ完了の確認」のチェック項目で記録する。

**フェーズ完了の確認**:
- [x] `go doc` での API 確認結果（コンストラクタ・`UidExpunge` の実シグネチャ）、または `go-imap-uidplus` が利用できず§1の `go.mod` フォールバック（`Execute` による直接 `UID EXPUNGE` 送信）に切り替えた旨を本セクションの依存関係タスクに追記済みであること
  - 上記「依存関係の追加と API 確認」セクションに記録済み（`go-imap-uidplus` を利用、フォールバック不要）。
- [x] `make fmt` を実行し差分がないこと
  - 差分なし。
- [x] `make test` が通過すること（`-tags integration` 抜きのユニットテスト）
  - 通過。
- [x] `go test -tags integration ./internal/imap/...` を実行し、greenmail に対する `TestIntegration_DeleteOlderThan` / `TestIntegration_SearchOlderThan_ReadOnly` を含む統合テストが通過すること
  - 通過。
- [x] `make lint` が通過すること
  - 0 issues。
- [x] greenmail の UIDPLUS 対応状況（`TestIntegration_DeleteOlderThan` の `deleted` が 0 より大きいか）を本セクションに記録すること
  - greenmail は UIDPLUS に対応している（`deleted=1`）。AC-12 のフォールバックパス（UIDPLUS 非対応時の警告ログ + 0 件削除）はユニットテスト `TestImapClient_DeleteOlderThan_UIDPLUSUnsupported` でのみ検証されている。

### PR-2 作成ポイント: MailFetcher.DeleteOlderThan / SearchOlderThan (UID EXPUNGE)

**対象ステップ**: Phase 2
**推奨タイトル**: `feat(0110): add MailFetcher.DeleteOlderThan and SearchOlderThan with UID EXPUNGE`
**レビュー観点**:
- `\Deleted` 付与と UID EXPUNGE が検索でヒットした UID のみを対象とし、メールボックス全体への EXPUNGE/CLOSE を発行しないこと（AC-08）。
- `cutoff` がゼロ値の場合に IMAP セッションへ一切アクセスせず `(0, nil)` を返すこと（AC-16）。
- UIDPLUS 非対応時に `\Deleted` を付与せず警告ログのみで `(0, nil)` を返すこと（AC-12）。
- `DeleteOlderThan` の read-write SELECT が `lastSelectReadOnly` を正しく更新し、既存の `Close()` の CLOSE/LOGOUT 切り替えロジック（タスク 0090 のポリシー）を変更せずに機能すること。
- `SearchOlderThan` が EXAMINE のみで状態を変更しないこと。
- `go-imap-uidplus` の実際の API が `go doc` の確認結果と一致していること。

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（#159）
- [x] PR がマージされた
- [x] 次のステップ用のブランチへ切り替えた

---

### Phase 3: internal/store カウントメソッド追加（AC-10）

#### 変更ファイル: `internal/store/store.go`

- [x] `Store` インターフェースの `DeleteEmailsBefore` メソッド定義（末尾）の直後に、以下の 2 メソッドを追加する。`02_architecture.md` §3.4 のドキュメントコメントに準拠する。

  ```go
  // CountReportsBefore returns the number of report records whose
  // date-range.end-datetime < cutoff, without deleting them. The predicate
  // mirrors DeleteReportsBefore exactly. Works on read-only stores.
  CountReportsBefore(cutoff time.Time) (count int, err error)

  // CountEmailsBefore returns the number of .eml files whose internal_date < cutoff,
  // without deleting them. Returns 0, nil immediately if cutoff is zero. The predicate
  // mirrors DeleteEmailsBefore exactly. Works on read-only stores.
  CountEmailsBefore(cutoff time.Time) (count int, err error)
  ```

#### 変更ファイル: `internal/store/reports.go`

- [x] `DeleteReportsBefore`（末尾）の直後に `CountReportsBefore` を実装する。`DeleteReportsBefore` と同じ `loadDataFile` + `EndDatetime.Before(cutoff)` 判定を用いるが、`s.readOnly` チェックを行わず、`df.Reports` を変更しない（カウントのみ）。

  ```go
  // CountReportsBefore implements Store.CountReportsBefore.
  func (s *fileStore) CountReportsBefore(cutoff time.Time) (count int, err error) {
      df, err := s.loadDataFile()
      if err != nil {
          return 0, fmt.Errorf("CountReportsBefore: load data file: %w", err)
      }

      for _, r := range df.Reports {
          if r.DateRange.EndDatetime.Before(cutoff) {
              count++
          }
      }
      return count, nil
  }
  ```

#### 変更ファイル: `internal/store/emails.go`

- [x] `DeleteEmailsBefore`（末尾、`cleanupEmptyDirs` の前）の直後に `CountEmailsBefore` を実装する。`DeleteEmailsBefore` 冒頭のゼロカットオフ早期リターンと `entry.InternalDate.Before(cutoff)` 判定を用いるが、`s.readOnly` チェック・ファイル削除・インデックス更新を行わない（カウントのみ）。

  ```go
  // CountEmailsBefore implements Store.CountEmailsBefore.
  func (s *fileStore) CountEmailsBefore(cutoff time.Time) (count int, err error) {
      if cutoff.IsZero() {
          return 0, nil
      }

      df, err := s.loadDataFile()
      if err != nil {
          return 0, fmt.Errorf("CountEmailsBefore: load data file: %w", err)
      }

      for _, entry := range df.Emails {
          if entry.InternalDate.Before(cutoff) {
              count++
          }
      }
      return count, nil
  }
  ```

#### 変更ファイル: `internal/store/reports_test.go`

- [x] "--- DeleteReportsBefore tests (Phase 3) ---" の節の末尾に、以下の新規テストを追加する。
  - `TestCountReportsBefore_BoundaryValues`: `TestDeleteReportsBefore_BoundaryValues`（304-331 行目）と同じ `before`/`equal`/`after` の3件のレポートを保存し、`s.CountReportsBefore(cutoff)` が `1`（`before` のみ）を返すことを検証する。さらに `s.GetAllReports()` で3件とも残っていること（削除されていないこと）を確認する。
  - `TestCountReportsBefore_ZeroCounted`: レポート未保存の状態で `s.CountReportsBefore(time.Now())` が `(0, nil)` を返すことを検証する。
  - `TestCountReportsBefore_ReadOnly`: `OpenReadWrite` でレポートを1件保存した後ストアを閉じ、同じ `rootDir` を `OpenReadOnly` で開き直し、`CountReportsBefore` が読み取り専用ストアでもエラーなく動作し正しい件数を返すことを検証する（`TestGetAllReports_ReadOnly_Empty`、209-220行目のオープンパターンを参考にする）。

#### 変更ファイル: `internal/store/emails_test.go`

- [x] "DeleteEmailsBefore tests" の節の末尾（`TestDeleteEmailsBefore_DirCleanupWarn` の後）に、以下の新規テストを追加する。
  - `TestCountEmailsBefore_ZeroCutoff`: `TestDeleteEmailsBefore_ZeroCutoff`（466-475行目）と同様に1件 `.eml` を保存し、`s.CountEmailsBefore(time.Time{})` が `(0, nil)` を返すことを検証する。
  - `TestCountEmailsBefore_Conditions`: `TestDeleteEmailsBefore_Conditions`（479-518行目）と同じ `before`/`equal`/`after` の3件の `.eml` を保存し、`s.CountEmailsBefore(cutoff)` が `1`（`before` のみ）を返すことを検証する。さらに3件の `.eml` ファイルすべてが削除されずに残っていること（`assert.FileExists`）と、インデックスに3件とも残っていることを確認する。
  - `TestCountEmailsBefore_ReadOnly`: `OpenReadWrite` で `.eml` を1件保存した後ストアを閉じ、同じ `rootDir` を `OpenReadOnly` で開き直し、`CountEmailsBefore` が読み取り専用ストアでもエラーなく動作し正しい件数を返すことを検証する。

#### 変更ファイル: `internal/store/testutil/mocks.go`

- [x] `DeleteEmailsBefore`（271-286行目）の直後に、以下の2メソッドを追加する。`DeleteReportsBefore`/`DeleteEmailsBefore` の判定ロジックを再利用するが、マップを変更せず、呼び出し回数・カットオフ記録用フィールドは追加しない（読み取り専用カウントのため、`02_architecture.md` の方針通り副作用追跡は不要）。

  ```go
  // CountReportsBefore implements store.Store.
  func (f *FakeStore) CountReportsBefore(cutoff time.Time) (int, error) {
      count := 0
      for _, r := range f.Reports {
          if r.DateRange.EndDatetime.Before(cutoff) {
              count++
          }
      }
      return count, nil
  }

  // CountEmailsBefore implements store.Store.
  func (f *FakeStore) CountEmailsBefore(cutoff time.Time) (int, error) {
      if cutoff.IsZero() {
          return 0, nil
      }
      count := 0
      for _, entry := range f.Emails {
          if entry.InternalDate.Before(cutoff) {
              count++
          }
      }
      return count, nil
  }
  ```

**フェーズ完了の確認**:
- [x] `make fmt` を実行し差分がないこと
- [x] `make test` が通過すること
- [x] `make lint` が通過すること

### PR-3 作成ポイント: Store.CountReportsBefore / CountEmailsBefore

**対象ステップ**: Phase 3
**推奨タイトル**: `feat(0110): add Store.CountReportsBefore and CountEmailsBefore for dry-run preview`
**レビュー観点**:
- `CountReportsBefore` / `CountEmailsBefore` の判定条件が、それぞれ `DeleteReportsBefore` / `DeleteEmailsBefore` と完全に一致していること（境界値: `EndDatetime == cutoff` / `InternalDate == cutoff` は数えない）。
- 呼び出し後にレポート・`.eml` ファイル・インデックスのいずれも変更されていないこと。
- 読み取り専用（`OpenReadOnly`）で開いたストアでもエラーなく動作すること。
- `CountEmailsBefore` がゼロカットオフで `(0, nil)` を即座に返すこと。

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（#160）
- [x] PR がマージされた
- [x] 次のステップ用のブランチへ切り替えた

---

### Phase 4: gc サブコマンド統合（AC-03, AC-07, AC-09〜AC-11, AC-13, AC-14）

#### 変更ファイル: `cmd/tlsrpt-digest/gc.go`

- [x] `gcRunner` 構造体に `fetchRunner`（`fetch.go` 36-42行目）と同じパターンで `newMailFetcher` と `credentials` フィールドを追加する。

  ```go
  // gcRunner implements SubcommandRunner for the gc subcommand.
  type gcRunner struct {
      now            func() time.Time
      newMailFetcher func(cfg imap.Config) (imap.MailFetcher, error)
      credentials    func() (username string, password config.Secret)
  }
  ```

- [x] `newGCRunner` を以下のように変更する。

  ```go
  func newGCRunner() *gcRunner {
      return &gcRunner{
          now:            time.Now,
          newMailFetcher: imap.NewIMAPClient,
          credentials: func() (string, config.Secret) {
              return os.Getenv("TLSRPT_IMAP_USERNAME"), config.Secret(os.Getenv("TLSRPT_IMAP_PASSWORD"))
          },
      }
  }
  ```

- [x] `Run` を、`02_architecture.md` §6.1 のフロー（recovery チェック → IMAP 認証情報チェック（非 dry-run かつ `retention_days > 0` のときのみ）→ dry-run 分岐）に従って書き換える。Step 1（recovery チェック）は変更しない。Step 2 以降を以下のように置き換える。

  ```go
  // Run executes the gc subcommand: delete (or, in dry-run, count) old report
  // records, .eml files, and IMAP messages.
  func (r *gcRunner) Run(ctx context.Context, boot *BootContext) (int, error) {
      now := r.now()
      mailbox := mailboxID(boot.Config)

      // Step 1: Fail closed if recovery is required.
      _, _, _, recoveryFound, err := boot.Store.LoadRecoveryRequired()
      if err != nil {
          slog.Error("gc: load recovery-required", "error", err)
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
          return exitError, fmt.Errorf("gc: load recovery-required: %w", err)
      }
      if recoveryFound {
          slog.Error("gc: recovery required; run tlsrpt-digest --config <path> recover to resolve")
          return exitError, nil
      }

      reportCutoff := gcReportCutoff(boot.Options, boot.Config, now)
      emailCutoff := gcEmailCutoff(boot.Options, boot.Config, now)
      imapEnabled := boot.Config.IMAP.RetentionDays > 0
      var imapCutoff time.Time
      if imapEnabled {
          imapCutoff = Duration{Days: boot.Config.IMAP.RetentionDays}.Cutoff(now)
      }

      // Step 2: IMAP credentials are required only for non-dry-run IMAP deletion (AC-11).
      var creds IMAPCredentials
      if imapEnabled && !boot.Options.DryRun {
          username, password := r.credentials()
          if username == "" || string(password) == "" {
              logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPCredentialsMissing, mailbox))
              return exitError, nil
          }
          creds = IMAPCredentials{Username: username, Password: password}
      }

      if boot.Options.DryRun {
          return r.runDryRun(ctx, boot, mailbox, reportCutoff, emailCutoff, imapCutoff, imapEnabled)
      }
      return r.runDelete(ctx, boot, mailbox, reportCutoff, emailCutoff, imapCutoff, imapEnabled, creds)
  }

  // runDelete performs the non-dry-run gc flow: delete local report records and
  // .eml files, then (if enabled) delete old IMAP messages, and log combined counts.
  func (r *gcRunner) runDelete(ctx context.Context, boot *BootContext, mailbox string, reportCutoff, emailCutoff, imapCutoff time.Time, imapEnabled bool, creds IMAPCredentials) (int, error) {
      reportDeleted, err := boot.Store.DeleteReportsBefore(reportCutoff)
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
          return exitError, fmt.Errorf("gc: delete reports: %w", err)
      }

      emailDeleted, err := boot.Store.DeleteEmailsBefore(emailCutoff)
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
          return exitError, fmt.Errorf("gc: delete emails: %w", err)
      }

      var imapDeleted int
      if imapEnabled {
          imapDeleted, err = r.deleteIMAPOlderThan(ctx, boot, mailbox, creds, imapCutoff)
          if err != nil {
              // Local deletions already completed; log their counts before returning
              // the error so they are not lost (AC-14).
              slog.Info("gc: deleted records", "reports", reportDeleted, "emails", emailDeleted, "imap_messages", 0)
              return exitError, err
          }
      }

      slog.Info("gc: deleted records", "reports", reportDeleted, "emails", emailDeleted, "imap_messages", imapDeleted)
      return exitOK, nil
  }

  // deleteIMAPOlderThan connects to IMAP and deletes messages older than cutoff (AC-07, AC-08).
  func (r *gcRunner) deleteIMAPOlderThan(ctx context.Context, boot *BootContext, mailbox string, creds IMAPCredentials, cutoff time.Time) (int, error) {
      fetcher, err := r.newMailFetcher(buildIMAPConfig(boot.Config, creds))
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, classifyIMAPClientError(err), mailbox))
          return 0, fmt.Errorf("gc: create imap client: %w", err)
      }
      defer func() {
          if closeErr := fetcher.Close(); closeErr != nil {
              slog.Error("gc: close imap client", "error", closeErr)
          }
      }()

      deleted, err := fetcher.DeleteOlderThan(ctx, cutoff)
      if err != nil {
          // AC-13: IMAP operation errors (including ErrMailboxReadOnly) are classified
          // separately from local store errors (gcNotifyKind), so they are never
          // misreported as SystemErrorKindStorePermission.
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
          return 0, fmt.Errorf("gc: delete imap messages: %w", err)
      }
      return deleted, nil
  }

  // runDryRun performs the dry-run gc flow: count local deletion candidates and,
  // if IMAP retention is enabled, preview IMAP deletion candidates via SearchOlderThan.
  // No records, files, or IMAP messages are deleted.
  func (r *gcRunner) runDryRun(ctx context.Context, boot *BootContext, mailbox string, reportCutoff, emailCutoff, imapCutoff time.Time, imapEnabled bool) (int, error) {
      reportCount, err := boot.Store.CountReportsBefore(reportCutoff)
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
          return exitError, fmt.Errorf("gc: count reports: %w", err)
      }

      emailCount, err := boot.Store.CountEmailsBefore(emailCutoff)
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
          return exitError, fmt.Errorf("gc: count emails: %w", err)
      }

      // Log local counts before the IMAP preview so they are not lost if the
      // IMAP search below fails.
      logGCDryRunLocalSummary(reportCutoff, emailCutoff, reportCount, emailCount)

      if imapEnabled {
          username, password := r.credentials()
          if username == "" || string(password) == "" {
              // Missing credentials are only an error for non-dry-run deletion;
              // in dry-run, they only disable the IMAP preview.
              slog.Warn("gc: dry-run: imap credentials missing; skipping imap deletion preview", "mailbox", mailbox)
              logGCDryRunIMAPSummary(nil)
          } else {
              imapUIDs, err := r.searchIMAPOlderThan(ctx, boot, mailbox, IMAPCredentials{Username: username, Password: password}, imapCutoff)
              if err != nil {
                  return exitError, err
              }
              logGCDryRunIMAPSummary(imapUIDs)
          }
      }

      if err := boot.Notifier.Flush(ctx); err != nil {
          slog.Warn("gc: dry-run flush notifications", "error", err)
      }
      return exitOK, nil
  }

  // searchIMAPOlderThan connects to IMAP and previews messages older than cutoff via
  // SearchOlderThan (read-only).
  func (r *gcRunner) searchIMAPOlderThan(ctx context.Context, boot *BootContext, mailbox string, creds IMAPCredentials, cutoff time.Time) ([]uint32, error) {
      fetcher, err := r.newMailFetcher(buildIMAPConfig(boot.Config, creds))
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, classifyIMAPClientError(err), mailbox))
          return nil, fmt.Errorf("gc: create imap client: %w", err)
      }
      defer func() {
          if closeErr := fetcher.Close(); closeErr != nil {
              slog.Error("gc: close imap client", "error", closeErr)
          }
      }()

      uids, err := fetcher.SearchOlderThan(ctx, cutoff)
      if err != nil {
          logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
          return nil, fmt.Errorf("gc: search imap messages: %w", err)
      }
      return uids, nil
  }

  // logGCDryRunLocalSummary logs what local report records and .eml files would
  // have been deleted in a real (non-dry) run, including the cutoff times used
  // for each candidate set. It is logged before any IMAP preview so the local
  // counts are not lost if the IMAP step fails (architecture §3.6).
  func logGCDryRunLocalSummary(reportCutoff, emailCutoff time.Time, reportCount, emailCount int) {
      slog.Info("gc: dry-run: local deletion candidates; no records or files deleted",
          "would_delete_reports", reportCount,
          "report_cutoff", reportCutoff,
          "would_delete_emails", emailCount,
          "email_cutoff", emailCutoff)
  }

  // logGCDryRunIMAPSummary logs the IMAP messages that would have been deleted
  // in a real (non-dry) run. imapUIDs is nil when credentials were unavailable.
  func logGCDryRunIMAPSummary(imapUIDs []uint32) {
      sample := imapUIDs
      truncated := false
      if len(sample) > dryRunUIDSampleMax {
          sample = sample[:dryRunUIDSampleMax]
          truncated = true
      }
      slog.Info("gc: dry-run: imap deletion candidates; no messages deleted",
          "would_delete_imap_count", len(imapUIDs),
          "would_delete_imap_uids_sample", sample,
          "would_delete_imap_uids_truncated", truncated)
  }
  ```

  `dryRunUIDSampleMax`（`fetch.go` 375行目）はパッケージ内の既存定数をそのまま再利用する（重複定義しない）。

- [x] `import` ブロックに `"os"` と `"github.com/isseis/tlsrpt-digest/internal/imap"` を追加する。

- [x] `gcReportCutoff` / `gcEmailCutoff` / `gcNotifyKind` / `notifyGCSystemError` は変更しない。

#### 変更ファイル: `cmd/tlsrpt-digest/main.go`

- [x] `validateFlags` 内の dry-run 対応サブコマンドのチェックを以下のように変更する（AC-10 の前提）。

  変更前:
  ```go
  if opts.DryRun && subcmd != subcommandFetch && subcmd != subcommandSummary {
      return errDryRunNotSupported
  }
  ```

  変更後:
  ```go
  if opts.DryRun && subcmd != subcommandFetch && subcmd != subcommandSummary && subcmd != subcommandGC {
      return errDryRunNotSupported
  }
  ```

- [x] `printDetailedHelp` の `Subcommands:` 一覧（236行目）にある `gc` の一行説明を、IMAP メッセージ削除にも触れるように変更する。

  変更前:
  ```
    gc          Delete report data older than the retention period
  ```

  変更後:
  ```
    gc          Delete report data, .eml files, and (if imap.retention_days > 0) old IMAP messages
  ```

- [x] `printDetailedHelp` の `gc:` セクションに `-n, --dry-run` の説明行を追加する。

  変更前:
  ```
    gc:
      --before <duration>          Override report retention duration (default: retention_days in config)
      --max-email-age <duration>   Override .eml file retention duration (default: max_email_age_days in config)
  ```

  変更後:
  ```
    gc:
      -n, --dry-run                 Preview deletions without modifying the local store or IMAP mailbox (read-only store, log Slack payload instead of sending it)
      --before <duration>           Override report retention duration (default: retention_days in config)
      --max-email-age <duration>    Override .eml file retention duration (default: max_email_age_days in config)
  ```

#### 変更ファイル: `cmd/tlsrpt-digest/main_test.go`

- [x] `TestParseCLI_DryRunSupportedSubcommands`（155-163行目）のループ対象に `subcommandGC` を追加する: `[]SubcommandName{subcommandFetch, subcommandSummary, subcommandGC}`。
- [x] `TestParseCLI_DryRunUnsupportedSubcommands`（165-172行目）のループ対象から `subcommandGC` を除去する: `[]SubcommandName{subcommandReprocess, subcommandRecover}`。
- [x] `TestRunCLI_DryRunUnsupportedSubcommandExits2`（174-181行目）のループ対象から `subcommandGC` を除去する: `[]SubcommandName{subcommandReprocess, subcommandRecover}`。

#### 変更ファイル: `cmd/tlsrpt-digest/gc_test.go`

- [x] `import` ブロックに `imaptestutil "github.com/isseis/tlsrpt-digest/internal/imap/testutil"` と `"github.com/isseis/tlsrpt-digest/internal/imap"` を追加する。
- [x] 以下の新規テストを追加する。`makeGCBoot` の第5引数 `cfg` には、`cfg.IMAP.RetentionDays` を設定した `*config.Config` を渡す（`cfg.IMAP.Host`/`Port`/`Mailbox` は既存の `makeGCBoot` デフォルトと同じ値を設定する）。
  - `TestGC_IMAPRetentionDisabled_NoIMAPConnection`（AC-09）: `cfg.IMAP.RetentionDays = 0`（デフォルト）のまま、`runner.newMailFetcher` に呼び出された場合は `t.Fatal` するクロージャを設定する。`runner.Run` が `exitOK` を返し、`newMailFetcher` が呼ばれないこと（IMAP 未接続でローカル GC のみ実行されること）を確認する。
  - `TestGC_IMAPRetentionEnabled_DeletesOlderThan`（AC-07, AC-08連携）: `cfg.IMAP.RetentionDays = 30`、`now = time.Date(2026, 1, 15, ...)` とする。`fakeFetcher := &imaptestutil.FakeMailFetcher{DeleteOlderThanResult: 2}` を用意し、`runner.newMailFetcher = func(imap.Config) (imap.MailFetcher, error) { return fakeFetcher, nil }`、`runner.credentials = func() (string, config.Secret) { return "user", config.Secret("pass") }` を設定する。`runner.Run` が `exitOK` を返し、`fakeFetcher.DeleteOlderThanCalls` に `Duration{Days: 30}.Cutoff(now)` と一致する1件のカットオフが記録されていることを検証する。
  - `TestGC_IMAPCredentialsMissing_NotifiesAndExits`（AC-11）: `cfg.IMAP.RetentionDays = 30`、`runner.credentials = func() (string, config.Secret) { return "", "" }` を設定する（`runner.newMailFetcher` は呼ばれた場合 `t.Fatal` するクロージャとする）。`runner.Run` が `exitError, nil` を返し、`spy.SystemErrors` に `notify.SystemErrorKindIMAPCredentialsMissing` が1件記録されていること、`st.DeleteReportsBeforeCallCount == 0` かつ `st.DeleteEmailsBeforeCallCount == 0`（ローカル削除より先にエラー終了すること）を検証する。
  - `TestGC_IMAPOperationError_Notifies`（AC-13）: `cfg.IMAP.RetentionDays = 30`、`fakeFetcher := &imaptestutil.FakeMailFetcher{DeleteOlderThanErr: errors.New("imap error")}` を設定する。ローカルストアには `TestGC_DeleteCountLog` と同様にレポート1件・メール1件を保存しておく。`runner.Run` が `exitError` とエラーを返し、`spy.SystemErrors` に `notify.SystemErrorKindIMAPOperationFailed`（`notify.SystemErrorKindStorePermission` ではないこと）が1件記録されていること、ログに `"reports=1"` と `"emails=1"`（IMAP エラー前に完了したローカル削除件数、AC-14）が含まれることを検証する（`captureSlog(t)` を使用）。
  - `TestGC_DryRun_NoDeletions`（AC-10）: `cfg.IMAP.RetentionDays = 0`、`opts := cliOptions{DryRun: true}` とし、`TestGC_DeleteCountLog` と同様にレポート1件・メール1件を保存する。`captureSlog(t)` でログを捕捉し、`runner.Run` が `exitOK` を返し、`st.DeleteReportsBeforeCallCount == 0` かつ `st.DeleteEmailsBeforeCallCount == 0`（削除が実行されないこと）、ログに `"would_delete_reports=1"` と `"would_delete_emails=1"` が含まれることを検証する。
  - `TestGC_DryRun_IMAPRetentionEnabled_PreviewsCandidates`（AC-10）: `cfg.IMAP.RetentionDays = 30`、`opts := cliOptions{DryRun: true}` とする。`fakeFetcher := &imaptestutil.FakeMailFetcher{SearchOlderThanResult: []uint32{10, 20}}` を設定し、`runner.credentials` には有効な認証情報を返すクロージャを設定する。`captureSlog(t)` でログを捕捉し、`runner.Run` が `exitOK` を返し、`fakeFetcher.SearchOlderThanCalls` に `Duration{Days: 30}.Cutoff(now)` と一致する1件のカットオフが記録されていること、`len(fakeFetcher.DeleteOlderThanCalls) == 0`（削除メソッドが呼ばれないこと）、ログに `"would_delete_imap_count=2"` が含まれることを検証する。
  - `TestGC_DryRun_IMAPCredentialsMissing_WarnsAndContinues`（AC-10, AC-11の dry-run 適用外確認）: `cfg.IMAP.RetentionDays = 30`、`opts := cliOptions{DryRun: true}`、`runner.credentials = func() (string, config.Secret) { return "", "" }`（`runner.newMailFetcher` は呼ばれた場合 `t.Fatal` するクロージャとする）。`captureSlog(t)` でログを捕捉し、`runner.Run` が `exitOK` を返し、`spy.SystemErrors` が空であること（AC-11 は dry-run に適用されないこと）、ログに `level=WARN` と `"imap credentials missing"` を含む行が出力されること、ログに `"would_delete_imap_count=0"` が含まれることを検証する。

**フェーズ完了の確認**:
- [x] `make fmt` を実行し差分がないこと
- [x] `make test` が通過すること
- [x] `make lint` が通過すること

### PR-4 作成ポイント: gc subcommand IMAP retention integration + --dry-run

**対象ステップ**: Phase 4
**推奨タイトル**: `feat(0110): integrate IMAP message retention into gc subcommand`
**レビュー観点**:
- `imap.retention_days = 0` のとき `newMailFetcher` が一切呼ばれず、IMAP 接続なしでローカル GC のみが実行されること（AC-09）。
- `imap.retention_days > 0` かつ非 dry-run のとき、正しいカットオフ（`Duration{Days: cfg.IMAP.RetentionDays}.Cutoff(now)`）で `DeleteOlderThan` が呼ばれること（AC-07）。
- IMAP 認証情報欠落時、非 dry-run では `SystemErrorKindIMAPCredentialsMissing` を通知してローカル削除前にエラー終了すること（AC-11）。dry-run では警告ログのみで継続し、ローカルカウントを表示して正常終了すること。
- IMAP 操作エラーが `SystemErrorKindIMAPOperationFailed` に分類され、`gcNotifyKind` の `SystemErrorKindStorePermission` に巻き込まれないこと（AC-13）。IMAP エラー時もローカル削除件数が先にログ出力されること（AC-14）。
- dry-run でローカル削除・IMAP 削除のいずれも実行されず、件数のみがログ出力されること（AC-10）。
- `gc --dry-run` が `main.go` の許可リストに追加され、`--dry-run` 未対応サブコマンド一覧（reprocess・recover）からは除外されたままであること。

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のステップ用のブランチへ切り替えた

---

### Phase 5: ドキュメント更新と Gmail 実環境検証

`02_architecture.md` §8 の Phase 5 完了条件（README に4点を記載し、実 Gmail アカウントでの動作確認結果を実装計画に記録すること）を満たす。AC との対応はないが、Phase 1〜4 で実装した機能をユーザーが安全に有効化できるようにするために必要な作業である。

#### 変更ファイル: `README.ja.md`（先に編集する）

- [ ] 「全設定項目」コードブロック（98-143行目）の `[imap]` セクションに `retention_days` の項目を追加する。`max_message_bytes` の項目（120-121行目）の直後に以下を追加する。

  ```toml

  # IMAP メールボックス上のメール保持期間（日数）（省略時: 0 = 無効）
  # 0 より大きい値を設定すると、gc 実行時に INTERNALDATE（日付截断）が
  # (今日 - retention_days) より古い IMAP メールを削除する（不可逆操作）。
  # 有効化する場合は、imap.fetch_days と summary.window_days の
  # いずれと比べても大きいか等しい値にすること（設定エラーで起動を拒否する）。
  retention_days = 0
  ```

- [ ] 「全設定項目」コードブロックの直後（144行目の ```` ``` ```` の後）に、新しいサブセクション「### IMAP メールボックスの保持期間（`imap.retention_days`）について」を追加し、以下の内容を含める。
  - `retention_days` のデフォルトは `0`（無効）であり、利用者が明示的に正の値を設定したときのみ IMAP 上のメール削除が有効化されること（オプトイン）。
  - 有効化する（`retention_days > 0` にする）と、`gc` の実行に IMAP 認証情報（`TLSRPT_IMAP_USERNAME` / `TLSRPT_IMAP_PASSWORD`）が必須になること。`retention_days = 0`（デフォルト）のままであれば `gc` は IMAP に接続せず、認証情報も不要であること。
  - IMAP からのメール削除は不可逆操作であり、削除対象を TLSRPT レポートに限定する絞り込みは行われないこと。そのため、TLSRPT レポート受信専用のメールボックスを使用することを推奨し、有効化する前に `gc --dry-run` で削除候補件数を確認することを推奨する。
  - Gmail を使用する場合の前提条件: Gmail の IMAP 設定（「メール転送と POP/IMAP」タブ）で、メッセージが `\Deleted` 付与・EXPUNGE されたときの挙動がデフォルトの「メールをアーカイブする」のままだと、UID EXPUNGE はラベル除去（全メールへの移動）となりストレージは解放されないこと。サーバー上の蓄積を実際に抑止するには、「完全に削除する」または「ゴミ箱に移動」（+ ゴミ箱メールの自動完全削除）に変更する必要があること。
  - `imap.fetch_days`（および `fetch --since` で指定する取得期間）は `imap.retention_days` 以下にすること。それより古いメールは `gc` によって IMAP 上から削除され、以後 `fetch` で取得できなくなること。

- [ ] `### gc` サブコマンドの説明（202-208行目）を以下のように変更する。

  変更前:
  ```
  ### gc

  保持期間を超えた古いデータを削除します。

  ```bash
  tlsrpt-digest --config path gc [--before duration] [--max-email-age duration]
  ```
  ```

  変更後:
  ```
  ### gc

  保持期間を超えた古いデータを削除します。`imap.retention_days` を設定している場合は、IMAP メールボックス上の古いメールも削除します（不可逆操作。詳細は[IMAP メールボックスの保持期間について](#imap-メールボックスの保持期間imapretention_daysについて)を参照）。

  ```bash
  tlsrpt-digest --config path gc [--dry-run] [--before duration] [--max-email-age duration]
  ```

  | オプション | 説明 |
  |---|---|
  | `--dry-run` | ローカルストア・IMAP メールボックスとも削除を行わず、削除対象の件数（IMAP は対象 UID のサンプルも含む）をログ出力する |
  | `--before duration` | レポート JSON データの保持期間の上書き（デフォルト: config の `store.retention_days`） |
  | `--max-email-age duration` | `.eml` ファイルの保持期間の上書き（デフォルト: config の `store.max_email_age_days`） |
  ```

  追加するセクション見出しが Markdown のアンカー `#imap-メールボックスの保持期間imapretention_daysについて` と一致することを確認する（GitHub の見出しアンカー生成規則: 小文字化し、`` ` `` や `(` `)` などの記号を除去し、空白を `-` に置換する）。

#### 変更ファイル: `README.md`（`/mktrans` で反映する）

- [ ] `README.ja.md` の編集が完了したら `/mktrans` を実行し、`README.md` の対応箇所（「All Configuration Items」コードブロックの `[imap]` セクション、新規セクション「IMAP Mailbox Retention (`imap.retention_days`)」、`### gc` サブコマンドの説明とオプション表）に翻訳を反映する。
- [ ] 翻訳後、`README.md` の新規セクションが `01_requirements.md` の用語（`retention_days`、`SystemErrorKindIMAPCredentialsMissing` 等）および `02_architecture.md` §3.3・§5.1・§5.3 の説明内容と整合していることを確認する。

#### 変更ファイル: `docs/translation_glossary.md`

- [ ] `/mktrans` の実行前に、新規追加する日本語の専門用語のうち未登録のものを確認する。`rg -n "保持期間|オプトイン|不可逆|Auto-Expunge|完全に削除する|ゴミ箱に移動|メールをアーカイブする" docs/translation_glossary.md` を実行し、該当行が存在しない用語について、対応する英訳（例: 保持期間 → retention period、オプトイン → opt-in、不可逆 → irreversible、完全に削除する → "Immediately delete the message forever"（Gmail UI 表記）、ゴミ箱に移動 → "Move the message to the Trash"（Gmail UI 表記）、メールをアーカイブする → "Archive the message"（Gmail UI 表記）、Auto-Expunge → Auto-Expunge）をアルファベット順の該当セクションに追加する。

#### 実環境での手動検証

- [x] 実アカウント（TLSRPT レポート受信用に使用しているテストメールボックス）に対して、以下の手順で手動検証を行い、結果を本ドキュメントの「Phase 5 実施結果」（後述）に追記する。

  > **注記**: 検証実施時点の開発環境では Gmail アカウントが利用できなかったため、Fastmail の実アカウント（`config.toml` の `imap.fastmail.com` / `tls-reports`）に対して機能検証（UIDPLUS 対応・dry-run 件数・実削除・メールボックスからの削除確認）を行った。Gmail 固有の「メールをアーカイブする」/「完全に削除する」/「ゴミ箱に移動」設定とストレージ解放挙動（README に記載）は、Gmail の公開 IMAP 設定 UI の仕様に基づく記述であり、本検証では実機確認していない。

  1. ~~`tlsrpt-digest --config <path> fetch` 実行時の IMAP `CAPABILITY` 応答に `UIDPLUS` が含まれることを、`internal/imap/client.go` のログ（または一時的なデバッグログ）で確認し、応答内容を記録する。~~ → コード変更不要の代替手順として、`openssl s_client` で IMAP サーバに直接接続し、ログイン後の `CAPABILITY` 応答を確認した（読み取り専用、LOGOUT のみ）。
  2. `config.toml` の `imap.retention_days` を、`fetch_days`/`window_days` の制約を満たす最小値である `14` に設定し、`gc --dry-run` を実行した。
  3. 同じ設定で `gc`（非 dry-run）を実行した。
  4. IMAP `SEARCH ALL`（EXAMINE、読み取り専用）および Fastmail の Web UI で、該当メールがメールボックスから削除されていることを確認した。
  5. ~~Gmail の IMAP 設定（「メール転送と POP/IMAP」）がデフォルト（「メールをアーカイブする」）の場合と、「完全に削除する」に変更した場合とで、削除後のストレージ使用量（Google アカウントのストレージ容量表示）に変化があるかを比較する。~~ → Fastmail には Gmail と同様の「アーカイブ vs 完全削除」設定がなく、本検証アカウントでは非該当。Gmail での挙動は README 記載のとおり Gmail 公式設定 UI の説明に基づく。
  6. 上記 1〜5 の結果を、本ドキュメントの「Phase 5 実施結果」セクションに記録した。

#### Phase 5 実施結果

- [x] 上記の手動検証完了後、以下のテンプレートに沿って結果を本セクションに追記する。

  ```
  - 検証日: 2026-06-11
  - 検証アカウント: Fastmail の実アカウント（imap.fastmail.com、メールボックス tls-reports。Gmail アカウントは未使用）
  - CAPABILITY 応答の UIDPLUS 有無: あり（ログイン後の CAPABILITY 応答に UIDPLUS を含むことを openssl s_client で確認）
  - gc --dry-run の would_delete_imap_count: 18（imap.retention_days=14 設定時。would_delete_imap_uids_sample に対象 18 UID を出力、would_delete_imap_uids_truncated=false）
  - gc（非 dry-run）の imap_messages 削除件数: 18（reports=2, emails=1 もあわせて削除）
  - メールボックス上での削除確認結果: IMAP SEARCH ALL（EXAMINE）の EXISTS が 31 → 13 に減少し、対象 18 件が削除されたことを確認。Fastmail Web UI でも tls-reports フォルダから該当メールが削除されていることを確認した。
  - デフォルト設定（メールをアーカイブする）でのストレージ解放有無: 非該当（Fastmail には Gmail のような「アーカイブ」設定が無いため未検証。Gmail での挙動は README に記載の Gmail 公式 IMAP 設定 UI の説明に基づく）
  - 「完全に削除する」設定変更後のストレージ解放有無: 非該当（同上の理由により未検証）
  - README への反映が必要な追加事項（あれば）: なし（Gmail 固有の Auto-Expunge 前提条件は README に記載済みであり、本検証はそれ以外の機能（UIDPLUS 検出・dry-run 件数・対象限定削除・削除確認）が実アカウントで動作することを確認するもの）
  ```

**フェーズ完了の確認**:
- [x] `make test` と `make lint` がグリーンであること（ドキュメントのみの変更でも回帰がないことを確認する）
- [x] README.ja.md と README.md の両方で、追加したセクションの Markdown リンク・コードブロックの構文が正しいこと（プレビューで確認する）

### PR-5 作成ポイント: README updates for IMAP retention + Gmail verification notes

**対象ステップ**: Phase 5
**推奨タイトル**: `docs(0110): document imap.retention_days opt-in and Gmail Auto-Expunge prerequisites`
**レビュー観点**:
- README.ja.md → README.md の順で編集され、内容が一致していること（`/mktrans` の翻訳ガイドライン準拠）。
- `imap.retention_days = 0`（デフォルト）が無効であり、有効化はオプトインであることが明記されていること。
- 有効化時に IMAP 認証情報が必須になること、TLSRPT 専用メールボックスの推奨、`gc --dry-run` での事前確認の推奨が記載されていること。
- Gmail の Auto-Expunge / 「完全に削除する」設定が前提条件であることが記載されていること。
- `fetch_days` / `--since` を `retention_days` 以下にする注意が記載されていること。
- Phase 5 実施結果セクションに実 Gmail アカウントでの検証結果が記録されていること。

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のステップ用のブランチへ切り替えた

---

## 3. 実装順序とマイルストーン

### 3.1 マイルストーン

| マイルストーン | フェーズ | 内容 | 成果物 |
|---|---|---|---|
| M1 | Phase 1 | `imap.retention_days` の config 拡張（型・デフォルト・バリデーション・エラー型） | AC-01〜AC-06 をカバーする `internal/config` のテスト |
| M2 | Phase 2 | `internal/imap` 拡張（`go-imap-uidplus` 依存追加・`DeleteOlderThan` / `SearchOlderThan` 実装・`FakeMailFetcher` 更新） | AC-08, AC-12, AC-15〜AC-17 をカバーするテストと、greenmail の UIDPLUS 対応状況の記録 |
| M3 | Phase 3 | `internal/store` の `CountReportsBefore` / `CountEmailsBefore` 追加 | AC-10 のストア側テスト |
| M4 | Phase 4 | `gc` サブコマンドへの IMAP 削除統合・`--dry-run` 対応 | AC-03, AC-07, AC-09〜AC-11, AC-13, AC-14 をカバーするテスト |
| M5 | Phase 5 | README 更新（オプトイン手順・専用メールボックス推奨・Gmail 設定前提・`--since` 注意）と実 Gmail アカウントでの手動検証 | 更新された `README.ja.md` / `README.md` と Phase 5 実施結果の記録 |

Phase 1〜3 は互いに独立しており並行可能である（`02_architecture.md` §8）。Phase 4 は Phase 1〜3 すべてに依存する（config の `RetentionDays`、`MailFetcher.DeleteOlderThan`/`SearchOlderThan`、`Store.CountReportsBefore`/`CountEmailsBefore` をいずれも利用するため）。Phase 5 は Phase 4 で追加される `gc --dry-run` の挙動を README に記載するため、Phase 4 の完了後に実施する。

### 3.2 PR 構成

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | Phase 1 | `IMAPConfig.RetentionDays` フィールド追加・デフォルト `0`・`ErrInvalidIMAPRetentionDays` / `ErrIMAPRetentionTooShort` によるバリデーション・単体テスト追加（AC-01〜AC-06） |
| PR-2 | Phase 2 | `go-imap-uidplus` 依存追加・`MailFetcher.DeleteOlderThan` / `SearchOlderThan` 実装・`fakeSession` / `FakeMailFetcher` 拡張・単体テストと greenmail 統合テスト追加（AC-08, AC-12, AC-15〜AC-17） |
| PR-3 | Phase 3 | `Store.CountReportsBefore` / `CountEmailsBefore` 追加・`FakeStore` 拡張・単体テスト追加（AC-10 の前提） |
| PR-4 | Phase 4 | `gc` サブコマンドへの IMAP 削除統合・`--dry-run` 対応・`main.go` の許可リスト更新・単体テスト追加（AC-03, AC-07, AC-09〜AC-11, AC-13, AC-14） |
| PR-5 | Phase 5 | README（日本語・英語）への `imap.retention_days` 設定例・運用上の注意・Gmail 前提条件の追記、Gmail 実環境での手動検証結果の記録 |

---

## 4. テスト戦略

### 4.1 単体テスト

`make test`（`-tags test`）で実行する。`02_architecture.md` §7.1 に対応する。

| テスト | ファイル | 検証 AC |
|---|---|---|
| `TestLoad_AllFields`（更新） | `internal/config/config_test.go` | AC-01 |
| `TestLoad_Default_IMAPRetentionDays0`（新規） | `internal/config/config_test.go` | AC-02 |
| `TestLoad_IMAPRetentionDaysValidation`（新規） | `internal/config/config_test.go` | AC-03, AC-04, AC-06 |
| `TestLoad_IMAPRetentionTooShort`（新規） | `internal/config/config_test.go` | AC-05, AC-06 |
| `TestImapClient_DeleteOlderThan_ZeroCutoff`（新規） | `internal/imap/client_test.go` | AC-16 |
| `TestImapClient_DeleteOlderThan_UIDPLUSUnsupported`（新規） | `internal/imap/client_test.go` | AC-12 |
| `TestImapClient_DeleteOlderThan_Success`（新規） | `internal/imap/client_test.go` | AC-08, AC-15 |
| `TestImapClient_DeleteOlderThan_EmptySearch`（新規） | `internal/imap/client_test.go` | AC-15 |
| `TestImapClient_DeleteOlderThan_ReadOnly`（新規） | `internal/imap/client_test.go` | AC-15 |
| `TestImapClient_DeleteOlderThan_SupportError`（新規） | `internal/imap/client_test.go` | AC-15 |
| `TestImapClient_SearchOlderThan_ZeroCutoff`（新規） | `internal/imap/client_test.go` | AC-16 |
| `TestImapClient_SearchOlderThan_UsesExamine`（新規） | `internal/imap/client_test.go` | AC-15 |
| `TestFakeMailFetcherDeleteOlderThan`（新規） | `internal/imap/testutil/mocks_test.go` | AC-17 |
| `TestFakeMailFetcherSearchOlderThan`（新規） | `internal/imap/testutil/mocks_test.go` | AC-17 |
| `TestCountReportsBefore_BoundaryValues`（新規） | `internal/store/reports_test.go` | AC-10 |
| `TestCountReportsBefore_ZeroCounted`（新規） | `internal/store/reports_test.go` | AC-10 |
| `TestCountReportsBefore_ReadOnly`（新規） | `internal/store/reports_test.go` | AC-10 |
| `TestCountEmailsBefore_ZeroCutoff`（新規） | `internal/store/emails_test.go` | AC-10 |
| `TestCountEmailsBefore_Conditions`（新規） | `internal/store/emails_test.go` | AC-10 |
| `TestCountEmailsBefore_ReadOnly`（新規） | `internal/store/emails_test.go` | AC-10 |
| `TestGC_IMAPRetentionDisabled_NoIMAPConnection`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-09 |
| `TestGC_IMAPRetentionEnabled_DeletesOlderThan`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-07, AC-08 |
| `TestGC_IMAPCredentialsMissing_NotifiesAndExits`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-11 |
| `TestGC_IMAPOperationError_Notifies`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-13, AC-14 |
| `TestGC_DryRun_NoDeletions`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-10 |
| `TestGC_DryRun_IMAPRetentionEnabled_PreviewsCandidates`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-10 |
| `TestGC_DryRun_IMAPCredentialsMissing_WarnsAndContinues`（新規） | `cmd/tlsrpt-digest/gc_test.go` | AC-10, AC-11 |
| `TestParseCLI_DryRunSupportedSubcommands`（更新） | `cmd/tlsrpt-digest/main_test.go` | AC-10 |
| `TestParseCLI_DryRunUnsupportedSubcommands`（更新） | `cmd/tlsrpt-digest/main_test.go` | AC-10 |
| `TestRunCLI_DryRunUnsupportedSubcommandExits2`（更新） | `cmd/tlsrpt-digest/main_test.go` | AC-10 |

既存の18件の `gc_test.go` テスト（`TestGC_RecoveryRequiredStops` 等）は、`makeGCBoot` のデフォルト `cfg.IMAP.RetentionDays == 0` により新規の IMAP 削除パスを通らないため変更不要である（Phase 4 の調査結果を参照）。

### 4.2 統合テスト

`go test -tags integration ./...`（greenmail 起動環境が必要）で実行する。`02_architecture.md` §7.2 に対応する。

| テスト | ファイル | 検証 AC |
|---|---|---|
| `TestIntegration_DeleteOlderThan`（新規） | `internal/imap/client_integration_test.go` | AC-07, AC-08, AC-12 |
| `TestIntegration_SearchOlderThan_ReadOnly`（新規） | `internal/imap/client_integration_test.go` | AC-08 |

greenmail が UIDPLUS に対応しない場合、`TestIntegration_DeleteOlderThan` は AC-12 のフォールバック経路（`deleted == 0`、警告ログ）のみを検証する。この場合、UID EXPUNGE の正常系（AC-08, AC-15）は `TestImapClient_DeleteOlderThan_Success` の `fakeSession` ベースの単体テストで担保する。Phase 2 の「フェーズ完了の確認」で実際の対応状況を記録する。

### 4.3 セキュリティテスト

`02_architecture.md` §7.3 に対応する。

- `TestGC_IMAPOperationError_Notifies`（`cmd/tlsrpt-digest/gc_test.go`）で、`spy.SystemErrors` に記録される `notify.SystemError` に生のエラー文字列・IMAP 認証情報が含まれないこと（`Kind` / `Component` / `Mailbox` フィールドのみであること）を確認する。`notify.SystemError` 構造体自体は変更しないため、新規の構造体フィールドテストは不要である。
- `TestGC_DryRun_NoDeletions` / `TestGC_DryRun_IMAPRetentionEnabled_PreviewsCandidates`（`cmd/tlsrpt-digest/gc_test.go`）で、dry-run 時に `st.DeleteReportsBeforeCallCount == 0`・`st.DeleteEmailsBeforeCallCount == 0`・`len(fakeFetcher.DeleteOlderThanCalls) == 0` であることを確認し、ストア書き込みおよび IMAP 状態変更（STORE / EXPUNGE）が発生しないことを検証する。

### 4.4 後方互換性テスト

- `make test` で既存テストがすべて通過することを確認する。`imap.retention_days` のデフォルト `0` により、既存の `gc_test.go` の18テストおよび `TestBootstrap_NonFetchSubcommandsSucceedWithoutIMAPCredentials` は変更なしで通過する（Phase 1, Phase 4 の調査結果を参照）。
- `make lint` で新規コードが linter 基準を満たすことを確認する。

---

## 5. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| `go-imap-uidplus` の実際の API（コンストラクタ・`UidExpunge` のシグネチャ）が想定と異なる | Phase 2 の `internal/imap/client.go` 実装が想定通りに書けない | Phase 2 冒頭で `go doc` による API 確認タスクを実施し、結果に応じて `uidplusSession` の実装を調整する（§2 Phase 2 参照） |
| greenmail が UIDPLUS（RFC 4315）に対応していない | `TestIntegration_DeleteOlderThan` で AC-08/AC-15 の正常系が検証できない | AC-12 のフォールバック経路（警告ログ + 削除0件）を統合テストで検証し、正常系は `fakeSession` ベースの単体テスト（`TestImapClient_DeleteOlderThan_Success`）で担保する。対応状況を Phase 2 の「フェーズ完了の確認」に記録する |
| Gmail の IMAP 設定（デフォルト「メールをアーカイブする」）では UID EXPUNGE がストレージを解放しない | `imap.retention_days` を有効化しても、主目的（サーバー上の蓄積抑止）が達成されない | README に Gmail 側の設定変更（「完全に削除する」または「ゴミ箱に移動」+ 自動完全削除）が前提条件であることを明記し（Phase 5）、実 Gmail アカウントでの動作確認を Phase 5 の手動検証項目とする |
| `DeleteOlderThan` は TLSRPT レポートか否かを問わずメールボックス内の古いメールをすべて削除する（不可逆） | 個人・共用メールボックスに対して有効化すると無関係なメールが削除される（`02_architecture.md` §5.1 脅威4） | デフォルト無効（オプトイン）+ README で TLSRPT 受信専用メールボックスの使用を推奨 + 有効化前に `gc --dry-run` で削除候補件数を確認する手順を記載する（Phase 5） |
| `imap.fetch_days` や `fetch --since` で `imap.retention_days` より古い期間を指定できてしまう（config バリデーションは `fetch_days` の静的な値のみを検証し、実行時の `--since` は対象外） | `--since` で指定した期間の一部がすでに `gc` によって IMAP 上から削除されており、`fetch` で取得できない | `02_architecture.md` §3.1 の残余リスクとして許容範囲内とし、README に「`fetch --since` は `imap.retention_days` 以下にすること」という運用上の注意を記載する（Phase 5） |
| `imap.retention_days > 0` かつ非 dry-run で IMAP 認証情報が欠落している | `gc` がローカル削除を実行した後に IMAP 接続で失敗し、ローカルとリモートの状態が不整合になる | 認証情報チェックをローカル削除より前に実施し（AC-11）、欠落時はローカル削除を一切行わずにエラー終了する（Phase 4 `Run` の Step 2） |

---

## 6. 実装チェックリスト

- [ ] PR-1 マージ済み（対象ステップ: Phase 1）
- [ ] PR-2 マージ済み（対象ステップ: Phase 2）
- [ ] PR-3 マージ済み（対象ステップ: Phase 3）
- [ ] PR-4 マージ済み（対象ステップ: Phase 4）
- [ ] PR-5 マージ済み（対象ステップ: Phase 5）

---

## 7. 成功基準

### 機能的完成
- `make test` が通過する（全単体テスト）
- `go test -tags integration ./internal/imap/...` が greenmail 環境で通過する（`TestIntegration_DeleteOlderThan` / `TestIntegration_SearchOlderThan_ReadOnly`）
- `make lint` が通過する

### 品質指標
- AC-01〜AC-17 のすべてに対応するテストまたは静的検証が少なくとも1つ存在する（§8 参照）
- 既存の `cmd/tlsrpt-digest/gc_test.go` の18テストが変更なく通過する（`imap.retention_days` のデフォルト `0` により新規 IMAP 削除パスを通らないため）
- `cmd/tlsrpt-digest/boot_test.go::TestBootstrap_NonFetchSubcommandsSucceedWithoutIMAPCredentials` が変更なく通過する

### セキュリティ検証
- IMAP 削除が検索でヒットした対象 UID のみに対する `UID STORE +FLAGS (\Deleted)` + `UID EXPUNGE` で行われ、無差別 EXPUNGE が発生しないことを `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_Success` の assertion が保証する（AC-08）
- UIDPLUS 非対応サーバーに対して `\Deleted` フラグが一切付与されないことを `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_UIDPLUSUnsupported` の assertion（`len(s.uidStoreCalls) == 0`）が保証する（AC-12）
- `gc --dry-run` 実行時にローカルストアの書き込みおよび IMAP の状態変更（read-write SELECT / STORE / EXPUNGE）が発生しないことを `cmd/tlsrpt-digest/gc_test.go::TestGC_DryRun_NoDeletions` と `TestGC_DryRun_IMAPRetentionEnabled_PreviewsCandidates` の assertion が保証する
- IMAP 操作エラー通知（`notify.SystemErrorKindIMAPOperationFailed`）のペイロードに生のエラー文字列・IMAP 認証情報が含まれないことを `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPOperationError_Notifies` で `spy.SystemErrors` の `Kind` / `Component` / `Mailbox` フィールドのみを検証することで確認する

### ドキュメント完成
- `README.ja.md` と `README.md` の両方に、`imap.retention_days` の設定例（デフォルト `0`、不変条件、不可逆操作である旨）が追加されている
- `README.ja.md` と `README.md` の両方に、`gc` サブコマンドの `--dry-run` オプションの説明が追加されている
- `README.ja.md` と `README.md` の両方に、TLSRPT 受信専用メールボックスの推奨、Gmail の Auto-Expunge / 「完全に削除する」設定が前提条件であること、`fetch_days` / `--since` を `retention_days` 以下にする注意が記載されている
- 本ドキュメントの「Phase 5 実施結果」セクションに、実 Gmail アカウントでの動作確認結果が記録されている

---

## 8. 受け入れ条件検証

| AC | 検証方法 |
|---|---|
| AC-01 | `internal/config/config_test.go::TestLoad_AllFields`（`assert.Equal(t, 30, cfg.IMAP.RetentionDays)`）|
| AC-02 | `internal/config/config_test.go::TestLoad_Default_IMAPRetentionDays0`（`assert.Equal(t, 0, cfg.IMAP.RetentionDays)`）|
| AC-03 | `internal/config/config_test.go::TestLoad_IMAPRetentionDaysValidation`（`N = 0` のケースで `require.NoError(t, err)`）、および `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPRetentionDisabled_NoIMAPConnection`（`retention_days = 0` で IMAP 削除ステップ自体が無効化されること）|
| AC-04 | `internal/config/config_test.go::TestLoad_IMAPRetentionDaysValidation`（`N = -1` のケースで `errors.Is(err, config.ErrInvalidIMAPRetentionDays)`）|
| AC-05 | `internal/config/config_test.go::TestLoad_IMAPRetentionTooShort`（`fetch_days`/`window_days` それぞれが支配的になる境界値ケースを含む）|
| AC-06 | `internal/config/config_test.go::TestLoad_IMAPRetentionDaysValidation` と `TestLoad_IMAPRetentionTooShort`（いずれも `errors.Is` でエラー型を判別）|
| AC-07 | `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPRetentionEnabled_DeletesOlderThan`（`Duration{Days: 30}.Cutoff(now)` と一致するカットオフで `DeleteOlderThan` が呼ばれることを検証）|
| AC-08 | `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_Success`（検索でヒットした UID のみ STORE + UID EXPUNGE されることを検証）、および greenmail が UIDPLUS に対応する場合の `internal/imap/client_integration_test.go::TestIntegration_DeleteOlderThan` |
| AC-09 | `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPRetentionDisabled_NoIMAPConnection`（`retention_days = 0` で `newMailFetcher` が呼ばれないことを検証）|
| AC-10 | `cmd/tlsrpt-digest/gc_test.go::TestGC_DryRun_NoDeletions`、`TestGC_DryRun_IMAPRetentionEnabled_PreviewsCandidates`、`TestGC_DryRun_IMAPCredentialsMissing_WarnsAndContinues`、および `cmd/tlsrpt-digest/main_test.go::TestParseCLI_DryRunSupportedSubcommands`（`gc --dry-run` が許可されること）|
| AC-11 | `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPCredentialsMissing_NotifiesAndExits`（`SystemErrorKindIMAPCredentialsMissing` 通知とローカル削除未実行を検証）|
| AC-12 | `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_UIDPLUSUnsupported`（`(0, nil)` 返却・`\Deleted` 未付与・警告ログを検証）|
| AC-13 | `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPOperationError_Notifies`（`SystemErrorKindIMAPOperationFailed` に分類され `SystemErrorKindStorePermission` に巻き込まれないことを検証）|
| AC-14 | `cmd/tlsrpt-digest/gc_test.go::TestGC_IMAPOperationError_Notifies`（IMAP エラー時もローカル削除件数が先にログ出力されることを検証）、および既存の `TestGC_DeleteCountLog`（`imap_messages` フィールド追加後も `"reports=1"`/`"emails=1"` の assertion が通過すること）|
| AC-15 | `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_Success`、`TestImapClient_DeleteOlderThan_EmptySearch`、`TestImapClient_DeleteOlderThan_ReadOnly`、`TestImapClient_DeleteOlderThan_SupportError`、`TestImapClient_SearchOlderThan_UsesExamine`（`DeleteOlderThan`/`SearchOlderThan` のシグネチャと内部フローを検証）|
| AC-16 | `internal/imap/client_test.go::TestImapClient_DeleteOlderThan_ZeroCutoff`、`TestImapClient_SearchOlderThan_ZeroCutoff`（cutoff ゼロ値で `(0, nil)` / `([]uint32{}, nil)` を即座に返すことを検証）|
| AC-17 | `internal/imap/testutil/mocks_test.go::TestFakeMailFetcherDeleteOlderThan`、`TestFakeMailFetcherSearchOlderThan`（呼び出し時の `cutoff` 値の記録と返却値・エラーの注入を検証）|

---

## 9. クロスサーチチェックリスト

変更・追加されたシンボルやコンセプトの影響範囲を確認する。

| 検索対象 | コマンド | 期待結果 |
|---|---|---|
| `dryRunUIDSampleMax` の定義箇所 | `rg -n "dryRunUIDSampleMax" --glob '*.go'` | `cmd/tlsrpt-digest/fetch.go`（既存定義）と `cmd/tlsrpt-digest/gc.go`（`logGCDryRunSummary` での利用、新規）のみ。重複定義が無いこと |
| `ErrInvalidIMAPRetentionDays` / `ErrIMAPRetentionTooShort` の命名衝突 | `rg -n "ErrInvalidIMAPRetentionDays\|ErrIMAPRetentionTooShort" internal/config/` | `errors.go`（定義）、`validate.go`（参照）、`config_test.go`（テスト）のみ。Phase 1 実施前に他箇所で同名シンボルが既に定義されていないこと |
| `uidplusSession` / `selectMailbox` / `uidSearchBefore` の命名衝突 | `rg -n "uidplusSession\|selectMailbox\|uidSearchBefore" internal/imap/` | Phase 2 実施前は 0 件であること（`internal/imap/client.go` に既存の同名シンボルが無いこと）。Phase 2 実施後は `client.go`（定義）と `client_test.go`（テスト）のみ |
| `SystemErrorKindIMAPOperationFailed` / `SystemErrorKindIMAPCredentialsMissing` / `SystemErrorKindIMAPConnectFailed` / `SystemErrorKindIMAPAuthFailed` の既存定義確認 | `rg -n "SystemErrorKindIMAPOperationFailed\|SystemErrorKindIMAPCredentialsMissing\|SystemErrorKindIMAPConnectFailed\|SystemErrorKindIMAPAuthFailed" internal/notify/` | Phase 4 実施前に `internal/notify/` 内にこれら4つの定数が `fetch` サブコマンド向けにすでに定義されていること（Phase 4 では新規定義しない）|
| `imap.retention_days` / 「保持期間」用語の README・用語集での一貫性 | `rg -n "retention_days\|保持期間\|オプトイン\|Auto-Expunge\|完全に削除する\|ゴミ箱に移動\|メールをアーカイブする" README.ja.md README.md docs/translation_glossary.md` | `README.ja.md` と `README.md` の双方に対応する記述があり、`docs/translation_glossary.md` に新規追加した用語（保持期間、オプトイン、不可逆、Auto-Expunge、完全に削除する、ゴミ箱に移動、メールをアーカイブする）の対訳が登録されていること |
| `gc --dry-run` の説明箇所の一貫性 | `rg -n "dry-run" README.ja.md README.md cmd/tlsrpt-digest/main.go` | `README.ja.md` / `README.md` の `gc` サブコマンド説明と `printDetailedHelp` の `gc:` セクションがいずれも `--dry-run` を記載していること |

---

## 10. 次のステップ

1. `03_implementation_plan.md` のレビューと承認
2. Phase 1 から順に実装を進める
3. 各フェーズ完了時に `make test` と `make lint` で回帰がないことを確認する
4. Phase 2 完了時に `go test -tags integration ./internal/imap/...` を実行し、greenmail の UIDPLUS 対応状況を記録する
5. Phase 5 完了時に実 Gmail アカウントでの動作確認（CAPABILITY 確認、`gc --dry-run` プレビュー、`gc` 実削除、Web UI 確認、ストレージ使用量比較）を行い、結果を「Phase 5 実施結果」セクションに記録する

