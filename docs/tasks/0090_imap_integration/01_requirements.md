# 要件定義書：greenmail を使った IMAP 統合テスト

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-28 |
| レビュー日 | - |
| レビュアー | - |
| コメント | 実装コードとの突き合わせレビューを実施。InsecureSkipVerify を廃止し既存の TLSCACert で対応、IMAPS ポート公開不要、既存統合テスト雛形・Makefile ターゲットの反映など修正。 |

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

- greenmail の CA 証明書（`testdata/greenmail-ca.pem`）をリポジトリにコミットし、既存の `TLSCACert` フィールドを介して統合テストから参照する（greenmail 2.1.3 はイメージに固定の自己署名証明書を組み込んでおり、起動ごとに再生成しない）
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
- devcontainer の IMAP テスト接続ポートを IMAPS（3993）に変更（greenmail は `setup.test.all` により 3993/IMAPS を待ち受ける。現行の 3143/平文ポートは TLS 専用の `imapClient`（`NewIMAPClient` は常に TLS でダイヤルする）では利用できない）
  - `dev-env` の `IMAP_TEST_PORT` を 3993 に変更する（`dev-env`→`greenmail` 間はコンテナ間通信のため、ポート公開設定の変更は不要）
  - `dev-env` の `IMAP_TEST_CA_CERT` を `testdata/greenmail-ca.pem` のリポジトリ内絶対パスに設定する
- GitHub Actions ワークフローへの統合テスト実行ジョブ追加
- 既存 `make test-integration` ターゲットを、`internal/imap` だけでなく recovery E2E テストも実行する内容に更新する

### 対象外（Out of Scope）

- greenmail の SMTP 機能（TLS、認証等）の詳細テスト
- `internal/imap` パッケージへの平文 IMAP（非 TLS）接続サポートの追加
- `internal/imap.Config` および TOML 設定への `InsecureSkipVerify` フィールド追加
- IMAP IDLE・PUSH 方式のテスト
- 実運用 IMAP サーバへの接続テスト

### 影響を受けるコンポーネント

- **直接変更**:
  - `internal/imap/client_integration_test.go`（既存の統合テスト雛形を実テストへ拡張）
  - `.devcontainer/docker-compose.base.yml`（`dev-env` の `IMAP_TEST_PORT` を 3993、`IMAP_TEST_CA_CERT` を証明書の絶対パスに設定）
  - `.github/workflows/`（統合テストジョブ追加、`IMAP_TEST_CA_CERT` 環境変数を設定）
  - `Makefile`（既存 `test-integration` ターゲットを `./...` または `internal/imap/...` + `cmd/tlsrpt-digest/...` に拡張）
- **新規作成**:
  - `testdata/greenmail-ca.pem`（greenmail 2.1.3 の自己署名 CA 証明書）
  - `cmd/tlsrpt-digest/recovery_integration_test.go`（recovery フロー E2E テスト）

---

## 3. 機能要件

### F-001: greenmail CA 証明書のテストデータ追加

greenmail 2.1.3 はイメージに固定の自己署名証明書を組み込んでいる（起動ごとに再生成しない）。この証明書を `testdata/greenmail-ca.pem` としてリポジトリにコミットし、統合テストで既存の `imap.Config.TLSCACert` フィールドを通じて参照することで、`internal/imap` パッケージのコード変更なしに greenmail への TLS 接続を実現する。

証明書ファイルへのパスは環境変数 `IMAP_TEST_CA_CERT` から取得し、ハードコードしない。

**受け入れ条件（Acceptance Criteria）**:

- **AC-01**: `testdata/greenmail-ca.pem` が存在し、greenmail 2.1.3 の自己署名証明書（PEM 形式）が格納されている
- **AC-02**: 統合テストが `imap.Config{TLSCACert: os.Getenv("IMAP_TEST_CA_CERT"), ...}` で greenmail:3993 に接続できる（証明書検証が成功する）
- **AC-03a**: `internal/imap.Config` および `internal/config.IMAPConfig` に `InsecureSkipVerify` フィールドは追加しない

### F-002: `imapClient` の greenmail 接続統合テスト

greenmail IMAPS（ポート 3993）に対して `imapClient` の各操作が正しく動作することを検証する。

テストメッセージの注入には greenmail の SMTP ポート（3025）を使用する。SMTP の配送先は受信者ユーザの INBOX に限られ（任意名のメールボックスへ SMTP で直接配送することはできない）、テスト間の干渉を避ける分離単位は「固有の受信者ユーザ（= その INBOX）」とする。greenmail は初回 SMTP 配送時にユーザを自動作成する（パスワード = メールアドレス）ため、各テストケースは `t.Name()` 等から導出した固有のメールアドレスを受信者として使用する。

