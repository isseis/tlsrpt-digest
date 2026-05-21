# 実装計画書：設定ファイル読み込み（TOML）

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-21 |
| レビュー日 | 2026-05-21 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

- **目的**: TOML 設定ファイルを読み込み、既定値の適用・値検証・整合性チェックを行って `*Config` を返す `config.Load` / `config.LoadFile` を実装する。詳細は `01_requirements.md` を参照。
- **実装原則**: `02_architecture.md` の設計に従う。コンポーネント責務・型設計・エラーハンドリング方針・セキュリティ考慮はそちらを参照すること。設計の詳細を本書で繰り返さない。
- **言語規約**: Go ソースファイルのコメント・識別子・文字列リテラルには英語のみを使用する（テストの説明文字列を除く）。
- **スコープ**: `internal/config/` が主な変更対象。`cmd/tlsrpt-digest/main.go` の `loadConfig` を `LoadFile` ベースに置換することも含む。
- **既存資産の再利用**:
  - `internal/config/secret.go` の `Secret` 型: 変更なしで流用（`cmd/tlsrpt-digest` が環境変数由来の認証情報を `Secret` でラップする）
  - `internal/config/config_test.go` の既存テスト: TOML データに `[imap]` 必須フィールドを追加して修正・流用する
  - `cmd/tlsrpt-digest/main_test.go` の `TestLoadConfig_*` 系テスト: `LoadFile` の新しい挙動に合わせて修正する

---

## 2. 実装順序とマイルストーン

| マイルストーン | 完了条件 |
|---|---|
| M1: 型・エラー定義 | フェーズ 1 完了後、`make test` が通る（既存テストへの後方互換を維持） |
| M2: 既定値・バリデーション | フェーズ 2 完了後、AC-01・AC-03〜AC-10・AC-10a のテストが通る |
| M3: ファイル読み込み | フェーズ 3 完了後、AC-02・AC-10b・AC-10c・AC-10d のテストが通る |
| M4: 既定値テスト網羅 | フェーズ 4 完了後、AC-11〜AC-17 のテストが通る |
| M5: 統合完了 | フェーズ 5 完了後、`make lint`・`make test`・`make deadcode` がすべて成功する |

フェーズ間の依存関係:
- フェーズ 2 はフェーズ 1 に依存する（型・エラー定義後でないとコンパイルできない）
- フェーズ 3 はフェーズ 2 に依存する（`Load` 完成後でないと `LoadFile` が実装できない）
- フェーズ 4 はフェーズ 2 に依存する（既定値テストは `Load` の動作を前提とする）
- フェーズ 5 はフェーズ 3・4 に依存する

---

## 3. テスト戦略

`02_architecture.md` セクション 7 に定める方針に従い、以下の層でテストする。

### 単体テスト

| 対象 | テストファイル | 確認内容 |
|---|---|---|
| `Load` の正常系・エラー系 | `internal/config/config_test.go`（変更） | AC-01・AC-03〜AC-10・AC-10a・AC-11〜AC-17 |
| `LoadFile` の正常系・エラー系 | `internal/config/load_file_test.go`（新規） | AC-02・AC-10b・AC-10c・AC-10d |
| `rawIMAPConfig` の構造保証 | `internal/config/security_test.go`（新規、`package config`） | AC-07 のセキュリティ保証（`rawIMAPConfig` に認証情報フィールドが存在しないことをリフレクションで確認） |
| `cmd` 側の設定読み込み挙動 | `cmd/tlsrpt-digest/main_test.go`（変更） | `loadConfig` の挙動変更（AC-02 の間接確認） |

### セキュリティテスト

`02_architecture.md` セクション 7.3 の要件に従い、以下の 2 つの視点でテストする。

1. **エラーメッセージへの機密値混入防止**: TOML に未知のキー（例: `imap.password`）を含めたとき strict デコードエラーが返り、かつそのエラー文字列にキーの値が含まれないことを `TestLoad_IMAPPasswordInTOML` で確認する（AC-07）。
2. **`rawIMAPConfig` の構造保証**: `rawIMAPConfig` が認証情報フィールドを持たないことを `TestRawIMAPConfig_NoCredentialFields` でリフレクション検証する。これにより、将来 `rawIMAPConfig` に誤って `Password` フィールドを追加した場合に即座に検出できる（AC-07 の実装上の不変条件）。

### 既存テストとの重複回避

- `TestSecret_RedactsStringAndLogValue`（`secret_test.go`）は `Secret` 型の保護を検証済みのため再テストしない。
- `TestNotifySlackConfig_AllowedHostValidation` は `allowed_host` の正規表現ケースを網羅しているため、TOML に IMAP 必須フィールドを追加する最小限の変更のみ行い、正規表現ケースの重複追加はしない。

