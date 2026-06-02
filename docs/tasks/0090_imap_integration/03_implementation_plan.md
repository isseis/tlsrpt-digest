# 実装計画書：greenmail を使った IMAP 統合テスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-06-02 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

本実装は以下の目的で行う。

1. `internal/imap.Config` に `InsecureSkipVerify` フィールドを追加し、自己署名証明書サーバ（greenmail）への接続を可能にする
2. greenmail を使った `imapClient` の IMAP 操作統合テストを追加する
3. UIDVALIDITY 変化の検出と recovery フロー End-to-End 統合テストを追加する
4. devcontainer および GitHub Actions で統合テストが実行できる環境を整備する

### 1.2 実装方針

アーキテクチャ設計書 §1.1 に定めた設計原則に従う。

- 本番コードの変更は `Config.InsecureSkipVerify` フィールド追加と `buildTLSConfig` への反映のみに限定する
- `InsecureSkipVerify: true` の設定はテストコードのみに限定する（`//go:build integration` タグ付きファイル）
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
- メールボックス管理ヘルパーは新規ファイル `testutil/helpers_integration.go` に追加する（後述）

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

---

## 2. 実装ステップ

### Phase 1: `Config.InsecureSkipVerify` フィールド追加と単体テスト（F-001）

#### 変更ファイル: `internal/imap/imap.go`

- [ ] `Config` 構造体の末尾に `InsecureSkipVerify bool` フィールドを追加する。フィールドコメントは次の英語文字列とする:
  ```
  // InsecureSkipVerify, when true, disables TLS certificate verification.
  // Intended only for integration tests against self-signed servers
  // (e.g. greenmail). Never set from production configuration paths.
  ```

#### 変更ファイル: `internal/imap/client.go`

- [ ] `buildTLSConfig` 内の `tls.Config` リテラルにある `InsecureSkipVerify: false,` を `InsecureSkipVerify: cfg.InsecureSkipVerify,` に変更する。
  - 変更前: `InsecureSkipVerify: false,`
  - 変更後: `InsecureSkipVerify: cfg.InsecureSkipVerify,`

#### 変更ファイル: `internal/imap/client_test.go`

- [ ] `TestBuildTLSConfigInsecureSkipVerify` テスト関数を追加する。関数の先頭に次のコメントを付ける: `// TestBuildTLSConfigInsecureSkipVerify verifies that InsecureSkipVerify is reflected in tls.Config (requirement F-001, AC-01).`
  - `buildTLSConfig(Config{InsecureSkipVerify: true})` を呼び、返値の `InsecureSkipVerify == true`、`RootCAs == nil`、`MinVersion == tls.VersionTLS12` を assert する

#### 変更ファイル: `cmd/tlsrpt-digest/boot_test.go`

- [ ] `TestBuildIMAPConfig` に `assert.False(t, got.InsecureSkipVerify)` を追加する（他のフィールド検証の末尾に追加する）

**フェーズ完了の確認**:
- [ ] `make test` が通過すること
- [ ] `make lint` が通過すること

---

### Phase 2: テストヘルパー整備（F-002・F-003 の前提）

#### 変更ファイル: `internal/imap/client_integration_test.go`

- [ ] `loadIntegrationConfig` に `InsecureSkipVerify: true` を追加する（返す `Config` のフィールドとして設定する）

- [ ] `requireFixedUserEnv(t *testing.T)` ヘルパーを追加する。スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_USER`、`IMAP_TEST_PASS`、`IMAP_TEST_MAILBOX` のいずれかが空文字の場合に `t.Skip("fixed-user integration env not configured")` を呼ぶ

- [ ] `requireSMTPEnv(t *testing.T)` ヘルパーを追加する。スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_SMTP_HOST`、`IMAP_TEST_SMTP_PORT` のいずれかが空文字の場合に `t.Skip("SMTP integration env not configured")` を呼ぶ

- [ ] `testRecipientEmail(t *testing.T) string` ヘルパーを追加する。`t.Name()` から英数字・ハイフン以外の文字をハイフンに置換し、末尾に `@test.example.com` を付与した文字列を返す

