# 要件定義書：greenmail を使った IMAP 統合テスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-28 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

`internal/imap` パッケージの `MailFetcher` 実装（`imapClient`）は、単体テストでは `fakeSession`（パッケージ内テスト用スタブ）を使っており、実際の IMAP サーバに対する動作検証が行われていない。

また、recovery フロー（UIDVALIDITY 不一致の検出から `recover` コマンドによる解消まで）の単体テストは `FakeStore` と `FakeMailFetcher` を使っており、どちらも本プロジェクトが独自に実装したテストダブルである。外部実装である実 IMAP サーバとの組み合わせで End-to-End の動作を検証するテストが存在しない。

開発環境の devcontainer には greenmail（インメモリ IMAP/SMTP サーバ）がすでに組み込まれており、統合テストのインフラとして利用できる状態にある。

### 1.2 目的

1. **主目的**: 実 IMAP サーバ（greenmail）に対して `imapClient` の各操作が正しく動作することを検証する統合テストを追加する
2. **副次的目的**: fetch 実行後にメールボックスの UIDVALIDITY が変化した場合に recovery フローが End-to-End で正しく機能することを検証する統合テストを追加する
3. **インフラ整備**: devcontainer および GitHub Actions で統合テストを実行できる環境を整備する

---

## 2. スコープ

### 対象範囲（In Scope）

- `internal/imap.Config` への `InsecureSkipVerify` フィールド追加
- `internal/imap` パッケージの統合テスト（`//go:build integration` タグ付き）
  - greenmail への接続・認証
  - `FetchMeta`：SMTP でメッセージを注入後にメタ情報取得
  - `Download`：メール本文のダウンロード
  - `MarkSeen`：既読フラグの付与と確認
  - `UIDValidity` の安定性確認
  - メールボックス削除→再作成による `UIDValidity` 変化の確認
- recovery フロー End-to-End 統合テスト（`cmd/tlsrpt-digest/` パッケージ内、`//go:build integration` タグ付き）
  - fetch 実行 → UIDVALIDITY 変化 → fetch 再実行でエラー検出 → `recover --mode keep-old` で解消
  - fetch 実行 → UIDVALIDITY 変化 → fetch 再実行でエラー検出 → `recover --mode discard-old --yes` で解消
- devcontainer の IMAP テスト接続ポートを IMAPS（3993）に変更
- GitHub Actions ワークフローへの統合テスト実行ジョブ追加
- 既存 `make test-integration` ターゲットの対象パッケージ拡張

### 対象外（Out of Scope）

- greenmail の SMTP 機能（TLS、認証等）の詳細テスト
- `internal/imap` パッケージへの平文 IMAP（非 TLS）接続サポートの追加
- `InsecureSkipVerify` を `internal/config.IMAPConfig` や TOML 設定キーとして公開すること
- IMAP IDLE・PUSH 方式のテスト
- 実運用 IMAP サーバへの接続テスト

### 影響を受けるコンポーネント

- **直接変更**:
  - `internal/imap/imap.go`
  - `internal/imap/client.go`
  - `internal/imap/client_integration_test.go`
  - `.devcontainer/docker-compose.base.yml`
  - `.github/workflows/`
  - `Makefile`
- **新規作成**:
  - `cmd/tlsrpt-digest/recovery_integration_test.go`

---

## 3. 機能要件

### F-001: `Config.InsecureSkipVerify` フィールドの追加

`internal/imap.Config` に `InsecureSkipVerify bool` フィールドを追加し、自己署名証明書を使用するサーバ（greenmail 等）への接続を可能にする。

このフィールドは統合テスト専用の内部設定であり、`internal/config.IMAPConfig` や TOML 設定キーには追加しない。通常のアプリケーション実行では従来どおり証明書検証を行う。

**受け入れ条件（Acceptance Criteria）**:

- **AC-01**: `InsecureSkipVerify` フィールドが `Config` 構造体に追加され、`InsecureSkipVerify: true` の場合に TLS 証明書検証がスキップされる
- **AC-02**: `InsecureSkipVerify` のデフォルト値（ゼロ値）は `false` であり、既存の動作（証明書検証あり）が変わらない
- **AC-03**: 本番の IMAP 設定構築コードは `InsecureSkipVerify` を設定しない

### F-002: `imapClient` の greenmail 接続統合テスト

greenmail IMAPS（ポート 3993）に対して `imapClient` の各操作が正しく動作することを検証する。

テストメッセージの注入には greenmail の SMTP ポート（3025）を使用する。SMTP の配送先は受信者ユーザの INBOX に限られ、テスト間の干渉を避ける分離単位は「固有の受信者ユーザ（= その INBOX）」とする。各テストケースはテスト名等から導出した固有のメールアドレスを受信者として使用する（有効なアドレスになるよう特殊文字をサニタイズする）。

**受け入れ条件（Acceptance Criteria）**:

- **AC-04**: 統合テストの実行に必要な環境変数が設定されていない場合、統合テストは `t.Skip()` でスキップされ、CI の通常テストパスを妨げない
  - IMAP 接続テスト（`IMAP_TEST_HOST`、`IMAP_TEST_PORT`）が未設定の場合はスキップ
  - SMTP 注入を伴うテストは `IMAP_TEST_SMTP_HOST`、`IMAP_TEST_SMTP_PORT` も必要
  - `IMAP_TEST_USER`/`IMAP_TEST_PASS`/`IMAP_TEST_MAILBOX` は固定ユーザで IMAP 接続する F-003・F-004 用テストで必要