### テストヘルパーファイル方針

`load_file_test.go` で使うログキャプチャヘルパー `newCapturingLogger` は `internal/config/helpers_test.go`（`//go:build test`、`package config_test`）に定義する。このヘルパーは公開 API のみを使うため、非公開型へのアクセスを前提とする `test_helpers.go`（Classification B: `package config` + `//go:build test`）ではなく、通常の `_test.go` ファイルとして定義する。`config_test` パッケージのテストファイル間で共有するため、`load_file_test.go` に直接書くのではなく専用の共有ファイルに切り出す。

### テストデータ方針

AC-10 の有効 PEM テストには、テスト関数内の定数として自己署名証明書 PEM 文字列を埋め込み、`t.TempDir()` 配下のファイルに書き出して使用する。外部 `testdata/` ファイルは作成しない。

---

## 4. 実装フェーズ

### フェーズ 1: 型定義とエラー定義

`02_architecture.md` セクション 3.2 および 4.2 を参照。

- [x] **1.1** `internal/config/types.go` を新規作成し `Config` 型階層と `rawConfig` 型階層を定義する
  - ファイル: `internal/config/types.go`
  - 作業内容:
    - `Config`・`IMAPConfig`・`NotifyConfig`・`NotifySlackConfig`・`StoreConfig`・`SummaryConfig` を定義する（TOML タグなし、公開型）
    - `rawConfig`・`rawIMAPConfig`・`rawNotifyConfig`・`rawNotifySlackConfig`・`rawStoreConfig`・`rawSummaryConfig` を定義する（ポインタフィールド、TOML タグあり、非公開型）
    - `config.go` に現在ある `NotifySlackConfig`・`NotifyConfig`・`Config` の型定義を `types.go` に移動し、`config.go` 側を削除する
  - 確認方法: `go build ./internal/config/` が通ること
  - 想定工数: 30 分 / 実績工数: -

- [x] **1.2** `internal/config/errors.go` を新規作成し sentinel エラーを集約する
  - ファイル: `internal/config/errors.go`
  - 作業内容:
    - `ErrConfigPathEmpty`・`ErrConfigFileRead`・`ErrConfigDecode` を定義する
    - `ErrInvalidIMAPHost`・`ErrInvalidIMAPPort`・`ErrInvalidFetchDays`・`ErrInvalidWindowDays`・`ErrInvalidRetentionDays`・`ErrInvalidMaxEmailAgeDays`・`ErrInvalidAllowedHost`・`ErrTLSCACertNotReadable`・`ErrTLSCACertNotPEM`・`ErrInvalidMaxMessageBytes` を定義する
    - `config.go` にある `ErrInvalidAllowedHost` の定義を `errors.go` に移動し、`config.go` 側を削除する（`errors.Is` の互換性は変数が同一パッケージ内で同一名のため維持される）
  - 確認方法: `go build ./...` が通ること（`ErrInvalidAllowedHost` を参照する他パッケージを含む）
  - 想定工数: 15 分 / 実績工数: -

- [x] **1.3** `make test` が通ることを確認する（M1）
  - 確認方法: `make test` が成功し、既存テストの失敗がないこと。失敗した場合は前フェーズのタスクに戻り原因を修正すること

---

### フェーズ 2: 既定値・バリデーション

`02_architecture.md` セクション 3.3・3.4・6.1・6.2 を参照。`applyDefaults` はエラーを返さない（`02_architecture.md` セクション 3.3 のシグネチャ通り）。明示的な不正値に対するエラーは `validate` が担う。

- [x] **2.1** `internal/config/defaults.go` を新規作成し既定値適用ロジックを実装する
  - ファイル: `internal/config/defaults.go`
  - 作業内容:
    - `applyDefaults(raw *rawConfig) Config` を実装する（戻り値はエラーなし）
    - `raw.IMAP.Mailbox` が nil または空文字のとき `Config.IMAP.Mailbox = "INBOX"` を設定する（AC-11）
    - `raw.IMAP.FetchDays` が nil のとき `Config.IMAP.FetchDays = 14` を設定する（AC-12）
    - `raw.Store.RootDir` が nil または空文字のとき `Config.Store.RootDir = "./store"` を設定する（AC-13）
    - `raw.IMAP.TLSCACert` が nil のとき `Config.IMAP.TLSCACert = ""` を設定する（AC-14）
    - `raw.Summary.WindowDays` が nil のとき `Config.Summary.WindowDays = 7` を設定する（AC-15）
    - `raw.Store.RetentionDays` が nil のとき `Config.Store.RetentionDays = 30` を設定する（AC-16）
    - `raw.Store.MaxEmailAgeDays` が nil のとき `Config.Store.MaxEmailAgeDays = 30` を設定する（AC-17）
    - 必須フィールド（`Host`・`Port`）は raw の値を `Config` にコピーする（ポインタが nil のとき 0 値を設定）。値の正当性検証は `validate.go` に委ねる
    - `raw.IMAP.MaxMessageBytes` が nil のとき `Config.IMAP.MaxMessageBytes = 0`（無制限）を設定する（`02_architecture.md` セクション 3.1 の既定値方針）
  - 確認方法: フェーズ 2.5 のテストが通ること
  - 想定工数: 30 分 / 実績工数: -

