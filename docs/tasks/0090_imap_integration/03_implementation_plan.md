# 実装計画書：greenmail を使った IMAP 統合テスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-06-02 |
| レビュー日 | 2026-06-02 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

本実装は以下の目的で行う。

1. `internal/imap.Config` に `InsecureSkipVerify` フィールドを追加し、greenmail 統合テスト専用として自己署名証明書サーバへの TLS 接続を可能にする（本番設定経路からは設定しない）
2. greenmail を使った `imapClient` の IMAP 操作統合テストを追加する
3. UIDVALIDITY 変化の検出と recovery フロー End-to-End 統合テストを追加する
4. devcontainer および GitHub Actions で統合テストが実行できる環境を整備する

### 1.2 実装方針

アーキテクチャ設計書 §1.1 に定めた設計原則に従う。

- 本番コードの変更は `Config.InsecureSkipVerify` フィールド追加と `buildTLSConfig` への反映のみに限定する
- `InsecureSkipVerify: true` の設定はテストコードのみに限定する。実サーバ接続に使う設定は `//go:build integration` タグ付きファイルに限定し、単体テストでは `buildTLSConfig` へのフィールド反映だけを検証する
- 統合テストはすべて `//go:build integration` タグを付与し、通常の `go test ./...` では実行されない

### 1.3 既存コード調査結果

#### `internal/imap/imap.go`
- **現状**: `Config` 構造体に `InsecureSkipVerify` フィールドが存在しない
- **変更が必要**: フィールド追加（AC-01）

#### `internal/imap/client.go`
- **現状**: `buildTLSConfig` 内で `InsecureSkipVerify: false` をハードコードしている（`tls.Config` リテラルの `InsecureSkipVerify` フィールド）
- **変更が必要**: `cfg.InsecureSkipVerify` を参照するよう変更（AC-01）

#### `internal/imap/client_test.go`
- **現状**: `TestBuildTLSConfigCustomCA` と `TestBuildTLSConfigSystemCA` が `InsecureSkipVerify == false` を assert している。フィールド追加後もゼロ値テストとして有効（AC-02 を継続カバー）
- **変更が必要**: `InsecureSkipVerify: true` の新規テストケース追加（AC-01）

#### `internal/imap/client_integration_test.go`
- **現状**: 既存の `loadIntegrationConfig` ヘルパーが存在するが:
  1. `InsecureSkipVerify` を設定していない（greenmail への TLS 接続に失敗する）
  2. スキップ条件が `IMAP_TEST_HOST` のみ（テスト種別ごとの追加スキップ条件がない）
  3. 実際の IMAP 操作テストが `TestIntegration_EnvConfig` のみで存在しない
- **変更が必要**:
  - `loadIntegrationConfig` に `InsecureSkipVerify: true` を追加
  - テスト種別ごとのスキップ判定ヘルパー追加（`requireFixedUserEnv`、`requireSMTPEnv`）
  - SMTP 注入ヘルパー・受信者アドレス導出ヘルパー追加
  - F-002・F-003 の統合テスト追加

#### `internal/imap/testutil/mocks.go`
- **現状**: `//go:build test` タグ、`FakeMailFetcher` のみ。変更不要。
- メールボックス管理ヘルパーは `testutil/helpers.go` に追加する（後述）

#### `cmd/tlsrpt-digest/boot.go`
- **現状**: `buildIMAPConfig`（`boot.go` 内）が `imap.Config` を構築しているが `InsecureSkipVerify` を設定しない。本番動作として正しい。
- **変更不要**（AC-03 の確認対象）

#### `cmd/tlsrpt-digest/boot_test.go`
- **現状**: `TestBuildIMAPConfig` が `buildIMAPConfig` をテストしているが、`InsecureSkipVerify == false` の assertion がない
- **変更が必要**: `assert.False(t, got.InsecureSkipVerify)` の assertion 追加（AC-03）

#### `cmd/tlsrpt-digest/fetch.go`
- **現状**: `fetchRunner.newMailFetcher` フィールドが存在する。recovery E2E テストはこのフィールドに `InsecureSkipVerify: true` を設定するラッパーを注入する。
- **変更不要**（アーキテクチャ設計書 §3.3 参照）

#### `cmd/tlsrpt-digest/main_test.go` / `test_helpers.go`
- **現状**: `withCommandRunners`（`main_test.go`）と `SpyNotificationSink`（`test_helpers.go`）が `//go:build test` タグで存在する。`make test-integration`（`-tags test,integration`）実行時に両方読み込まれるため、recovery E2E テストから再利用できる。
- **変更不要**

#### `cmd/tlsrpt-digest/recovery_integration_test.go`
- **現状**: 存在しない
- **新規作成が必要**: F-004 の recovery E2E テスト（AC-11〜AC-13）

#### `Makefile`
- **現状**: `test-integration` ターゲットが `./internal/imap/...` のみを対象としている
  - 現在のコマンド: `go test -v -count=1 -tags test,integration ./internal/imap/...`
- **変更が必要**: `./cmd/tlsrpt-digest/...` の追加（AC-14、AC-15 の前提）

#### `.devcontainer/docker-compose.base.yml`
- **現状**（下記5箇所を変更する）:
  1. `dev-env.IMAP_TEST_PORT=3143`（平文 IMAP）→ `3993`（IMAPS）
  2. `dev-env.IMAP_TEST_PASS=imap-test@example.com`（SMTP 自動作成ユーザのパスワード）→ `imap-test`（事前登録固定ユーザのパスワード）
  3. `greenmail.GREENMAIL_OPTS` に `-Dgreenmail.users=imap-test:imap-test@example.com -Dgreenmail.users.login=email` を追加
  4. ポートマッピング `"127.0.0.1:3143:3143"` → `"127.0.0.1:3993:3993"`
  5. healthcheck の確認ポート `3143` → `3993`

#### `.github/workflows/ci.yml`
- **現状**: `integration-test` ジョブが存在しない。`check-changes` ジョブが `has-code-changes`・`has-devcontainer-changes` を出力する。
- **変更が必要**: `has-integration-changes` 出力追加と `integration-test` ジョブ追加（AC-14〜AC-16）

#### `.github/scripts/classify-changes.sh`
- **現状**: 存在しない。`ci.yml` の `Classify changed files` ステップに変更分類ロジックがインライン実装されている。
- **新規作成が必要**: 統合テスト実行判定を含む変更分類ロジックをシェルスクリプトとして切り出し、CI とテストから同じ実装を呼び出す（AC-16）

#### `.github/scripts/classify-changes_test.sh`
- **現状**: 存在しない。
- **新規作成が必要**: `classify-changes.sh` に代表的な変更ファイル一覧を入力し、`has-integration-changes` が期待どおり出力されることを検証する（AC-16）

---

## 2. 実装ステップ

### Phase 1: `Config.InsecureSkipVerify` フィールド追加と単体テスト（F-001）

#### 変更ファイル: `internal/imap/imap.go`

- [x] `Config` 構造体の末尾に `InsecureSkipVerify bool` フィールドを追加する。フィールドコメントは次の英語文字列とする:
  ```
  // InsecureSkipVerify, when true, disables TLS certificate verification.
  // Intended only for integration tests against self-signed servers
  // (e.g. greenmail). Never set from production configuration paths.
  ```

#### 変更ファイル: `internal/imap/client.go`