- [ ] `injectTestMail(t *testing.T, smtpAddr, recipient, subject, body string)` ヘルパーを追加する。`net/smtp` の `SendMail` を使い `from@test.example.com` から `recipient` 宛てにメールを送信する。`msg` パラメータは RFC 2822 準拠のヘッダ付き本文（例: `"From: from@test.example.com\r\nTo: " + recipient + "\r\nSubject: " + subject + "\r\n\r\n" + body`）として組み立てる。これにより `TestIntegration_Download` でダウンロードしたバイト列に `Subject:` ヘッダが含まれることが保証される。失敗時は `require.NoError` でテストを即座に停止させる

- [ ] `loadSMTPTestConfig(t *testing.T) (cfg Config, smtpAddr string)` ヘルパーを追加する。`requireSMTPEnv(t)` を呼び出した後、次のように `Config` を構築して返す:
  - `Host`: `IMAP_TEST_HOST`、`Port`: `IMAP_TEST_PORT`（デフォルト `993`）
  - `Username`: `testRecipientEmail(t)`、`Password`: `config.Secret(testRecipientEmail(t))`
    - greenmail は未登録の受信者宛て SMTP 配送を受け入れると、受信者アドレスをユーザ名・パスワードとしてメールボックスを自動作成する。これにより Username と Password に同じ受信者アドレスを使うことで IMAP ログインが成功する（`02_architecture.md` §6.1 参照）
  - `Mailbox`: `"INBOX"`、`InsecureSkipVerify`: `true`
  - `smtpAddr`: `IMAP_TEST_SMTP_HOST:IMAP_TEST_SMTP_PORT`

- [ ] `testMailboxName(t *testing.T) string` ヘルパーを追加する。テスト名から英数字・ハイフン以外の文字をハイフンに置換し、32 文字以内に切り詰めた文字列を返す（IMAP メールボックス名に `@` は使用できないため、`testRecipientEmail(t)` と別の実装が必要）。`TestIntegration_UIDValidity_Change` での非 INBOX メールボックス名生成に使用する。

#### 新規ファイル: `internal/imap/testutil/helpers_integration.go`

- [ ] `//go:build test` タグでファイルを作成する。パッケージ名は `imaptestutil`。
- [ ] `CreateMailbox(t *testing.T, cfg imap.Config, mailbox string)` を追加する。`crypto/tls` と `emersion/go-imap` の `imapclient.DialTLS` を使い、`buildTLSConfig` の代わりに `tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify, MinVersion: tls.VersionTLS12}` を渡してダイヤルし、`CREATE mailbox` コマンドを実行する。失敗は `t.Fatal` で報告する。
- [ ] `DeleteMailbox(t *testing.T, cfg imap.Config, mailbox string)` を追加する。同じ接続方法で `DELETE mailbox` コマンドを実行する。失敗は `t.Fatal` で報告する。

> **テストヘルパーファイルの分類**: `docs/dev/developer_guide/test_organization.md` に従い `//go:build test` タグを付与する。これにより `make lint`（`--build-tags test`）の静的解析対象に含まれ、linter によるコンパイルエラー早期検出が機能する。ファイル名 `helpers_integration.go` は「integration テスト専用のヘルパー（実サーバ接続を伴う）」を通常の `helpers.go` から分離して識別しやすくするための慣行として採用する。実際に関数が呼び出されるのは `//go:build integration` タグ付きテストファイルから（`make test-integration` 実行時）のみであり、単体テストビルドで副作用は生じない。

**フェーズ完了の確認**:
- [ ] `make test` が通過すること（統合テストはタグなしでスキップされること）

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
  - `imap.NewIMAPClient(cfg)` で接続する
  - `FetchMeta(ctx, time.Now().AddDate(-1, 0, 0))` を呼ぶ
  - `len(result.Messages) == 0` かつ `result.UIDValidity > 0` を `require.Empty` / `require.Positive` で検証する

