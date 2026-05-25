# プロセス間ロック設計ガイドライン

## 概要

本プロジェクトは複数の CLI サブコマンドが同じ store を読み書きする。特に `recover --mode discard-old --yes` と `recover --abort-reset --yes` は複数ファイルを移動・削除する破壊的操作であり、複数 writer が同時に実行されると reset manifest、staging、sentinel の状態が競合する。

このガイドラインは、プロセス間ロックの責務を明確に分離し、今後の実装・拡張で同じ前提を維持するための設計方針を定める。

---

## 1. ロックの種類と責務

| ロック | 対象 | 目的 | 保持範囲 |
|---|---|---|---|
| store-wide process lock | writer vs writer | `fetch` / `gc` / `reprocess` / `recover` 同士の同時書き込みを防ぐ | 書き込み系サブコマンドの store open 前から処理完了まで |
| summary consistency guard | summary vs recovery_required writer | `summary` が stale な「復旧不要」判断で通知することを防ぐ | `summary` の集計・送信可否確認中、および writer の recovery_required 更新中 |

この 2 種類は代替関係ではない。store-wide process lock は writer 同士を直列化する。summary consistency guard は、store-wide process lock を取得しない `summary` と recovery_required を変更する writer の間だけを同期する。

---

## 2. store-wide process lock の契約

書き込み系サブコマンドは、store を開く前に `{root_dir}/.tlsrpt-digest-store.lock` に対して non-blocking の排他 `flock` を取得する。取得できない場合は、他プロセスが同じ store を書き込み中とみなし、待機せず失敗する。

対象サブコマンド:

- `fetch`
- `gc`
- `reprocess`
- `recover --mode keep-old`
- `recover --mode discard-old`（`--yes` なしの dry-run を含む）
- `recover --mode discard-old --yes`
- `recover --abort-reset --yes`

成立条件:

1. すべての writer は同じ lock path を使う。
2. lock は `store.Open(...)` より前に取得する。
3. lock handle はサブコマンドの処理完了まで保持する。
4. `store.Open(...)` 失敗時は lock handle を即座に close する（成功時は処理完了まで保持）。
5. `recover --mode discard-old --yes` と `recover --abort-reset --yes` は、lock を保持した状態で `OpenRecoverReset` を使う。
6. CLI や内部ツールが `ResetForRecovery` / `AbortReset` を直接呼ぶ場合も、同じ writer lock を保持する。
7. `internal/store` 単体テストが `ResetForRecovery` / `AbortReset` を直接呼ぶ場合は、OS レベルの writer lock は不要だが、単一ゴルーチンから呼ぶなど single writer 前提を明示する。

`ResetForRecovery` / `AbortReset` は manifest read から cleanup までを 1 writer 前提で実行する。したがって、これらの API を安全に使うためには呼び出し側が store-wide process lock を保持している必要がある。

---

## 3. summary consistency guard の契約

`summary` は store-wide process lock を取得しない。`fetch` と並走できる設計にするためである。その代わり、`summary` は `AcquireSummaryConsistencyGuard()` で共有 lock を取得し、送信直前まで `recovery_required` の出現を検出できる境界を持つ。guard file のパスは `{root_dir}/.tlsrpt-digest-summary.lock` である。

writer 側で summary consistency guard の排他 lock が必要な操作は、`recovery_required` を作成または解除する操作に限る。

対象:

- `SaveRecoveryRequired`
- `ClearRecoveryRequired`
- `ApplyRecovery`
- `ResetForRecovery` 内の commit 処理（`commitReset`）

対象外:

- `ResetForRecovery` の初期 manifest/staging 作成
- `stageDataFile`
- `stageEmailsDir`
- `AbortReset` の restore 処理
- commit 後 cleanup

対象外の操作は `recovery_required` を変更しないため、summary の「復旧不要」判断と同期する必要がない。writer 同士の競合は store-wide process lock が担当する。

---

## 4. 過剰保護を避ける方針

summary consistency guard を store-wide writer lock の代わりに使ってはならない。guard は `recovery_required` の可視性を守るためのロックであり、reset manifest や staging の状態機械全体を直列化するものではない。

避けるべき例:

- manifest 作成だけを summary guard で囲み、後続の staging / commit / cleanup は無保護にする
- `recovery_required` を変更しない処理を summary guard で長時間囲み、`summary` を不要にブロックする
- store-wide process lock と summary guard の責務を同じコメントや API 名で混同する

望ましい分離:

- writer vs writer: cmd 層の store-wide process lock
- summary vs recovery_required writer: `internal/store` の summary consistency guard
- crash recovery: reset manifest、staging、sentinel commit barrier

---

## 5. 実装・レビュー時のチェックリスト

書き込み系サブコマンドを追加または変更する場合:

- [ ] store open 前に store-wide process lock を取得している
- [ ] 処理完了まで lock handle を保持している
- [ ] 異常終了パスでも lock handle を close している
- [ ] `recover --mode discard-old --yes` / `recover --abort-reset --yes` は lock 保持中に `OpenRecoverReset` を使っている
- [ ] CLI や内部ツールで `ResetForRecovery` / `AbortReset` を直接呼ぶ場合は、同じ writer lock を保持している
- [ ] `internal/store` 単体テストで `ResetForRecovery` / `AbortReset` を直接呼ぶ場合は、single writer 前提（単一ゴルーチンなど）を明示している

`recovery_required` を変更する store API を追加または変更する場合:

- [ ] `{root_dir}/.tlsrpt-digest-summary.lock` に対して排他 lock を取得している（`withGuardExclusive` を使用）
- [ ] `recovery_required` を変更しない処理を summary guard で囲んでいない
- [ ] summary が stale な「復旧不要」判断で通知しないことをテストしている

---

## 6. 関連文書

- `docs/dev/adr/0003_reset_phase_design.ja.md`
- `docs/tasks/0070_entrypoint/02_architecture.md` §3.3 / §6.4
- `docs/tasks/0070_entrypoint/03_implementation_plan.md` ステップ 1-5 / 3-3
