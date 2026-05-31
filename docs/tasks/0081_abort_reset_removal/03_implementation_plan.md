# 実装計画書：AbortReset・フェーズ 5 の廃止およびフェーズ 2・3 フォールバックの削除

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-31 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

`AbortReset`（`recover --abort-reset --yes`）機能を廃止し、有効なリセットフェーズを `{1, 4}` のみに確定する。フェーズ 2・3 の後方互換フォールバックを削除し、旧バージョンが書いたフェーズ 2・3・5 のマニフェストを fail-closed で扱う。設計の詳細は [02_architecture.md](02_architecture.md) を参照。

### 1.2 実装原則

- **削除優先**：実装の主体は新機能追加ではなくコード削除である。削除後のコンパイルエラーと `make deadcode` を安全ネットとして活用する。
- **fail-closed の徹底**：`validateManifestPhase` を `{1, 4}` の 2 値判定に変更し、フェーズ 2・3・5 をすべて `ErrResetManifestPhaseUnknown` で拒否する。
- **フェーズ順序**：ストア層（`internal/store`）のコア削除 → CLI・通知層 → テスト整合 → ドキュメント改訂の順で進める。各フェーズ完了後に `make test && make lint` が通ることを確認する。

### 1.3 既存コード調査結果

以下にフェーズ別の調査結果を示す。変更不要なコンポーネントは省略した。

#### `internal/store/recovery.go`

- **削除対象**：
  - `resetPhaseAborting = 5` 定数
  - `AbortReset()` メソッド全体（約 90 行）
  - `restoreFromStaging()` 関数（約 20 行）
  - `ResetForRecovery` 内のフェーズ 5 拒否チェック（`if mfst.Phase == resetPhaseAborting { return ErrResetAbortInProgress }`）
- **変更対象**：
  - `validateManifestPhase`：現在は範囲判定 `p < resetPhaseManifestWritten || p > resetPhaseAborting`（= `[1, 5]` を受理）。`{1, 4}` の 2 値明示判定へ変更する。
  - `resetManifest` 型コメント：「Backward: → 5」「Legacy values 2 and 3」を削除する。
  - `validateManifestPhase` 関数コメント：「known range (1–5)」を「valid phases {1, 4}」相当へ更新する。
  - `ResetForRecovery` 関数コメント：「Legacy pre-commit values 2 and 3 are treated as phase 1」「Phase=5 (aborting) is refused」を削除する。
  - 前進コード中のコメント `// A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)` → 「phase 1」のみに整理する。
  - `HasPendingReset` コメント：「pre-commit (phase 1, or legacy 2–3) and aborting (phase 5)」を「pre-commit phase 1」に整理する。

#### `internal/store/store.go`

- `AbortReset() error` メソッドおよびそのドキュメントコメントを削除する。
- `HasPendingReset` コメント：「pre-commit phase 1 or legacy 2–3, or aborting phase 5」を「pre-commit phase 1」に更新する。

#### `internal/store/errors.go`

- **削除対象**：`ErrResetNotPending`、`ErrResetAbortInProgress`
- **変更対象**：
  - `ErrPendingReset` 値：`"store: pending reset detected; use OpenRecoverReset to continue or abort"` から `" or abort"` を除去し、結果を `"store: pending reset detected; use OpenRecoverReset to continue"`（末尾の余分な空白なし）にする。
  - `ErrPendingReset` ドキュメントコメント：「resume or abort the reset」→「resume the reset」に更新する。

#### `internal/store/types.go`

- `OpenRecoverReset` ドキュメントコメント：「ResetForRecovery and AbortReset even when...」「Only recover subcommand (discard-old --yes / --abort-reset --yes) may use this mode.」から `AbortReset` および `--abort-reset --yes` への言及を削除する。

#### `cmd/tlsrpt-digest/main.go`

- **削除対象**：
  - `errAbortResetRequiresYes` 変数
  - `errAbortAndModeExclusive` 変数
  - `cliOptions.RecoverAbort bool` フィールド
  - `registerFlags` の `--abort-reset` フラグ登録行
  - `validateFlags` の abort 関連チェック（2 箇所：`opts.RecoverAbort && opts.RecoverMode != ""` と `opts.RecoverAbort && !opts.RecoverYes`）
  - `validateFlags` の `opts.RecoverYes && !opts.RecoverAbort && opts.RecoverMode == ""` → `opts.RecoverYes && opts.RecoverMode == ""` に簡略化
  - `recoverStoreOpenMode` の abort 分岐（`opts.RecoverAbort && opts.RecoverYes`）
  - `runCLI` の `errors.Is(err, errAbortResetRequiresYes)` 分岐
- **変更対象**：
  - `errYesRequiresModeOrAbort` → `errYesRequiresMode` へリネームし、値を `"--yes requires --mode"` に更新する。
  - `runCLI` の `errors.Is(err, errYesRequiresModeOrAbort)` を `errYesRequiresMode` に更新する。
  - `recoverStoreOpenMode` 関数のドキュメントコメント（L177-178、`// (discard-old --yes, abort-reset --yes) and OpenReadWrite for all others.`）から `abort-reset --yes` を削除し、`// (discard-old --yes) and OpenReadWrite for all others.` に更新する。（`StoreOpenModeOverride` コメントは `boot.go` のみに存在し `main.go` には無い。`main.go` 側で更新すべきコメントはこの `recoverStoreOpenMode` のもの）