- [x] **2.2** `internal/config/validate.go` を新規作成し値検証ロジックを実装する
  - ファイル: `internal/config/validate.go`
  - 作業内容:
    - `validate(cfg *Config) error` を実装する
    - `cfg.IMAP.Host` が空のとき `fmt.Errorf("config: %w", ErrInvalidIMAPHost)` を返す（AC-05）
    - `cfg.IMAP.Port` が 1〜65535 の範囲外のとき `fmt.Errorf("config: %w: %d", ErrInvalidIMAPPort, port)` を返す（ポインタが nil で 0 値になった場合も範囲外として扱う）（AC-06）
    - `cfg.IMAP.FetchDays` が 1 未満のとき `fmt.Errorf("config: %w: %d", ErrInvalidFetchDays, days)` を返す（AC-09）
    - `cfg.Summary.WindowDays` が 1 未満のとき `ErrInvalidWindowDays` をラップして返す（AC-10a）
    - `cfg.Store.RetentionDays` が 1 未満のとき `ErrInvalidRetentionDays` をラップして返す（AC-10a）
    - `cfg.Store.MaxEmailAgeDays` が 1 未満のとき `ErrInvalidMaxEmailAgeDays` をラップして返す（AC-10a）
    - `cfg.IMAP.MaxMessageBytes` が負のとき `ErrInvalidMaxMessageBytes` をラップして返す（`02_architecture.md` セクション 4.2 の設計上の防衛的検証。対応する AC は存在しないが `0 = 無制限` の仕様上負数は不正値）
    - `cfg.IMAP.TLSCACert` が非空のとき、ファイル読み込み失敗で `ErrTLSCACertNotReadable`、PEM 形式不正で `ErrTLSCACertNotPEM` をラップして返す（AC-10）
    - `cfg.Notify.Slack.AllowedHost` を検証するため、`config.go` にある `validateAllowedHost` 関数および `reValidHostname` 変数を本ファイルに移動する（AC-08）
  - 確認方法: フェーズ 2.5 のテストが通ること
  - 想定工数: 45 分 / 実績工数: -

- [x] **2.3** `internal/config/config.go` の `Load` を `defaults.go`・`validate.go` を使う形に再構成する
  - ファイル: `internal/config/config.go`
  - 作業内容:
    - strict decode で `rawConfig` にデコードし、デコードエラーを `fmt.Errorf("config: %w: %w", ErrConfigDecode, err)` でラップして返す（AC-03・AC-04）
    - `applyDefaults` を呼び出して `Config` を生成する
    - `validate` を呼び出して値を検証する
    - タスク 1.1・1.2 で移動済みの型定義・エラー定義・関数（`validate()` メソッド・`validateAllowedHost()` 等）が `config.go` に残っていないことを確認する
  - 確認方法: `go build ./internal/config/` が通ること
  - 想定工数: 30 分 / 実績工数: -

- [x] **2.4** `internal/config/helpers_test.go` を新規作成し `LoadFile` テスト向けログキャプチャヘルパーを定義する
  - ファイル: `internal/config/helpers_test.go`（新規、`package config_test`）
  - 作業内容:
    - `//go:build test` は付けない。`_test.go` ファイルは Go ツールチェーンによってテストビルド時のみコンパイルされるため、本番バイナリへの混入を防ぐビルドタグは不要である
    - `newCapturingLogger() (*slog.Logger, *bytes.Buffer)` を定義する: `bytes.Buffer` を出力先とする `slog.NewTextHandler` でロガーを構築し、バッファへの参照でログ内容を文字列検査できるようにする
    - このヘルパーは `load_file_test.go` の WARN/INFO ログ検証テスト（AC-10b・AC-10c・AC-10d）で使用する
  - 確認方法: `go test -count=1 ./internal/config/` が通ること（コンパイルエラーがないこと）
  - 想定工数: 10 分 / 実績工数: -