なお `FetchMeta` は Envelope が nil または INTERNALDATE がゼロのメッセージをスキップするため、メタ情報（特に Message-ID）を検証するテストでは、注入するメールに `Message-ID` ヘッダを含める。

**受け入れ条件（Acceptance Criteria）**:

- **AC-04**: 統合テストの実行に必要な環境変数（IMAP 接続テストは `IMAP_TEST_HOST`、`IMAP_TEST_PORT`、`IMAP_TEST_USER`、`IMAP_TEST_PASS`、`IMAP_TEST_MAILBOX`、`IMAP_TEST_CA_CERT`。SMTP 注入を伴うテストは `IMAP_TEST_SMTP_HOST`、`IMAP_TEST_SMTP_PORT` も）が設定されていない場合、統合テストは `t.Skip()` でスキップされ、CI の通常テストパスを妨げない
- **AC-05**: greenmail の空のメールボックスに対して `FetchMeta` を実行すると、空のメッセージリストと 0 より大きい `UIDValidity` が返る
- **AC-06**: SMTP でテストメッセージを注入後、`FetchMeta` でそのメッセージのメタ情報（UID、サイズ、Message-ID を含む）が取得できる
- **AC-07**: `FetchMeta` で取得した UID を指定して `Download` を実行すると、メール本文（ヘッダと本文を含む）が取得できる
- **AC-08**: `MarkSeen` を実行した後、`FetchMeta` で該当メッセージの `Seen` フラグが `true` になっている
- **AC-09**: 同一メールボックスへの連続 `FetchMeta` 呼び出しで `UIDValidity` の値が一致する（安定している）

### F-003: UIDVALIDITY 変化の検出統合テスト

メールボックスを削除して再作成すると `UIDValidity` が変化することを実 IMAP サーバで確認する。

INBOX は IMAP 仕様上削除できないため、本テストでは IMAP `CREATE` で作成した固有名の**非 INBOX メールボックス**を対象に `DELETE`→`CREATE` で再作成する。メールボックスの作成・削除は `MailFetcher`／`imapSession` の抽象に含まれないため、`emersion/go-imap` クライアントを直接用いるテストヘルパーで行う。

**受け入れ条件（Acceptance Criteria）**:

- **AC-10**: 固有名の非 INBOX メールボックスで `FetchMeta` を実行して `UIDValidity = V1` を得た後、そのメールボックスを削除して同名で再作成し、再度 `FetchMeta` を実行すると `UIDValidity = V2 ≠ V1` となる

### F-004: recovery フロー End-to-End 統合テスト

実 IMAP サーバ（greenmail）とファイルシステム上の実ストアを使い、fetch コマンド実行 → UIDVALIDITY 変化 → fetch 再実行による recovery-required 検出 → recover コマンドによる解消、という一連のフローを End-to-End で検証する。

対象メールボックスは F-003 と同様に `CREATE` した固有名の非 INBOX メールボックスとする（`imapClient` は `Config.Mailbox` に接続するため、設定でこのメールボックスを指定する）。本フローは UIDVALIDITY の記録・不一致検出のみが対象であり、メッセージ本体の有無には依存しないため空のメールボックスでも検証できる。

**受け入れ条件（Acceptance Criteria）**:

- **AC-11**: fetch を実行してストアに `UIDValidity` が記録された後、メールボックスを削除→再作成して `UIDValidity` を変化させ、fetch を再実行すると、(1) fetch が非ゼロの終了コード（`exitError`）を返し、(2) ストアの recovery-required センチネルが設定される（`LoadRecoveryRequired` が `found = true` を返す）
  - 補足: fetch は UIDVALIDITY 不一致を検出すると `SaveRecoveryRequired` でセンチネルに記録したうえで `exitError` を返す（エラー値は返さない）。`store.ErrRecoveryRequired` というエラーシンボルは存在しないため、本 AC は終了コードとセンチネル状態で検証する。
- **AC-12**: AC-11 の状態から `recover --mode keep-old` を実行すると recovery-required 状態が解消され、次の fetch が正常終了する（`exitOK`）
- **AC-13**: AC-11 の状態から `recover --mode discard-old --yes` を実行すると recovery-required 状態が解消され、次の fetch が正常終了する（`exitOK`）

### F-005: GitHub Actions 統合テスト実行環境