#### `cmd/tlsrpt-digest/recover.go`

- **削除対象**：
  - `runAbortReset` 関数全体
  - `printInfo` 内の `opts.RecoverAbort` 分岐（`selectedMode = "abort-reset"`）および「Roll back reset」行
  - `executeMode` の `case opts.RecoverAbort:` 分岐
  - `import "errors"` パッケージ（`runAbortReset` 削除後に未使用となるため）

#### `cmd/tlsrpt-digest/boot.go`

- 行 236（`store.ErrPendingReset` ラッパー）：「`or recover --abort-reset --yes to roll back`」を削除し、「`run recover --mode discard-old --yes to continue`」のみにする。
- L87 コメント（`BootstrapOptions.StoreOpenModeOverride`）：`OpenRecoverReset for discard-old/abort-reset.` の `/abort-reset` を削除し `OpenRecoverReset for discard-old.` に更新する。（このコメントは `boot.go` のみに存在する。`main.go` の `recoverStoreOpenMode` コメントは別物として §`main.go` の変更対象で扱う）

#### `internal/notify/format.go`

- `systemErrorHint` 関数：`"Run: tlsrpt-digest recover --mode discard-old --yes (or --abort-reset --yes)"` → `(or --abort-reset --yes)` を削除する。

#### `internal/store/testutil/mocks.go`

- **削除対象**：`AbortResetErr` フィールド、`AbortResetCallCount` フィールド、`AbortReset()` メソッド
- **変更対象**：`PendingReset bool` フィールドのコメント「for AbortReset testing」を削除する。

#### `internal/store/recovery_test.go`（削除対象テスト）

以下のテスト関数を全件削除する：

| 関数名 | 理由 |
|---|---|
| `TestAbortReset_UnknownPhaseFailsClosed` | `AbortReset` 廃止 |
| `TestAbortReset_CrashDuringCommitRefusesAbort` | `AbortReset` 廃止 |
| `TestAbortReset_PhaseManifestWritten` | `AbortReset` 廃止 |
| `TestAbortReset_NoPendingReset` | `AbortReset` 廃止 |
| `TestAbortReset_AfterCommit` | `AbortReset` 廃止 |
| `TestAbortReset_RestoresOldData` | `AbortReset` 廃止 |
| `TestAbortReset_Idempotent` | `AbortReset` 廃止 |
| `TestAbortReset_ResumesFromAbortingPhase` | `AbortReset` 廃止 |
| `TestAbortReset_CrashAfterRestoreBeforeManifestRemoval` | `AbortReset` 廃止 |
| `TestResetForRecovery_RefusesAbortingPhase` | フェーズ 5 拒否チェック削除 |
| `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` | フェーズ 2・3 フォールバック削除 |
| `TestOpen_CleansUpAfterCommitCrashWindow` | フェーズ 3（`resetPhase(3)`）を植え込む C4 コミットクラッシュウィンドウ検証。同等のフェーズ 1 版 `TestOpen_CleansUpAfterCommitCrashWindowManifestWritten` が既存のため重複として削除 |

> **注記**：[01_requirements.md](01_requirements.md) §6 が削除対象として挙げる `TestResetForRecovery_ResumesLegacyPhase3Manifest` および `TestResetForRecovery_LegacyPhase2Manifest*` は、現行コードベースに該当する関数が存在しない（命名が要件作成時の想定と異なる）。実際にレガシー値 2・3 を植え込んでいる関数は本計画が列挙する `TestApplyRecovery_RefusesPendingReset`・`TestResetForRecovery_IdempotentAfterCrashBeforeCommit`・`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`・`TestOpen_BlockedByPreCommitReset`・`TestResetForRecovery_CommitCrashWindow_ZeroUID`・`TestOpen_CleansUpAfterCommitCrashWindow` の各関数、および `TestAbortReset_*`（全件削除）内の植え込みであり、本計画ではこれらを正として扱う。

#### `internal/store/recovery_test.go`（更新対象テスト）

- `TestApplyRecovery_RefusesPendingReset`：植え込み値を `resetPhase(2)` → `resetPhaseManifestWritten` に変更する（フェーズ 2 は fail-closed になるため）。
- `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`：植え込み値を `resetPhase(2)` → `resetPhaseManifestWritten` に変更し、関数コメントと植え込み箇所コメントの「legacy phase-2」記述を「コミット前（フェーズ 1）の部分ステージング状態」に書き換える。本テストは「tlsrpt.json がステージング済み・emails/ が root に残る部分ステージング状態」からの冪等な収束を検証するもので、植え込みをフェーズ 1 に変えても `advanceResetPhases` の再実行により収束する（`assertResetConverged` のアサーションは維持する）。変更後に `make test` で収束を再確認する。
- `TestOpen_BlockedByPreCommitReset`：植え込み値を `resetPhase(3)` → `resetPhaseManifestWritten` に変更する。
- `TestResetForRecovery_CommitCrashWindow_ZeroUID`：植え込み値を `resetPhase(3)` → `resetPhaseManifestWritten` に変更する（C4 クラッシュウィンドウの検証はフェーズ 1 でも成立する）。
- `TestResetPhasePersistedNumericValues`：`assert.Equal(t, resetPhase(5), resetPhaseAborting)` を削除し、フェーズ 1・4 のアサーションのみ残す。
- `TestValidateManifestPhaseRange`：有効値を `{1, 4}` のみに変更し、拒否値を `{0, 2, 3, 5, 6, 99}` に更新する。