##### AC-06: FetchMeta でメタ情報取得
- [ ] `TestIntegration_FetchMeta` を追加する。先頭コメント: `// TestIntegration_FetchMeta verifies FetchMeta retrieves metadata of an injected message (requirement F-002, AC-06).`
  - `loadSMTPTestConfig(t)` を呼ぶ（`cfg.Username` は `testRecipientEmail(t)` から生成したテスト専用の動的ユーザアドレスであり、固定ユーザ `IMAP_TEST_USER` とは別物である）
  - `injectTestMail(t, smtpAddr, cfg.Username, "fetch-meta-test", "test body")` でメール注入。greenmail が `cfg.Username` のメールボックスを自動作成する
  - `imap.NewIMAPClient(cfg)` で接続（`cfg.Username` = 受信者アドレス、`cfg.Password` = 受信者アドレス）
  - `FetchMeta` を実行し、`len(result.Messages) == 1`、`result.Messages[0].UID > 0`、`result.Messages[0].Size > 0`、`result.Messages[0].MessageID != ""` を検証する

##### AC-07: Download でメール本文取得
- [ ] `TestIntegration_Download` を追加する。先頭コメント: `// TestIntegration_Download verifies Download retrieves full message body (requirement F-002, AC-07).`
  - `injectTestMail` で subject を `"download-test"` としてメール注入する
  - `FetchMeta` で UID を取得する
  - `Download(ctx, []uint32{uid})` を実行する
  - 返されたバイト列に `Subject: download-test` が含まれることを `require.Contains` で検証する（`injectTestMail` が RFC 2822 ヘッダを正しく構築した結果、Subject ヘッダが本文に含まれる）

##### AC-08: MarkSeen で Seen フラグ付与
- [ ] `TestIntegration_MarkSeen` を追加する。先頭コメント: `// TestIntegration_MarkSeen verifies MarkSeen sets the Seen flag (requirement F-002, AC-08).`
  - メール注入後 `FetchMeta` で `result.Messages[0].Seen == false` を確認する
  - `MarkSeen(ctx, []uint32{uid})` を実行する
  - 別セッションで `imap.NewIMAPClient(cfg)` を再接続し、`FetchMeta` を呼んで `result.Messages[0].Seen == true` を検証する

##### AC-09: UIDValidity 安定性確認
- [ ] `TestIntegration_UIDValidity_Stable` を追加する。先頭コメント: `// TestIntegration_UIDValidity_Stable verifies UIDValidity is stable across consecutive FetchMeta calls (requirement F-002, AC-09).`
  - `requireFixedUserEnv(t)` を呼ぶ
  - 同一 INBOX に対して `FetchMeta` を 2 回呼ぶ
  - 両呼び出しの `UIDValidity` が同一であることを `require.Equal` で検証する

##### AC-10: UIDValidity 変化検出
- [ ] `TestIntegration_UIDValidity_Change` を追加する。先頭コメント: `// TestIntegration_UIDValidity_Change verifies UIDValidity changes after mailbox DELETE and CREATE (requirement F-003, AC-10).`
  - `requireFixedUserEnv(t)` を呼ぶ
  - `loadIntegrationConfig(t)` で固定ユーザの接続設定 `fixedCfg` を取得する
  - `testMailboxName(t)` でテスト固有の非 INBOX メールボックス名を導出する（`@` を含まない IMAP 互換の名前。Phase 2 で `client_integration_test.go` に追加する `testMailboxName` ヘルパーを使用する）
  - `imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` でメールボックスを作成し、`t.Cleanup(func() { imaptestutil.DeleteMailbox(t, fixedCfg, mailbox) })` を登録する
  - `fixedCfg` の `Mailbox` フィールドを当該非 INBOX メールボックス名に設定した新しい `Config` で `imap.NewIMAPClient` を呼んで接続し、`FetchMeta` で `V1 := result.UIDValidity` を取得する
  - `imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)` で削除し、`imaptestutil.CreateMailbox(t, fixedCfg, mailbox)` で同名で再作成する（この時点でクリーンアップには2回分の DeleteMailbox が登録されることになるが、2回目の削除は t.Cleanup では行われない点に注意。明示的に1回だけ登録すること）
  - 新しい接続で `FetchMeta` を呼び `V2 := result.UIDValidity` を取得し、`require.NotEqual(t, V1, V2)` で検証する

