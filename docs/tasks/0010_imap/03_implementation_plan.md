# 実装計画書：IMAP接続・メタ情報取得・選択的ダウンロード

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-12 |
| 更新日 | - |

---

## 1. 実装概要

### 目的

`internal/imap` パッケージを実装し、要件定義書（F-001〜F-005）のすべての受け入れ基準を満たす。

### 実装原則

- インターフェースと型定義を先に確定し、テストダブル（`FakeMailFetcher`）を先に実装してから実 IMAP クライアントを実装する
- Go コードのコメント・識別子・文字列リテラルは英語のみ使用する
- `go test ./...` および `golangci-lint run` が通ることを各フェーズ末に確認する

### 前提条件

`Config.Password` フィールドは `config.Secret` 型（task 0060 で定義予定）を使用する。
task 0010 の着手時点で `internal/config` が存在しない場合、先行して `internal/config/secret.go` に `Secret` 型のみを定義する。

### 参照ドキュメント

- 要件定義書: [01_requirements.md](01_requirements.md)
- アーキテクチャ設計書: [02_architecture.md](02_architecture.md)

---

## 2. 実装ステップ

### フェーズ 0: 前提条件（`internal/config/secret.go`）

`config.Secret` が未定義の場合のみ実施する。

- [ ] **0-1** `internal/config/secret.go` を作成し `Secret` 型を定義する
  - `String() string` → `"[REDACTED]"` を返す
  - `LogValue() slog.Value` → `"[REDACTED]"` を返す
  - `Value() string` → 実際の値を返す専用メソッド
  - 成功条件: コンパイルが通る
  - AC 対応: アーキテクチャ設計書 §5（パスワード非漏洩）

---

### フェーズ 1: インターフェースと型定義（`internal/imap/imap.go`）

**推定工数**: 1 時間

- [ ] **1-1** `Config` 構造体を定義する
  - フィールド: `Host string`, `Port int`, `Username string`, `Password config.Secret`, `Mailbox string`, `TLSCACert string`, `MaxMessageBytes int64`
  - 成功条件: コンパイルが通る

- [ ] **1-2** `MessageMeta` 構造体を定義する
  - フィールド: `UID uint32`, `Size uint32`, `Date time.Time`, `Seen bool`, `MessageID string`
  - 成功条件: コンパイルが通る

- [ ] **1-3** `FetchMetaResult` 構造体を定義する
  - フィールド: `Messages []MessageMeta`, `UIDValidity uint32`
  - 成功条件: コンパイルが通る

- [ ] **1-4** `MailFetcher` インターフェースを定義する
  - メソッド: `FetchMeta`, `Download`, `MarkSeen`, `Close`
  - 成功条件: コンパイルが通る
  - AC 対応: F-004 AC-1, AC-2

---

### フェーズ 2: テストダブル（`internal/imap/fake.go`）

**推定工数**: 2 時間

- [ ] **2-1** `FakeMailFetcher` 構造体を定義し `MailFetcher` インターフェースを実装する
  - `var _ MailFetcher = (*FakeMailFetcher)(nil)` によるコンパイル時チェックを追加する
  - AC 対応: F-004 AC-1

- [ ] **2-2** `FetchMeta` の戻り値プリセットフィールドとスパイを実装する
  - フィールド: `FetchMetaResult FetchMetaResult`, `FetchMetaErr error`, `FetchMetaCalls []time.Time`
  - AC 対応: F-002 AC-5, F-004 AC-4

- [ ] **2-3** `Download` の戻り値プリセットフィールドとスパイを実装する
  - フィールド: `DownloadResult map[uint32]*mail.Message`, `DownloadErr error`, `DownloadCalls [][]uint32`
  - AC 対応: F-004 AC-3, F-005 AC-4

- [ ] **2-4** `MarkSeen` のスパイとエラー注入フィールドを実装する
  - フィールド: `MarkSeenErr error`, `MarkSeenCalls [][]uint32`
  - AC 対応: F-004 AC-4