#### `internal/store/store_test.go`（更新対象テスト）

- `TestOpen_PendingReset_FailsClosedForReadWrite`：`resetPhase(3)` → `resetPhaseManifestWritten` に変更する。
- `TestOpen_PendingReset_OpenRecoverResetSucceeds`：`resetPhase(3)` → `resetPhaseManifestWritten` に変更する。

#### `cmd/tlsrpt-digest/recover_test.go`（削除対象テスト）

| 関数名 | 理由 |
|---|---|
| `TestRecover_AbortResetYesCallsAbortReset` | `AbortReset` 廃止 |
| `TestRecover_AbortResetNoPendingReset` | `AbortReset` 廃止 |
| `TestRecover_AbortResetFailure` | `AbortReset` 廃止 |
| `TestRecover_AbortResetAlone` | `--abort-reset` フラグ削除 |

#### `cmd/tlsrpt-digest/recover_test.go`（更新対象テスト）

- `TestRecover_YesAlone`：エラーメッセージのアサーションを `"--yes requires --mode"` に更新する（`"or --abort-reset"` を除去）。
- `TestRecover_NoRecoveryRequired`：`{RecoverAbort: true, RecoverYes: true}` ケースを削除し、`st.AbortResetCallCount = 0` および `assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。
- `TestRecover_CommittedCleanupPending_StatusDisplay`：`{RecoverAbort: true, RecoverYes: true}` ケースを削除し、`AbortResetCallCount` 参照を削除する。
- `TestRecover_StatusDisplayNoMode`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。
- `TestRecover_PendingResetDisplaysOptions`：`assert.Contains(t, output, "abort-reset --yes")` を削除する。
- `TestBootstrap_PendingResetShowsGuidance`（`recover_test.go` 内）：`assert.Contains(t, err.Error(), "recover --abort-reset --yes")` を削除する。
- `TestRecover_PendingResetShowsStatusForNonDestructiveModes`：`AbortResetCallCount` アサーションと `assert.Contains(t, output, "recover --abort-reset --yes")` を削除する。
- `TestRecover_HasPendingResetFailure`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。

#### `cmd/tlsrpt-digest/main_test.go`（更新対象テスト）

- `TestRunCLI_RecoverResetOpenMode`：「abort reset confirmed」ケース（`-abort-reset -yes`）を削除する。

#### `cmd/tlsrpt-digest/boot_test.go`（更新対象テスト）

- `TestBootstrap_PendingResetAdvice`（`boot_test.go:383`、`recover_test.go` の `TestBootstrap_PendingResetShowsGuidance` とは別関数）：`ErrPendingReset` ラッパーが `"recover --abort-reset --yes"` を含むことを検証しているアサーション（`assert.Contains(t, err.Error(), "recover --abort-reset --yes")`、L396）を削除する。

#### `internal/notify/format_test.go`（更新対象テスト）

- `TestFormatSystemError_ActionHint_UIDValidityChanged` および `TestFormatSystemError_ActionHint_RecoveryRequired`：通知本文が `abort-reset` を含まないことを確認するアサーション（`assert.NotContains(t, body, "abort-reset")`）を追加する。

---

## 2. 実装ステップ

### フェーズ 1：ストア層のコア削除（`internal/store`）

**目標**：`AbortReset` 廃止・フェーズ定数削除・`validateManifestPhase` 変更・エラー型削除をコンパイルエラーなしで完了する。

#### 1-1. `internal/store/recovery.go` の変更

- [ ] `resetPhaseAborting = 5` 定数を削除する。（AC-08）
- [ ] `resetManifest` 型コメントから「Backward: → 5」「Legacy values 2 and 3 (data_staged, emails_staged) were written by older versions; they are never written by the current code but are accepted as pre-commit.」を削除し、有効フェーズ `{1, 4}` のみの説明に整合させる。（AC-13）
- [ ] `validateManifestPhase` を `{1, 4}` の 2 値明示判定に変更する：`p != resetPhaseManifestWritten && p != resetPhaseCommitted` のとき `ErrResetManifestPhaseUnknown` を返す。（AC-09）
- [ ] `validateManifestPhase` のコメントを「valid phases {1, 4}」に更新する。（AC-09）
- [ ] `ResetForRecovery` のコメントから「Legacy pre-commit values 2 and 3 are treated as phase 1 (all staging ops re-run idempotently).」「Phase=5 (aborting) is refused with ErrResetAbortInProgress.」を削除する。（AC-12、AC-13）
- [ ] `ResetForRecovery` 内のフェーズ 5 拒否チェックとそのコメントブロックを削除する（`if mfst.Phase == resetPhaseAborting { return ErrResetAbortInProgress }` およびその直前のコメント）。（AC-12）
- [ ] `ResetForRecovery` の前進ロジック内のコメント「A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)」の「i.e. phrase 1 or legacy 2–3」部分を削除し「(pre-commit phase 1)」に整合させる。（AC-13）
- [ ] `HasPendingReset` のコメントから「pre-commit (phase 1, or legacy 2–3) and aborting (phase 5)」を削除し「pre-commit phase 1」に更新する。（AC-13）
- [ ] `AbortReset()` メソッド全体を削除する。（AC-02）
- [ ] `restoreFromStaging()` 関数全体を削除する。（AC-04）

**成功条件**：`go build ./internal/store/...` が通ること。

#### 1-2. `internal/store/store.go` の変更

- [ ] `Store` インターフェースから `AbortReset() error` メソッドとそのドキュメントコメントを削除する。（AC-01）
- [ ] `HasPendingReset` のドキュメントコメントを更新する：「pre-commit phase 1 or legacy 2–3, or aborting phase 5」→「pre-commit phase 1」。（AC-13）

#### 1-3. `internal/store/errors.go` の変更

- [ ] `ErrResetAbortInProgress` を削除する。（AC-05）
- [ ] `ErrResetNotPending` を削除する。（AC-06）
- [ ] `ErrPendingReset` のエラー文字列から `"to continue or abort"` を削除し、`"use OpenRecoverReset to continue"` に更新する。（AC-07a）
- [ ] `ErrPendingReset` のドキュメントコメントから `"or abort"` を削除する。（AC-07a）

#### 1-4. `internal/store/types.go` の変更

- [ ] `OpenRecoverReset` のドキュメントコメントから `AbortReset` および `--abort-reset --yes` への言及を削除し、`ResetForRecovery`（`discard-old --yes`）のみを許可するモードである旨へ整合させる。

**成功条件**：`go build ./internal/store/...` が通ること。フェーズ 1 完了時点では `testutil/mocks.go` の `FakeStore.AbortReset` がインターフェースを満たさなくなるため、ビルドエラーが出る（フェーズ 3 で解消）。

---

### フェーズ 2：CLI・通知層の削除と文言更新

**目標**：`cmd/tlsrpt-digest` および `internal/notify` から abort への言及をすべて除去し、operator 向け案内を新フェーズ定義に整合させる。

#### 2-1. `cmd/tlsrpt-digest/main.go` の変更

- [ ] `errAbortResetRequiresYes` 変数を削除する。（AC-07d）
- [ ] `errAbortAndModeExclusive` 変数を削除する。（AC-07d）
- [ ] `errYesRequiresModeOrAbort` を `errYesRequiresMode` にリネームし、値を `"--yes requires --mode"` に更新する。（AC-07d）
- [ ] `cliOptions` 構造体から `RecoverAbort bool` フィールドを削除する。（AC-03）
- [ ] `registerFlags` の `fs.BoolVar(&opts.RecoverAbort, "abort-reset", ...)` 行を削除する。（AC-03）
- [ ] `validateFlags` から abort 関連チェックを削除する：`opts.RecoverAbort && opts.RecoverMode != ""` チェック、`opts.RecoverAbort && !opts.RecoverYes` チェックを削除する。（AC-03）
- [ ] `validateFlags` の `--yes` 単独チェック条件を `opts.RecoverYes && !opts.RecoverAbort && opts.RecoverMode == ""` から `opts.RecoverYes && opts.RecoverMode == ""` に簡略化する。（AC-07d）
- [ ] `recoverStoreOpenMode` の `opts.RecoverAbort && opts.RecoverYes` 分岐を削除する。（AC-03）
- [ ] `runCLI` の `errors.Is(err, errAbortResetRequiresYes)` チェックを削除し、`errYesRequiresMode` の `exitError` 返却を `errors.Is(err, errYesRequiresMode)` に更新する。（AC-07d）
- [ ] `recoverStoreOpenMode` 関数のドキュメントコメント（L177-178）から `abort-reset --yes` を削除し、`// (discard-old --yes) and OpenReadWrite for all others.` に更新する。