**フェーズ完了の確認**:
- [ ] devcontainer 上で `make test-integration` （`./internal/imap/...` のみ）が通過すること

---

### Phase 4: recovery フロー End-to-End 統合テスト（F-004）

#### 新規ファイル: `cmd/tlsrpt-digest/recovery_integration_test.go`

- [ ] `//go:build integration` タグ付きでファイルを作成する。パッケージ名は `main`。

- [ ] `loadRecoveryTestEnv(t *testing.T)` ヘルパーを追加する:
  - スキップ条件: `IMAP_TEST_HOST`、`IMAP_TEST_USER`、`IMAP_TEST_PASS`、`IMAP_TEST_MAILBOX` のいずれかが空文字の場合に `t.Skip("recovery integration env not configured")` を呼ぶ
  - `t.Setenv("TLSRPT_IMAP_USERNAME", os.Getenv("IMAP_TEST_USER"))` と `t.Setenv("TLSRPT_IMAP_PASSWORD", os.Getenv("IMAP_TEST_PASS"))` で fetch サブコマンドが読む IMAP 認証情報を設定する

- [ ] `buildTestConfigTOML(t *testing.T, rootDir, imapHost string, imapPort int, mailbox string) string` ヘルパーを追加する:
  - 一時ディレクトリに `config.toml` を書き込み、そのパスを返す
  - TOML の内容: `[imap]` セクションに `host`・`port`・`mailbox` を設定し、`[store]` セクションに `root_dir = rootDir` を設定する
  - `[notify.slack]` セクションに `allowed_host = ""` を設定する（Slack 通知を使用しないため）
  - 動作上の注意: `allowed_host = ""` かつ `dry-run=true` のとき、`setupNotifyHandlers` が呼ぶ `notify.BuildHandlers` は空の webhookURL をスキップして dry-run ハンドラのみを生成するため、Slack URL なしでも Bootstrap が正常に完了する（`boot.go` の `setupNotifyHandlers` 実装参照）

- [ ] `insecureMailFetcherFactory(cfg imap.Config) (imap.MailFetcher, error)` ヘルパーを追加する。先頭コメント: `// insecureMailFetcherFactory wraps imap.NewIMAPClient with InsecureSkipVerify=true for integration tests.`
  - 受け取った `cfg` の `InsecureSkipVerify` を `true` に設定してから `imap.NewIMAPClient(cfg)` を呼ぶ

- [ ] `testMailboxName(t *testing.T) string` ヘルパーを追加する。テスト名から英数字・ハイフン以外の文字をハイフンに置換した文字列を先頭32文字以内に制限して返す（IMAP メールボックス名の長さ制限に対応）

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
  11. `runCLI` で fetch を再実行し終了コードが `exitError` であることを検証する（AC-11 (1)）
  12. `store.Open(rootDir, store.IMAPIdentity{Host: imapHost, Port: imapPort, Mailbox: mailbox}, store.OpenReadOnly)` でストアを開き `LoadRecoveryRequired` を呼び `found == true` を検証する（AC-11 (2)）。`IMAPIdentity` の `Host`・`Port`・`Mailbox` は `buildTestConfigTOML` に渡した値と必ず一致させること。ストアを開いた後は `defer st.Close()` または `t.Cleanup` で閉じること
  13. `runCLI` で `recover -config <configPath> -mode keep-old` を実行し `exitOK` を検証する（AC-12）
  14. `runCLI` で fetch を再実行し `exitOK` を検証する（recovery 解消の確認）