- [x] **2.5** `internal/config/config_test.go` に新機能のテストを追加し、既存テストを更新する
  - ファイル: `internal/config/config_test.go`
  - 作業内容（既存テストの更新）:
    - `TestLoad_ValidAllowedHost`・`TestLoad_EmptyAllowedHost`・`TestLoad_MissingNotifySection`・`TestNotifySlackConfig_AllowedHostValidation` のそれぞれの TOML データに `[imap]` セクション（`host = "imap.example.com"` および `port = 993`）を追加する（IMAP 必須フィールドが検証に通るようにするため）
  - 作業内容（新規テスト）:
    - `TestLoad_AllFields`: 全設定フィールドを含む TOML を読み込み、各フィールドの値が正しく `Config` に設定されることを確認する。`imap.max_message_bytes` を省略したケースで `Config.IMAP.MaxMessageBytes == 0` であることも確認する（AC-01 + `max_message_bytes` 既定値）
    - `TestNotifySlackConfig_AllowedHostValidation` の更新後も AC-08 の全ケース（有効ホスト名・スキームつき・ポートつき・前後スペース・空文字）が引き続き網羅されていることを確認する（AC-08）
    - `TestLoad_TOMLSyntaxError`: 文法エラーのある TOML で `errors.Is(err, config.ErrConfigDecode)` が真であることを確認する（AC-03）
    - `TestLoad_UnknownTopLevelKey`: `[imap]` セクションに未知のキーを含む TOML でデコードエラーが返ることを確認する（AC-04 の補強）
    - `TestLoad_IMAPHostValidation`: `imap.host` が空文字・未指定のとき `errors.Is(err, config.ErrInvalidIMAPHost)` が真であることを確認する（AC-05）
    - `TestLoad_IMAPPortValidation`: `imap.port` について (a) `port = 0`（TOML に明示的なゼロ値）、(b) `port = -1`（負値）、(c) `port = 65536`（上限超え）、(d) `[imap]` セクション存在かつ `port` キーなし（nil → 0 変換）の 4 ケースで `errors.Is(err, config.ErrInvalidIMAPPort)` が真であることを確認し、有効値（1・443・65535）では成功することを確認する（AC-06）
    - `TestLoad_IMAPPasswordInTOML`: TOML に `imap.password = "secret-value"` を含むとき strict デコードでエラーとなり、かつエラー文字列に `"secret-value"` が含まれないことを確認する（AC-07 + セキュリティ検証）
    - `TestLoad_FetchDaysValidation`: `imap.fetch_days` が 0・負のとき `errors.Is(err, config.ErrInvalidFetchDays)` が真であり、`fetch_days = 1` のとき成功することを確認する（AC-09、下限境界値）
    - `TestLoad_TLSCACert`: (1) 存在しないパスを指定したとき `errors.Is(err, config.ErrTLSCACertNotReadable)` が真、(2) 不正 PEM を含むファイルを指定したとき `errors.Is(err, config.ErrTLSCACertNotPEM)` が真、(3) 有効な PEM を含むファイルを指定したとき成功、の 3 ケースを確認する。有効 PEM はテスト関数内の定数として自己署名証明書文字列を埋め込み `t.TempDir()` 配下のファイルに書き出して使用する（AC-10）
    - `TestLoad_DaysValidation`: `summary.window_days`・`store.retention_days`・`store.max_email_age_days` がそれぞれ 0・負のとき各 `ErrInvalid*Days` sentinel が `errors.Is` で識別でき、値が 1 のとき成功することを確認する（AC-10a、下限境界値を含む）
  - 確認方法: `go test -v ./internal/config/` で全テストが PASS すること
  - 想定工数: 60 分 / 実績工数: -

- [x] **2.6** `internal/config/security_test.go` を新規作成し `rawIMAPConfig` の構造保証テストを追加する
  - ファイル: `internal/config/security_test.go`（新規、`package config`）
  - 作業内容:
    - `TestRawIMAPConfig_NoCredentialFields`: `reflect.TypeOf(rawIMAPConfig{})` を使ってフィールドを列挙し、フィールド名（小文字化）と TOML タグ値のいずれにも `"password"`・`"username"` が含まれないことを確認する。これにより将来 `rawIMAPConfig` に誤って認証情報フィールドが追加された場合に即検出できる（AC-07 の不変条件）
  - 確認方法: `go test -v ./internal/config/` で `TestRawIMAPConfig_NoCredentialFields` が PASS すること
  - 想定工数: 20 分 / 実績工数: -

- [x] **2.7** `make test` が通ることを確認する（M2）
  - 確認方法: `make test` が成功すること。失敗した場合は前フェーズのタスクに戻り原因を修正すること

---

### フェーズ 3: ファイル読み込みと整合性チェック