- [ ] **2-5** `Close` を no-op として実装する（`return nil`）
  - フィールド: `CloseErr error`（エラー注入用）

- [ ] **2-6** `FakeMailFetcher` の単体テストを作成する（`internal/imap/fake_test.go`）
  - `FetchMeta` が設定値と呼び出し引数を記録すること
  - `Download` が設定値を返し呼び出し引数を記録すること
  - `MarkSeen` が呼び出し引数を記録すること
  - `Close` が nil を返すこと
  - 成功条件: `go test ./internal/imap/...` が通る
  - AC 対応: F-002 AC-5, F-004 AC-3, AC-4, F-005 AC-4

---

### フェーズ 3: 実装（`internal/imap/client.go`）

**推定工数**: 4 時間

#### 3-A: TLS 接続・認証・クローズ

- [ ] **3-1** `NewIMAPClient(cfg Config) (MailFetcher, error)` を実装する
  - `tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: false}` を設定する
  - `cfg.TLSCACert` が空でなければ PEM ファイルを読み込み `x509.CertPool` を構築する。空であれば `RootCAs: nil`（OS バンドル）とする
  - `cfg.Mailbox` が空であれば `"INBOX"` をデフォルト値として使用する
  - `emersion/go-imap` の `client.DialTLS` で接続し `Login` で認証する
  - エラーは `fmt.Errorf("imap: <operation>: %w", err)` 形式でラップして返す（`%w` を維持し将来のリトライで区別可能にする）
  - AC 対応: F-001 AC-1, AC-2, AC-3, AC-4, AC-5, AC-6, AC-7

- [ ] **3-2** `Close() error` を実装する
  - `Logout()` を呼び出して接続を閉じる
  - エラーは `fmt.Errorf("imap: logout: %w", err)` でラップして返す

#### 3-B: メタ情報取得

- [ ] **3-3** `FetchMeta(ctx, since) (FetchMetaResult, error)` を実装する
  - `since` の時・分・秒を `time.Date(...)` で切り捨てて日付のみにする
  - `SELECT mailbox` → `UID SEARCH SINCE date` → `UID FETCH (UID RFC822.SIZE FLAGS ENVELOPE)` を発行する
  - `SELECT` 応答の `UIDValidity` を `FetchMetaResult.UIDValidity` に格納する
  - `ENVELOPE` の `Date`・`MessageId` を `MessageMeta.Date`・`MessageMeta.MessageID` に格納する
  - `MaxMessageBytes > 0` かつ `MessageMeta.Size > uint32(cfg.MaxMessageBytes)` のメッセージを結果から除外し WARN ログを出力する（`slog.Warn`）
  - 該当メッセージが存在しない場合 `FetchMetaResult{Messages: []MessageMeta{}}` を返す（エラーにしない）
  - SEEN フラグを変更しない（STORE コマンドを発行しない）
  - AC 対応: F-002 AC-1, AC-2, AC-3, AC-4

#### 3-C: 選択的ダウンロード

- [ ] **3-4** `Download(ctx, uids) (map[uint32]*mail.Message, error)` を実装する
  - `UID FETCH uid-set BODY.PEEK[]` を発行する（`RFC822` ではなく `BODY.PEEK[]` を使用し SEEN フラグを変更しない）
  - 応答メッセージを `mail.ReadMessage` でパースし `map[uint32]*mail.Message` に格納する
  - 要求した UID が 1 件でも欠落していれば `fmt.Errorf("imap: download: uid %d not found", uid)` を返す
  - AC 対応: F-005 AC-1, AC-2, AC-3

#### 3-D: 既読マーク

- [ ] **3-5** `MarkSeen(ctx, uids) error` を実装する
  - `UID STORE uid-set +FLAGS (\Seen)` を発行する
  - 既に SEEN のメッセージへの再付与はサーバーが無視するため追加処理不要
  - AC 対応: F-003 AC-1, AC-2, AC-3