- [x] `buildTLSConfig` 内の `tls.Config` リテラルにある `InsecureSkipVerify: false,` を `InsecureSkipVerify: cfg.InsecureSkipVerify,` に変更する。
  - 変更前: `InsecureSkipVerify: false,`
  - 変更後: `InsecureSkipVerify: cfg.InsecureSkipVerify,`

- [x] `buildTLSConfig` の `tls.Config` リテラルに、`gosec` の `G402` を限定的に抑制する `//nolint:gosec` を付与する。理由コメントには「`InsecureSkipVerify` は設定ファイルに公開せず、AC-03 の `buildIMAPConfig` テストと integration-only 使用箇所チェックで本番経路への混入を防ぐ」ことを書く。抑制範囲は当該 `tls.Config` リテラルのみに限定し、パッケージ全体やファイル全体では抑制しない。

#### 変更ファイル: `internal/imap/client_test.go`

- [x] `TestBuildTLSConfigInsecureSkipVerify` テスト関数を追加する。関数の先頭に次のコメントを付ける: `// TestBuildTLSConfigInsecureSkipVerify verifies that InsecureSkipVerify is reflected in tls.Config (requirement F-001, AC-01).`
  - `buildTLSConfig(Config{InsecureSkipVerify: true})` を呼び、返値の `InsecureSkipVerify == true`、`RootCAs == nil`、`MinVersion == tls.VersionTLS12` を assert する

#### 変更ファイル: `cmd/tlsrpt-digest/boot_test.go`

- [x] `TestBuildIMAPConfig` に `assert.False(t, got.InsecureSkipVerify)` を追加する（他のフィールド検証の末尾に追加する）

**フェーズ完了の確認**:
- [x] `make test` が通過すること
- [x] `make lint` が通過すること

### PR-1 作成ポイント: internal API and unit tests

**対象ステップ**: Phase 1

**推奨タイトル**: `feat(0090): add InsecureSkipVerify to imap.Config and buildTLSConfig`

**レビュー観点**: `InsecureSkipVerify` のゼロ値が既存の証明書検証動作に影響しないこと / `buildTLSConfig` がフィールドを正しく `tls.Config` へ反映すること / `buildIMAPConfig` が `InsecureSkipVerify` を設定しないことをテストで保証していること / `gosec G402` の `//nolint` 抑制範囲がリテラル 1 箇所のみに限定されていること

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

### Phase 2: テストヘルパー整備（F-002・F-003 の前提）

#### 変更ファイル: `internal/imap/client_integration_test.go`

- [x] `loadIntegrationConfig` に `InsecureSkipVerify: true` を追加する（返す `Config` のフィールドとして設定する）

- [x] `requireFixedUserEnv(t *testing.T)` ヘルパーを追加する。スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_PORT`、`IMAP_TEST_USER`、`IMAP_TEST_PASS`、`IMAP_TEST_MAILBOX` のいずれかが空文字の場合に `t.Skip("fixed-user integration env not configured")` を呼ぶ

- [x] `requireSMTPEnv(t *testing.T)` ヘルパーを追加する。スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_PORT`、`IMAP_TEST_SMTP_HOST`、`IMAP_TEST_SMTP_PORT` のいずれかが空文字の場合に `t.Skip("SMTP integration env not configured")` を呼ぶ

- [x] `testRunID` を `sync.OnceValue` で実装する。`ulid.Make().String()` をバイナリ実行ごとに一度だけ生成し、非 INBOX メールボックス名のクロスラン衝突防止 suffix として使う

- [x] `testRecipientEmail() string` ヘルパーを追加する。`ulid.Make().String() + "@test.example.com"` を返す。テスト名を含めないことで RFC 5321 ローカルパート上限（64 バイト）を確実に遵守し、呼び出しごとに新規 ULID を生成するためテスト間の衝突も防ぐ

- [x] `testMessageID() string` ヘルパーを追加する。`"<" + ulid.Make().String() + "@test.example.com>"` を返す。呼び出しごとに新規 ULID を生成し一意性を保証する。`FetchMeta` の検証では go-imap/greenmail の正規化差異に備え、期待値と実測値の両方を `normalizeMessageID` で正規化してから比較する

- [x] `injectTestMail(t *testing.T, smtpAddr, recipient, subject, body, messageID string)` ヘルパーを追加する。`net/smtp` の `SendMail` を使い `from@test.example.com` から `recipient` 宛てにメールを送信する。`msg` パラメータは RFC 2822 準拠のヘッダ付き本文として組み立て、`From`、`To`、`Subject`、`Message-ID` ヘッダを必ず含め、各ヘッダ行は `\r\n` で区切り、ヘッダと本文の間に `\r\n\r\n` を置く。これにより `TestIntegration_Download` で `Subject:` ヘッダを、`TestIntegration_FetchMeta` で注入した `Message-ID` を検証できる。失敗時は `require.NoError` でテストを即座に停止させる

- [x] `normalizeMessageID(messageID string) string` ヘルパーを追加する。先頭末尾の空白を除去し、外側の `<` `>` の有無だけを正規化する。`TestIntegration_FetchMeta` は `result.Messages` から注入した Message-ID と正規化後に一致するメッセージを探して検証する

- [x] `loadSMTPTestConfig(t *testing.T) (cfg Config, smtpAddr string)` ヘルパーを追加する。`requireSMTPEnv(t)` を呼び出した後、次のように `Config` を構築して返す:
  - `Host`: `IMAP_TEST_HOST`、`Port`: 必須環境変数 `IMAP_TEST_PORT`
  - `Username`: `testRecipientEmail()`、`Password`: `config.Secret(testRecipientEmail())`（`*testing.T` パラメータなし）
    - greenmail は未登録の受信者宛て SMTP 配送を受け入れると、受信者アドレスをユーザ名・パスワードとしてメールボックスを自動作成する。これにより Username と Password に同じ受信者アドレスを使うことで IMAP ログインが成功する（`02_architecture.md` §6.1 参照）
  - `Mailbox`: `"INBOX"`、`InsecureSkipVerify`: `true`
  - `smtpAddr`: `IMAP_TEST_SMTP_HOST:IMAP_TEST_SMTP_PORT`

- [x] `testMailboxName(t *testing.T) string` ヘルパーを追加する。テスト名から英数字・ハイフン以外の文字をハイフンに置換し、まず先頭を 24 文字以内に切り詰めてから `"-" + testRunID()` の suffix を付加した文字列を返す（IMAP メールボックス名に `@` は使用できないため、`testRecipientEmail(t)` と別の実装が必要）。切り詰めは suffix を削らないよう**先頭部分を先に**制限する。`TestIntegration_UIDValidity_Change` での非 INBOX メールボックス名生成に使用する。

- [x] 環境変数判定をテスト可能にするため、`missingFixedUserEnv(getenv func(string) string) []string` と `missingSMTPEnv(getenv func(string) string) []string` を追加し、`requireFixedUserEnv` と `requireSMTPEnv` はその結果が空でない場合にだけ `t.Skip(...)` を呼ぶようにする。`IMAP_TEST_PORT` の値は整数として parse できることも検査対象に含める（`missingSMTPEnv` では `IMAP_TEST_SMTP_PORT` も同様に整数検証を行う）。

- [x] `TestIntegration_EnvRequirements` を追加する。`missingFixedUserEnv` と `missingSMTPEnv` にテスト用 `getenv` を渡し、`IMAP_TEST_HOST` 欠落、`IMAP_TEST_PORT` 欠落、`IMAP_TEST_PORT` 不正値、SMTP host/port 欠落、固定ユーザの user/pass/mailbox 欠落をそれぞれ検証する（AC-04）。