`02_architecture.md` セクション 3.3・3.4・6.3・6.4 を参照。

- [x] **3.1** `internal/config/load_file.go` を新規作成し `LoadFile` を実装する
  - ファイル: `internal/config/load_file.go`
  - 作業内容:
    - `LoadFile(path string, logger *slog.Logger) (*Config, error)` を実装する
    - `path == ""` のとき `fmt.Errorf("config: %w", ErrConfigPathEmpty)` を返す（AC-02）
    - `os.ReadFile` でファイルを読み込み、失敗時に `fmt.Errorf("config: %w: %w", ErrConfigFileRead, err)` を返す（AC-02）
    - `Load` を呼び出して `*Config` を取得する
    - `logger` が nil のとき `slog.Default()` を使う（`02_architecture.md` セクション 3.3 の方針）
    - `Config.Store.RootDir` が相対パスのとき `filepath.Abs` で絶対化し（エラー時はラップして返す）、変換後のパスを `logger.Info(...)` で出力する。絶対パスの場合は変更せずログも出力しない（AC-10d）
    - `Store.RetentionDays > Store.MaxEmailAgeDays` のとき `logger.Warn(...)` を出力して継続する（AC-10b）
    - `IMAP.FetchDays >= Store.RetentionDays` のとき `logger.Warn(...)` を出力して継続する（AC-10c）
  - 確認方法: フェーズ 3.2 のテストが通ること
  - 想定工数: 45 分 / 実績工数: -

- [x] **3.2** `internal/config/load_file_test.go` を新規作成し `LoadFile` のテストを実装する
  - ファイル: `internal/config/load_file_test.go`（新規、`package config_test`）
  - 作業内容:
    - `TestLoadFile_EmptyPath`: 空文字列を渡したとき `errors.Is(err, config.ErrConfigPathEmpty)` が真であることを確認する（AC-02）
    - `TestLoadFile_NonexistentPath`: 存在しないパスを渡したとき `errors.Is(err, config.ErrConfigFileRead)` が真であることを確認する（AC-02）
    - `TestLoadFile_NilLogger`: `logger` に nil を渡したとき `slog.Default()` が使われパニックなしで正常終了することを確認する
    - `TestLoadFile_ValidTOML`: 全フィールドを含む TOML ファイルを `t.TempDir()` に書き出し `LoadFile` を呼んで `*Config` が正しく返ることを確認する（AC-01 の `LoadFile` 経由での統合確認）
    - `TestLoadFile_ConsistencyWarning_RetentionGtEmailAge`: `store.retention_days > store.max_email_age_days` の TOML を読み込んだとき WARN ログが出力され、エラーなしで `*Config` が返ることを確認する。ログキャプチャには `helpers_test.go` の `newCapturingLogger` を使用する（AC-10b）
    - `TestLoadFile_ConsistencyWarning_FetchDaysGteRetentionDays`: `imap.fetch_days >= store.retention_days` の TOML を読み込んだとき WARN ログが出力され、エラーなしで `*Config` が返ることを確認する（AC-10c）
    - `TestLoadFile_RelativeRootDir_Absolutized`: `store.root_dir` に相対パスを指定したとき、返却された `Config.Store.RootDir` が絶対パスであり、INFO ログに絶対化後のパスが含まれることを確認する（AC-10d）
    - `TestLoadFile_AbsoluteRootDir_Unchanged`: `store.root_dir` に絶対パスを指定したとき、値が変更されず INFO ログも出力されないことを確認する（AC-10d の反転確認）
  - 確認方法: `go test -v ./internal/config/` で全テストが PASS すること
  - 想定工数: 60 分 / 実績工数: -

- [x] **3.3** `make test` が通ることを確認する（M3）
  - 確認方法: `make test` が成功すること。失敗した場合は前フェーズのタスクに戻り原因を修正すること

---

### フェーズ 4: 既定値テスト網羅

`02_architecture.md` セクション 6.2 の既定値方針を参照。