#### 2-2. `cmd/tlsrpt-digest/recover.go` の変更

- [ ] `printInfo` の `if opts.RecoverAbort { selectedMode = "abort-reset" }` ブロックを削除する。（AC-07c）
- [ ] `printInfo` の「Roll back reset:」行（`fmt.Fprintln(r.stdout, "  Roll back reset: ...")` 相当）を削除する。（AC-07c）
- [ ] `executeMode` の `case opts.RecoverAbort:` 分岐を削除する。（AC-03）
- [ ] `runAbortReset` 関数全体を削除する。（AC-02）
- [ ] `import "errors"` パッケージが未使用となるため削除する。（AC-02 の副作用：`runAbortReset` 削除により唯一の使用箇所が消えるため）

#### 2-3. `cmd/tlsrpt-digest/boot.go` の変更

- [ ] `Bootstrap` 内の `ErrPendingReset` ラッパーメッセージ（L236）から「`or recover --abort-reset --yes to roll back`」を削除し、`"run recover --mode discard-old --yes to continue"` のみにする。（AC-07b）
- [ ] `BootstrapOptions.StoreOpenModeOverride` のドキュメントコメント（L87）の `OpenRecoverReset for discard-old/abort-reset.` から `/abort-reset` を削除し `OpenRecoverReset for discard-old.` に更新する。