#### 新規ファイル: `internal/imap/testutil/helpers.go`

- [x] `//go:build test` タグでファイルを作成する。パッケージ名は `imaptestutil`。
- [x] `CreateMailbox(t *testing.T, cfg imap.Config, mailbox string)` を追加する。`crypto/tls` と `emersion/go-imap` の `imapclient.DialTLS` を使い、`buildTLSConfig` の代わりに `tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify, MinVersion: tls.VersionTLS12}` を渡してダイヤルする。`tls.Config` リテラルには `//nolint:gosec // InsecureSkipVerify is set only in integration tests via cfg.InsecureSkipVerify; production paths never set it true` コメントを付与する（`make lint --build-tags test` で gosec G402 が `testutil/helpers.go` に適用されるため）。接続成功直後に接続終了の `defer` を登録し、`cfg.Username` と `cfg.Password.Value()` で `LOGIN` 成功後に `LOGOUT` の `defer` を登録してから、`CREATE mailbox` コマンドを実行する。CREATE が失敗して `t.Fatal` へ進む場合でも、登録済みの `LOGOUT` と接続終了が実行されるようにする。
- [x] `DeleteMailbox(t *testing.T, cfg imap.Config, mailbox string)` を追加する。同じ接続・LOGIN・LOGOUT 手順で `DELETE mailbox` コマンドを実行する。対象メールボックスが存在しない場合も greenmail の応答をそのままテスト失敗として扱う。DELETE が失敗して `t.Fatal` へ進む場合でも、登録済みの `LOGOUT` と接続終了が実行されるようにする。

> **テストヘルパーファイルの分類**: `docs/dev/developer_guide/test_organization.md` の Classification A に従い、公開 API のみを使う cross-package helper として `internal/imap/testutil/helpers.go` に配置し、`//go:build test` タグを付与する。これにより `make lint`（`--build-tags test`）の静的解析対象に含まれ、linter によるコンパイルエラー早期検出が機能する。

**フェーズ完了の確認**:
- [x] `make test` が通過すること（統合テストはタグなしでスキップされること）
- [x] `go test -run '^$' -tags test,integration ./internal/imap/...` が通過すること（`client_integration_test.go` と `testutil/helpers.go` のコンパイルエラーを Phase 2 の段階で検出する。`-c -o /dev/null` は複数パッケージに対して使用できないため `-run '^$'` でテスト実行をスキップしつつコンパイルを確認する）
- [x] `make lint` が通過すること

### PR-2 作成ポイント: integration test helpers

**対象ステップ**: Phase 2

**推奨タイトル**: `feat(0090): add IMAP test helpers for SMTP injection and mailbox mgmt`

**レビュー観点**: テストヘルパーが `//go:build test` タグ（`testutil/helpers.go`）または `//go:build integration` タグ（`client_integration_test.go`）で本番ビルドから除外されていること / `testRunID` / `testRecipientEmail` の一意性設計が devcontainer 長時間稼働時の再実行衝突を防いでいること / `CreateMailbox` / `DeleteMailbox` の defer 登録順序（LOGIN 後に LOGOUT が登録されること）が正しいこと / `TestIntegration_EnvRequirements` が AC-04 の全必須環境変数を網羅していること

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

### Phase 3: IMAP クライアント操作統合テスト（F-002・F-003）

#### 変更ファイル: `internal/imap/client_integration_test.go`

##### `TestIntegration_EnvConfig` の更新（既存テスト）
- [ ] 先頭に `requireFixedUserEnv(t)` の呼び出しを追加する。
  - 注意: `loadIntegrationConfig` 自体も `IMAP_TEST_HOST` が未設定の場合に `t.Skip` を呼ぶ。`requireFixedUserEnv` を先頭に加えることで `IMAP_TEST_USER`・`IMAP_TEST_PASS`・`IMAP_TEST_MAILBOX` が未設定の場合にもスキップするようになる。これにより `loadIntegrationConfig` 内スキップと `requireFixedUserEnv` のスキップが重複するが、いずれも `IMAP_TEST_HOST` を含むため「IMAP_TEST_HOST 未設定」は `requireFixedUserEnv` が先に検出してスキップする。実装後は `loadIntegrationConfig` 内の `t.Skip` を `requireFixedUserEnv` に集約することも検討できる（ただし後方互換性のある変更に限定する）。
- [ ] `require.True(t, cfg.InsecureSkipVerify)` の assertion を追加する（`loadIntegrationConfig` の変更確認）

##### AC-05: 空メールボックス接続テスト
- [ ] `TestIntegration_EmptyInbox` を追加する。先頭に次のコメントを付ける: `// TestIntegration_EmptyInbox verifies FetchMeta on an empty fixed-user mailbox (requirement F-002, AC-05).`
  - `requireFixedUserEnv(t)` を呼ぶ
  - `loadIntegrationConfig(t)` で `imap.Config` を構築する
  - `imap.NewIMAPClient(cfg)` で接続し、直後に `t.Cleanup(func() { client.Close() })` を登録する
  - `FetchMeta(ctx, time.Now().AddDate(-1, 0, 0))` を呼ぶ
  - `len(result.Messages) == 0` かつ `result.UIDValidity > 0` を `require.Empty` / `require.Positive` で検証する

##### AC-06: FetchMeta でメタ情報取得
- [ ] `TestIntegration_FetchMeta` を追加する。先頭コメント: `// TestIntegration_FetchMeta verifies FetchMeta retrieves metadata of an injected message (requirement F-002, AC-06).`
  - `loadSMTPTestConfig(t)` を呼ぶ（`cfg.Username` は `testRecipientEmail(t)` から生成したテスト専用の動的ユーザアドレスであり、固定ユーザ `IMAP_TEST_USER` とは別物である）
  - `messageID := testMessageID(t)` を作成し、`injectTestMail(t, smtpAddr, cfg.Username, "fetch-meta-test", "test body", messageID)` でメール注入する
  - `imap.NewIMAPClient(cfg)` で接続し、直後に `t.Cleanup(func() { client.Close() })` を登録する（`cfg.Username` = 受信者アドレス、`cfg.Password` = 受信者アドレス）
  - `context.Background()` と `time.Now().AddDate(-1, 0, 0)` を使って `FetchMeta` を実行する
  - `result.Messages` から `normalizeMessageID(meta.MessageID) == normalizeMessageID(messageID)` のメッセージを 1 件だけ見つけ、その `UID > 0`、`Size > 0` を検証する。メールボックス全体の `len(result.Messages) == 1` は assert しない（同一 greenmail run に過去メールが残る可能性に備える）

##### AC-07: Download でメール本文取得
- [ ] `TestIntegration_Download` を追加する。先頭コメント: `// TestIntegration_Download verifies Download retrieves full message body (requirement F-002, AC-07).`
  - `loadSMTPTestConfig(t)` を呼ぶ（`requireSMTPEnv` によるスキップ判定と `cfg`/`smtpAddr` の取得。AC-06 と同様にテスト専用の動的ユーザアドレスを使用する）
  - `injectTestMail` で subject を `"download-test"`、messageID を `testMessageID(t)` としてメール注入する
  - `imap.NewIMAPClient(cfg)` で接続し、直後に `t.Cleanup(func() { client.Close() })` を登録する
  - `context.Background()` と `time.Now().AddDate(-1, 0, 0)` を使って `FetchMeta` を呼び、注入した `Message-ID` に一致するメッセージの UID を取得する
  - 同じ context で `Download(ctx, []uint32{uid})` を実行する
  - 返されたバイト列に `Subject: download-test` が含まれることを `require.Contains` で検証する（`injectTestMail` が RFC 2822 ヘッダを正しく構築した結果、Subject ヘッダが本文に含まれる）