---

### フェーズ 4: テスト（`internal/imap/client_test.go`）

**推定工数**: 2 時間

- [ ] **4-1** TLS 設定ロジックのテスト
  - カスタム CA ファイルを読み込んで `CertPool` が構築されること（F-001 AC-6）
  - 存在しないパスをエラーで返すこと（F-001 AC-6）
  - 不正なファイル（非 PEM）をエラーで返すこと（F-001 AC-6）
  - `InsecureSkipVerify` が `false` であること（F-001 AC-5）
  - `TLSMinVersion` が `tls.VersionTLS12` であること（F-001 AC-4）
  - 成功条件: `go test ./internal/imap/...` が通る

- [ ] **4-2** `since` 日付切り捨てのユニットテスト
  - `time.Time` の時・分・秒が切り捨てられること（F-002 AC-1）
  - 成功条件: `go test ./internal/imap/...` が通る

- [ ] **4-3** `MaxMessageBytes` フィルタリングのユニットテスト
  - `Size > MaxMessageBytes` のメッセージが除外されること
  - `Size == MaxMessageBytes` のメッセージが通過すること（境界値）
  - `MaxMessageBytes == 0` のとき全件通過すること
  - 成功条件: `go test ./internal/imap/...` が通る

- [ ] **4-4** `Download` の UID 欠落エラーテスト
  - 要求 UID が 1 件でも欠落した場合にエラーを返すこと（F-005 AC-2）
  - 成功条件: `go test ./internal/imap/...` が通る

- [ ] **4-5** 統合テストの骨格を用意する（`internal/imap/client_integration_test.go`）
  - `//go:build integration` ビルドタグを付与する
  - 環境変数 `IMAP_TEST_HOST`, `IMAP_TEST_USER`, `IMAP_TEST_PASS` で接続先を指定する
  - 通常の CI では skip される（F-001 AC-1, F-002 AC-2, AC-4, F-003 AC-1, AC-3, F-005 AC-1, AC-3 の結合確認）

---

## 3. 実装順序とマイルストーン

| マイルストーン | 内容 | 完了条件 |
|---|---|---|
| M0 | `config.Secret` 型が利用可能 | `internal/config` パッケージがコンパイルを通る |
| M1 | 型・インターフェース確定 | `internal/imap/imap.go` がコンパイルを通る |
| M2 | `FakeMailFetcher` が利用可能 | `go test ./internal/imap/...` が通る |
| M3 | 実 IMAP クライアントが完成 | フェーズ3の全ステップが完了 |
| M4 | 全テストが通る | `make test` + `make lint` が通る |

---

## 4. テスト戦略

### 単体テスト（実 IMAP サーバー不要）

| テスト対象 | ファイル | 主なケース |
|---|---|---|
| `FakeMailFetcher` | `fake_test.go` | 設定値の返却・スパイ記録・Close no-op |
| TLS 設定ロジック | `client_test.go` | カスタム CA・OS バンドル・MinVersion |
| `since` 日付切り捨て | `client_test.go` | 時刻部分の除去 |
| `MaxMessageBytes` フィルタ | `client_test.go` | 境界値・0 の場合 |
| UID 欠落エラー | `client_test.go` | 1件欠落でエラー |

### 統合テスト（実 IMAP サーバー必要）

- `//go:build integration` で通常 CI はスキップ
- 環境変数で接続先を指定
- F-001 AC-1, F-002 AC-2・AC-4, F-003 AC-1・AC-3, F-005 AC-1・AC-3 を確認

---

## 5. リスク管理

| リスク | 軽減策 |
|---|---|
| `emersion/go-imap` v1 の API が想定と異なる | フェーズ2で `FakeMailFetcher` を先に完成させてインターフェースを固め、フェーズ3で実装に入る |
| `BODY.PEEK[]` が一部サーバーで未サポート | 統合テストで実サーバー対象に確認する |
| `ENVELOPE` の `Date` フォーマットが RFC 2822 非準拠のメールで解析失敗 | 解析失敗時は `Date: time.Time{}` としてスキップし WARN ログを出力する |