- **AC-05**: 固定ユーザで接続した空のメールボックスに対して `FetchMeta` を実行すると、空のメッセージリストと 0 より大きい `UIDValidity` が返る
  - このユーザは greenmail に起動時オプションで事前登録されていること（SMTP 配送によるユーザ作成は INBOX にメッセージが残るため不可）
- **AC-06**: SMTP でテストメッセージを注入後、`FetchMeta` でそのメッセージのメタ情報（UID、サイズ、Message-ID を含む）が取得できる
- **AC-07**: `FetchMeta` で取得した UID を指定して `Download` を実行すると、メール本文（ヘッダと本文を含む）が取得できる
- **AC-08**: `MarkSeen` を実行した後、`FetchMeta` で該当メッセージの `Seen` フラグが `true` になっている
- **AC-09**: 同一メールボックスへの連続 `FetchMeta` 呼び出しで `UIDValidity` の値が一致する（安定している）

### F-003: UIDVALIDITY 変化の検出統合テスト

メールボックスを削除して再作成すると `UIDValidity` が変化することを実 IMAP サーバで確認する。INBOX は IMAP 仕様上削除できないため、固有名の非 INBOX メールボックスを `CREATE`/`DELETE`/`CREATE` で操作する。

**受け入れ条件（Acceptance Criteria）**:

- **AC-10**: 固有名の非 INBOX メールボックスで `FetchMeta` を実行して `UIDValidity = V1` を得た後、そのメールボックスを削除して同名で再作成し、再度 `FetchMeta` を実行すると `UIDValidity = V2 ≠ V1` となる

### F-004: recovery フロー End-to-End 統合テスト

実 IMAP サーバ（greenmail）とファイルシステム上の実ストアを使い、fetch コマンド実行 → UIDVALIDITY 変化 → fetch 再実行による recovery-required 検出 → recover コマンドによる解消、という一連のフローを End-to-End で検証する。

対象メールボックスは F-003 と同様に固有名の非 INBOX メールボックスとする。本フローは UIDVALIDITY の記録・不一致検出が対象であり、空のメールボックスでも検証できる。

fetch サブコマンドが IMAP 認証情報を読み取る環境変数（`IMAP_TEST_USER`/`IMAP_TEST_PASS` とは別）をテスト実行時に設定する必要がある。

**受け入れ条件（Acceptance Criteria）**:

- **AC-11**: fetch を実行してストアに `UIDValidity` が記録された後、メールボックスを削除→再作成して `UIDValidity` を変化させ、fetch を再実行すると、(1) fetch が非ゼロの終了コードを返し、(2) ストアの recovery-required 状態が設定される
- **AC-12**: AC-11 の状態から `recover --mode keep-old` を実行すると recovery-required 状態が解消され、次の fetch が正常終了する
- **AC-13**: AC-11 の状態から `recover --mode discard-old --yes` を実行すると recovery-required 状態が解消され、次の fetch が正常終了する

### F-005: GitHub Actions 統合テスト実行環境

GitHub Actions ワークフローで greenmail をサービスコンテナとして起動し、統合テストを実行できるジョブを追加する。

**受け入れ条件（Acceptance Criteria）**:

- **AC-14**: GitHub Actions ワークフローに `integration-test` ジョブが追加され、greenmail サービスコンテナが起動した状態で統合テストが実行される
- **AC-15**: 通常の unit test ジョブ（`integration` タグなし）は引き続き greenmail なしで実行され、統合テストは別ジョブとして分離される
- **AC-16**: `integration-test` ジョブは、Go ソース・Makefile・GitHub Actions ワークフロー・devcontainer・`testdata/` のいずれかが変更された PR で実行される

---

## 4. 非機能要件

### テスト分離

- 統合テストはすべて `//go:build integration` タグを付与し、通常の `go test ./...` では実行されない
- 各統合テストは独立して実行でき、実行順序に依存しない
- テストケースは固有の分離単位（SMTP 注入を伴うテストは固有の受信者ユーザの INBOX、`CREATE`/`DELETE` を伴うテストは固有名の非 INBOX メールボックス）を使用し、テスト間のメッセージ・状態の混入を防ぐ

### 保守性

- SMTP 送信・メールボックス作成/削除などのテスト共通処理はヘルパー関数としてまとめ、各テストケースから再利用できること
- greenmail の接続情報は環境変数から取得し、ハードコードしない

### セキュリティ

- `InsecureSkipVerify: true` はテスト環境（greenmail）専用であり、本番コードから参照する設定ファイルには設定しない
- `internal/config.IMAPConfig`、TOML 設定、ユーザ向け設定サンプルには `InsecureSkipVerify` を追加しない

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- IMAP クライアントには既存の `emersion/go-imap` を使用する
- SMTP 送信には Go 標準ライブラリの `net/smtp` パッケージを使用する（新規依存を追加しない）
- テストには `stretchr/testify` を使用する
- 統合テストは devcontainer 環境と GitHub Actions 環境の両方で実行できること

---

## 6. 前提と既知のリスク

- **InsecureSkipVerify の使用**: greenmail 2.1.3 の証明書に SAN がないため、統合テストでは TLS ホスト名検証をバイパスする必要がある。このフィールドはテスト専用であり、`internal/config.IMAPConfig` や TOML 設定には公開しない。
- **UIDVALIDITY の変化**: 「同名メールボックスを削除→再作成すると `UIDValidity` が変化する」ことは greenmail の実装挙動に依存する（RFC 3501 は再作成時の `UIDVALIDITY` 変化を保証しない）。本タスクは greenmail `2.1.3`（docker-compose で固定済み）の挙動を前提とする。将来バージョンで挙動が変わった場合、AC-10／AC-11 が失敗しうる。