##### AC-08: MarkSeen で Seen フラグ付与
- [ ] `TestIntegration_MarkSeen` を追加する。先頭コメント: `// TestIntegration_MarkSeen verifies MarkSeen sets the Seen flag (requirement F-002, AC-08).`
  - `loadSMTPTestConfig(t)` を呼ぶ（`requireSMTPEnv` によるスキップ判定と `cfg`/`smtpAddr` の取得。AC-06 と同様にテスト専用の動的ユーザアドレスを使用する）
  - メール注入後、`imap.NewIMAPClient(cfg)` で接続し直後に `t.Cleanup(func() { client.Close() })` を登録してから、`context.Background()` と `time.Now().AddDate(-1, 0, 0)` を使って `FetchMeta` を呼び、注入した `Message-ID` に一致するメッセージを選択して `Seen == false` を確認する
  - 同じ context で `MarkSeen(ctx, []uint32{uid})` を実行する
  - 別セッションで `imap.NewIMAPClient(cfg)` を再接続し直後に `t.Cleanup(func() { client2.Close() })` を登録してから、同じ since 値で `FetchMeta` を呼んで、同じ `Message-ID` のメッセージが `Seen == true` になっていることを検証する

##### AC-09: UIDValidity 安定性確認
- [ ] `TestIntegration_UIDValidity_Stable` を追加する。先頭コメント: `// TestIntegration_UIDValidity_Stable verifies UIDValidity is stable across consecutive FetchMeta calls (requirement F-002, AC-09).`
  - `requireFixedUserEnv(t)` を呼ぶ
  - `imap.NewIMAPClient(cfg)` で接続し、直後に `t.Cleanup(func() { client.Close() })` を登録する
  - `context.Background()` と `time.Now().AddDate(-1, 0, 0)` を使い、同一 INBOX に対して `FetchMeta` を 2 回呼ぶ
  - 両呼び出しの `UIDValidity` が同一であることを `require.Equal` で検証する

##### AC-10: UIDValidity 変化検出
- [ ] `TestIntegration_UIDValidity_Change` を追加する。先頭コメント: `// TestIntegration_UIDValidity_Change verifies UIDValidity changes after mailbox DELETE and CREATE (requirement F-003, AC-10).`
  - `requireFixedUserEnv(t)` を呼ぶ
  - `loadIntegrationConfig(t)` で固定ユーザの接続設定 `fixedCfg` を取得する
  - `testMailboxName(t)` でテスト固有の非 INBOX メールボックス名を導出する（`@` を含まない IMAP 互換の名前。Phase 2 で `client_integration_test.go` に追加する `testMailboxName` ヘルパーを使用する）
  - `imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` でメールボックスを作成し、`t.Cleanup(func() { imaptestutil.DeleteMailbox(t, fixedCfg, mailbox) })` を登録する
  - `fixedCfg` の `Mailbox` フィールドを当該非 INBOX メールボックス名に設定した新しい `Config` で `imap.NewIMAPClient` を呼んで接続し、直後に `t.Cleanup(func() { client1.Close() })` を登録してから `FetchMeta` で `V1 := result.UIDValidity` を取得する。取得後すぐに `client1.Close()` を呼ぶ（メールボックスが選択状態のまま別セッションから `DELETE` を実行するとサーバが拒否する場合があるため、削除前に接続を切る）
  - `imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)` で削除し、`imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` で同名で再作成する。クリーンアップは初回作成後に登録した 1 つだけにし、テスト終了時に最終的な再作成済みメールボックスを削除する
  - 再度 `imap.NewIMAPClient` で接続し直後に `t.Cleanup(func() { client2.Close() })` を登録してから `FetchMeta` を呼び `V2 := result.UIDValidity` を取得し、`require.NotEqual(t, V1, V2)` で検証する

**フェーズ完了の確認**:
- [ ] greenmail IMAPS 環境（Phase 5 の devcontainer 更新後、または同等の手動設定）で `go test -v -count=1 -tags test,integration ./internal/imap/...` が通過すること

> **手動設定での検証方法**: Phase 5 の devcontainer 変更前に検証する場合は、greenmail を `GREENMAIL_OPTS="-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.users=imap-test:imap-test@example.com -Dgreenmail.users.login=email"` で起動し、環境変数 `IMAP_TEST_PORT=3993`・`IMAP_TEST_PASS=imap-test`・`IMAP_TEST_SMTP_PORT=3025` を設定すること（Phase 5 の `.devcontainer/docker-compose.base.yml` 変更内容と同等）。

### PR-3 作成ポイント: IMAP client integration tests

**対象ステップ**: Phase 3

**推奨タイトル**: `test(0090): add greenmail integration tests for imapClient operations`

**レビュー観点**: 各テストが独立して実行でき SMTP 注入受信者アドレスの一意性によりテスト間のメッセージ混入が防がれていること / `TestIntegration_UIDValidity_Change` が固有名の非 INBOX メールボックスを使用していること / `normalizeMessageID` による Message-ID 正規化比較が go-imap/greenmail の実装差異を吸収できること / `//go:build integration` タグが全テスト関数に適用されていること

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

### Phase 4: recovery フロー End-to-End 統合テスト（F-004）

#### 新規ファイル: `cmd/tlsrpt-digest/recovery_integration_test.go`

- [ ] `//go:build integration` タグ付きでファイルを作成する。パッケージ名は `main`。

- [ ] `loadRecoveryTestEnv(t *testing.T)` ヘルパーを追加する:
  - スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_PORT`、`IMAP_TEST_USER`、`IMAP_TEST_PASS`、`IMAP_TEST_MAILBOX` のいずれかが空文字の場合に `t.Skip("recovery integration env not configured")` を呼ぶ
  - `t.Setenv("TLSRPT_IMAP_USERNAME", os.Getenv("IMAP_TEST_USER"))` と `t.Setenv("TLSRPT_IMAP_PASSWORD", os.Getenv("IMAP_TEST_PASS"))` で fetch サブコマンドが読む IMAP 認証情報を設定する

- [ ] `missingRecoveryEnv(getenv func(string) string) []string` ヘルパーを追加し、`loadRecoveryTestEnv` はその結果が空でない場合にだけ `t.Skip(...)` を呼ぶようにする。`IMAP_TEST_PORT` の値は整数として parse できることも検査対象に含める。

- [ ] `TestIntegration_RecoveryEnvRequirements` を追加する。`missingRecoveryEnv` にテスト用 `getenv` を渡し、`IMAP_TEST_HOST` 欠落、`IMAP_TEST_PORT` 欠落、`IMAP_TEST_PORT` 不正値、`IMAP_TEST_USER` 欠落、`IMAP_TEST_PASS` 欠落、`IMAP_TEST_MAILBOX` 欠落を検証する。加えて `loadRecoveryTestEnv` 相当の credential propagation が `TLSRPT_IMAP_USERNAME` / `TLSRPT_IMAP_PASSWORD` に固定ユーザの値を設定することを検証する（AC-04）。

- [ ] `buildTestConfigTOML(t *testing.T, rootDir, imapHost string, imapPort int, mailbox string) string` ヘルパーを追加する:
  - 一時ディレクトリに `config.toml` を書き込み、そのパスを返す
  - TOML の内容: `[imap]` セクションに `host`・`port`・`mailbox` を設定し、`[store]` セクションに `root_dir = rootDir` を設定する
  - `[notify.slack]` セクションに `allowed_host = ""` を設定する（Slack 通知を使用しないため）
  - 動作上の注意: `allowed_host = ""` かつ `dry-run=true` のとき、`setupNotifyHandlers` が呼ぶ `notify.BuildHandlers` は空の webhookURL をスキップして dry-run ハンドラのみを生成するため、Slack URL なしでも Bootstrap が正常に完了する（`boot.go` の `setupNotifyHandlers` 実装参照）

- [ ] `insecureMailFetcherFactory(cfg imap.Config) (imap.MailFetcher, error)` ヘルパーを追加する。先頭コメント: `// insecureMailFetcherFactory wraps imap.NewIMAPClient with InsecureSkipVerify=true for integration tests.`
  - 受け取った `cfg` の `InsecureSkipVerify` を `true` に設定してから `imap.NewIMAPClient(cfg)` を呼ぶ