#### 2-4. `internal/notify/format.go` の変更

- [ ] `systemErrorHint` 関数の `SystemErrorKindUIDValidityChanged / SystemErrorKindRecoveryRequired` ケースの返却値から `(or --abort-reset --yes)` を削除する。（AC-07e）

**成功条件**：`go build ./cmd/tlsrpt-digest/...` と `go build ./internal/notify/...` が通ること。

---

### フェーズ 3：テスト整合

**目標**：削除済みコードへの参照をテストから除去し、新規 fail-closed テストを追加する。`make test` と `make lint` と `make deadcode` がすべて通ること。

#### 3-1. `internal/store/testutil/mocks.go` の変更

- [ ] `FakeStore.AbortResetErr` フィールドを削除する。（AC-01）
- [ ] `FakeStore.AbortResetCallCount` フィールドを削除する。（AC-01）
- [ ] `FakeStore.AbortReset()` メソッド全体を削除する。（AC-01）
- [ ] `FakeStore.PendingReset` フィールドのコメントから「for AbortReset testing」を削除する。

**成功条件**：`go build ./internal/store/testutil/...` が通ること（インターフェース整合確認）。

#### 3-2. `internal/store/recovery_test.go` の変更（削除）

- [ ] `TestAbortReset_UnknownPhaseFailsClosed` を削除する。
- [ ] `TestAbortReset_CrashDuringCommitRefusesAbort` を削除する。
- [ ] `TestAbortReset_PhaseManifestWritten` を削除する。
- [ ] `TestAbortReset_NoPendingReset` を削除する。
- [ ] `TestAbortReset_AfterCommit` を削除する。
- [ ] `TestAbortReset_RestoresOldData` を削除する。
- [ ] `TestAbortReset_Idempotent` を削除する。
- [ ] `TestAbortReset_ResumesFromAbortingPhase` を削除する。
- [ ] `TestAbortReset_CrashAfterRestoreBeforeManifestRemoval` を削除する。
- [ ] `TestResetForRecovery_RefusesAbortingPhase` を削除する。（AC-12）
- [ ] `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` を削除する。（AC-10 の fail-closed テストへ置換）
- [ ] `TestOpen_CleansUpAfterCommitCrashWindow` を削除する（`resetPhase(3)` 植え込みが fail-closed になり破綻するため。C4 コミットクラッシュウィンドウのクリーンアップ検証は既存のフェーズ 1 版 `TestOpen_CleansUpAfterCommitCrashWindowManifestWritten` が担うため重複となる）。

#### 3-3. `internal/store/recovery_test.go` の変更（更新）

- [ ] `TestApplyRecovery_RefusesPendingReset`：植え込み値 `resetPhase(2)` を `resetPhaseManifestWritten` に変更する。
- [ ] `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`：植え込み値 `resetPhase(2)` を `resetPhaseManifestWritten` に変更し、関数コメント・植え込み箇所コメントの「legacy phase-2」記述を「コミット前（フェーズ 1）の部分ステージング状態」へ書き換える。`assertResetConverged` のアサーションは維持し、`make test` で収束を再確認する。
- [ ] `TestResetForRecovery_IdempotentAfterCrashBeforeCommit`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更し、「legacy phase-3 manifest」コメントを除去する。
- [ ] `TestOpen_BlockedByPreCommitReset`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。
- [ ] `TestResetForRecovery_CommitCrashWindow_ZeroUID`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。
- [ ] `TestResetPhasePersistedNumericValues`：`resetPhaseAborting` のアサーション行を削除し、フェーズ 1・4 のみ検証する。（AC-08）
- [ ] `TestValidateManifestPhaseRange`：有効値を `{1, 4}` のみにし、拒否値を `{0, 2, 3, 5, 6, 99}` に更新する。（AC-09）

#### 3-4. `internal/store/recovery_test.go` の変更（新規追加）

- [ ] **`TestLegacyPhaseFailsClosed_ResetForRecovery`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `ResetForRecovery` を呼び出すと `ErrResetManifestPhaseUnknown` が返り、かつマニフェストファイルとステージングディレクトリが削除されずに残ることを検証する。（AC-10、AC-11）
- [ ] **`TestLegacyPhaseFailsClosed_OpenReadWrite`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `Open(OpenReadWrite)` を呼び出すと `ErrResetManifestPhaseUnknown` が返り、かつマニフェストファイルとステージングディレクトリが削除されずに残ることを検証する。（AC-10、AC-11）

