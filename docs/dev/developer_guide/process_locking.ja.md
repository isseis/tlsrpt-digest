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

書き込み系サブコマンド同士を直列化し、リセットマニフェスト・ステージング・センチネルの
状態機械を単一 writer 前提で安全に操作できるようにする。

ここでいう状態機械とは、UIDVALIDITY 変化時の復旧操作（`ResetForRecovery` / `AbortReset`）の
進捗を管理する仕組みである。リセットマニフェスト（`resetPhase` 1–5 を記録する進捗台帳）・
ステージングディレクトリ・センチネル（`recovery_required` と `UIDValidity` の確定状態）の
3 要素から成る。詳細は [ADR-0003](../adr/0003_reset_phase_design.ja.md) を参照。

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

### 3.1 なぜ必要か

`summary` は `fetch` と並走できる設計であるため store-wide process lock を取得しない。
`fetch` は UID validity の変更を検出すると `recovery_required` センチネルを書き込む。
`summary` がこの書き込みを見逃したまま集計結果を送信すると、
不整合なサマリーが通知される。

これを防ぐのが summary consistency guard である。

### 3.2 ガードファイルのライフサイクル

ガードファイルが実際に存在することが guard 機能の前提条件である。
ガードファイルは **`Open(OpenReadWrite)` が作成する**。
これにより `summary` は `O_CREATE` を必要とせず、読み取り専用マウント上でも
shared lock を取得できる。

`AcquireSummaryConsistencyGuard` は呼び出し状態に応じて次のように動作する。

| 状態 | 動作 |
|---|---|
| `rootDir` 不在 | no-op ガードを返す（空ストア。writer が存在できないため `recovery_required` の書き込みも不可能） |
| `rootDir` あり・ガードファイルあり | `LOCK_SH` を取得（通常パス） |
| `rootDir` あり・ガードファイル不在 | `O_CREATE\|O_RDWR` でガードファイルを作成し `LOCK_SH` を取得（機能導入前の store や手動削除の救済） |
| `rootDir` あり・ガードファイル不在・作成失敗 | エラーを返す（fail-closed）。読み取り専用マウントは動作保証対象外 |

### 3.3 ロック種別と動作

ロックファイル: `{root_dir}/.tlsrpt-digest-summary.lock`

| 取得者 | flock 種別 | 取得失敗時の動作 |
|---|---|---|
| `summary`（`AcquireSummaryConsistencyGuard`） | shared（`LOCK_SH`） | ブロック（待機） |
| `recovery_required` を変更する store API（`withGuardExclusive`） | exclusive（`LOCK_EX`） | ブロック（待機） |

`summary` が shared lock を保持している間、`recovery_required` センチネルへの書き込みを
試みた fetch は exclusive lock 取得でブロック（待機）する。fetch はエラーにならず、
summary が shared lock を解放するまで待ち続ける。ブロックが発生するのは
`SaveRecoveryRequired` の呼び出し箇所のみであり、それ以前のメール取得や
レポート保存は summary と並走して進む。

### 3.4 `recovery_required` を変更する store API（排他 lock が必要）

- `SaveRecoveryRequired`
- `ClearRecoveryRequired`
- `ApplyRecovery`（※後述）
- `ResetForRecovery` のコミット処理（`commitReset`）

以下は `recovery_required` を変更しないため guard 不要：

- `ResetForRecovery` の初期マニフェスト/ステージング作成
- `stageDataFile` / `stageEmailsDir`
- `AbortReset` の restore 処理（センチネルを変更しないため guard 不要。ただし センチネルの
  `recovery_required` は最後まで保持されるので summary は引き続き fail-closed となる）
- コミット後のクリーンアップ

**`ApplyRecovery` の追加保護について**

`ApplyRecovery`（keep-old リカバリ）は `withGuardExclusive` だけでは不十分で、
**`HasPendingReset()` による事前チェックも必要**である。

理由：リセット操作中（フェーズ 1〜5）にはデータファイルがステージングに移動されている
可能性がある。`withGuardExclusive` はセンチネルの可視性しか保証しないため、
保留リセットを無視して `recovery_required` を消すと「new UIDValidity + cleared
recovery_required + データなし」という矛盾状態が生じる。

`ApplyRecovery` はマニフェストが存在する場合に `ErrPendingReset` を返すことで、
この経路を store 層で閉じている。**`recovery_required` を変更する新しい API を追加する
場合も同様に、保留リセット中の呼び出し可否を設計時に検討し、必要なら
`HasPendingReset()` 事前チェックを追加すること。**

### 3.5 summary の recovery_required チェック設計

**shared lock の保持範囲**

shared lock は Bootstrap 時（`AcquireSummaryConsistencyGuard`）に取得され、
`guard.Close()`（`boot.Close()`）まで保持される。
つまり summary コマンドの実行全体にわたって保持される。

この間、fetch の `SaveRecoveryRequired` は exclusive lock 取得でブロックされるため、
**summary 実行中にセンチネルが書き込まれることは物理的に不可能**である。

