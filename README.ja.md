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

# 1 通あたりの最大メッセージサイズ（バイト）（省略時: 0 = 制限なし）
max_message_bytes = 0

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

保持期間を超えた古いデータを削除します。

```bash
tlsrpt-digest --config path gc [--before duration] [--max-email-age duration]
```

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
| [パッケージリファレンス](docs/dev/developer_guide/package_reference.md) | 各パッケージの責務と内部構造 |

---

## 開発への参加

開発環境のセットアップから PR マージまでの流れは [開発者オンボーディングガイド](docs/dev/developer_guide/development_process.ja.md) を参照してください。

---

## ライセンス

[MIT License](LICENSE)