- [ ] `TestIntegration_Recovery_DiscardOld` を追加する。先頭コメント: `// TestIntegration_Recovery_DiscardOld verifies recover --mode discard-old --yes resolves UIDVALIDITY mismatch (requirement F-004, AC-11, AC-13).`
  - `TestIntegration_Recovery_KeepOld` と同じ手順でステップ 1〜12 を実施する。メールボックス名は `testMailboxName(t)` で生成するためテスト関数名が異なれば自動的に別の固有名になる
  - ステップ 13 を `runCLI` で `recover -config <configPath> -mode discard-old -yes` に変更し `exitOK` を検証する（AC-13）
  - ステップ 14 は同様に fetch が `exitOK` を返すことを検証する

**フェーズ完了の確認**:
- [ ] devcontainer 上で `make test-integration` が通過すること（Makefile 拡張後）

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

- [ ] `greenmail` サービスの healthcheck コマンドを変更する:
  - 変更前: `test: ["CMD", "bash", "-c", "echo > /dev/tcp/localhost/3143"]`
  - 変更後: `test: ["CMD", "bash", "-c", "echo > /dev/tcp/localhost/3993"]`

#### 変更ファイル: `Makefile`

- [ ] `test-integration` ターゲットのコマンドを変更する:
  - 変更前: `go test -v -count=1 -tags test,integration ./internal/imap/...`
  - 変更後: `go test -v -count=1 -tags test,integration ./internal/imap/... ./cmd/tlsrpt-digest/...`

- [ ] `Makefile` 内の `test-integration` に関するコメントを更新する:
  - 変更前: `# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).`（以下2行のコメント）
  - 変更後: `# Run integration tests against GreenMail (requires devcontainer or manual GreenMail setup).\n# Covers internal/imap and cmd/tlsrpt-digest integration tests.`

#### 変更ファイル: `.github/workflows/ci.yml`

- [ ] `check-changes` ジョブの `outputs:` に `has-integration-changes: ${{ steps.check.outputs.has-integration-changes }}` を追加する

- [ ] `check-changes` ジョブの `Classify changed files` ステップのスクリプトに以下を追加する:
  - 判定条件: Go ソース（`*.go`）、`Makefile`、`.github/workflows/` 以下のファイル、`.devcontainer/` 以下のファイル、`testdata/` 以下のファイルのいずれかが変更された場合に `has-integration-changes=true` を出力する
  - スクリプト例:
    ```sh
    integration_files=$(echo "$changed_files" | grep -E '(\.go$|^Makefile$|^\.github/workflows/|^\.devcontainer/|^testdata/)' || true)
    if [ -z "$integration_files" ]; then
      echo "has-integration-changes=false" >> $GITHUB_OUTPUT
    else
      echo "has-integration-changes=true" >> $GITHUB_OUTPUT
    fi
    ```

- [ ] `integration-test` ジョブを追加する:
  - `needs: check-changes` を設定する
  - 実行条件: `if: needs.check-changes.outputs.has-integration-changes == 'true'`
  - greenmail service container（`greenmail/standalone:2.1.3`）を設定する:
    - `ports: ["3993:3993", "3025:3025"]`
    - `env.GREENMAIL_OPTS`: `-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.users=imap-test:imap-test@example.com -Dgreenmail.users.login=email`
    - healthcheck コマンド: `["CMD", "bash", "-c", "echo > /dev/tcp/localhost/3993"]`、`interval: 5s`、`timeout: 3s`、`retries: 10`、`start_period: 10s`
  - 環境変数（ジョブレベル `env:` に設定）:
    - `IMAP_TEST_HOST: localhost`
    - `IMAP_TEST_PORT: "3993"`
    - `IMAP_TEST_SMTP_HOST: localhost`
    - `IMAP_TEST_SMTP_PORT: "3025"`
    - `IMAP_TEST_USER: imap-test@example.com`
    - `IMAP_TEST_PASS: imap-test`
    - `IMAP_TEST_MAILBOX: INBOX`
  - ステップ: Checkout（`actions/checkout@v4`）→ Setup Go（`actions/setup-go@v5`、`go-version-file: go.mod`）→ `make test-integration`