- [ ] `testRunID()` と `testMailboxName(t *testing.T) string` ヘルパーを追加する。`testRunID` は Phase 2 と同じ方針で run ごとに一意な suffix を返す。`testMailboxName` はテスト名から英数字・ハイフン以外の文字をハイフンに置換し、プレフィックスを 24 文字以内に切り詰めてから `"-" + testRunID()` の suffix を付加して返す（suffix が 32 文字上限によって切り捨てられないよう、先にプレフィックスを切り詰める）。失敗・中断した過去 run のメールボックス名と衝突しないようにする

- [ ] `TestIntegration_Recovery_KeepOld` を追加する。先頭コメント: `// TestIntegration_Recovery_KeepOld verifies fetch detects UIDVALIDITY change and recover --mode keep-old resolves it (requirement F-004, AC-11, AC-12).`
  アーキテクチャ設計書 §6.2 のシーケンス図に従い次の手順を実装する:
  1. `loadRecoveryTestEnv(t)` を呼ぶ
  2. `t.TempDir()` でストア root を作成する
  3. 固定ユーザ接続設定（`IMAP_TEST_HOST`/`IMAP_TEST_PORT`/`InsecureSkipVerify:true`）を構築する
  4. `testMailboxName(t)` で固有の非 INBOX メールボックス名を決定する
  5. `imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` でメールボックスを作成する（`t.Cleanup` で削除登録）
  6. `buildTestConfigTOML(t, rootDir, ...)` で設定ファイルを作成する
  7. `fr := newFetchRunner(); fr.newMailFetcher = insecureMailFetcherFactory` でランナーを構成する
  8. `withCommandRunners(t, map[SubcommandName]SubcommandRunner{subcommandFetch: fr, subcommandRecover: newRecoverRunner()})` でコマンドランナーを差し替える
  9. `runCLI` で `fetch -config <configPath> -dry-run` を実行し `exitOK` を検証する（UIDVALIDITY 初回記録）。`-dry-run` フラグは Slack 等の HTTP 通知リクエストをスキップするためのものであり、ストアへの書き込み（`SaveUIDValidity` 等）は dry-run でも通常どおり実行される。空メールボックスに対しては通知が発生しないため `-dry-run` がなくても動作するが、明示的に付与することで Slack 設定なしで Bootstrap が成功することを保証する（`buildTestConfigTOML` の `allowed_host = ""` と合わせて Slack URL 不要にする設計）
  10. `imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)` と `imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` で UIDVALIDITY を変化させる
  11. `runCLI` で `fetch -config <configPath> -dry-run` を再実行し終了コードが `exitError` であることを検証する（AC-11 (1)）。`-dry-run` を付与することで `allowed_host = ""` の設定でも Bootstrap が正常に完了し、UIDVALIDITY 不一致が原因の `exitError` であることを確認できる
  12. `store.Open(rootDir, store.IMAPIdentity{Host: imapHost, Port: imapPort, Mailbox: mailbox}, store.OpenReadOnly)` でストアを開き `LoadRecoveryRequired` を呼び `found == true` を検証する（AC-11 (2)）。`IMAPIdentity` の `Host`・`Port`・`Mailbox` は `buildTestConfigTOML` に渡した値と必ず一致させること。`store.Store` には `Close` メソッドがないため、存在しない close 処理を追加しないこと
  13. `runCLI` で `recover -config <configPath> -mode keep-old` を実行し `exitOK` を検証する（AC-12）
  14. `runCLI` で `fetch -config <configPath> -dry-run` を再実行し `exitOK` を検証する（recovery 解消の確認）

- [ ] `TestIntegration_Recovery_DiscardOld` を追加する。先頭コメント: `// TestIntegration_Recovery_DiscardOld verifies recover --mode discard-old --yes resolves UIDVALIDITY mismatch (requirement F-004, AC-11, AC-13).`
  - `TestIntegration_Recovery_KeepOld` と同じ手順でステップ 1〜12 を実施する。メールボックス名は `testMailboxName(t)` で生成するためテスト関数名が異なれば自動的に別の固有名になる（ステップ 11 の fetch 再実行も `-dry-run` 付きで行う）
  - ステップ 13 を `runCLI` で `recover -config <configPath> -mode discard-old -yes` に変更し `exitOK` を検証する（AC-13）
  - ステップ 14 は同様に `fetch -config <configPath> -dry-run` が `exitOK` を返すことを検証する

**フェーズ完了の確認**:
- [ ] greenmail IMAPS 環境（Phase 5 の devcontainer 更新後、または同等の手動設定）で `go test -v -count=1 -tags test,integration ./cmd/tlsrpt-digest/...` が通過すること

> **手動設定での検証方法**: Phase 5 の devcontainer 変更前に検証する場合は、Phase 3 と同様に `GREENMAIL_OPTS`・`IMAP_TEST_PORT=3993`・`IMAP_TEST_PASS=imap-test`・`IMAP_TEST_SMTP_PORT=3025` を設定した greenmail を起動すること。

### PR-4 作成ポイント: recovery E2E integration tests

**対象ステップ**: Phase 4

**推奨タイトル**: `test(0090): add greenmail E2E integration tests for recovery flow`

**レビュー観点**: `insecureMailFetcherFactory` による依存注入が本番の `buildIMAPConfig` を変更せずに greenmail へ接続できていること / 非ゼロ終了コード（AC-11(1)）とストアの recovery-required 状態（AC-11(2)）の両方を検証していること / `t.Parallel()` を使用しておらず `withCommandRunners` のグローバル変数変更による並行実行干渉がないこと / `testMailboxName` が各テスト間でメールボックス名の衝突を防いでいること

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

### Phase 5: インフラ整備（F-005）

#### 変更ファイル: `.devcontainer/docker-compose.base.yml`

- [ ] `dev-env` サービスの `IMAP_TEST_PORT` を変更する:
  - 変更前: `- IMAP_TEST_PORT=3143`
  - 変更後: `- IMAP_TEST_PORT=3993`

- [ ] `dev-env` サービスの `IMAP_TEST_PASS` を変更する:
  - 変更前: `- IMAP_TEST_PASS=imap-test@example.com`
  - 変更後: `- IMAP_TEST_PASS=imap-test`

