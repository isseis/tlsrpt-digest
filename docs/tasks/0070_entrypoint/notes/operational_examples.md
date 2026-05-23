# 運用設定例メモ（0070 エントリポイント）

要件定義書から分離した運用設定例。本ファイルは要件定義書・アーキテクチャ設計書の正式な内容ではなく、**実装計画書（`03_implementation_plan.md`）作成時の参照および将来の README 執筆の素材**として保持する。

スケジューリングは systemd timer または crontab のいずれかで設定する。本質的な動作は同じであり、環境に応じて選択する。

---

## 1. systemd timer を使う場合

`fetch` 用・`summary` 用・`gc` 用でそれぞれ `.service` / `.timer` ファイルを作成する。

```ini
# /etc/systemd/system/tlsrpt-digest-fetch.service
[Unit]
Description=tlsrpt-digest IMAP fetch (one-shot)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tlsrpt-digest fetch -config /etc/tlsrpt-digest/config.toml
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-fetch.timer
[Unit]
Description=Run tlsrpt-digest fetch hourly

[Timer]
OnCalendar=hourly
Persistent=true   # システム停止中の実行分を復旧時に補完する

[Install]
WantedBy=timers.target
```

```ini
# /etc/systemd/system/tlsrpt-digest-summary.service
[Unit]
Description=tlsrpt-digest periodic summary (one-shot)

[Service]
Type=oneshot
# --window の値（または設定ファイルの集計期間 summary.window_days）はタイマーの送信頻度と整合させる。
# 下のタイマー例は毎週月曜のため --window 7d 相当。日次運用なら --window 1d。
ExecStart=/usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml --window 7d
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-summary.timer
[Unit]
Description=Run tlsrpt-digest periodic summary every Monday morning

[Timer]
OnCalendar=Mon 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

```ini
# /etc/systemd/system/tlsrpt-digest-gc.service
[Unit]
Description=tlsrpt-digest record GC (one-shot)

[Service]
Type=oneshot
# --before: JSON レポートレコードの保持期間。--max-email-age: .eml ファイルの保持期間（省略時は store.max_email_age_days の設定値）
ExecStart=/usr/local/bin/tlsrpt-digest gc -config /etc/tlsrpt-digest/config.toml --before 30d
EnvironmentFile=/etc/tlsrpt-digest/secrets.env
```

```ini
# /etc/systemd/system/tlsrpt-digest-gc.timer
[Unit]
Description=Run tlsrpt-digest GC daily

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

有効化：

```bash
systemctl enable --now tlsrpt-digest-fetch.timer
systemctl enable --now tlsrpt-digest-summary.timer
systemctl enable --now tlsrpt-digest-gc.timer
```

> **多重起動について**: `fetch`・`gc`・`recover`・`reprocess` はいずれも起動時にストア単位のプロセス排他ロックを取得するため、これらが同時に起動した場合は後発プロセスがロックを取得できず ERROR レベルのメッセージを出力し（Slack 通知あり。Slack ハンドラはロック取得前に初期化されるため通知可能）、終了コード 1 で終了する。例えば前回の `fetch` が長引いてタイマーが再発火した場合、次の `fetch` が停止する。同様に `fetch` 実行中に `gc` タイマーが発火した場合も `gc` が停止する。いずれもオペレータが想定外の長時間実行を把握できる。systemd の場合、`OnFailure=` ディレクティブで追加通知を設定することも可能。

---

## 2. crontab を使う場合

`/etc/cron.d/tlsrpt-digest` などに記述する。本質的には systemd timer と同等であり、環境変数は別ファイルで管理するか、実行前に source する。

> **環境変数の引き継ぎ**: crontab の `. secrets.env` は変数をシェルに読み込むが、`export` されていないと子プロセス（`tlsrpt-digest`）に渡らない。`set -a && . secrets.env && set +a` を使うこと（`&&` でつなぐことで `secrets.env` の読み込み失敗時に即停止する）。`secrets.env` を systemd の `EnvironmentFile=` と共用している場合、`export KEY=value` 形式にすると systemd 側が解釈できなくなるため、ファイルは `KEY=value` 形式のまま cron 側で `set -a` を使うのが安全。

```crontab
# 毎時0分に IMAP メール取得
0 * * * *  root  set -a && . /etc/tlsrpt-digest/secrets.env && set +a && /usr/local/bin/tlsrpt-digest fetch -config /etc/tlsrpt-digest/config.toml

# 毎週月曜9時に定期サマリ（7 日分、設定の summary.window_days で代替可）
0 9 * * 1  root  set -a && . /etc/tlsrpt-digest/secrets.env && set +a && /usr/local/bin/tlsrpt-digest summary -config /etc/tlsrpt-digest/config.toml --window 7d

# 毎日3時に古いレコードを削除（30 日以前）
0 3 * * *  root  set -a && . /etc/tlsrpt-digest/secrets.env && set +a && /usr/local/bin/tlsrpt-digest gc -config /etc/tlsrpt-digest/config.toml --before 30d
```

---

## 3. UIDVALIDITY 変化時の手動復旧

UIDVALIDITY 変化を検出した場合、`fetch` と `summary` は fail closed で停止する。復旧はオペレータが明示的に方針を選んで実施する。

旧データも保持して運転を再開する場合:

```bash
/usr/local/bin/tlsrpt-digest recover -config /etc/tlsrpt-digest/config.toml --mode keep-old
```

旧データを破棄して空状態から再開する場合:

```bash
/usr/local/bin/tlsrpt-digest recover -config /etc/tlsrpt-digest/config.toml --mode discard-old --yes
```

`discard-old` はローカルのレポートデータと `.eml` を削除するため、必要に応じて事前にバックアップを取得すること。

---

## 4. 環境変数まとめ（README 用素材）

| 変数 | 用途 | 必須 |
|---|---|---|
| `TLSRPT_IMAP_USERNAME` | IMAP ログイン名 | 必須 |
| `TLSRPT_IMAP_PASSWORD` | IMAP パスワード | 必須 |
| `TLSRPT_SLACK_WEBHOOK_URL_SUCCESS` | 正常時通知用 Webhook URL（INFO） | Slack 通知有効時 |
| `TLSRPT_SLACK_WEBHOOK_URL_ERROR` | 異常時通知用 Webhook URL（WARN/ERROR） | Slack 通知有効時 |

Gmail App Password を使う場合は IMAP アクセス用に App Password を発行し `TLSRPT_IMAP_PASSWORD` に設定する。設定ファイル側の `imap.mailbox` は `[Gmail]/All Mail` などラベル名を指定する。
