# プロセス間ロック設計ガイドライン

## 概要

本プロジェクトは複数の CLI サブコマンドが同じ store を読み書きする。
同時実行による不整合を防ぐため、目的の異なる 2 種類のロックを使い分ける。

| ロック | 解決する問題 |
|---|---|
| store-wide process lock | 書き込み系サブコマンド同士の同時実行を防ぐ |
| summary consistency guard | `summary` と `fetch` の並走時に `recovery_required` の見逃しを防ぐ |

この 2 つは代替関係ではなく、それぞれ独立した問題を解決する。

---

## 1. サブコマンドの並走可否

| | fetch | gc | reprocess | recover | summary |
|---|---|---|---|---|---|
| **fetch** | ✗ | ✗ | ✗ | ✗ | ○ |
| **gc** | ✗ | ✗ | ✗ | ✗ | ○ |
| **reprocess** | ✗ | ✗ | ✗ | ✗ | ○ |
| **recover** | ✗ | ✗ | ✗ | ✗ | ○ |
| **summary** | ○ | ○ | ○ | ○ | ○ |

`fetch`・`gc`・`reprocess`・`recover` は store-wide process lock（排他）を保持するため
互いに並走できない。`summary` は store-wide process lock を取得せず、
書き込み系サブコマンドと並走できる。

`summary` 同士の並走は summary consistency guard（shared lock）が許容するため問題ない。

---

## 2. store-wide process lock

### 目的

書き込み系サブコマンド同士を直列化し、reset manifest・staging・sentinel の
状態機械を単一 writer 前提で安全に操作できるようにする。

### ロックファイル

`{root_dir}/.tlsrpt-digest-store.lock`（排他 flock、non-blocking）

取得できない場合は、他プロセスが同じ store を書き込み中とみなし待機せず失敗する。

### 対象サブコマンド

- `fetch`
- `gc`
- `reprocess`
- `recover`（`--mode keep-old` / `discard-old` / `--abort-reset` のいずれも）

### 契約

1. store を開く（`store.Open(...)` 呼び出し）より前に取得する。
2. 処理完了まで保持する（異常終了パスを含む）。
3. `recover --mode discard-old --yes` / `recover --abort-reset --yes` は
   lock 保持中に `OpenRecoverReset` を使う。
4. `ResetForRecovery` / `AbortReset` は 1 writer 前提で設計されているため、
   呼び出し側が必ず store-wide process lock を保持する。
5. `internal/store` 単体テストから直接呼ぶ場合は OS レベルの lock は不要だが、
   単一ゴルーチンなど single writer 前提を明示する。

---

## 3. summary consistency guard

### なぜ必要か

`summary` は `fetch` と並走できる設計であるため store-wide process lock を取得しない。
`fetch` は UID validity の変更を検出すると `recovery_required` sentinel を書き込む。
`summary` がこの書き込みを見逃したまま集計結果を送信すると、
不整合なサマリーが通知される。

これを防ぐのが summary consistency guard である。

### ロックファイルとロック種別

ロックファイル: `{root_dir}/.tlsrpt-digest-summary.lock`

| 取得者 | flock 種別 |
|---|---|
| `summary` | shared |
| `recovery_required` を変更する store API | exclusive |

`summary` が shared lock を保持している間、`recovery_required` sentinel への
書き込み（exclusive lock 取得）はブロックされる。fetch のその他の処理
（メール取得・レポート保存など）は並走する。

### `recovery_required` を変更する store API（排他 lock が必要）

- `SaveRecoveryRequired`
- `ClearRecoveryRequired`
- `ApplyRecovery`
- `ResetForRecovery` の commit 処理（`commitReset`）

以下は `recovery_required` を変更しないため guard 不要：

- `ResetForRecovery` の初期 manifest/staging 作成
- `stageDataFile` / `stageEmailsDir`
- `AbortReset` の restore 処理
- commit 後の cleanup

### `SaveRecoveryRequired` を呼ぶのは fetch のみ

`SaveRecoveryRequired` を呼び出すのは現在 `fetch` だけである。
`gc`・`reprocess`・`recover` は `recovery_required` sentinel を書き込まない。
これらは store-wide process lock によって `fetch` とも `summary` とも
同時実行されないため、summary consistency guard の対象外である。