- [ ] `greenmail` サービスの `GREENMAIL_OPTS` を変更する:
  - 変更前: `- GREENMAIL_OPTS=-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0`
  - 変更後: `- GREENMAIL_OPTS=-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.users=imap-test:imap-test@example.com -Dgreenmail.users.login=email`

- [ ] `greenmail` サービスのポートマッピングを変更する:
  - 変更前: `- "127.0.0.1:3143:3143"  # IMAP`
  - 変更後: `- "127.0.0.1:3993:3993"  # IMAPS`

- [ ] `greenmail` サービスの healthcheck コマンドを変更する（devcontainer は Docker Compose なので `healthcheck.test:` キーで記述する）:
  - 変更前: `test: ["CMD", "bash", "-c", "echo > /dev/tcp/localhost/3143"]`
  - 変更後: `test: ["CMD", "bash", "-c", "echo > /dev/tcp/localhost/3993"]`
  - 注: `bash` の `/dev/tcp` 機能は `nc` と異なり JRE ベースイメージに含まれる `bash` で動作する（元の devcontainer でも同コマンドを使用しており実績あり）。devcontainer は Docker Compose 形式のため `healthcheck.test:` キーが有効。GitHub Actions CI の service container（`options: --health-cmd ...`）とは記述形式が異なる

#### 変更ファイル: `Makefile`

- [ ] `test-integration` ターゲットのコマンドを変更する:
  - 変更前: `go test -v -count=1 -tags test,integration ./internal/imap/...`
  - 変更後: `go test -v -count=1 -tags test,integration ./internal/imap/... ./cmd/tlsrpt-digest/...`

- [ ] `Makefile` 内の `test-integration` に関するコメントを更新する:
  - 変更前: `# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).`（以下2行のコメント）
  - 変更後: `# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).\n# Covers internal/imap and cmd/tlsrpt-digest integration tests.`

#### 変更ファイル: `.github/workflows/ci.yml`

- [ ] `check-changes` ジョブの `outputs:` に `has-integration-changes: ${{ steps.check.outputs.has-integration-changes }}` を追加する

- [ ] `check-changes` ジョブの `Classify changed files` ステップを、下記 `classify-changes.sh` を呼び出す形へ変更する。インラインの変更分類ロジックを増やさず、CI とテストが同じスクリプトを使うようにする。具体的には `changed_files` を一時ファイルへ書き出し、`GITHUB_OUTPUT` が設定された状態で `.github/scripts/classify-changes.sh <tmp-file>` を実行し、スクリプト自身が `$GITHUB_OUTPUT` へ `has-code-changes`、`has-devcontainer-changes`、`has-integration-changes` を追記する

- [ ] 既存の docs-only / non-code 変更通知ステップ（存在する場合）の文言を更新し、`.devcontainer/` 変更など `has-code-changes=false` でも `has-integration-changes=true` の場合は `integration-test` が実行されることを表示する。通知条件も `has-code-changes == 'false' && has-integration-changes == 'false'` のように、統合テストが走るケースを「全テスト skipped」と誤表示しない形に変更する

- [ ] `integration-test` ジョブを追加する:
  - `needs: check-changes` を設定する
  - 実行条件: `if: needs.check-changes.outputs.has-integration-changes == 'true'`
  - greenmail service container（`greenmail/standalone:2.1.3`）を設定する:
    - `ports: ["3993:3993", "3025:3025"]`
    - `env.GREENMAIL_OPTS`: `-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.users=imap-test:imap-test@example.com -Dgreenmail.users.login=email`
    - ヘルスチェックは `options:` フィールドに Docker の `--health-*` フラグで指定する（GitHub Actions の `services` は `healthcheck:` キーをサポートしないため）: `options: --health-cmd "bash -c 'echo > /dev/tcp/localhost/3993'" --health-interval 5s --health-timeout 3s --health-retries 10 --health-start-period 10s`
  - 環境変数（ジョブレベル `env:` に設定）:
    - `IMAP_TEST_HOST: localhost`
    - `IMAP_TEST_PORT: "3993"`
    - `IMAP_TEST_SMTP_HOST: localhost`
    - `IMAP_TEST_SMTP_PORT: "3025"`
    - `IMAP_TEST_USER: imap-test@example.com`
    - `IMAP_TEST_PASS: imap-test`
    - `IMAP_TEST_MAILBOX: INBOX`
  - ステップ: Checkout（`actions/checkout@v4`）→ Setup Go（`actions/setup-go@v5`、`go-version-file: go.mod`）→ `make test-integration`

#### 新規ファイル: `.github/scripts/classify-changes.sh`

- [ ] `changed_files`（改行区切り）を標準入力または第 1 引数のファイルパスから受け取り、`has-code-changes`、`has-devcontainer-changes`、`has-integration-changes` を算出するスクリプトを作成する。`GITHUB_OUTPUT` が設定されている場合はそのファイルへ `key=value` を追記し、未設定の場合は stdout に同じ `key=value` を出力する。既存 `ci.yml` の `has-code-changes`・`has-devcontainer-changes` 判定と同じ挙動を維持する

- [ ] `has-integration-changes` の判定条件は、Go ソース（`*.go`）、`Makefile`、`.github/workflows/` 以下のファイル、`.github/scripts/` 以下のファイル、`.devcontainer/` 以下のファイル、`testdata/` 以下のファイルのいずれかが変更された場合に `true` とする。その他の docs-only 変更では `false` とする（`.github/scripts/` を含めることで、変更分類スクリプト自体の変更が integration-test ジョブをスキップしてマージされることを防ぐ）

#### 新規ファイル: `.github/scripts/classify-changes_test.sh`

- [ ] `classify-changes.sh` のテストスクリプトを作成する。以下の代表ケースをそれぞれ入力し、`has-code-changes`、`has-devcontainer-changes`、`has-integration-changes` の 3 出力すべての期待値を検証する:
  - `internal/imap/client.go` → code `true` / devcontainer `false` / integration `true`
  - `Makefile` → code `true` / devcontainer `false` / integration `true`
  - `.github/workflows/ci.yml` → code `true` / devcontainer `false` / integration `true`
  - `.github/scripts/classify-changes.sh` → code `true` / devcontainer `false` / integration `true`
  - `.devcontainer/docker-compose.base.yml` → code `false` / devcontainer `true` / integration `true`
  - `testdata/tlsrpt_google.eml` → code `true` / devcontainer `false` / integration `true`
  - `docs/overview.md` のみ → code `false` / devcontainer `false` / integration `false`
  - `LICENSE` のみ → code `false` / devcontainer `false` / integration `false`

- [ ] `classify-changes_test.sh` を `bash .github/scripts/classify-changes_test.sh` で実行できるようにする。失敗時は非ゼロ終了し、期待値と実際の output を表示する

#### 新規ファイル: `.github/scripts/verify-integration-workflow.go`

- [ ] `.github/workflows/ci.yml` を検証するスクリプトを作成する。`go run .github/scripts/verify-integration-workflow.go` で実行できる Go スクリプトとして実装し（`ruby` は Go 開発コンテナに含まれないため）、`gopkg.in/yaml.v3` などの構造化パーサで YAML を読み、文字列 grep だけに依存しない。ファイル先頭に `//go:build ignore` を付与して通常ビルドから除外する

- [ ] `integration-test` ジョブについて、`needs: check-changes`、`if: needs.check-changes.outputs.has-integration-changes == 'true'`、greenmail service image `greenmail/standalone:2.1.3`、ports `3993:3993` と `3025:3025`、`GREENMAIL_OPTS` の固定ユーザ設定、ジョブ env の `IMAP_TEST_*` 全項目、`make test-integration` 実行ステップを検証する（AC-14）