**フェーズ完了の確認**:
- [ ] devcontainer を再起動（または `docker-compose down && docker-compose up`）し、`make test-integration` が通過すること
- [ ] PR で `.github/workflows/ci.yml` の変更を含む変更を push し、GitHub Actions の `integration-test` ジョブが起動することを確認する

---

## 3. 実装順序とマイルストーン

| マイルストーン | フェーズ | 内容 | 成果物 |
|---|---|---|---|
| M1 | Phase 1 | F-001 本番コード変更と単体テスト | `Config.InsecureSkipVerify` フィールドと AC-01〜03 をカバーするテスト |
| M2 | Phase 2 | テストヘルパー整備 | SMTP 注入・メールボックス管理・スキップ判定ヘルパー |
| M3 | Phase 3 | F-002・F-003 統合テスト | `client_integration_test.go` の AC-05〜10（devcontainer で動作確認済み）|
| M4 | Phase 4 | F-004 recovery E2E テスト | `recovery_integration_test.go` の AC-11〜13（devcontainer で動作確認済み）|
| M5 | Phase 5 | F-005 インフラ整備 | devcontainer・Makefile・CI ジョブ（GitHub Actions で動作確認済み）|

Phase 1 は他フェーズの前提（TLS 設定の基盤）であり最初に実施する。Phase 5 の devcontainer 変更（`IMAP_TEST_PORT` 変更）は Phase 2〜4 のテスト実行に影響するため、Phase 2 開始前に `docker-compose.base.yml` の必要箇所のみを先に変更することを推奨する。

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

### Phase 1（F-001）
- [ ] `internal/imap/imap.go`: `Config.InsecureSkipVerify` フィールド追加
- [ ] `internal/imap/client.go`: `buildTLSConfig` のフィールド反映
- [ ] `internal/imap/client_test.go`: `TestBuildTLSConfigInsecureSkipVerify` 追加
- [ ] `cmd/tlsrpt-digest/boot_test.go`: `TestBuildIMAPConfig` に `InsecureSkipVerify` assertion 追加
- [ ] `make test` 通過
- [ ] `make lint` 通過

### Phase 2（テストヘルパー）
- [ ] `internal/imap/client_integration_test.go`: `loadIntegrationConfig` に `InsecureSkipVerify: true` 追加
- [ ] `internal/imap/client_integration_test.go`: `requireFixedUserEnv` 追加
- [ ] `internal/imap/client_integration_test.go`: `requireSMTPEnv` 追加
- [ ] `internal/imap/client_integration_test.go`: `testRecipientEmail` 追加
- [ ] `internal/imap/client_integration_test.go`: `testMailboxName` 追加
- [ ] `internal/imap/client_integration_test.go`: `injectTestMail` 追加
- [ ] `internal/imap/client_integration_test.go`: `loadSMTPTestConfig` 追加
- [ ] `internal/imap/testutil/helpers_integration.go`: `CreateMailbox` 追加
- [ ] `internal/imap/testutil/helpers_integration.go`: `DeleteMailbox` 追加
- [ ] `make test` 通過（統合テストはスキップされること）
- [ ] `make lint` 通過

### Phase 3（F-002・F-003）
- [ ] `TestIntegration_EnvConfig` 更新（`requireFixedUserEnv` と `InsecureSkipVerify` assert 追加）
- [ ] `TestIntegration_EmptyInbox` 追加（AC-05）
- [ ] `TestIntegration_FetchMeta` 追加（AC-06）
- [ ] `TestIntegration_Download` 追加（AC-07）
- [ ] `TestIntegration_MarkSeen` 追加（AC-08）
- [ ] `TestIntegration_UIDValidity_Stable` 追加（AC-09）
- [ ] `TestIntegration_UIDValidity_Change` 追加（AC-10）
- [ ] devcontainer で `go test -v -count=1 -tags test,integration ./internal/imap/...` 通過