- [ ] **4.1** `internal/config/config_test.go` に AC-11〜AC-17 の既定値テストを追加する
  - ファイル: `internal/config/config_test.go`
  - 作業内容（各テスト関数）:
    - `TestLoad_Default_MailboxINBOX`: `imap.mailbox` を未設定のとき `Config.IMAP.Mailbox == "INBOX"` であることを確認する（AC-11）
    - `TestLoad_Default_FetchDays14`: `imap.fetch_days` を未設定のとき `Config.IMAP.FetchDays == 14` であることを確認する（AC-12）
    - `TestLoad_Default_StoreRootDir`: `store.root_dir` を未設定のとき `Config.Store.RootDir == "./store"` であることを確認する（AC-13。絶対化は `LoadFile` の責務のため `Load` の段階では相対パスのまま）
    - `TestLoad_Default_TLSCACertEmpty`: `imap.tls_ca_cert` を未設定のとき `Config.IMAP.TLSCACert == ""` であり検証エラーが起きないことを確認する（AC-14）
    - `TestLoad_Default_WindowDays7`: `summary.window_days` を未設定のとき `Config.Summary.WindowDays == 7` であることを確認する（AC-15）
    - `TestLoad_Default_RetentionDays30`: `store.retention_days` を未設定のとき `Config.Store.RetentionDays == 30` であることを確認する（AC-16）
    - `TestLoad_Default_MaxEmailAgeDays30`: `store.max_email_age_days` を未設定のとき `Config.Store.MaxEmailAgeDays == 30` であることを確認する（AC-17）
  - 確認方法: `go test -v ./internal/config/` で全テストが PASS すること
  - 想定工数: 30 分 / 実績工数: -

- [ ] **4.2** `make test` が通ることを確認する（M4）
  - 確認方法: `make test` が成功すること。失敗した場合は前フェーズのタスクに戻り原因を修正すること

---

### フェーズ 5: 呼び出し側統合

`02_architecture.md` セクション 3.4（`cmd/tlsrpt-digest/main.go` の変更方針）を参照。

- [ ] **5.1** `cmd/tlsrpt-digest/main.go` の `loadConfig` を `LoadFile` ベースに置換する
  - ファイル: `cmd/tlsrpt-digest/main.go`
  - 作業内容:
    - `loadConfig(path string) (*config.Config, error)` の実装を `config.LoadFile(path, slog.Default())` を呼ぶだけに変更する
    - 従来の「`path == ""` のとき空 `Config` を返す」暫定挙動を削除する（`02_architecture.md` セクション 1.1 の設計原則 8: `ErrConfigPathEmpty` を返す）
    - `os.ReadFile` の直接呼び出しを削除する（`LoadFile` 内で行うため）
  - 確認方法: `go build ./cmd/tlsrpt-digest/` が通ること
  - 想定工数: 15 分 / 実績工数: -

- [ ] **5.2** `cmd/tlsrpt-digest/main.go` に IMAP 設定を組み立てる補助関数を追加する
  - ファイル: `cmd/tlsrpt-digest/main.go`
  - 作業内容:
    - `buildIMAPConfig(cfg *config.Config) imap.Config` を追加する
    - `os.Getenv("TLSRPT_IMAP_USERNAME")` と `os.Getenv("TLSRPT_IMAP_PASSWORD")` を取得する
    - `imap.Config` に `cfg.IMAP.Host`・`cfg.IMAP.Port`・`cfg.IMAP.Mailbox`・`cfg.IMAP.TLSCACert`・`cfg.IMAP.MaxMessageBytes` を設定し、ユーザ名を `Username` フィールドに、パスワードを `config.Secret(password)` でラップして `Password` フィールドに格納する
    - `main()` から `_ = buildIMAPConfig(cfg)` として呼び出す（タスク 0070 で実際の利用先に置き換えるまでの仮置き。Go コンパイラの「使用されていない変数」エラーを回避しつつ `make deadcode` への到達性を維持するため）
  - 確認方法: `go build ./cmd/tlsrpt-digest/` が通ること
  - 想定工数: 30 分 / 実績工数: -

- [ ] **5.3** `cmd/tlsrpt-digest/main_test.go` の `loadConfig` 関連テストを更新する
  - ファイル: `cmd/tlsrpt-digest/main_test.go`
  - 作業内容:
    - `TestLoadConfig_EmptyPath`: `loadConfig("")` がエラーを返し `errors.Is(err, config.ErrConfigPathEmpty)` が真であることを確認するよう変更する（従来の「空 `Config` を返す」挙動は廃止）
    - `TestLoadConfig_ValidFile`: 書き込む TOML に `[imap]` セクション（`host = "imap.example.com"` および `port = 993`）を追加する（IMAP 必須フィールドのバリデーションを通過させるため）
    - `TestLoadConfig_NonexistentFile`: `errors.Is(err, config.ErrConfigFileRead)` が真であることを確認するよう強化する
    - `newConfigWithAllowedHost` ヘルパー: `Config` に `IMAP`・`Store`・`Summary` フィールドが追加されてもゼロ値で問題ないことを確認する（コンパイルエラーがなければ変更不要）
  - 確認方法: `go test -tags test -v ./cmd/tlsrpt-digest/` で全テストが PASS すること
  - 想定工数: 20 分 / 実績工数: -

- [ ] **5.4** `make fmt` を実行してフォーマットを確認する
  - 確認方法: `make fmt` 後に `git diff` で差分がないこと