GitHub Actions ワークフローで greenmail をサービスコンテナとして起動し、統合テストを実行できるジョブを追加する。

**受け入れ条件（Acceptance Criteria）**:

- **AC-14**: GitHub Actions ワークフローに `integration-test` ジョブが追加され、greenmail サービスコンテナが起動した状態で `make test-integration`（または同等の `go test -tags test,integration ./...`）が実行される
- **AC-15**: 通常の unit test ジョブ（`integration` タグなし）は引き続き greenmail なしで実行され、統合テストは別ジョブとして分離される
- **AC-16**: `integration-test` ジョブは、Go ソース・Makefile・GitHub Actions・devcontainer のいずれかが変更された PR で実行される。通常テストの変更検出が documentation-only PR をスキップする方針と競合しないよう、統合テスト用の変更検出条件を明示的に追加する

---

## 4. 非機能要件

### テスト分離

- 統合テストはすべて `//go:build integration` タグを付与し、通常の `go test ./...` では実行されない
- 各統合テストは独立して実行でき、実行順序に依存しない
- テストケースは固有の分離単位（SMTP 注入を伴うテストは固有の受信者ユーザの INBOX、`CREATE`/`DELETE` を伴うテストは `t.Name()` 等を含む固有名の非 INBOX メールボックス）を使用し、テスト間のメッセージ・状態の混入を防ぐ

### 保守性

- SMTP 送信・メールボックス作成/削除などのテスト共通処理はヘルパー関数としてまとめ、各テストケースから再利用できること（メールボックスの `CREATE`/`DELETE`/`APPEND` は `MailFetcher` 抽象に存在しないため、`emersion/go-imap` クライアントを直接呼び出すヘルパーで実装する）
- greenmail の接続情報は環境変数から取得し、ハードコードしない

### セキュリティ

- `testdata/greenmail-ca.pem` はテスト専用の自己署名証明書であり、本番設定ファイルでは使用しない
- 統合テストでも証明書検証を行い（`InsecureSkipVerify` を使わない）、テストコードが本番のセキュリティ要件から乖離しない

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）
- IMAP クライアントには既存の `emersion/go-imap` を使用する
- SMTP 送信には Go 標準ライブラリの `net/smtp` パッケージを使用する（新規依存を追加しない）
- テストには `stretchr/testify` を使用する
- 統合テストは devcontainer 環境と GitHub Actions 環境の両方で実行できること

---

## 6. テスト方針

### 統合テスト（本タスクの主体）

- `internal/imap/client_integration_test.go` の既存雛形を拡張し、`//go:build integration` タグ付きで IMAP クライアント操作テストを追加する
- `cmd/tlsrpt-digest/recovery_integration_test.go` に `//go:build integration` タグ付きで recovery フロー E2E テストを追加する
- テスト環境変数が未設定の場合は `t.Skip()` でスキップする
- `make test-integration` は `go test -v -count=1 -tags test,integration ./...`、または少なくとも `internal/imap/...` と `cmd/tlsrpt-digest/...` の両方を対象にする

### 単体テスト（変更なし）

- `internal/imap` パッケージのコード変更がないため、既存の `TestBuildTLSConfig*` テストへの影響はない

---

## 7. 前提と既知のリスク

- **CA 証明書のバージョン固定**: `testdata/greenmail-ca.pem` は greenmail `2.1.3` に固定された証明書（有効期限 2027-05-10）である。将来イメージバージョンを上げた場合は証明書を更新する必要がある。
- **UIDVALIDITY の変化**: 「同名メールボックスを削除→再作成すると `UIDValidity` が変化する」ことは greenmail の実装挙動に依存する（RFC 3501 は再作成時の `UIDVALIDITY` 変化を保証はしない）。本タスクは greenmail `2.1.3`（docker-compose で固定済み）の挙動を前提とする。将来バージョンで挙動が変わった場合、AC-10／AC-11 が失敗しうる。
- **SMTP ヘルパーの実装**: greenmail の SMTP（3025）は平文・無認証で待ち受ける。`net/smtp` の `SendMail` は STARTTLS をネゴシエートしようとする場合があるため、必要に応じて `smtp.Client` の低レベル API（`Mail`/`Rcpt`/`Data`）で平文送信する。
- **既存 Makefile ターゲット**: `make test-integration` は既に存在するが、現状は `./internal/imap/...` のみを対象にしている。recovery E2E 追加後もローカルと CI の実行範囲が一致するよう、本タスクで対象パッケージを拡張する。