---

## 6. 実装チェックリスト

### フェーズ 0
- [ ] `internal/config/secret.go`: `Secret` 型定義

### フェーズ 1
- [ ] `Config` 構造体
- [ ] `MessageMeta` 構造体
- [ ] `FetchMetaResult` 構造体
- [ ] `MailFetcher` インターフェース

### フェーズ 2
- [ ] `FakeMailFetcher` 実装
- [ ] `FakeMailFetcher` テスト（`fake_test.go`）

### フェーズ 3
- [ ] `NewIMAPClient`（TLS 設定・認証）
- [ ] `Close`
- [ ] `FetchMeta`（SELECT・SEARCH・FETCH・UIDValidity・MaxMessageBytes）
- [ ] `Download`（BODY.PEEK[]・UID 欠落エラー）
- [ ] `MarkSeen`

### フェーズ 4
- [ ] TLS 設定テスト
- [ ] 日付切り捨てテスト
- [ ] `MaxMessageBytes` フィルタテスト
- [ ] UID 欠落エラーテスト
- [ ] 統合テスト骨格

### 最終確認
- [ ] `make test` が通る
- [ ] `make lint` が通る
- [ ] `make fmt` を実行済み
- [ ] Go コードに日本語が含まれない

---

## 7. 成功基準

- 全受け入れ基準（F-001〜F-005）に対応するテストが存在する
- `make test` と `make lint` が通る
- `FakeMailFetcher` が `MailFetcher` インターフェースをコンパイル時チェックで完全に実装している
- Go コードのコメント・識別子・文字列リテラルに日本語が含まれない

---

## 8. 受け入れ基準の確認（AC トレーサビリティ）

### F-001: IMAP サーバーへの接続

**AC-1: 正しい接続情報での接続成功**
- テスト: `client_integration_test.go::TestNewIMAPClient_Success`（統合テスト）
- 実装: `client.go::NewIMAPClient`

**AC-2: 接続失敗時の意味あるエラー**
- テスト: `client_integration_test.go::TestNewIMAPClient_BadHost`, `TestNewIMAPClient_BadCredentials`
- 実装: `client.go::NewIMAPClient`（`%w` ラップによる文脈付きエラー）

**AC-3: TLS 接続（ポート 993）**
- テスト: `client_test.go::TestNewIMAPClient_UsesTLSDial`
- 実装: `client.go::NewIMAPClient`（`client.DialTLS` 使用）

**AC-4: TLS 1.2 以上を要求**
- テスト: `client_test.go::TestNewIMAPClient_TLSMinVersion`
- 実装: `client.go::NewIMAPClient`（`tls.Config{MinVersion: tls.VersionTLS12}`）

**AC-5: InsecureSkipVerify を使用しない**
- テスト: `client_test.go::TestNewIMAPClient_NoInsecureSkipVerify`
- 実装: `client.go::NewIMAPClient`（`InsecureSkipVerify: false` を明示）

**AC-6: カスタム CA 証明書の使用**
- テスト: `client_test.go::TestBuildTLSConfig_CustomCA`, `TestBuildTLSConfig_MissingFile`, `TestBuildTLSConfig_InvalidPEM`
- 実装: `client.go::NewIMAPClient`（`x509.CertPool` 構築）

**AC-7: OS バンドルの使用（CA 未設定時）**
- テスト: `client_test.go::TestBuildTLSConfig_SystemCA`
- 実装: `client.go::NewIMAPClient`（`RootCAs: nil`）

---

### F-002: 期間指定メタ情報取得

**AC-1: since の時刻切り捨てと日付以降の全件取得**
- テスト: `client_test.go::TestTruncateToDate`
- 実装: `client.go::FetchMeta`（`time.Date` で時・分・秒を 0 にする）