- [ ] **5.5** `make test` と `make lint` が通ることを確認する

- [ ] **5.6** `make deadcode` で不要なコードがないことを確認する（M5）
  - 注意: `buildIMAPConfig` は `_ = buildIMAPConfig(cfg)` で呼び出すため `make deadcode` には未到達として報告されない。タスク 0070 で `_` を実際の利用先に置き換えること

---

## 5. 受け入れ条件トレーサビリティ

`01_requirements.md` の各受け入れ条件とテストの対応を記録する。実装完了後にファイルパスと行番号を記入すること。

**AC-01**: 有効な TOML ファイルを指定した場合、すべての設定値を正しく読み込む
- テスト: `internal/config/config_test.go::TestLoad_AllFields`
- テスト: `internal/config/load_file_test.go::TestLoadFile_ValidTOML`
- 実装: `internal/config/config.go`（`Load`）・`internal/config/load_file.go`（`LoadFile`）

**AC-02**: 指定ファイルが存在しない場合（またはパスが空の場合）、エラーを返す
- テスト: `internal/config/load_file_test.go::TestLoadFile_EmptyPath`（`ErrConfigPathEmpty`）
- テスト: `internal/config/load_file_test.go::TestLoadFile_NonexistentPath`（`ErrConfigFileRead`）
- 実装: `internal/config/load_file.go`（`LoadFile`）

**AC-03**: TOML の文法エラーがある場合、エラーを返す
- テスト: `internal/config/config_test.go::TestLoad_TOMLSyntaxError`（`errors.Is(err, config.ErrConfigDecode)`）
- 実装: `internal/config/config.go`（`Load`）

**AC-04**: 未知のキーが含まれる場合、エラーを返す
- テスト: `internal/config/config_test.go::TestNotifySlackConfig_UnknownKey`（既存・更新）
- テスト: `internal/config/config_test.go::TestLoad_UnknownTopLevelKey`（新規）
- 実装: `internal/config/config.go`（`Load`、`DisallowUnknownFields`）

**AC-05**: IMAP ホスト名が空の場合はエラーを返す
- テスト: `internal/config/config_test.go::TestLoad_IMAPHostValidation`
- 実装: `internal/config/validate.go`（`validate`）

**AC-06**: IMAP ポート番号が 1〜65535 の範囲外の場合はエラーを返す
- テスト: `internal/config/config_test.go::TestLoad_IMAPPortValidation`（エラーケース: (a)`port = 0`・(b)負・(c)65536・(d)キー未設定〈nil→0〉; 成功ケース: 1・443・65535）
- 実装: `internal/config/validate.go`（`validate`）

**AC-07**: IMAP ユーザ名およびパスワードは設定ファイルに記載しない
- テスト: `internal/config/config_test.go::TestLoad_IMAPPasswordInTOML`（strict デコードエラー確認 + エラー文字列に機密値が含まれないことの確認）
- テスト: `internal/config/security_test.go::TestRawIMAPConfig_NoCredentialFields`（`rawIMAPConfig` のフィールドにリフレクションで `password`・`username` が存在しないことを確認）
- 実装: `internal/config/config.go`（`Load`、`DisallowUnknownFields`）・`internal/config/types.go`（`rawIMAPConfig` の型定義）

**AC-08**: `notify.slack.allowed_host` の形式検証（Slack Webhook URL は TOML に記載しない）
- テスト: `internal/config/config_test.go::TestNotifySlackConfig_AllowedHostValidation`（既存・更新）
- 実装: `internal/config/validate.go`（`validateAllowedHost`）

**AC-09**: `imap.fetch_days` が 1 未満の場合はエラーを返す
- テスト: `internal/config/config_test.go::TestLoad_FetchDaysValidation`（エラーケース: 0・負; 成功ケース: 1〈下限境界〉）
- 実装: `internal/config/validate.go`（`validate`）

**AC-10**: `imap.tls_ca_cert` が設定されている場合、ファイルの可読性と PEM 形式を確認する
- テスト: `internal/config/config_test.go::TestLoad_TLSCACert`（存在しないパス・不正 PEM・有効 PEM の 3 ケース）
- 実装: `internal/config/validate.go`（`validate`）

**AC-10a**: `summary.window_days`・`store.retention_days`・`store.max_email_age_days` はいずれも 1 以上
- テスト: `internal/config/config_test.go::TestLoad_DaysValidation`（エラーケース: 各フィールドで 0 以下; 成功ケース: 各フィールドで 1〈下限境界〉）
- 実装: `internal/config/validate.go`（`validate`）