これら 2 つのテストは `recovery_test.go`（`package store` の内部テスト）に追加し、`resetPhase` 型に直接アクセスする。新規ヘルパーファイルは不要（既存の `writeResetManifest`・`resetManifestPath`・`resetStagingPath` を再利用できる）。

#### 3-5. `internal/store/store_test.go` の変更

- [ ] `TestOpen_PendingReset_FailsClosedForReadWrite`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。
- [ ] `TestOpen_PendingReset_OpenRecoverResetSucceeds`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。

#### 3-6. `cmd/tlsrpt-digest/recover_test.go` の変更（削除）

- [ ] `TestRecover_AbortResetYesCallsAbortReset` を削除する。
- [ ] `TestRecover_AbortResetNoPendingReset` を削除する。
- [ ] `TestRecover_AbortResetFailure` を削除する。
- [ ] `TestRecover_AbortResetAlone` を削除する。

#### 3-7. `cmd/tlsrpt-digest/recover_test.go` の変更（更新）

- [ ] `TestRecover_YesAlone`：`assert.Contains(t, stderr.String(), "--yes requires --mode or --abort-reset")` を `assert.Contains(t, stderr.String(), "--yes requires --mode")` かつ `assert.NotContains(t, stderr.String(), "--abort-reset")` に更新する。（AC-07d）
- [ ] `TestRecover_NoRecoveryRequired`：`{RecoverAbort: true, RecoverYes: true}` ケースを削除し、`st.AbortResetCallCount = 0` および `assert.Equal(t, 0, st.AbortResetCallCount)` の参照をすべて削除する。
- [ ] `TestRecover_CommittedCleanupPending_StatusDisplay`：`{RecoverAbort: true, RecoverYes: true}` ケースを削除し、`AbortResetCallCount` 参照を削除する。
- [ ] `TestRecover_StatusDisplayNoMode`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。
- [ ] `TestRecover_PendingResetDisplaysOptions`：`assert.Contains(t, output, "abort-reset --yes")` を削除する。（AC-07c）
- [ ] `TestBootstrap_PendingResetShowsGuidance`（`recover_test.go:401`。`boot_test.go` の `TestBootstrap_PendingResetAdvice` とは別関数）：`assert.Contains(t, err.Error(), "recover --abort-reset --yes")`（L415）を削除する。あわせて関数ドキュメントコメントの「guidance for both continue and abort paths」を「guidance for the continue path」相当へ更新する。（AC-07b）
- [ ] `TestRecover_PendingResetShowsStatusForNonDestructiveModes`：`AbortResetCallCount` アサーションと `assert.Contains(t, output, "recover --abort-reset --yes")` を削除する。（AC-07c）
- [ ] `TestRecover_HasPendingResetFailure`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。

#### 3-8. `cmd/tlsrpt-digest/main_test.go` の変更

- [ ] `TestRunCLI_RecoverResetOpenMode`：「abort reset confirmed」ケース（`args: []string{"recover", "-abort-reset", "-yes"}`）を削除する。（AC-03）
- [ ] `TestRunCLI_AbortResetFlagUndefined` を新規追加する：`recover --abort-reset --yes` の実行が `flag.Parse` により `flag provided but not defined: -abort-reset` 相当のエラーを返し、終了コード 2 となることを検証する。（AC-03）

#### 3-9. `cmd/tlsrpt-digest/boot_test.go` の変更

- [ ] `TestBootstrap_PendingResetAdvice`（`boot_test.go:383`。`recover_test.go` の `TestBootstrap_PendingResetShowsGuidance` と混同しないこと）：`ErrPendingReset` ラッパーが `"recover --abort-reset --yes"` を含まないことを検証する。既存の `assert.Contains(t, err.Error(), "recover --abort-reset --yes")`（L396）を `assert.NotContains(t, err.Error(), "abort-reset")` に更新する。（AC-07b）

#### 3-10. `internal/notify/format_test.go` の変更

- [ ] `TestFormatSystemError_ActionHint_UIDValidityChanged`：`assert.NotContains(t, body, "abort-reset")` を追加する。（AC-07e）
- [ ] `TestFormatSystemError_ActionHint_RecoveryRequired`：`assert.NotContains(t, body, "abort-reset")` を追加する。（AC-07e）

#### 3-11. 静的検査

- [ ] `make fmt` を実行してフォーマットを揃える。
- [ ] `make test` を実行して全テストが通ることを確認する。
- [ ] `make lint` を実行してリントエラーがないことを確認する。
- [ ] `make deadcode` を実行して新たな未使用関数が検出されないことを確認する。（AC-04）

---

### フェーズ 4：ドキュメント改訂

**目標**：ADR-0003・開発者ガイド・用語集・運用手順を新フェーズ定義 `{1, 4}` に整合させる。

#### 4-1. `docs/dev/adr/0003_reset_phase_design.ja.md` の変更