唯一の競合ウィンドウは「Bootstrap が shared lock を取得する前に fetch がセンチネルを
書き込む」タイミングだけである。

**チェックのタイミングと目的**

`summary` は `CheckRecoveryRequired` を集計開始前に 1 回だけ呼ぶ。
これは shared lock 取得前にセンチネルが書き込まれていた場合を検出するためである。

```
Bootstrap: shared lock 取得
           CheckRecoveryRequired   ← shared lock 取得前の書き込みを検出
               ↓ found=true: 通知して終了
           GenerateSummary（store 読み取り）
               ↓ ReportCount == 0: exitOK
           buildNotifier
           LogSummary / Flush（Slack 送信）
boot.Close(): shared lock 解放
           fetch: ここでセンチネル書き込みが可能になる
```

`recovery_required` が立っていれば、その後の `recover` でストアデータがすべて削除される
ことが確定している。消えるデータの集計を送信しても混乱を招くだけなので、通知して終了する。

**なぜ送信直前の再チェックをしないか**

summary が shared lock を保持している間は fetch のセンチネル書き込みがブロックされるため、
`CheckRecoveryRequired` 通過後にセンチネルが変化することはない。再チェックは不要である。

### 3.6 `SaveRecoveryRequired` を呼ぶのは fetch のみ

`SaveRecoveryRequired` を呼び出すのは現在 `fetch` だけである。
`gc`・`reprocess`・`recover` は `summary` と並走できるが、
`recovery_required` センチネルを書き込まないため summary consistency guard の対象外である。

**summary consistency guard が対象とする競合は `fetch` との並走のみ**である。

---

## 4. 過剰保護を避ける方針

summary consistency guard を store-wide process lock の代わりに使ってはならない。
guard は `recovery_required` の可視性のみを守るものであり、
マニフェストやステージングの状態機械全体を直列化するものではない。

避けるべきパターン：

- `recovery_required` を変更しない処理を summary guard で囲み、
  `summary` を不要にブロックする
- マニフェスト作成だけを summary guard で囲み、
  後続のステージング / コミット / クリーンアップを無保護にする
- 2 種類のロックの責務を同じコメントや API 名で混同する

望ましい責務分担：

| 問題 | 解決手段 |
|---|---|
| 書き込み系サブコマンド同士の直列化 | store-wide process lock（cmd 層） |
| `summary` vs `fetch` の `recovery_required` 競合 | summary consistency guard（`internal/store` 層） |
| crash recovery の原子性 | リセットマニフェスト・ステージング・センチネル commit barrier |

---

## 5. 実装・レビュー時のチェックリスト

**書き込み系サブコマンドを追加・変更するとき**

- [ ] store open 前に store-wide process lock を取得している
- [ ] 処理完了まで（異常終了パスを含む）lock handle を保持している
- [ ] `recover --mode discard-old --yes` / `recover --abort-reset --yes` は
  lock 保持中に `OpenRecoverReset` を使っている
- [ ] `ResetForRecovery` / `AbortReset` を直接呼ぶ場合は store-wide process lock を
  保持している
- [ ] `internal/store` 単体テストから直接呼ぶ場合は single writer 前提を明示している
- [ ] 新たに `SaveRecoveryRequired` を呼ぶ場合は §3 の契約に従い、
  summary consistency guard との整合を確認している

**`recovery_required` を変更する store API を追加・変更するとき**

- [ ] `{root_dir}/.tlsrpt-digest-summary.lock` に対して排他 lock を取得している
  （`withGuardExclusive` を使用）
- [ ] `recovery_required` を変更しない処理を summary guard で囲んでいない
- [ ] `summary` が stale な「復旧不要」判断で送信しないことをテストしている
- [ ] 保留リセット中（マニフェスト存在時）に呼ばれた場合の挙動を設計している：
  データファイルがステージングに移動済みの可能性があるため、`recovery_required` を
  クリアするだけでは不整合になるケースを考慮する。問題がある場合は `HasPendingReset()`
  による事前チェックで `ErrPendingReset` を返す（`ApplyRecovery` の実装を参照）

**summary サブコマンドまたは recovery_required チェック設計を変更するとき**

- [ ] `CheckRecoveryRequired` の呼び出しタイミングと目的が §3.5 と一致している
- [ ] チェック位置を追加・変更した場合は §3.5 を更新している
- [ ] 新たな store にアクセスする前に `Open(OpenReadWrite)` が一度でも実行されること
  （ガードファイル作成の前提）を確認している

---

## 6. 関連文書

- [ADR-0003: ResetForRecovery のフェーズ設計とコミット後クリーンアップの扱い](../adr/0003_reset_phase_design.ja.md)
- `docs/tasks/0070_entrypoint/02_architecture.md` §3.3 / §6.4
- `docs/tasks/0070_entrypoint/03_implementation_plan.md` ステップ 1-5 / 3-3