**AC-2: UID, RFC822.SIZE, SEEN フラグ, Message-ID を含む**
- テスト: `client_integration_test.go::TestFetchMeta_Fields`（統合テスト）
- 実装: `client.go::FetchMeta`（`UID FETCH UID RFC822.SIZE FLAGS ENVELOPE`）

**AC-3: 該当なしの場合に空スライスを返す**
- テスト: `client_integration_test.go::TestFetchMeta_EmptyMailbox`（統合テスト）
- 実装: `client.go::FetchMeta`

**AC-4: SEEN フラグを変更しない**
- テスト: `client_integration_test.go::TestFetchMeta_NoSideEffect`（統合テスト）
- 実装: `client.go::FetchMeta`（STORE コマンドを発行しない）

**AC-5: FakeMailFetcher が実装する**
- テスト: `fake_test.go::TestFakeMailFetcher_FetchMeta`
- 実装: `fake.go::FakeMailFetcher.FetchMeta`

---

### F-003: 既読マーク

**AC-1: SEEN フラグを付与**
- テスト: `client_integration_test.go::TestMarkSeen_AddsFlag`（統合テスト）
- 実装: `client.go::MarkSeen`（`UID STORE +FLAGS (\Seen)`）

**AC-2: 失敗時にエラーを返す**
- テスト: `fake_test.go::TestFakeMailFetcher_MarkSeen_Error`（エラー注入）
- 実装: `client.go::MarkSeen`

**AC-3: 既に SEEN への再付与が成功する（冪等）**
- テスト: `client_integration_test.go::TestMarkSeen_Idempotent`（統合テスト）
- 実装: `client.go::MarkSeen`（サーバーの RFC 準拠動作）

---

### F-004: MailFetcher インターフェース

**AC-1: インターフェースが定義され両実装が満たす**
- テスト: `fake.go::var _ MailFetcher = (*FakeMailFetcher)(nil)`、`client.go::var _ MailFetcher = (*imapClient)(nil)`
- 実装: `imap.go::MailFetcher`

**AC-2: FetchMeta・Download・MarkSeen を含む**
- テスト: コンパイル時チェック（AC-1 と同じ）
- 実装: `imap.go::MailFetcher`

**AC-3: FakeMailFetcher が事前定義メッセージを返せる**
- テスト: `fake_test.go::TestFakeMailFetcher_Download`
- 実装: `fake.go::FakeMailFetcher.Download`

**AC-4: FakeMailFetcher がスパイ機能を持つ**
- テスト: `fake_test.go::TestFakeMailFetcher_Spy`
- 実装: `fake.go::FakeMailFetcher`（`MarkSeenCalls`, `DownloadCalls`, `FetchMetaCalls`）

---

### F-005: 選択的ダウンロード

**AC-1: UID リストを受け取りヘッダ・本文・添付を返す**
- テスト: `client_integration_test.go::TestDownload_Success`（統合テスト）
- 実装: `client.go::Download`（`UID FETCH BODY.PEEK[]`）

**AC-2: UID 不存在時にエラーを返す**
- テスト: `client_test.go::TestDownload_MissingUID`
- 実装: `client.go::Download`（応答 UID と要求 UID の照合）

**AC-3: SEEN フラグを変更しない**
- テスト: `client_integration_test.go::TestDownload_NoFlagChange`（統合テスト）
- 実装: `client.go::Download`（`BODY.PEEK[]` を使用、`RFC822` は使用しない）

**AC-4: FakeMailFetcher が実装する**
- テスト: `fake_test.go::TestFakeMailFetcher_Download`
- 実装: `fake.go::FakeMailFetcher.Download`

---

## 9. 次のステップ

実装完了後、`0020_tlsrpt` の実装に進む。`MailFetcher.Download` で取得した `*mail.Message` を `internal/tlsrpt` パッケージが受け取り、RFC 8460 JSON 添付をパースする。