**AC-10b**: `store.retention_days > store.max_email_age_days` のとき WARN ログを出力する
- テスト: `internal/config/load_file_test.go::TestLoadFile_ConsistencyWarning_RetentionGtEmailAge`
- 実装: `internal/config/load_file.go`（`LoadFile`）

**AC-10c**: `imap.fetch_days >= store.retention_days` のとき WARN ログを出力する
- テスト: `internal/config/load_file_test.go::TestLoadFile_ConsistencyWarning_FetchDaysGteRetentionDays`
- 実装: `internal/config/load_file.go`（`LoadFile`）

**AC-10d**: `store.root_dir` に相対パスを指定した場合、絶対パスへ正規化し INFO ログに出力する
- テスト: `internal/config/load_file_test.go::TestLoadFile_RelativeRootDir_Absolutized`
- テスト: `internal/config/load_file_test.go::TestLoadFile_AbsoluteRootDir_Unchanged`
- 実装: `internal/config/load_file.go`（`LoadFile`）

**AC-11**: `imap.mailbox` が未設定のとき `"INBOX"` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_MailboxINBOX`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-12**: `imap.fetch_days` が未設定のとき `14` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_FetchDays14`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-13**: `store.root_dir` が未設定のとき `"./store"` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_StoreRootDir`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-14**: `imap.tls_ca_cert` が未設定のとき空文字（OS CA バンドル）を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_TLSCACertEmpty`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-15**: `summary.window_days` が未設定のとき `7` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_WindowDays7`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-16**: `store.retention_days` が未設定のとき `30` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_RetentionDays30`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

**AC-17**: `store.max_email_age_days` が未設定のとき `30` を適用する
- テスト: `internal/config/config_test.go::TestLoad_Default_MaxEmailAgeDays30`
- 実装: `internal/config/defaults.go`（`applyDefaults`）

---

## 6. 実装チェックリスト

フェーズ完了時に記入すること。

| フェーズ | 完了条件 | 状態 |
|---|---|---|
| フェーズ 1 | `make test` が成功し既存テストの後方互換が維持されている | [x] |
| フェーズ 2 | AC-01・AC-03〜AC-10・AC-10a の全テストが PASS | [x] |
| フェーズ 3 | AC-02・AC-10b・AC-10c・AC-10d の全テストが PASS | [x] |
| フェーズ 4 | AC-11〜AC-17 の全テストが PASS | [ ] |
| フェーズ 5 | `make lint`・`make test`・`make deadcode` がすべて成功 | [ ] |

---

## 7. リスク管理

| リスク | 対策 |
|---|---|
| `imap.host`・`imap.port` の必須化により既存テストが一括で失敗する | フェーズ 2.5 で全既存テストの TOML データに `[imap]` セクションを追加する。`make test` をフェーズ 2 末尾で実行して回帰を即検出する |
| `ErrInvalidAllowedHost` の移動（`config.go` → `errors.go`）が外部の `errors.Is` を壊す | 同一パッケージ内で同じ変数名を維持するため `errors.Is` の互換性は保たれる。`go build ./...` で全パッケージのコンパイル成功を確認する |
| `TestLoadConfig_EmptyPath`（`cmd/`）の挙動変更によりテストが失敗する | フェーズ 5.3 で同テストを `ErrConfigPathEmpty` を確認するよう更新する |
| `LoadFile` の相対パス絶対化で CWD 依存のテストが不安定になる | TOML ファイルを `t.TempDir()` に配置し `store.root_dir = "./data"` のような相対パスを指定する。`filepath.Abs` はテスト実行時の CWD を基準にするため安定している |
| `buildIMAPConfig` が `make deadcode` に未到達として報告される | フェーズ 5.2 で `_ = buildIMAPConfig(cfg)` として呼び出すことで回避する |

---

## 8. 完了条件

- [ ] `make lint` がエラーなく完了する
- [ ] `make test` がすべて成功する
- [ ] `01_requirements.md` の全受け入れ条件（AC-01〜AC-17）に対応するテストが存在する
- [ ] セクション 5 の受け入れ条件トレーサビリティ表に実装ファイルの行番号が記入されている
- [ ] `make deadcode` を実行済みで意図しない未到達コードがない

---

## 9. 次のステップ

- **タスク 0070**: `cmd/tlsrpt-digest` の各サブコマンド（`fetch`・`gc`・`summary` 等）から `Config.IMAP`・`Config.Store` を使って各コンポーネントを初期化する統合を実装する。フェーズ 5.2 で追加する `buildIMAPConfig` をそこから呼び出す。
- **将来タスク（未定）**: 環境変数による TOML 設定値の上書き（`02_architecture.md` セクション 9 の拡張性考慮事項）。