- [ ] 通常の `test` ジョブについて、greenmail service を持たないこと、`make test` を実行すること、`-tags integration` または `test-integration` を含まないことを検証する（AC-15）

- [ ] docs-only / non-code 変更通知ステップが存在する場合、その条件と表示文言が `has-integration-changes` を考慮しており、統合テストが実行される変更を「全テスト skipped」と表示しないことを検証する

**フェーズ完了の確認**:
- [ ] `bash .github/scripts/classify-changes_test.sh` が通過すること
- [ ] `go run .github/scripts/verify-integration-workflow.go` が通過すること
- [ ] `actionlint .github/workflows/ci.yml` が通過すること
- [ ] devcontainer で `make test-integration` が通過すること
- [ ] PR で `.github/workflows/ci.yml` の変更を含む変更を push し、GitHub Actions の `integration-test` ジョブが起動することを確認する

### PR-5 作成ポイント: CI and devcontainer infrastructure

**対象ステップ**: Phase 5

**推奨タイトル**: `feat(0090): add CI integration-test job and update devcontainer to IMAPS`

**レビュー観点**: devcontainer と CI の greenmail 設定（ポート・固定ユーザ GREENMAIL_OPTS）が一致していること / `classify-changes.sh` の `has-integration-changes` 判定条件が devcontainer・testdata の変更も捕捉し既存 `has-code-changes` ロジックと整合していること / `verify-integration-workflow.go` が文字列 grep ではなく構造化 YAML パーサで検証していること / 通常 `test` ジョブが greenmail なし・integration タグなしのまま維持されていること

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

## 3. 実装順序とマイルストーン

### 3.1 マイルストーン

| マイルストーン | フェーズ | 内容 | 成果物 |
|---|---|---|---|
| M1 | Phase 1 | F-001 本番コード変更と単体テスト | `Config.InsecureSkipVerify` フィールドと AC-01〜03 をカバーするテスト |
| M2 | Phase 2 | テストヘルパー整備 | SMTP 注入・メールボックス管理・スキップ判定ヘルパー |
| M3 | Phase 3 | F-002・F-003 統合テスト | `client_integration_test.go` の AC-05〜10（greenmail IMAPS 環境で動作確認済み）|
| M4 | Phase 4 | F-004 recovery E2E テスト | `recovery_integration_test.go` の AC-11〜13（greenmail IMAPS 環境で動作確認済み）|
| M5 | Phase 5 | F-005 インフラ整備 | devcontainer・Makefile・CI ジョブ・変更分類/ workflow 検証テスト（GitHub Actions で動作確認済み）|

Phase 1 は他フェーズの前提（TLS 設定の基盤）であり最初に実施する。Phase 3・4 の実サーバ検証は greenmail IMAPS 環境を必要とするため、Phase 5 の devcontainer 更新後に実行するか、同等の手動 greenmail 設定で実行する。

### 3.2 PR 構成

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | Phase 1 | `Config.InsecureSkipVerify` フィールド追加・`buildTLSConfig` 反映・単体テスト追加（AC-01〜03） |
| PR-2 | Phase 2 | SMTP 注入・メールボックス管理・スキップ判定テストヘルパー整備（`client_integration_test.go`・`testutil/helpers.go`） |
| PR-3 | Phase 3 | IMAP クライアント操作統合テスト追加（AC-05〜10） |
| PR-4 | Phase 4 | recovery フロー End-to-End 統合テスト追加（AC-11〜13） |
| PR-5 | Phase 5 | devcontainer ポート変更・Makefile 拡張・CI `integration-test` ジョブ追加・変更分類スクリプト整備（AC-14〜16） |

---

## 4. テスト戦略

### 4.1 単体テスト

`make test`（`-tags test`）で実行する。

| テスト | ファイル | 検証 AC |
|---|---|---|
| `TestBuildTLSConfigInsecureSkipVerify`（新規） | `internal/imap/client_test.go` | AC-01 |
| `TestBuildTLSConfigCustomCA`（既存、変更なし） | `internal/imap/client_test.go` | AC-02 |
| `TestBuildTLSConfigSystemCA`（既存、変更なし） | `internal/imap/client_test.go` | AC-02 |
| `TestBuildIMAPConfig`（`InsecureSkipVerify` assertion 追加） | `cmd/tlsrpt-digest/boot_test.go` | AC-03 |

既存テストを削除・置換しない。`InsecureSkipVerify` フィールド追加後もゼロ値（`false`）の既存 assert は引き続き有効である。

### 4.2 統合テスト

`make test-integration`（`-tags test,integration`）で実行する。greenmail が起動している環境（devcontainer または GitHub Actions）が必要。

| テスト | ファイル | 検証 AC |
|---|---|---|
| `TestIntegration_EnvConfig`（更新） | `internal/imap/client_integration_test.go` | AC-04 |
| `TestIntegration_EmptyInbox`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-05 |
| `TestIntegration_FetchMeta`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-06 |
| `TestIntegration_Download`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-07 |
| `TestIntegration_MarkSeen`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-08 |
| `TestIntegration_UIDValidity_Stable`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-09 |
| `TestIntegration_UIDValidity_Change`（新規） | `internal/imap/client_integration_test.go` | AC-04, AC-10 |
| `TestIntegration_Recovery_KeepOld`（新規） | `cmd/tlsrpt-digest/recovery_integration_test.go` | AC-04, AC-11, AC-12 |
| `TestIntegration_Recovery_DiscardOld`（新規） | `cmd/tlsrpt-digest/recovery_integration_test.go` | AC-04, AC-11, AC-13 |

### 4.3 後方互換性テスト

- `make test` で既存テストがすべて通過することを確認する（`InsecureSkipVerify` ゼロ値の動作は既存テストでカバー済み）
- `make lint` で新規コードが linter 基準を満たすことを確認する

---

## 5. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| greenmail のバージョン差異（UIDVALIDITY 変化の RFC 非保証） | AC-10・AC-11 が失敗しうる | `docker-compose.base.yml` と CI で `2.1.3` に固定済み（`01_requirements.md` §6 既知リスク参照）|
| `IMAP_TEST_PASS` 変更（`imap-test@example.com` → `imap-test`）によるローカル環境への影響 | devcontainer 再起動前は旧パスワードが残り、`TestIntegration_EmptyInbox` などが認証失敗 | Phase 5 実施時に devcontainer を再起動することをコミットメッセージに明記する |
| CI での greenmail service container のヘルスチェック遅延 | 統合テスト接続失敗 | healthcheck の `start_period: 10s`・`retries: 10` を設定する（§2 Phase 5 参照）|
| `withCommandRunners` の利用（グローバル変数変更）による並行実行干渉 | テスト結果の不安定化 | recovery E2E テストでは `t.Parallel()` を使用しない |

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
- `make test-integration` が devcontainer と GitHub Actions の両環境で通過する
- `make lint` が通過する

### 品質指標
- 各 AC（AC-01〜AC-16）に対応するテストまたは静的検証が少なくとも 1 つ存在する
- `InsecureSkipVerify: true` が本番コードパスに混入していないことを `rg -n "InsecureSkipVerify.*true" internal/config/ cmd/tlsrpt-digest/boot.go` の結果が 0 件であることで確認できる