- [ ] フェーズ一覧表からフェーズ 2・3・5 の行を削除する。（AC-14）
- [ ] 「フェーズ 5（recovery_required リセットマーカー）を設ける理由」節を削除するか「廃止の経緯」として更新する。（AC-15）
- [ ] 状態遷移図から P5 ノードおよびその遷移（P1→P5、P5→RR）を削除する。（AC-16）
- [ ] 不変条件表の「フェーズ 5 が書かれている ⟹ `AbortReset` のみが続行できる」行を削除する。（AC-17）
- [ ] ユーザー操作時の挙動表から `recover --abort-reset --yes` 列を削除または「廃止済み」として更新する。（AC-18）
- [ ] §1 要件表・§2 `AbortReset` 言及・§7「将来の変更・拡張方針」の `AbortReset` サブ節・§8・§9 関連ファイル表の `AbortReset`・`ErrResetNotPending`・`ErrResetAbortInProgress` 言及を削除または更新する。

#### 4-2. `docs/dev/developer_guide/process_locking.ja.md` の変更

- [ ] 対象サブコマンド一覧（L65）から `--abort-reset` を削除する。（AC-20）
- [ ] 契約節（L71・L73）・チェックリスト節（L298・L300）から `recover --abort-reset --yes` および `ResetForRecovery / AbortReset` のペア記述を除去し、`recover --mode discard-old --yes` / `ResetForRecovery` のみが `OpenRecoverReset` を使うよう整合させる。（AC-20）
- [ ] 状態機械の説明（L43）の `（`ResetForRecovery` / `AbortReset`）` から `AbortReset` を削除する。（AC-20）
- [ ] `AbortReset の restore 処理` を説明する箇条書き（L182）を削除する（`restoreFromStaging` 廃止に伴い該当処理が存在しなくなるため）。（AC-20）
- [ ] リセットマニフェスト定義（L48）の `resetPhase`（1〜5） を `resetPhase`（1・4） に更新する。（AC-20）

#### 4-3. `docs/translation_glossary.md` の変更

- [ ] 「保留リセット / pending reset」の定義を更新する：現状「フェーズ 1〜3 および フェーズ 5」→「フェーズ 1 のみ」に更新し、`AbortReset` 廃止に整合させる。

#### 4-4. `docs/operations/legacy_reset_manifest_upgrade.ja.md` の新規作成

- [ ] フェーズ 2・3 のマニフェストが残存するストアのアップグレード前に旧バージョンで `recover --mode discard-old --yes` を完了する手順を記載する。（AC-21）
- [ ] フェーズ 5 のマニフェストが残存するストアのアップグレード前に旧バージョンで `AbortReset`（`recover --abort-reset --yes`）を完了する手順を記載する。（AC-22）

#### 4-5. 英語版の反映

- [ ] `docs/dev/adr/0003_reset_phase_design.ja.md` → `.md` に `/mktrans` で反映する。（AC-19）
- [ ] `docs/dev/developer_guide/process_locking.ja.md` → `.md` に `/mktrans` で反映する。（AC-20）
- [ ] `docs/operations/legacy_reset_manifest_upgrade.ja.md` → `.md` に `/mktrans` で反映する。（AC-21、AC-22）

---

## 3. テストヘルパー計画

新規のテストヘルパーファイルは追加しない。理由：

- **`internal/store/recovery_test.go`** に追加する fail-closed テスト（3-4 節）は、`writeResetManifest`・`resetManifestPath`・`resetStagingPath` などの既存内部ヘルパーを直接使用できるため、`package store` 内部テストに留まる。新規 `test_helpers.go` は不要。
- **`internal/store/testutil/mocks.go`** はフィールド・メソッド削除のみであり、新規追加なし。

---

## 4. 受け入れ条件対応表

