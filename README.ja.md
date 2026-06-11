# tlsrpt-digest

SMTP TLS Reporting (RFC 8460) レポートを IMAP メールボックスから自動取得・解析し、TLS 接続の失敗を検出したときに即時アラートを、正常時には定期サマリを Slack で通知するツールです。

## 目次

- [概要](#概要)
- [インストール](#インストール)
- [クイックスタート](#クイックスタート)
- [設定ファイル（TOML）](#設定ファイルtoml)
- [環境変数](#環境変数)
- [サブコマンド](#サブコマンド)
- [スケジューリング](#スケジューリング)
- [詳細ドキュメント](#詳細ドキュメント)

---

## 概要

Google などの大手メール送信者は、受信側の TLS ポリシー（MTA-STS / DANE）の適用状況を RFC 8460 準拠の JSON レポートとして毎日メールで送信します。tlsrpt-digest はこのレポートを自動処理します。

**処理の流れ：**

1. IMAP メールボックスに接続し、未処理のレポートメールを取得する
2. 添付の `.json.gz` ファイルを展開・パースし、`failure_session_count` を評価する
3. 失敗が検出された場合 → Slack に即時アラートを送信する
4. 失敗なしの場合 → データをローカルに蓄積し、定期サマリとして通知する

プログラムは one-shot で実行して終了します。定期実行は systemd timer または cron で管理します。

---

## インストール

### ビルド

Go 1.26 以上が必要です。

```bash
git clone https://github.com/isseis/tlsrpt-digest.git
cd tlsrpt-digest
make build
# バイナリ: ./build/tlsrpt-digest
```

### Docker コンテナで動かす場合の注意

最小イメージ（`ubuntu:24.04` など）では `ca-certificates` が未インストールの場合があります。IMAP の TLS 接続に必要なため、Dockerfile に以下を追加してください。

```dockerfile
RUN apt-get update && apt-get install -y ca-certificates
```

---

## クイックスタート

### 1. 設定ファイルを作成する

`config.toml` を作成します（テンプレートは次のセクションを参照）。

### 2. 環境変数を設定する

```bash
export TLSRPT_IMAP_USERNAME="your-imap-username"
export TLSRPT_IMAP_PASSWORD="your-imap-password"
export TLSRPT_SLACK_WEBHOOK_URL_SUCCESS="https://hooks.slack.com/services/..."
export TLSRPT_SLACK_WEBHOOK_URL_ERROR="https://hooks.slack.com/services/..."
```

### 3. fetch を実行する

```bash
./build/tlsrpt-digest --config config.toml fetch
```

---

## 設定ファイル（TOML）

設定ファイルのパスは `-c` / `--config` フラグで必ず指定してください。省略するとエラーになります。

> **注意：** パスワードや Webhook URL などの機密情報は設定ファイルに書かず、[環境変数](#環境変数) で渡します。

### 最小構成

```toml
[imap]
host = "imap.example.com"
port = 993

[notify.slack]
allowed_host = "hooks.slack.com"
```

> **注意：** Slack Webhook URL を環境変数で設定する場合は `[notify.slack] allowed_host` が必須です。Slack 通知を使用しない場合のみ `[notify.slack]` セクション全体を省略できます。

### 全設定項目

```toml
[imap]
# IMAP サーバのホスト名（必須）
host = "imap.example.com"

# IMAP サーバのポート番号（必須）
port = 993

# 監視するメールボックス名（省略時: "INBOX"）
# TLSRPT レポートを専用フォルダに振り分けている場合は明示指定が必要
# Gmail のカスタムラベルはそのままフォルダ名として使用可（"[Gmail]/" プレフィックス不要）
mailbox = "tls-reports"

# fetch 時に取得対象とする期間（日数）（省略時: 14）
fetch_days = 14

# カスタム CA 証明書ファイルのパス（省略時: システム証明書を使用）
# 自己署名証明書を使用する IMAP サーバに接続する場合に設定
tls_ca_cert = ""

# 1 通あたりの最大メッセージサイズ（バイト）（省略時: 1048576 = 1 MiB、0 を指定すると制限なし）
max_message_bytes = 1048576

# IMAP メールボックス上のメール保持期間（日数）（省略時: 0 = 無効）
# 0 より大きい値を設定すると、gc 実行時に INTERNALDATE（時刻を切り捨てた日付）が
# (今日 - retention_days) より古い IMAP メールを削除する（不可逆操作）。
# 有効化する場合は、imap.fetch_days と summary.window_days の
# いずれと比べても大きいか等しい値にすること（設定エラーで起動を拒否する）。
retention_days = 0

[notify.slack]
# Slack Webhook URL の許可ホスト名（Webhook URL のホスト名と一致させること）
# 誤った送信先への通知を防ぐためのセキュリティ検証。スキームやポート番号は含めないこと
# 例: "hooks.slack.com"
allowed_host = "hooks.slack.com"

[store]
# レポートデータの保存ディレクトリ（省略時: "./store"）
root_dir = "/var/lib/tlsrpt-digest"

# レポート JSON データの保持期間（日数）（省略時: 30）
retention_days = 30

# 元メール（.eml ファイル）の保持期間（日数）（省略時: 30）
# retention_days 以上に設定すること（再処理時に元メールが必要なため）
max_email_age_days = 30

[summary]
# summary コマンドが対象とする期間（日数）（省略時: 7）
window_days = 7
```

### IMAP メールボックスの保持期間（`imap.retention_days`）について

#### 基本動作（オプトイン）

`retention_days` のデフォルトは `0`（無効）です。利用者が明示的に正の値を設定したときのみ、IMAP メールボックス上のメール削除が有効化されます（オプトイン）。ここでの「IMAP メールボックス」は `imap.mailbox` で指定したメールボックスを指し、`gc` はそれ以外のメールボックスのメールを削除しません。

有効化する（`retention_days > 0` にする）と、`gc` の実行に IMAP 認証情報（`TLSRPT_IMAP_USERNAME` / `TLSRPT_IMAP_PASSWORD`）が必須になります。`retention_days = 0`（デフォルト）のままであれば、`gc` は IMAP に接続せず、認証情報も不要です。

#### 前提条件

IMAP サーバーが UIDPLUS（RFC 4315）に対応していない場合、`gc` は IMAP メールを削除しません（`server does not support UIDPLUS; skipping delete` という警告ログを出力し、削除件数は 0 になります）。`retention_days > 0` を設定していても、サーバーが UIDPLUS に対応していなければメールボックスの蓄積は抑止されないため、事前に IMAP サーバーが UIDPLUS に対応していることを確認してください。

Gmail を使用する場合は、IMAP 設定（「メール転送と POP/IMAP」タブ）で、メッセージに `\Deleted` が付与され EXPUNGE されたときの挙動を確認してください。デフォルトの「メールをアーカイブする」のままだと、UID EXPUNGE はラベル除去（「すべてのメール」への移動）となりストレージは解放されません。サーバー上の蓄積を実際に抑止するには、「メールを完全に削除する」または「ゴミ箱に移動する」（+ ゴミ箱メールの自動完全削除）に変更する必要があります。

#### 削除前の確認（`gc --dry-run`）

IMAP からのメール削除は不可逆操作であり、削除対象を TLSRPT レポートに限定する絞り込みは行われません。そのため、TLSRPT レポート受信専用のメールボックスを使用することを推奨します。

`gc --dry-run` は `imap.retention_days` が 0 より大きい場合のみ IMAP 上の削除候補を確認します。`retention_days = 0` のまま `gc --dry-run` を実行すると `would_delete_imap_count=0` と表示されますが、これは削除候補が存在しないという意味ではなく、確認自体が行われていないことを意味します。

そのため、有効化する際は次の手順で進めてください。

1. 希望する正の値を `imap.retention_days` に設定する。
2. `gc --dry-run` を実行して削除候補件数を確認する。
3. 結果を確認したうえで、（dry-run なしの）`gc` を初めて実行する。

#### 設定値の関係

`imap.fetch_days`（および `fetch --since` で指定する取得期間）は `imap.retention_days` 以下にしてください。それより古いメールは `gc` によって IMAP 上から削除され、以後 `fetch` で取得できなくなります。

`imap.retention_days` を上書きするコマンドラインフラグはありません（`gc --before` / `--max-email-age` はローカルストアの保持期間のみを上書きし、IMAP 削除のカットオフには影響しません）。IMAP の保持期間は設定ファイルでのみ変更できます。

---

## 環境変数

機密情報は以下の環境変数で設定します。

| 環境変数 | 説明 |
|---|---|
| `TLSRPT_IMAP_PASSWORD` | IMAP 認証パスワード |
| `TLSRPT_IMAP_USERNAME` | IMAP 認証ユーザ名 |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | 正常時（サマリ）通知用 Slack Webhook URL |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | 異常時（アラート）通知用 Slack Webhook URL |

### Gmail を使う場合

Google は通常のパスワードによる IMAP 接続を廃止しています。Gmail を使う場合は **アプリパスワード** が必要です。

1. Google アカウント → セキュリティ → 2 段階認証を有効化する
2. セキュリティ → アプリパスワード → パスワードを生成する
3. 生成されたパスワードを `TLSRPT_IMAP_PASSWORD` に設定する

---

## サブコマンド

```
tlsrpt-digest --config <path> <fetch|summary|reprocess|gc|recover> [オプション]
tlsrpt-digest help
```

`--config` はグローバルフラグで、サブコマンドの前に指定します。詳細なヘルプは `tlsrpt-digest help` で確認できます。

### fetch

IMAP メールボックスからレポートを取得・処理します。

```bash
tlsrpt-digest --config path fetch [--dry-run] [--since duration]
```

| オプション | 説明 |
|---|---|
| `--dry-run` | IMAP に接続してメタ情報を確認するが、メールのダウンロード・保存・通知は行わない |
| `--since duration` | 取得期間の上書き（例: `2d`、`1w`。単位は日 `d` または週 `w`）。`7d` の場合、本日の UTC 0 時を終点として、その7日前の UTC 0 時以降が対象となる |

### summary

蓄積されたレポートを集計して定期サマリを送信します。

```bash
tlsrpt-digest --config path summary [--dry-run] [--window duration]
```

| オプション | 説明 |
|---|---|
| `--window duration` | 集計期間の上書き（例: `7d`、`2w`。単位は日 `d` または週 `w`）。`7d` の場合、本日の UTC 0 時を終点として、その7日前の UTC 0 時から本日の UTC 0 時までが対象となる |

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

### reprocess

保存済みの `.eml` ファイルを再解析します。通知設定の変更後や、初回処理のエラー後に使用します。

```bash
tlsrpt-digest --config path reprocess [--notify]
```

### recover

ストアの整合性を確認・修復します。

```bash
tlsrpt-digest --config path recover [--mode keep-old|discard-old] [--yes]
```

---

## スケジューリング

tlsrpt-digest 自体はスケジューリング機能を持ちません。systemd timer または cron で定期実行します。

### systemd timer を使う場合

`fetch` 用と `summary` 用の 2 つのタイマーを設定します。

**`/etc/systemd/system/tlsrpt-digest-fetch.service`**

```ini
[Unit]
Description=tlsrpt-digest fetch

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml fetch
EnvironmentFile=/etc/tlsrpt-digest/env
User=tlsrpt
```

**`/etc/systemd/system/tlsrpt-digest-fetch.timer`**

```ini
[Unit]
Description=tlsrpt-digest fetch timer

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

> `Persistent=true` を設定すると、システム停止中に実行されなかった分を再起動後に補完します。

**`/etc/systemd/system/tlsrpt-digest-summary.service`**

```ini
[Unit]
Description=tlsrpt-digest summary

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml summary
EnvironmentFile=/etc/tlsrpt-digest/env
User=tlsrpt
```

**`/etc/systemd/system/tlsrpt-digest-summary.timer`**

```ini
[Unit]
Description=tlsrpt-digest summary timer

[Timer]
OnCalendar=Mon *-*-* 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

環境変数ファイル **`/etc/tlsrpt-digest/env`**：

```
TLSRPT_IMAP_USERNAME=your-imap-username
TLSRPT_IMAP_PASSWORD=your-imap-password
TLSRPT_SLACK_WEBHOOK_URL_SUCCESS=https://hooks.slack.com/services/...
TLSRPT_SLACK_WEBHOOK_URL_ERROR=https://hooks.slack.com/services/...
```

タイマーを有効化：

```bash
systemctl daemon-reload
systemctl enable --now tlsrpt-digest-fetch.timer
systemctl enable --now tlsrpt-digest-summary.timer
```

### cron を使う場合

シークレットをラッパースクリプト経由で読み込むことで、crontab ファイルへの平文記載を避けます。

環境変数ファイルは systemd の場合と同じ **`/etc/tlsrpt-digest/env`** を使用します（`chmod 600`、`chown root` で保護）。

ラッパースクリプト **`/usr/local/bin/tlsrpt-digest-fetch`**：

```sh
#!/bin/sh
set -eu
. /etc/tlsrpt-digest/env
exec /usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml fetch
```

ラッパースクリプト **`/usr/local/bin/tlsrpt-digest-summary`**：

```sh
#!/bin/sh
set -eu
. /etc/tlsrpt-digest/env
exec /usr/local/bin/tlsrpt-digest --config /etc/tlsrpt-digest/config.toml summary
```

スクリプトを実行可能にします：

```bash
chmod 755 /usr/local/bin/tlsrpt-digest-fetch
chmod 755 /usr/local/bin/tlsrpt-digest-summary
```

crontab（`crontab -e` で編集）：

```crontab
# 毎時 fetch を実行
0 * * * * /usr/local/bin/tlsrpt-digest-fetch

# 毎週月曜 09:00 に summary を送信
0 9 * * 1 /usr/local/bin/tlsrpt-digest-summary
```

---

## 詳細ドキュメント

| ドキュメント | 内容 |
|---|---|
| [プロジェクト概要](docs/overview.ja.md) | アーキテクチャ、処理フロー、設計判断の詳細 |
| [パッケージリファレンス](docs/dev/developer_guide/package_reference.ja.md) | 各パッケージの責務と内部構造 |

---

## 開発への参加

開発環境のセットアップから PR マージまでの流れは [開発者オンボーディングガイド](docs/dev/developer_guide/development_process.ja.md) を参照してください。

---

## ライセンス

[MIT License](LICENSE)