### セキュリティ検証
- `buildIMAPConfig` が `InsecureSkipVerify` を設定しないことを `TestBuildIMAPConfig` の assertion が保証する（AC-03）
- `InsecureSkipVerify: true` の実サーバ接続への使用箇所が `//go:build integration` タグ付きファイルのみに限定されることを確認する。`internal/imap/client_test.go::TestBuildTLSConfigInsecureSkipVerify` のように `buildTLSConfig` のフィールド反映だけを検証する単体テストは許容する

### ドキュメント完成
- `Makefile` のコメントが更新された `test-integration` 対象パッケージ（`./internal/imap/...` と `./cmd/tlsrpt-digest/...`）を反映している
- 運用・開発者向けの現行ドキュメントに `3143` の記述が新たに追加されていないこと。0090 タスク文書内の変更前後説明と `docs/tasks/0010_imap/` 以下の旧タスク文書における既存の `3143` 記述は許容する

---

## 8. 受け入れ条件検証

| AC | 検証方法 |
|---|---|
| AC-01 | `internal/imap/client_test.go::TestBuildTLSConfigInsecureSkipVerify`（`require.True(t, cfg.InsecureSkipVerify)`）|
| AC-02 | `internal/imap/client_test.go::TestBuildTLSConfigSystemCA`（`require.False(t, cfg.InsecureSkipVerify)`、`Config{}` でのゼロ値確認が主要カバレッジ）、`internal/imap/client_test.go::TestBuildTLSConfigCustomCA`（`require.False(t, cfg.InsecureSkipVerify)`、CA 設定時もゼロ値のままであることの補完的確認）|
| AC-03 | `cmd/tlsrpt-digest/boot_test.go::TestBuildIMAPConfig`（`assert.False(t, got.InsecureSkipVerify)`）|
| AC-04 | `internal/imap/client_integration_test.go::TestIntegration_EnvRequirements`（`IMAP_TEST_HOST` 欠落、`IMAP_TEST_PORT` 欠落、不正 port、SMTP host/port 欠落、固定ユーザ user/pass/mailbox 欠落を検証）、`cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_RecoveryEnvRequirements`（recovery 用 host/port/user/pass/mailbox 欠落、不正 port、`TLSRPT_IMAP_*` credential propagation を検証）、および `TestIntegration_EnvConfig`、`TestIntegration_EmptyInbox`、`TestIntegration_FetchMeta`、`TestIntegration_Download`、`TestIntegration_MarkSeen`、`TestIntegration_UIDValidity_Stable`、`TestIntegration_UIDValidity_Change`、`TestIntegration_Recovery_KeepOld`、`TestIntegration_Recovery_DiscardOld` |
| AC-05 | `internal/imap/client_integration_test.go::TestIntegration_EmptyInbox`（`require.Empty(t, result.Messages)`、`require.Positive(t, result.UIDValidity)`）|
| AC-06 | `internal/imap/client_integration_test.go::TestIntegration_FetchMeta`（`result.Messages` から注入した `Message-ID` と一致するメッセージを検索し、UID・Size の非ゼロ assert を行う）|
| AC-07 | `internal/imap/client_integration_test.go::TestIntegration_Download`（返値バイト列に注入メールのヘッダが含まれることを `require.Contains` で検証）|
| AC-08 | `internal/imap/client_integration_test.go::TestIntegration_MarkSeen`（`MarkSeen` 後に別セッションで `Seen == true` を `require.True` で検証）|
| AC-09 | `internal/imap/client_integration_test.go::TestIntegration_UIDValidity_Stable`（2 回の `FetchMeta` 結果の `UIDValidity` が `require.Equal` で一致）|
| AC-10 | `internal/imap/client_integration_test.go::TestIntegration_UIDValidity_Change`（DELETE→CREATE 後の `UIDValidity` が `require.NotEqual` で変化）|
| AC-11 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_KeepOld`（fetch 再実行後の終了コードが `exitError`、かつ `LoadRecoveryRequired` で `found == true`）|
| AC-12 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_KeepOld`（`recover --mode keep-old` 後の fetch が `exitOK`）|
| AC-13 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_DiscardOld`（`recover --mode discard-old --yes` 後の fetch が `exitOK`）|
| AC-14 | `.github/scripts/verify-integration-workflow.go`（greenmail service image、ports、固定ユーザ env、job condition、`make test-integration` を構造化 YAML parse で検証）、`actionlint .github/workflows/ci.yml`、および PR 上の GitHub Actions 実行結果 |
| AC-15 | `.github/scripts/verify-integration-workflow.go`（通常 `test` ジョブに greenmail service、`-tags integration`、`test-integration` がないことを構造化 YAML parse で検証）、`actionlint .github/workflows/ci.yml` |
| AC-16 | `.github/scripts/classify-changes_test.sh`（代表ケースごとに `has-code-changes`、`has-devcontainer-changes`、`has-integration-changes` の 3 出力を検証）、および `actionlint .github/workflows/ci.yml` |

---

## 9. クロスサーチチェックリスト

変更・追加されたシンボルやコンセプトの影響範囲を確認する。

| 検索対象 | コマンド | 期待結果 |
|---|---|---|
| `InsecureSkipVerify` のすべての出現箇所 | `rg -rn "InsecureSkipVerify" --glob '*.go'` | `internal/imap/imap.go`（フィールド定義）、`internal/imap/client.go`（反映）、`internal/imap/client_test.go`（単体テスト）、`cmd/tlsrpt-digest/boot_test.go`（本番設定経路テスト）、`internal/imap/client_integration_test.go`（統合テスト）、`internal/imap/testutil/helpers.go`（TLS 設定参照）、`cmd/tlsrpt-digest/recovery_integration_test.go`（統合テスト）のみ。`internal/config/` と `cmd/tlsrpt-digest/boot.go` には 0 件 |
| `InsecureSkipVerify: true` の設定箇所 | `rg -rn "InsecureSkipVerify.*true" --glob '*.go'` | `internal/imap/client_test.go` は `buildTLSConfig` の単体テストとして許容する。実サーバ接続に使う設定は `//go:build integration` タグ付きファイル（`client_integration_test.go`、`recovery_integration_test.go`）のみ |
| 旧ポート番号 `3143` の残存 | `rg -rn "3143" .devcontainer/ .github/workflows/` | 0 件（すべて `3993` に更新済み）|
| 現行ドキュメント内の `3143` 残存 | `rg -rn "3143" docs/ --glob '!docs/tasks/0010_imap/**' --glob '!docs/tasks/0090_imap_integration/**'` | 0 件（旧タスクと本タスクの変更前後説明は許容）|
| `test-integration` の対象パッケージ | `rg -n "internal/imap\|cmd/tlsrpt-digest" Makefile` | `test-integration` ターゲットに両パスが含まれること |
| `loadIntegrationConfig` の呼び出し箇所 | `rg -rn "loadIntegrationConfig" internal/imap/` | `client_integration_test.go` のみ |

---

## 10. 次のステップ

1. `03_implementation_plan.md` のレビューと承認
2. Phase 1 から順に実装を進める
3. 各フェーズ完了時に `make test` と `make lint` で回帰がないことを確認する
4. Phase 3・4 の実サーバ検証は、Phase 5 の devcontainer 更新後または同等の手動 greenmail 設定で実行する
5. Phase 5 完了時に `make test-integration`、変更分類テスト、workflow 検証テスト、`actionlint` を実行し、PR で GitHub Actions の `integration-test` ジョブが正常に起動することを確認する