**summary consistency guard が対象とする競合は `fetch` との並走のみ**であり、
それ以外の競合は store-wide process lock が排除する。

---

## 4. summary の recovery_required チェック設計

### チェックのタイミング

`summary` は集計開始前に `CheckRecoveryRequired` を 1 回だけ呼ぶ。

```
CheckRecoveryRequired   ← 集計開始前
    ↓ found=true: 通知して終了
GenerateSummary（store 読み取り）
    ↓ ReportCount == 0: exitOK
buildNotifier
LogSummary / Flush（Slack 送信）
```

### チェックの目的と保証範囲

集計開始前に `recovery_required` が立っていれば、その後の `recover` でストアデータが
すべて削除されることが確定している。消えるデータの集計を送信しても混乱を招くだけなので、
通知して終了する。

集計開始後に fetch が `recovery_required` を書き込んだ場合は、そのまま集計・送信を続ける。
fetch がエラー通知を別途送るため、ユーザーは問題を把握できる。
集計済みデータはその時点の正しいデータであり、送信しても問題ない。

### なぜ送信直前の再チェックをしないか

以下の理由から、集計後・送信直前の再チェックは不要と判断している。

- 蓄積済みレポートデータは UID validity 変更の影響を受けない。
  変更が起きても過去に正常処理されたレポートの内容は正しい。
- fetch がエラー通知を送るため、ユーザーはすでに異常を把握している。
- summary は定期スナップショットであり、わずかなタイミング差は許容範囲内である。

---

## 5. 過剰保護を避ける方針

summary consistency guard を store-wide process lock の代わりに使ってはならない。
guard は `recovery_required` の可視性のみを守るものであり、
manifest や staging の状態機械全体を直列化するものではない。

避けるべきパターン：

- `recovery_required` を変更しない処理を summary guard で囲み、
  `summary` を不要にブロックする
- manifest 作成だけを summary guard で囲み、
  後続の staging / commit / cleanup を無保護にする
- 2 種類のロックの責務を同じコメントや API 名で混同する

望ましい責務分担：

| 問題 | 解決手段 |
|---|---|
| 書き込み系サブコマンド同士の直列化 | store-wide process lock（cmd 層） |
| `summary` vs `fetch` の `recovery_required` 競合 | summary consistency guard（`internal/store` 層） |
| crash recovery の原子性 | reset manifest・staging・sentinel commit barrier |

---

## 6. 実装・レビュー時のチェックリスト

**書き込み系サブコマンドを追加・変更するとき**

- [ ] store open 前に store-wide process lock を取得している
- [ ] 処理完了まで（異常終了パスを含む）lock handle を保持している
- [ ] `recover --mode discard-old --yes` / `recover --abort-reset --yes` は
  lock 保持中に `OpenRecoverReset` を使っている
- [ ] `ResetForRecovery` / `AbortReset` を直接呼ぶ場合は store-wide process lock を
  保持している
- [ ] `internal/store` 単体テストから直接呼ぶ場合は single writer 前提を明示している
- [ ] 新たに `SaveRecoveryRequired` を呼ぶ場合は section 3 の契約に従い、
  summary consistency guard との整合を確認している

**`recovery_required` を変更する store API を追加・変更するとき**

- [ ] `{root_dir}/.tlsrpt-digest-summary.lock` に対して排他 lock を取得している
  （`withGuardExclusive` を使用）
- [ ] `recovery_required` を変更しない処理を summary guard で囲んでいない
- [ ] `summary` が stale な「復旧不要」判断で送信しないことをテストしている

**summary サブコマンドまたは recovery_required チェック設計を変更するとき**

- [ ] `CheckRecoveryRequired` の呼び出しタイミングと目的が section 4 と一致している
- [ ] チェック位置を追加・変更した場合は section 4 を更新している

---

## 7. 関連文書

- `docs/dev/adr/0003_reset_phase_design.ja.md`
- `docs/tasks/0070_entrypoint/02_architecture.md` §3.3 / §6.4
- `docs/tasks/0070_entrypoint/03_implementation_plan.md` ステップ 1-5 / 3-3