### Phase 4（F-004）
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `loadRecoveryTestEnv` 追加
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `buildTestConfigTOML` 追加
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `insecureMailFetcherFactory` 追加
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `testMailboxName` 追加
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `TestIntegration_Recovery_KeepOld` 追加（AC-11, AC-12）
- [ ] `cmd/tlsrpt-digest/recovery_integration_test.go`: `TestIntegration_Recovery_DiscardOld` 追加（AC-11, AC-13）
- [ ] devcontainer で `make test-integration` 通過（Makefile 拡張後）

### Phase 5（F-005）
- [ ] `.devcontainer/docker-compose.base.yml`: `IMAP_TEST_PORT` 変更（`3143`→`3993`）
- [ ] `.devcontainer/docker-compose.base.yml`: `IMAP_TEST_PASS` 変更
- [ ] `.devcontainer/docker-compose.base.yml`: `GREENMAIL_OPTS` 更新
- [ ] `.devcontainer/docker-compose.base.yml`: ポートマッピング変更
- [ ] `.devcontainer/docker-compose.base.yml`: healthcheck 変更
- [ ] `Makefile`: `test-integration` ターゲット拡張とコメント更新
- [ ] `.github/workflows/ci.yml`: `has-integration-changes` 出力追加
- [ ] `.github/workflows/ci.yml`: `integration-test` ジョブ追加
- [ ] devcontainer 再起動後に `make test-integration` 通過
- [ ] PR で `integration-test` ジョブが起動することを確認

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
- `InsecureSkipVerify: true` の設定箇所が `//go:build integration` タグ付きファイルのみに限定されることを `rg -rn "InsecureSkipVerify.*true" --glob '*.go'` の結果がすべて integration タグ付きファイルであることで確認できる

### ドキュメント完成
- `Makefile` のコメントが更新された `test-integration` 対象パッケージ（`./internal/imap/...` と `./cmd/tlsrpt-digest/...`）を反映している
- `docs/tasks/0090_imap_integration/` 以外のドキュメントに `3143` の記述が新たに追加されていないこと（`rg -rn "3143" docs/ --glob '!docs/tasks/0010_imap/**'` の結果が 0 件であること。`docs/tasks/0010_imap/` 以下の旧タスク文書における既存の `3143` 記述は許容する）

---

## 8. 受け入れ条件検証

| AC | 検証方法 |
|---|---|
| AC-01 | `internal/imap/client_test.go::TestBuildTLSConfigInsecureSkipVerify`（`require.True(t, cfg.InsecureSkipVerify)`）|
| AC-02 | `internal/imap/client_test.go::TestBuildTLSConfigSystemCA`（`require.False(t, cfg.InsecureSkipVerify)`、`Config{}` でのゼロ値確認が主要カバレッジ）、`internal/imap/client_test.go::TestBuildTLSConfigCustomCA`（`require.False(t, cfg.InsecureSkipVerify)`、CA 設定時もゼロ値のままであることの補完的確認）|
| AC-03 | `cmd/tlsrpt-digest/boot_test.go::TestBuildIMAPConfig`（`assert.False(t, got.InsecureSkipVerify)`）|
| AC-04 | `rg -n "t\.Skip\(" internal/imap/client_integration_test.go cmd/tlsrpt-digest/recovery_integration_test.go` の結果が `requireFixedUserEnv`・`requireSMTPEnv`・`loadRecoveryTestEnv` の関数本体内に存在すること。なお `01_requirements.md` AC-04 に記載の「`IMAP_TEST_PORT` 未設定」はスキップ条件でなくデフォルト値（993）へのフォールバックとして実装する。`IMAP_TEST_PORT` 自体はオプション環境変数のため未設定でも接続試行が可能であり、スキップ不要と判断する |
| AC-05 | `internal/imap/client_integration_test.go::TestIntegration_EmptyInbox`（`require.Empty(t, result.Messages)`、`require.Positive(t, result.UIDValidity)`）|
| AC-06 | `internal/imap/client_integration_test.go::TestIntegration_FetchMeta`（`require.Len(t, result.Messages, 1)`、UID・Size・MessageID の非ゼロ assert）|
| AC-07 | `internal/imap/client_integration_test.go::TestIntegration_Download`（返値バイト列に注入メールのヘッダが含まれることを `require.Contains` で検証）|
| AC-08 | `internal/imap/client_integration_test.go::TestIntegration_MarkSeen`（`MarkSeen` 後に別セッションで `Seen == true` を `require.True` で検証）|
| AC-09 | `internal/imap/client_integration_test.go::TestIntegration_UIDValidity_Stable`（2 回の `FetchMeta` 結果の `UIDValidity` が `require.Equal` で一致）|
| AC-10 | `internal/imap/client_integration_test.go::TestIntegration_UIDValidity_Change`（DELETE→CREATE 後の `UIDValidity` が `require.NotEqual` で変化）|
| AC-11 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_KeepOld`（fetch 再実行後の終了コードが `exitError`、かつ `LoadRecoveryRequired` で `found == true`）|
| AC-12 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_KeepOld`（`recover --mode keep-old` 後の fetch が `exitOK`）|
| AC-13 | `cmd/tlsrpt-digest/recovery_integration_test.go::TestIntegration_Recovery_DiscardOld`（`recover --mode discard-old --yes` 後の fetch が `exitOK`）|
| AC-14 | `rg -n "integration-test:" .github/workflows/ci.yml` の結果が 1 件以上あること |
| AC-15 | `rg -n "make test$" .github/workflows/ci.yml` で既存 `test` ジョブに `-tags integration` が含まれないこと、かつ `rg -n "integration-test:" .github/workflows/ci.yml` で別ジョブが定義されること |
| AC-16 | `rg -A 20 "has-integration-changes" .github/workflows/ci.yml` の結果に `\.go`・`Makefile`・`\.github/workflows/`・`\.devcontainer/`・`testdata/` の各パターンが含まれること |