| AC | 対応する実装タスク | 対応するテスト |
|---|---|---|
| AC-01 | 1-2（`Store` インターフェース `AbortReset` 削除） | 3-1（`FakeStore` 整合）、コンパイル通過 |
| AC-02 | 1-1（`AbortReset` 実装削除） | コンパイル通過 |
| AC-03 | 2-1（`--abort-reset` フラグ削除） | 3-6（CLI テスト削除）、3-8（`TestRunCLI_AbortResetFlagUndefined` 追加） |
| AC-04 | 1-1（`restoreFromStaging` 削除） | 3-11（`make deadcode`） |
| AC-05 | 1-3（`ErrResetAbortInProgress` 削除） | コンパイル通過 |
| AC-06 | 1-3（`ErrResetNotPending` 削除） | コンパイル通過 |
| AC-07a | 1-3（`ErrPendingReset` 文言更新） | なし（grep による横断確認） |
| AC-07b | 2-3（`boot.go` 文言更新） | 3-9（`boot_test.go` アサーション更新） |
| AC-07c | 2-2（`recover.go` 文言削除） | 3-7（`TestRecover_PendingResetDisplaysOptions` 更新） |
| AC-07d | 2-1（`errYesRequiresMode` 更新） | 3-7（`TestRecover_YesAlone` 更新） |
| AC-07e | 2-4（`systemErrorHint` 更新） | 3-10（`format_test.go` アサーション追加） |
| AC-08 | 1-1（`resetPhaseAborting` 削除） | 3-3（`TestResetPhasePersistedNumericValues` 更新） |
| AC-09 | 1-1（`validateManifestPhase` 変更） | 3-3（`TestValidateManifestPhaseRange` 更新） |
| AC-10 | 1-1（`validateManifestPhase` 変更） | 3-4（`TestLegacyPhaseFailsClosed_ResetForRecovery`・`_OpenReadWrite` 追加） |
| AC-11 | 1-1（`validateManifestPhase` 変更） | 3-4（`TestLegacyPhaseFailsClosed_ResetForRecovery`・`_OpenReadWrite` 追加） |
| AC-12 | 1-1（フェーズ 5 拒否チェック削除） | 3-2（`TestResetForRecovery_RefusesAbortingPhase` 削除）、3-4（代替テスト） |
| AC-13 | 1-1〜1-4（コメント整理） | なし（grep による横断確認） |
| AC-14 | 4-1（ADR フェーズ表更新） | ドキュメントレビュー |
| AC-15 | 4-1（ADR 設計根拠節更新） | ドキュメントレビュー |
| AC-16 | 4-1（ADR 状態遷移図更新） | ドキュメントレビュー |
| AC-17 | 4-1（ADR 不変条件表更新） | ドキュメントレビュー |
| AC-18 | 4-1（ADR ユーザー操作表更新） | ドキュメントレビュー |
| AC-19 | 4-5（英語版 `/mktrans`） | ドキュメントレビュー |
| AC-20 | 4-2（`process_locking.ja.md` 更新） | ドキュメントレビュー |
| AC-21 | 4-4（運用手順書新規作成） | ドキュメントレビュー |
| AC-22 | 4-4（運用手順書新規作成） | ドキュメントレビュー |

---

## 5. abort 文言の横断確認チェックリスト

フェーズ 2〜3 完了後に以下のパターンを `grep -rn` で検索し、operator 向け案内から意図しない abort への言及が残っていないことを確認する。

- `--abort-reset`
- `Roll back reset`
- `to continue or abort`
- `resume or abort`
- `abort-reset --yes`

検索対象ファイル：`internal/store/errors.go`、`cmd/tlsrpt-digest/boot.go`、`cmd/tlsrpt-digest/recover.go`、`cmd/tlsrpt-digest/main.go`、`internal/notify/format.go`。

ドキュメントファイル（`docs/dev/developer_guide/process_locking.ja.md`、`docs/dev/adr/0003_reset_phase_design.ja.md`）はフェーズ 4 で対応するため、ここでは除外する。

加えて、フェーズ 2 完了後に `cmd/tlsrpt-digest/main.go` の `recoverStoreOpenMode` コメント（L178）にも `abort-reset --yes` が残らないことを確認する（このコメントの除去はフェーズ 2-1 で実施済みのはずであり、本チェックリストは取りこぼし検出を目的とする）。

---

## 6. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| レガシー値（2・3）を植え込む既存テストの見落とし | `validateManifestPhase` を `{1, 4}` に絞った後、植え込み値が fail-closed となり `make test` が破綻する | §1.3・フェーズ 3 で全 `resetPhase(2)`/`resetPhase(3)` 植え込み箇所を関数単位で洗い出し済み（`TestAbortReset_*` は全削除、その他は植え込み値変更または削除）。実装時に `grep -rn "resetPhase(2)\|resetPhase(3)" internal/store/*_test.go` を再実行し、計画外の植え込みが残っていないことを確認する。 |
| 削除に伴うコンパイルエラーの取りこぼし | ビルド不能 | フェーズ順序（ストア層 → CLI・通知層 → テスト整合）を守り、各フェーズ末で `go build` / `make test` を実行する。`make deadcode` で未使用関数の取りこぼしも検出する。 |
| operator 向け文言からの abort 参照の取りこぼし | 廃止済みフラグを案内してしまう | §5 の横断 grep チェックリストで `--abort-reset` 等を検索し、コメントを含めて残存がないことを確認する。 |
| `/mktrans` 反映漏れによる日英不整合 | ドキュメントの不整合 | フェーズ 4-5 で対象 3 ファイルの `/mktrans` 反映を明示タスク化済み。日本語版を先に確定してから反映する。 |

## 7. 成功条件

- `make test`・`make lint`・`make deadcode`・`make fmt` がすべてエラーなく完了する。
- §4 受け入れ条件対応表の全 AC（AC-01〜AC-22、欠番除く）が、対応する実装タスクとテスト／検証方法に紐づき、検証済みである。
- §5 の横断 grep チェックリストで、実装対象ファイルの operator 向け案内・コメントに意図しない abort 参照が残っていない。
- ADR-0003・`process_locking`・運用手順書の日本語版と英語版（`/mktrans` 反映）がフェーズ定義 `{1, 4}` に整合している。

## 8. 次のステップ

- 本計画のレビュー・承認（ステータスを `approved` に更新）後、`/runplan` でフェーズ 1〜4 を順に実装する。
- 実装中は各チェックボックスをリアルタイムで更新する（完了 `[x]`、部分完了 `[-]`）。
- 実装完了後、PR を作成しレビューに回す。