---

## 9. クロスサーチチェックリスト

変更・追加されたシンボルやコンセプトの影響範囲を確認する。

| 検索対象 | コマンド | 期待結果 |
|---|---|---|
| `InsecureSkipVerify` のすべての出現箇所 | `rg -rn "InsecureSkipVerify" --glob '*.go'` | `internal/imap/imap.go`（フィールド定義）、`internal/imap/client.go`（反映）、`internal/imap/client_test.go`（テスト）、`cmd/tlsrpt-digest/boot_test.go`（テスト）、`internal/imap/client_integration_test.go`（テスト）、`internal/imap/testutil/helpers_integration.go`（TLS 設定参照）、`cmd/tlsrpt-digest/recovery_integration_test.go`（テスト）のみ。`internal/config/` と `cmd/tlsrpt-digest/boot.go` には 0 件 |
| `InsecureSkipVerify: true` の設定箇所 | `rg -rn "InsecureSkipVerify.*true" --glob '*.go'` | `//go:build integration` タグ付きファイル（`client_integration_test.go`、`recovery_integration_test.go`）のみ |
| 旧ポート番号 `3143` の残存 | `rg -rn "3143" .devcontainer/ .github/workflows/` | 0 件（すべて `3993` に更新済み）|
| `docs/` 内の `3143` 残存 | `rg -rn "3143" docs/` | `docs/tasks/0010_imap/` 以下のみ（旧タスクの記録として許容。0090 タスクのドキュメントには 0 件）|
| `test-integration` の対象パッケージ | `rg -n "internal/imap\|cmd/tlsrpt-digest" Makefile` | `test-integration` ターゲットに両パスが含まれること |
| `loadIntegrationConfig` の呼び出し箇所 | `rg -rn "loadIntegrationConfig" internal/imap/` | `client_integration_test.go` のみ |

---

## 10. 次のステップ

1. `03_implementation_plan.md` のレビューと承認
2. Phase 1 から順に実装を進める
3. 各フェーズ完了時に `make test` と `make lint` で回帰がないことを確認する
4. Phase 3・4 完了時に devcontainer 上で `make test-integration` を実行して動作を確認する
5. Phase 5 完了時に PR を作成し、GitHub Actions の `integration-test` ジョブが正常に起動することを確認する
