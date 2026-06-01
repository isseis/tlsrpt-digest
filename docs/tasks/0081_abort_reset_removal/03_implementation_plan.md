# 実装計画書：AbortReset・フェーズ 5 の廃止およびフェーズ 2・3 フォールバックの削除

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-31 |
| レビュー日 | 2026-06-01 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装概要

### 1.1 目的

`AbortReset`（`recover --abort-reset --yes`）機能を廃止し、有効なリセットフェーズを `{1, 4}` のみに確定する。フェーズ 2・3 の後方互換フォールバックを削除し、旧バージョンが書いたフェーズ 2・3・5 のマニフェストを fail-closed で扱う。設計の詳細は [02_architecture.md](02_architecture.md) を参照。

### 1.2 実装原則

- **削除優先**：実装の主体は新機能追加ではなくコード削除である。削除後のコンパイルエラーと `make deadcode` を安全ネットとして活用する。
- **fail-closed の徹底**：`validateManifestPhase` を `{1, 4}` の 2 値判定に変更し、フェーズ 2・3・5 をすべて `ErrResetManifestPhaseUnknown` で拒否する。
- **フェーズ順序**：ストア層（`internal/store`）のコア削除 → CLI・通知層 → テスト整合 → ドキュメント改訂の順で進める。フェーズ 1〜2 は対象パッケージの `go build` を主な確認とし、テスト・モック整合後のフェーズ 3 で `make test`・`make lint`・`make deadcode` を実行する。

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
  - `validateManifestPhase` 関数コメント：「known range (1–5)」を「valid phases {1, 4}」へ更新する。
  - `ResetForRecovery` 関数コメント：「Legacy pre-commit values 2 and 3 are treated as phase 1」「Phase=5 (aborting) is refused」を削除する。
  - 前進コード中のコメント `// A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)` を英語のままフェーズ 1 のみの説明へ整理する。
  - `HasPendingReset` コメント：「pre-commit (phase 1, or legacy 2–3) and aborting (phase 5)」を英語のまま「pre-commit phase 1」に整理する。
  - 残存コメント「reset or abort is still in progress」「ResetForRecovery or AbortReset first」から abort への言及を削除する。

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
- `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`：植え込み値を `resetPhase(2)` → `resetPhaseManifestWritten` に変更し、関数コメントと植え込み箇所コメントの「legacy phase-2」記述を英語で「partial pre-commit phase-1 staging state」へ書き換える。本テストは「`tlsrpt.json` がステージング済み・`emails/` が root に残る部分ステージング状態」からの冪等な収束を検証するもので、植え込みをフェーズ 1 に変えても `advanceResetPhases` の再実行により収束する（`assertResetConverged` のアサーションは維持する）。変更後に `make test` で収束を再確認する。
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

- `TestRecover_YesAlone`：テストコメントから `--abort-reset` への言及を削除し、エラーメッセージのアサーションを `"--yes requires --mode"` に更新する（`"or --abort-reset"` を除去）。
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

- [x] `resetPhaseAborting = 5` 定数を削除する。（AC-08）
- [x] `resetManifest` 型コメントから「Backward: → 5」「Legacy values 2 and 3 (data_staged, emails_staged) were written by older versions; they are never written by the current code but are accepted as pre-commit.」を削除し、有効フェーズ `{1, 4}` のみの説明に整合させる。（AC-13）
- [x] `validateManifestPhase` を `{1, 4}` の 2 値明示判定に変更する：`p != resetPhaseManifestWritten && p != resetPhaseCommitted` のとき `ErrResetManifestPhaseUnknown` を返す。（AC-09）
- [x] `validateManifestPhase` のコメントを「valid phases {1, 4}」に更新する。（AC-09）
- [x] `ResetForRecovery` のコメントから「Legacy pre-commit values 2 and 3 are treated as phase 1 (all staging ops re-run idempotently).」「Phase=5 (aborting) is refused with ErrResetAbortInProgress.」を削除する。（AC-12、AC-13）
- [x] `ResetForRecovery` 内のフェーズ 5 拒否チェックとそのコメントブロックを削除する（`if mfst.Phase == resetPhaseAborting { return ErrResetAbortInProgress }` およびその直前のコメント）。（AC-12）
- [x] `ResetForRecovery` の前進ロジック内のコメント「A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)」の「i.e. phase 1 or legacy 2–3」部分を削除し、英語のまま「pre-commit phase 1」の説明に整合させる。（AC-13）
- [x] `HasPendingReset` のコメントから「pre-commit (phase 1, or legacy 2–3) and aborting (phase 5)」を削除し、英語のまま「pre-commit phase 1」に更新する。（AC-13）
- [x] 残存コメント「reset or abort is still in progress」「ResetForRecovery or AbortReset first」を、abort 廃止後の前進のみの説明へ英語で更新する。（AC-07、AC-13）
- [x] `AbortReset()` メソッド全体を削除する。（AC-02）
- [x] `restoreFromStaging()` 関数全体を削除する。（AC-04）

**成功条件**：`go build ./internal/store/...` が通ること。

#### 1-2. `internal/store/store.go` の変更

- [x] `Store` インターフェースから `AbortReset() error` メソッドとそのドキュメントコメントを削除する。（AC-01）
- [x] `HasPendingReset` のドキュメントコメントを更新する：「pre-commit phase 1 or legacy 2–3, or aborting phase 5」→「pre-commit phase 1」。（AC-13）

#### 1-3. `internal/store/errors.go` の変更

- [x] `ErrResetAbortInProgress` を削除する。（AC-05）
- [x] `ErrResetNotPending` を削除する。（AC-06）
- [x] `ErrPendingReset` のエラー文字列から `"to continue or abort"` を削除し、`"use OpenRecoverReset to continue"` に更新する。（AC-07）
- [x] `ErrPendingReset` のドキュメントコメントから `"or abort"` を削除する。（AC-07）

#### 1-4. `internal/store/types.go` の変更

- [x] `OpenRecoverReset` のドキュメントコメントから `AbortReset` および `--abort-reset --yes` への言及を削除し、`ResetForRecovery`（`discard-old --yes`）のみを許可するモードである旨へ整合させる。

**成功条件**：`go build ./internal/store/...` が通ること。フェーズ 1 完了時点では、`internal/store/testutil` を含むテスト用パッケージの整合はフェーズ 3 で確認する。

---

### フェーズ 2：CLI・通知層の削除と文言更新

**目標**：`cmd/tlsrpt-digest` および `internal/notify` から abort への言及をすべて除去し、operator 向け案内を新フェーズ定義に整合させる。

#### 2-1. `cmd/tlsrpt-digest/main.go` の変更

- [x] `errAbortResetRequiresYes` 変数を削除する。（AC-07）
- [x] `errAbortAndModeExclusive` 変数を削除する。（AC-07）
- [x] `errYesRequiresModeOrAbort` を `errYesRequiresMode` にリネームし、値を `"--yes requires --mode"` に更新する。（AC-07）
- [x] `cliOptions` 構造体から `RecoverAbort bool` フィールドを削除する。（AC-03）
- [x] `registerFlags` の `fs.BoolVar(&opts.RecoverAbort, "abort-reset", ...)` 行を削除する。（AC-03）
- [x] `validateFlags` から abort 関連チェックを削除する：`opts.RecoverAbort && opts.RecoverMode != ""` チェック、`opts.RecoverAbort && !opts.RecoverYes` チェックを削除する。（AC-03）
- [x] `validateFlags` の `--yes` 単独チェック条件を `opts.RecoverYes && !opts.RecoverAbort && opts.RecoverMode == ""` から `opts.RecoverYes && opts.RecoverMode == ""` に簡略化する。（AC-07）
- [x] `recoverStoreOpenMode` の `opts.RecoverAbort && opts.RecoverYes` 分岐を削除する。（AC-03）
- [x] `runCLI` の `errors.Is(err, errAbortResetRequiresYes)` チェックを削除し、`errYesRequiresMode` の `exitError` 返却を `errors.Is(err, errYesRequiresMode)` に更新する。（AC-07）
- [x] `recoverStoreOpenMode` 関数のドキュメントコメント（L177-178）から `abort-reset --yes` を削除し、`// (discard-old --yes) and OpenReadWrite for all others.` に更新する。

#### 2-2. `cmd/tlsrpt-digest/recover.go` の変更

- [x] `printInfo` の `if opts.RecoverAbort { selectedMode = "abort-reset" }` ブロックを削除する。（AC-07）
- [x] `printInfo` の「Roll back reset:」行（`fmt.Fprintln(r.stdout, "  Roll back reset: ...")` 相当）を削除する。（AC-07）
- [x] `executeMode` の `case opts.RecoverAbort:` 分岐を削除する。（AC-03）
- [x] `runAbortReset` 関数全体を削除する。（AC-02）
- [x] `import "errors"` パッケージが未使用となるため削除する。（AC-02 の副作用：`runAbortReset` 削除により唯一の使用箇所が消えるため）

#### 2-3. `cmd/tlsrpt-digest/boot.go` の変更

- [x] `Bootstrap` 内の `ErrPendingReset` ラッパーメッセージ（L236）の `" or recover --abort-reset --yes to roll back"` を削除し、結果の文字列を `"store reset is incomplete; run recover --mode discard-old --yes to continue: %w"` にする（`"store reset is incomplete; "` プレフィックスと `": %w"` ラッパーは保持する）。（AC-07）
- [x] `BootstrapOptions.StoreOpenModeOverride` のドキュメントコメント（L87）の `OpenRecoverReset for discard-old/abort-reset.` から `/abort-reset` を削除し `OpenRecoverReset for discard-old.` に更新する。

#### 2-4. `internal/notify/format.go` の変更

- [x] `systemErrorHint` 関数の `SystemErrorKindUIDValidityChanged / SystemErrorKindRecoveryRequired` ケースの返却値から ` (or --abort-reset --yes)` を削除する（先行スペースごと削除し、結果を `"Run: tlsrpt-digest recover --mode discard-old --yes"` にする）。（AC-07e）

**成功条件**：`go build ./cmd/tlsrpt-digest/...` と `go build ./internal/notify/...` が通ること。

---

### フェーズ 3：テスト整合

**目標**：削除済みコードへの参照をテストから除去し、新規 fail-closed テストを追加する。`make test` と `make lint` と `make deadcode` がすべて通ること。

#### 3-1. `internal/store/testutil/mocks.go` の変更

- [x] `FakeStore.AbortResetErr` フィールドを削除する。（AC-01）
- [x] `FakeStore.AbortResetCallCount` フィールドを削除する。（AC-01）
- [x] `FakeStore.AbortReset()` メソッド全体を削除する。（AC-01）
- [x] `FakeStore.PendingReset` フィールドのコメントから「for AbortReset testing」を削除する。

**成功条件**：`go test -tags test ./internal/store/testutil` が通ること（テスト用ビルドタグ込みでインターフェース整合確認）。

#### 3-2. `internal/store/recovery_test.go` の変更（削除）

- [x] `TestAbortReset_UnknownPhaseFailsClosed` を削除する。
- [x] `TestAbortReset_CrashDuringCommitRefusesAbort` を削除する。
- [x] `TestAbortReset_PhaseManifestWritten` を削除する。
- [x] `TestAbortReset_NoPendingReset` を削除する。
- [x] `TestAbortReset_AfterCommit` を削除する。
- [x] `TestAbortReset_RestoresOldData` を削除する。
- [x] `TestAbortReset_Idempotent` を削除する。
- [x] `TestAbortReset_ResumesFromAbortingPhase` を削除する。
- [x] `TestAbortReset_CrashAfterRestoreBeforeManifestRemoval` を削除する。
- [x] `TestResetForRecovery_RefusesAbortingPhase` を削除する。（AC-12）
- [x] `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` を削除する。このテストはフェーズ 2・3 かつ `CurrUIDValidity` 不一致（150 vs 200）のマニフェストが `cleanupCompletedReset` でサイレントに除去されることを検証していたが、フェーズ 2・3 は fail-closed になるため `Open(OpenRecoverReset)` の時点で失敗する。削除後は fail-closed テスト（3-10 節）がフェーズ 2・3 の拒否を検証し、UID 不一致パスの収束は別途 `TestResetForRecovery_StaleUIDMismatchManifestReset` で補う（3-10 節参照）。
- [x] `TestOpen_CleansUpAfterCommitCrashWindow` を削除する（`resetPhase(3)` 植え込みが fail-closed になり破綻するため。C4 コミットクラッシュウィンドウのクリーンアップ検証は既存のフェーズ 1 版 `TestOpen_CleansUpAfterCommitCrashWindowManifestWritten` が担うため重複となる）。

#### 3-3. `internal/store/recovery_test.go` の変更（更新）

- [x] `TestApplyRecovery_RefusesPendingReset`：植え込み値 `resetPhase(2)` を `resetPhaseManifestWritten` に変更する。コメント（L144-145）を英語で次のように書き換える：`// Plant a phase-1 manifest to verify that ApplyRecovery refuses while a pre-commit reset is in progress.`（2 行の既存コメントを 1 行に置換し、`ErrPendingReset` アサーション自体は変更しない）。
- [x] `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`：植え込み値 `resetPhase(2)` を `resetPhaseManifestWritten` に変更し、関数コメント・植え込み箇所コメントの「legacy phase-2」記述を英語で「partial pre-commit phase-1 staging state」へ書き換える。`assertResetConverged` のアサーションは維持し、`make test` で収束を再確認する。
- [x] `TestResetForRecovery_IdempotentAfterCrashBeforeCommit`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更し、「legacy phase-3 manifest」コメントを除去する。（AC-13：§8 の `resetPhase(3)` 横断検索でこのテストの更新漏れを検出する）
- [x] `TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`：コメント内の `writeResetManifest(phase=2)` と「not yet advanced to phase=2」を、フェーズ 1 マニフェスト書き込み後かつ data staging 後にクラッシュした説明へ英語で更新する。
- [x] `TestOpen_BlockedByPreCommitReset`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。あわせて関数コメント（L1129）の「or an AbortReset is partially applied」を削除し、英語のまま「i.e. a pre-commit reset manifest is present」相当へ更新する。
- [x] `TestResetForRecovery_CommitCrashWindow_ZeroUID`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更し、コメント内の `phase=3`・`phase=emails_staged`・「legacy value」をフェーズ 1 のコミットクラッシュウィンドウ説明へ英語で更新する。
- [x] `TestResetPhasePersistedNumericValues`：`resetPhaseAborting` のアサーション行を削除し、フェーズ 1・4 のみ検証する。（AC-08）
- [x] `TestValidateManifestPhaseRange`：有効値を `{1, 4}` のみにし、拒否値を `{0, 2, 3, 5, 6, 99}` に更新する。（AC-09）

#### 3-4. `internal/store/store_test.go` の変更

- [x] `TestOpen_PendingReset_FailsClosedForReadWrite`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。
- [x] `TestOpen_PendingReset_FailsClosedForReadWrite`：コメント「legacy phase-3 manifest」をフェーズ 1 の保留マニフェスト説明へ英語で更新する。
- [x] `TestOpen_PendingReset_OpenRecoverResetSucceeds`：植え込み値 `resetPhase(3)` を `resetPhaseManifestWritten` に変更する。
- [x] `TestOpen_PendingReset_OpenRecoverResetSucceeds`：コメント「legacy phase-3 manifest」をフェーズ 1 の保留マニフェスト説明へ英語で更新する。

#### 3-5. `cmd/tlsrpt-digest/recover_test.go` の変更（削除）

- [x] `TestRecover_AbortResetYesCallsAbortReset` を削除する。
- [x] `TestRecover_AbortResetNoPendingReset` を削除する。
- [x] `TestRecover_AbortResetFailure` を削除する。
- [x] `TestRecover_AbortResetAlone` を削除する。

#### 3-6. `cmd/tlsrpt-digest/recover_test.go` の変更（更新）

- [x] `TestRecover_YesAlone`：テストコメントから `--abort-reset` を削除し、`assert.Contains(t, stderr.String(), "--yes requires --mode or --abort-reset")` を `assert.Contains(t, stderr.String(), "--yes requires --mode")` かつ `assert.NotContains(t, stderr.String(), "--abort-reset")` に更新する。（AC-07）
- [x] `TestRecover_NoRecoveryRequired`：`[]cliOptions{...}` スライスリテラルから `{RecoverAbort: true, RecoverYes: true}` エントリ（L262 付近の 1 行）を削除する（残り 4 エントリはそのまま維持する）。あわせてループ本体の `st.AbortResetCallCount = 0`（全イテレーションに共通のリセット行）と `assert.Equal(t, 0, st.AbortResetCallCount)`（全イテレーションの共通アサーション行）を削除する。`AbortResetCallCount` フィールド自体が `FakeStore` から削除されるため、これらすべての参照を除去する必要がある。
- [x] `TestRecover_CommittedCleanupPending_StatusDisplay`：`{RecoverAbort: true, RecoverYes: true}` ケースを削除し、`AbortResetCallCount` 参照を削除する。
- [x] `TestRecover_StatusDisplayNoMode`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。
- [x] `TestRecover_PendingResetDisplaysOptions`：`assert.Contains(t, output, "abort-reset --yes")` を削除する。（AC-07）
- [x] `TestBootstrap_PendingResetShowsGuidance`（`recover_test.go:401`。`boot_test.go` の `TestBootstrap_PendingResetAdvice` とは別関数）：`assert.Contains(t, err.Error(), "recover --abort-reset --yes")`（L415）を削除し、代わりに `assert.NotContains(t, err.Error(), "abort-reset")` を追加する。あわせて関数ドキュメントコメントを英語で「guidance for the continue path」へ更新する（L399 の「guidance for both continue and abort paths」→「guidance for the continue path」）。（AC-07）
- [x] `TestRecover_PendingResetShowsStatusForNonDestructiveModes`：`AbortResetCallCount` アサーション（L460）と `assert.Contains(t, output, "recover --abort-reset --yes")`（L469）を削除し、代わりに `assert.NotContains(t, output, "abort-reset")` を追加して abort 参照の再出現を防ぐ回帰ガードとする。（AC-07）
- [x] `TestRecover_HasPendingResetFailure`：`assert.Equal(t, 0, st.AbortResetCallCount)` を削除する。

#### 3-7. `cmd/tlsrpt-digest/main_test.go` の変更

- [x] `TestRunCLI_RecoverResetOpenMode`：「abort reset confirmed」ケース（`args: []string{"recover", "-abort-reset", "-yes"}`）を削除する。（AC-03）
- [x] `TestRunCLI_AbortResetFlagUndefined` を新規追加する：`recover --abort-reset --yes` の実行が `flag.Parse` により `flag provided but not defined: -abort-reset` 相当のエラーを返し、終了コード 2 となることを検証する。（AC-03）

#### 3-8. `cmd/tlsrpt-digest/boot_test.go` の変更

- [x] `TestBootstrap_PendingResetAdvice`（`boot_test.go:383`。`recover_test.go` の `TestBootstrap_PendingResetShowsGuidance` と混同しないこと）：`ErrPendingReset` ラッパーが `"recover --abort-reset --yes"` を含まないことを検証する。既存の `assert.Contains(t, err.Error(), "recover --abort-reset --yes")`（L396）を `assert.NotContains(t, err.Error(), "abort-reset")` に更新する。（AC-07）

#### 3-9. `internal/notify/format_test.go` の変更

- [x] `TestFormatSystemError_ActionHint_UIDValidityChanged`：`assert.NotContains(t, body, "abort-reset")` を追加する。（AC-07e）
- [x] `TestFormatSystemError_ActionHint_RecoveryRequired`：`assert.NotContains(t, body, "abort-reset")` を追加する。（AC-07e）

#### 3-10. `internal/store/recovery_test.go` の変更（新規追加）

- [x] **`TestLegacyPhaseFailsClosed_ResetForRecovery`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `ResetForRecovery` を呼び出すと `ErrResetManifestPhaseUnknown` が返り、かつマニフェストファイルとステージングディレクトリが削除されずに残ることを検証する。（AC-10、AC-11）
- [x] **`TestLegacyPhaseFailsClosed_OpenReadWrite`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `Open(OpenReadWrite)` を呼び出すと `ErrResetManifestPhaseUnknown` が返り、かつマニフェストファイルとステージングディレクトリが削除されずに残ることを検証する。（AC-10、AC-11）
- [x] **`TestHasPendingReset_LegacyPhaseFailsClosed`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `HasPendingReset` を呼び出すと `ErrResetManifestPhaseUnknown` が返り、かつマニフェストファイルとステージングディレクトリが削除されずに残ることを検証する。（AC-10、AC-11）
- [x] **`TestLegacyPhaseFailsClosed_ApplyRecovery`**（テーブル駆動）を追加する：フェーズ 2・3・5 のマニフェストが存在する状態で `ApplyRecovery` を呼び出すと、`HasPendingReset` の内部で `validateManifestPhase` が fail-closed し、`ErrResetManifestPhaseUnknown` を含むエラーが返ることを検証する（`errors.As` で `*ErrResetManifestPhaseUnknown` を確認）。`Open(OpenRecoverReset)` でストアを開いてから `ApplyRecovery` を呼び出す。（AC-10、AC-11：`ApplyRecovery → HasPendingReset` 経路の fail-closed）

- [x] **`TestResetForRecovery_StaleUIDMismatchManifestReset`** を追加する：フェーズ 1（`resetPhaseManifestWritten`）かつ `CurrUIDValidity: 150`（現在の `recovery_required` の 200 と不一致）のマニフェストが存在する状態で `Open(OpenRecoverReset)` → `ResetForRecovery(200)` を呼び出すと、`cleanupCompletedReset` の UID 不一致検出により stale マニフェストとステージングが除去されて収束することを検証する（`assertResetConverged` のアサーションを維持）。`TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` が担っていた UID 不一致クリーンアップパスを、有効フェーズ 1 で再現する置換テスト。**コードパス確認**：`cleanupCompletedReset` は `readResetManifest` 後に `currUIDValidity != mfst.CurrUIDValidity` を判定し stale マニフェストを削除する分岐を持つ。この分岐は削除された `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts`（フェーズ 2/3 植え込み）が exercised していたパスと同一であり、フェーズ 1 植え込みでも UID 不一致条件のみで到達可能であることを実装前に `cleanupCompletedReset` のコードで確認すること。

これら 5 つのテストは `recovery_test.go`（`package store` の内部テスト）に追加し、`resetPhase` 型に直接アクセスする。新規ヘルパーファイルは不要（既存の `writeResetManifest`・`resetManifestPath`・`resetStagingPath` を再利用できる）。

#### 3-11. 静的検査

- [x] `make fmt` を実行してフォーマットを揃える。
- [x] `make test` を実行して全テストが通ることを確認する。
- [x] `make lint` を実行してリントエラーがないことを確認する。
- [x] `make deadcode` を実行して新たな未使用関数が検出されないことを確認する。（AC-04）

### PR-1 作成ポイント: abort-reset feature removal

**対象ステップ**: 1-1 / 1-2 / 1-3 / 1-4 / 2-1 / 2-2 / 2-3 / 2-4 / 3-1 / 3-2 / 3-3 / 3-4 / 3-5 / 3-6 / 3-7 / 3-8 / 3-9 / 3-10 / 3-11

**推奨タイトル**: `refactor(0081): remove AbortReset feature and legacy phase fallbacks`

**レビュー観点**: `Store` インターフェースの `AbortReset` 削除と `cmd` 層への波及 / `validateManifestPhase` の `{1, 4}` 2 値判定への変更 / operator 向け案内からの abort 参照除去 / ステップ 3-10 の fail-closed テスト：フェーズ 2・3・5 マニフェスト存在下でステージングとマニフェストが保全される（削除されない）ことの assertion が正確か・`ErrResetManifestPhaseUnknown` の unwrap 経路が `ApplyRecovery → HasPendingReset` 経由で正しく伝播するか・`TestResetForRecovery_StaleUIDMismatchManifestReset` が削除した `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` の UID 不一致クリーンアップパスを正しく置換しているか

> **注意**：ステップ 1-1〜2-4 の完了時点では `go build ./internal/store/... && go build ./cmd/tlsrpt-digest/...` を確認基準とし、`make test && make lint` がグリーンになるのはステップ 3-11 完了後のみ。

- [x] `make test && make lint` がグリーンであることを確認した（ステップ 3-11 完了後）
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

### フェーズ 4：ドキュメント改訂

**目標**：ADR-0003・開発者ガイド・用語集・運用手順を新フェーズ定義 `{1, 4}` に整合させる。

#### 4-1. `docs/dev/adr/0003_reset_phase_design.ja.md` の変更

- [x] フェーズ一覧表からフェーズ 2・3・5 の行を削除する。（AC-14）
- [x] 「フェーズ 5（recovery_required リセットマーカー）を設ける理由」節を削除し、必要な経緯は「チェックポイントフェーズ（フェーズ 2・3）廃止の判断」節に 0081 でフェーズ 5 も廃止した旨の 1 段落として統合する。（AC-15）
- [x] 状態遷移図から P5 ノードおよびその遷移（P1→P5、P5→RR）を削除する。（AC-16）
- [x] 不変条件表の「フェーズ 5 が書かれている ⟹ `AbortReset` のみが続行できる」行を削除する。（AC-17）
- [x] ユーザー操作時の挙動表から `recover --abort-reset --yes` 列を削除し、保留リセット時の案内を継続操作（`recover --mode discard-old --yes`）のみにする。（AC-18）
- [x] §1 要件表から `AC-abort` 行を削除し、§2 のステージングディレクトリ説明から `AbortReset` で復元可能という記述を削除する。
- [x] §4 の後方互換説明を、レガシー値 2・3・5 は `validateManifestPhase` で fail-closed する説明へ置換する（設計根拠は `02_architecture.md` §2.3・§2.4 を参照する）。
- [x] §7「将来の変更・拡張方針」の `AbortReset` サブ節を削除し、将来フェーズ追加時の手順から `AbortReset` への処理追加を削除する。
- [x] §8・§9 の関連ファイル表から `AbortReset`・`ErrResetNotPending`・`ErrResetAbortInProgress`・`restoreFromStaging` への言及を削除する。
- [x] ADR 日本語版の更新後、`AbortReset`・`abort-reset`・`resetPhaseAborting`・`ErrResetAbortInProgress`・`ErrResetNotPending`・`restoreFromStaging`・`legacy values 2`・`legacy values 2/3`・`phase < resetPhaseCommitted`・`フェーズ 5`・`{1, 4, 5}` を検索し、廃止の経緯として意図して残すもの以外がないことを確認する。（AC-14〜AC-18）

#### 4-2. `docs/dev/developer_guide/process_locking.ja.md` の変更

- [x] 対象サブコマンド一覧（L65）から `--abort-reset` を削除する。（AC-20）
- [x] 契約節（L71・L73）・チェックリスト節（L298・L300）から `recover --abort-reset --yes` および `ResetForRecovery / AbortReset` のペア記述を除去し、`recover --mode discard-old --yes` / `ResetForRecovery` のみが `OpenRecoverReset` を使うよう整合させる。（AC-20）
- [x] 状態機械の説明（L43）の `（`ResetForRecovery` / `AbortReset`）` から `AbortReset` を削除する。（AC-20）
- [x] `AbortReset の restore 処理` を説明する箇条書き（L182）を削除する（`restoreFromStaging` 廃止に伴い該当処理が存在しなくなるため）。（AC-20）
- [x] リセットマニフェスト定義（L48）の `resetPhase`（1〜5） を `resetPhase`（1・4） に更新する。（AC-20）
- [x] フェーズ説明（現行 L191 付近）の「フェーズ 1〜5」を「フェーズ 1・4」に更新し、フェーズ 2・3・5 がレガシー値として fail-closed される説明へ置換する。（AC-20）
- [x] シーケンス図・説明（現行 L204 付近）の「フェーズ 2–3」参照を削除し、フェーズ 1 の再実行またはレガシー値 fail-closed の説明に整合させる。（AC-20）
- [x] 日本語版更新後、`AbortReset`・`--abort-reset`・`resetPhase 1`・`1〜5`・`1-5`・`フェーズ 2–3`・`フェーズ 2・3` を検索し、廃止済み経緯として意図して残すもの以外がないことを確認する。（AC-20）

#### 4-3. `docs/translation_glossary.md` の変更

- [x] 「保留リセット / pending reset」の定義を更新する：現状「フェーズ 1〜3 および フェーズ 5」→「フェーズ 1 のみ」に更新し、`AbortReset` 廃止に整合させる。
- [x] 用語集全体を `rg -n -e "AbortReset" -e "abort-reset" -e "フェーズ 5" -e "フェーズ 2" -e "フェーズ 3" -e "ErrResetAbortInProgress" -e "ErrResetNotPending" -e "中断機能" docs/translation_glossary.md` で検索し、廃止済みの状態・エラー型・機能への言及を削除または「廃止済み」として更新する。

#### 4-4. `docs/operations/legacy_reset_manifest_upgrade.ja.md` の新規作成

- [x] `docs/operations/` を運用ランブック置き場として新設し、本手順書の冒頭に対象読者（アップグレード作業者）と適用条件（フェーズ 2・3・5 マニフェストが残る旧ストア）を明記する。
- [x] フェーズ 2・3 のマニフェストが残存するストアのアップグレード前に旧バージョンで `recover --mode discard-old --yes` を完了する手順を記載する。（AC-21）
- [x] フェーズ 5 のマニフェストが残存するストアのアップグレード前に旧バージョンで `AbortReset`（`recover --abort-reset --yes`）を完了する手順を記載する。（AC-22）
- [x] 手順書内にアップグレード後の確認として、旧マニフェストが残る場合は新バージョンが `ErrResetManifestPhaseUnknown` で停止し、ステージング・マニフェストを保全することを記載する（`02_architecture.md` §2.4 参照）。（AC-21、AC-22）

### PR-2 作成ポイント: Japanese documentation update

**対象ステップ**: 4-1 / 4-2 / 4-3 / 4-4

**推奨タイトル**: `docs(0081): update ADR-0003, process locking guide, and add upgrade runbook`

**レビュー観点**: ADR フェーズ表・状態遷移図・不変条件表からのフェーズ 2・3・5 削除（AC-14〜AC-18） / `process_locking` の `--abort-reset` 参照除去（AC-20） / 運用手順書（フェーズ 2・3・5 残存ストアの事前作業手順）の完全性（AC-21、AC-22）

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

#### 4-5. 英語版の反映

- [x] `docs/dev/adr/0003_reset_phase_design.ja.md` → `.md` に `/mktrans` で反映する。（AC-19）
- [x] `docs/dev/developer_guide/process_locking.ja.md` → `.md` に `/mktrans` で反映する。（AC-20）
- [x] `docs/operations/legacy_reset_manifest_upgrade.ja.md` → `.md` に `/mktrans` で反映する。（AC-21、AC-22）
- [x] 英語版反映後、ADR・process locking・運用手順の `.md` でも日本語版と同じ検索パターンを確認し、翻訳漏れがないことを検証する。（AC-19〜AC-22）

### PR-3 作成ポイント: English translations

**対象ステップ**: 4-5

**推奨タイトル**: `docs(0081): translate ADR-0003, process locking guide, and upgrade runbook to English`

**レビュー観点**: 日英間の内容整合性（廃止済みフェーズ 2・3・5 への言及が英語版に残っていないこと） / `--abort-reset` 参照が翻訳で再導入されていないこと（§8 検索パターンを英語版でも実行して確認）

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した（4-5 は PR-2 に統合）
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

## 3. 実装順序とマイルストーン

### 3.1 マイルストーン

| マイルストーン | 対象フェーズ | 成果物 | 完了判定 |
|---|---|---|---|
| M1：ストア層から中断機能を削除 | フェーズ 1 | `internal/store` の `AbortReset`・フェーズ 5・専用エラー削除 | `go build ./internal/store/...` が通る |
| M2：CLI・通知案内から abort 経路を削除 | フェーズ 2 | `cmd/tlsrpt-digest` と `internal/notify` のフラグ・文言更新 | `go build ./cmd/tlsrpt-digest/...` と `go build ./internal/notify/...` が通る |
| M3：テストを新しい fail-closed 契約へ整合 | フェーズ 3 | Go テスト更新、新規 fail-closed テスト、静的検査 | `go test -tags test ./internal/store/testutil`・`make test`・`make lint`・`make deadcode` が通る |
| M4：設計・運用ドキュメントを `{1, 4}` に整合 | フェーズ 4 | ADR、process locking、運用手順、英語版反映 | §6 の受け入れ条件検証と §8 の検索がすべて完了 |

### 3.2 PR 構成

フェーズ 1〜3 は `Store` インターフェース変更が `cmd` 層のコンパイルに直接波及し、かつフェーズ 3 のテスト削除・更新がフェーズ 1・2 のシンボル削除を前提とするため、`make test && make lint` がグリーンになる最小単位は「フェーズ 1〜3 一体」となる。フェーズ 4（ドキュメント）は日本語版と英語翻訳に分割できる。

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | 1-1 / 1-2 / 1-3 / 1-4 / 2-1 / 2-2 / 2-3 / 2-4 / 3-1 / 3-2 / 3-3 / 3-4 / 3-5 / 3-6 / 3-7 / 3-8 / 3-9 / 3-10 / 3-11 | `AbortReset` 機能の全削除：`internal/store` API・CLI・通知層コード＋テスト整合（3-10 が新規 fail-closed テスト） |
| PR-2 | 4-1 / 4-2 / 4-3 / 4-4 | 日本語ドキュメント改訂：ADR-0003・`process_locking`・用語集・運用手順書新規作成 |
| PR-3 | 4-5 | 英語版反映：`/mktrans` で ADR・`process_locking`・運用手順書を翻訳 |

---

## 4. テスト戦略

### 4.1 ユニットテスト

- `internal/store/recovery_test.go` で `validateManifestPhase` の有効値 `{1, 4}` と拒否値 `{0, 2, 3, 5, 6, 99}` を直接検証する。（AC-09）
- `ResetForRecovery`・`Open(OpenReadWrite)`・`HasPendingReset` の各入口で、レガシー値 2・3・5 が `ErrResetManifestPhaseUnknown` を返し、ステージングとマニフェストを保全することをテーブル駆動で検証する。（AC-10、AC-11）
- 既存のクラッシュ再開テストは、レガシー値植え込みをフェーズ 1 植え込みへ変更し、非自明な部分ステージング状態の冪等収束を維持する。（AC-09、AC-13）
- 削除される `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` が持っていた `CurrUIDValidity` 不一致クリーンアップパスの検証は、`TestResetForRecovery_StaleUIDMismatchManifestReset` でフェーズ 1 を使って再現する。

### 4.2 CLI・通知テスト

- `cmd/tlsrpt-digest/main_test.go` で `recover --abort-reset --yes` が未定義フラグとして終了コード 2 になることを検証する。（AC-03）
- `cmd/tlsrpt-digest/recover_test.go` と `boot_test.go` で、保留リセット時の案内が継続操作のみを示し、`abort-reset` を含まないことを検証する。（AC-07）
- `internal/notify/format_test.go` で system error の operator 向け案内が `abort-reset` を含まないことを検証する。（AC-07e）

### 4.3 静的検証

- 削除 AC（AC-01、AC-02、AC-04〜AC-06、AC-08、AC-12、AC-13）は、コンパイルだけに依存せず、§8 の検索タスクで識別子・文言の不在を確認する。
- ドキュメント AC（AC-14〜AC-22）は、対象ファイルの具体的な更新タスクと、ADR・process locking・運用手順の日本語版・英語版に対する検索タスクで検証する。
- 新規テストヘルパーファイルは追加せず、既存ヘルパーを再利用するため `docs/dev/developer_guide/test_organization.md` との衝突はない。

---

## 5. テストヘルパー計画

新規のテストヘルパーファイルは追加しない。理由：

- **`internal/store/recovery_test.go`** に追加する fail-closed テスト（3-10 節）は、`writeResetManifest`・`resetManifestPath`・`resetStagingPath` などの既存内部ヘルパーを直接使用できるため、`package store` 内部テストに留まる。新規 `test_helpers.go` は不要。
- **`internal/store/testutil/mocks.go`** はフィールド・メソッド削除のみであり、新規追加なし。

---

## 6. 受け入れ条件検証

| AC | 対応する実装タスク | 対応するテスト・検証タスク |
|---|---|---|
| AC-01 | 1-2（`Store` インターフェース `AbortReset` 削除） | `go test -tags test ./internal/store/testutil` が成功する。`rg -n "AbortReset\\(\\)" internal/store cmd/tlsrpt-digest` の期待結果：一致なし。 |
| AC-02 | 1-1（`AbortReset` 実装削除） | `rg -n "func .*AbortReset" internal cmd` と `rg -n "runAbortReset" cmd internal` の期待結果：一致なし。`make deadcode` が成功する。 |
| AC-03 | 2-1（`--abort-reset` フラグ削除） | `cmd/tlsrpt-digest/main_test.go::TestRunCLI_AbortResetFlagUndefined` が、終了コード 2 と `flag provided but not defined: -abort-reset` 相当を検証する。 |
| AC-04 | 1-1（`restoreFromStaging` 削除） | `rg -n "restoreFromStaging" internal cmd` の期待結果：一致なし。`make deadcode` が成功する。 |
| AC-05 | 1-3（`ErrResetAbortInProgress` 削除） | `rg -n "ErrResetAbortInProgress" internal cmd` の期待結果：一致なし。`make test` が成功する。 |
| AC-06 | 1-3（`ErrResetNotPending` 削除） | `rg -n "ErrResetNotPending" internal cmd` の期待結果：一致なし。`make test` が成功する。 |
| AC-07 | 1-3（`ErrPendingReset` 文言更新）、2-1〜2-3（CLI・bootstrap 文言更新） | `cmd/tlsrpt-digest/recover_test.go::TestRecover_YesAlone`、`::TestRecover_PendingResetDisplaysOptions`、`::TestBootstrap_PendingResetShowsGuidance`、`cmd/tlsrpt-digest/boot_test.go::TestBootstrap_PendingResetAdvice`。加えて `rg -n -e "--abort-reset" -e "Roll back reset" -e "to continue or abort" -e "resume or abort" -e "abort-reset --yes" internal/store/errors.go cmd/tlsrpt-digest/boot.go cmd/tlsrpt-digest/recover.go cmd/tlsrpt-digest/main.go` の期待結果：一致なし。 |
| AC-07e | 2-4（`systemErrorHint` 更新） | `internal/notify/format_test.go::TestFormatSystemError_ActionHint_UIDValidityChanged` と `::TestFormatSystemError_ActionHint_RecoveryRequired` が `abort-reset` 不在を検証する。 |
| AC-08 | 1-1（`resetPhaseAborting` 削除） | `internal/store/recovery_test.go::TestResetPhasePersistedNumericValues`。`rg -n "resetPhaseAborting" internal cmd` の期待結果：一致なし。 |
| AC-09 | 1-1（`validateManifestPhase` 変更） | `internal/store/recovery_test.go::TestValidateManifestPhaseRange` が有効値 `{1, 4}` と拒否値 `{0, 2, 3, 5, 6, 99}` を検証する。 |
| AC-10 | 1-1（`validateManifestPhase` 変更） | `internal/store/recovery_test.go::TestLegacyPhaseFailsClosed_ResetForRecovery`、`::TestLegacyPhaseFailsClosed_OpenReadWrite`、`::TestHasPendingReset_LegacyPhaseFailsClosed`、`::TestLegacyPhaseFailsClosed_ApplyRecovery` がフェーズ 2・3 の fail-closed と保全を検証する。 |
| AC-11 | 1-1（`validateManifestPhase` 変更） | `internal/store/recovery_test.go::TestLegacyPhaseFailsClosed_ResetForRecovery`、`::TestLegacyPhaseFailsClosed_OpenReadWrite`、`::TestHasPendingReset_LegacyPhaseFailsClosed`、`::TestLegacyPhaseFailsClosed_ApplyRecovery` がフェーズ 5 の fail-closed と保全を検証する。 |
| AC-12 | 1-1（フェーズ 5 拒否チェック削除） | `rg -n "mfst\\.Phase == resetPhaseAborting" internal/store/recovery.go` と `rg -n "ErrResetAbortInProgress" internal/store/recovery.go` の期待結果：一致なし。AC-11 の fail-closed テストが代替挙動を検証する。 |
| AC-13 | 1-1〜1-4（コメント整理） | `rg -n -e "legacy 2" -e "legacy 2-3" -e "legacy 2–3" -e "phase < resetPhaseCommitted" -e "reset or abort" -e "or AbortReset" internal/store/recovery.go internal/store/store.go internal/store/types.go internal/store/recovery_test.go internal/store/store_test.go` の期待結果：一致なし。`rg -n -e "phase=2" -e "phase=3" -e "phase=emails_staged" -e "legacy phase-2" -e "legacy phase-3" -e "legacy value" -e "2–3" -e "2・3" internal/store -g "*_test.go"` の期待結果：削除対象テストまたは新規 fail-closed テストの意図的な記述のみ。 |
| AC-14 | 4-1（ADR フェーズ表更新） | `rg -n -e "resetPhaseAborting" -e "フェーズ 5" -e "Phase 5" -e "{1, 4, 5}" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の期待結果：廃止の経緯として明示した段落以外は一致なし。 |
| AC-15 | 4-1（ADR 設計根拠節更新） | `rg -n -e "フェーズ 5（recovery_required リセットマーカー）を設ける理由" -e "reason for phase 5" -e "ErrResetAbortInProgress" -e "restoreFromStaging" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の期待結果：一致なし。 |
| AC-16 | 4-1（ADR 状態遷移図更新） | `rg -n -e "P5" -e "P1 --> P5" -e "P5 --> RR" -e "abort-reset" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の期待結果：廃止の経緯として明示した記述以外は一致なし。 |
| AC-17 | 4-1（ADR 不変条件表更新） | `rg -n -e "フェーズ 5 が書かれている" -e "Phase 5 is written" -e "AbortReset のみ" -e "only AbortReset" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の期待結果：一致なし。 |
| AC-18 | 4-1（ADR ユーザー操作表更新） | `rg -n -e "recover --abort-reset --yes" -e "Roll back reset" -e "continue or abort" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の期待結果：廃止の経緯として明示した記述以外は一致なし。 |
| AC-19 | 4-5（英語版 `/mktrans`） | `rg -n -e "resetPhaseAborting" -e "AbortReset" -e "abort-reset" -e "Phase 5" -e "legacy values 2" docs/dev/adr/0003_reset_phase_design.md docs/dev/developer_guide/process_locking.md docs/operations/legacy_reset_manifest_upgrade.md` の期待結果：運用手順で旧バージョン実行を説明する箇所以外は一致なし。 |
| AC-20 | 4-2（`process_locking.ja.md` 更新）、4-3（用語集更新） | `rg -n -e "AbortReset" -e "--abort-reset" -e "resetPhase 1" -e "1〜5" -e "1-5" -e "フェーズ 2–3" -e "フェーズ 2・3" docs/dev/developer_guide/process_locking.ja.md docs/dev/developer_guide/process_locking.md` の期待結果：廃止の経緯として明示した記述以外は一致なし。`rg -n -e "フェーズ 1〜3" -e "フェーズ 5" -e "AbortReset" docs/translation_glossary.md` の期待結果：保留リセット定義に stale な説明がない。 |
| AC-21 | 4-4（運用手順書新規作成） | `rg -n -e "recover --mode discard-old --yes" -e "フェーズ 2" -e "フェーズ 3" -e "phase 2" -e "phase 3" docs/operations/legacy_reset_manifest_upgrade.ja.md docs/operations/legacy_reset_manifest_upgrade.md` の期待結果：事前完了手順が日英両方に存在する。 |
| AC-22 | 4-4（運用手順書新規作成） | `rg -n -e "AbortReset" -e "recover --abort-reset --yes" -e "フェーズ 5" -e "phase 5" docs/operations/legacy_reset_manifest_upgrade.ja.md docs/operations/legacy_reset_manifest_upgrade.md` の期待結果：旧バージョンで中断完了してからアップグレードする手順が日英両方に存在する。 |

---

## 7. 実装チェックリスト

- [ ] PR-1 マージ済み（対象ステップ: 1-1 / 1-2 / 1-3 / 1-4 / 2-1 / 2-2 / 2-3 / 2-4 / 3-1 / 3-2 / 3-3 / 3-4 / 3-5 / 3-6 / 3-7 / 3-8 / 3-9 / 3-10 / 3-11）
- [ ] PR-2 マージ済み（対象ステップ: 4-1 / 4-2 / 4-3 / 4-4）
- [ ] PR-3 マージ済み（対象ステップ: 4-5）
- [ ] §6 の全 AC 行に実装タスクとテスト・検証タスクが紐づいている。
- [ ] §8 の横断検索で、意図しない abort・レガシーフェーズ参照が残っていない。

---

## 8. abort 文言の横断確認チェックリスト

実装フェーズ 2・3 完了後（ステップ 2-1〜3-11 完了後）に以下のパターンを `rg -n` で検索し、operator 向け案内から意図しない abort への言及が残っていないことを確認する。

- `--abort-reset`
- `Roll back reset`
- `to continue or abort`
- `resume or abort`
- `abort-reset --yes`

検索対象ファイル：`internal/store/errors.go`、`cmd/tlsrpt-digest/boot.go`、`cmd/tlsrpt-digest/recover.go`、`cmd/tlsrpt-digest/main.go`、`internal/notify/format.go`。

加えて、実装フェーズ 2 完了後（ステップ 2-1〜2-4 完了後）に `cmd/tlsrpt-digest/main.go` の `recoverStoreOpenMode` コメント（L178）にも `abort-reset --yes` が残らないことを確認する（このコメントの除去はステップ 2-1 で実施済みのはずであり、本チェックリストは取りこぼし検出を目的とする）。

- 実装フェーズ 3 完了後（ステップ 3-1〜3-11 完了後）に `internal/` と `cmd/` 全体で `AbortReset|abort-reset|reset or abort|or AbortReset|ErrResetAbortInProgress|ErrResetNotPending|restoreFromStaging|resetPhaseAborting` を検索し、削除対象識別子・コメントが残っていないことを確認する。（AC-01、AC-02、AC-04〜AC-08、AC-12、AC-13）
- 実装フェーズ 3 完了後に `internal/store/*_test.go` で `resetPhase(2)`・`resetPhase(3)`・`resetPhase(5)` を検索し、fail-closed テスト（ステップ 3-10）以外のレガシー値植え込みが残っていないことを確認する。（AC-10、AC-11）
- 実装フェーズ 3 完了後に `internal/store/*_test.go` で `phase=2`・`phase=3`・`phase=emails_staged`・`legacy phase-2`・`legacy phase-3`・`legacy value`・`2–3`・`2・3` を検索し、削除対象テストまたは新規 fail-closed テスト（ステップ 3-10）の意図的な記述以外に stale コメントが残っていないことを確認する。（AC-10、AC-11、AC-13）
- 実装フェーズ 4 完了後（ステップ 4-1〜4-5 完了後）に ADR・process locking・運用手順の日本語版と英語版で、4-1・4-2・4-5 に列挙した検索パターンを確認する。（AC-14〜AC-22）
- 実装フェーズ 4 完了後に `docs/translation_glossary.md` で `フェーズ 1〜3`・`フェーズ 5`・`AbortReset` を検索し、保留リセット定義に stale な説明が残っていないことを確認する。（AC-20）

---

## 9. リスク管理

| リスク | 影響 | 緩和策 |
|---|---|---|
| レガシー値（2・3）を植え込む既存テストの見落とし | `validateManifestPhase` を `{1, 4}` に絞った後、植え込み値が fail-closed となり `make test` が破綻する | §1.3・フェーズ 3 で全 `resetPhase(2)`/`resetPhase(3)` 植え込み箇所を関数単位で洗い出し済み（`TestAbortReset_*` は全削除、その他は植え込み値変更または削除）。実装時に `rg -n "resetPhase\\((2|3|5)\\)" internal/store -g "*_test.go"` を再実行し、計画外の植え込みが残っていないことを確認する。 |
| 削除に伴うコンパイルエラーの取りこぼし | ビルド不能 | フェーズ順序（ストア層 → CLI・通知層 → テスト整合）を守り、各フェーズ末で `go build` / `make test` を実行する。`make deadcode` で未使用関数の取りこぼしも検出する。 |
| operator 向け文言からの abort 参照の取りこぼし | 廃止済みフラグを案内してしまう | §8 の横断検索チェックリストで `--abort-reset` 等を検索し、コメントを含めて残存がないことを確認する。 |
| `/mktrans` 反映漏れによる日英不整合 | ドキュメントの不整合 | フェーズ 4-5 で対象 3 ファイルの `/mktrans` 反映を明示タスク化済み。日本語版を先に確定してから反映する。 |

## 10. 成功条件

- `make test`・`make lint`・`make deadcode`・`make fmt` がすべてエラーなく完了する。
- §6 受け入れ条件検証の全 AC（AC-01〜AC-22、欠番除く）が、対応する実装タスクとテスト／検証方法に紐づき、検証済みである。
- §8 の横断検索チェックリストで、実装対象ファイルの operator 向け案内・コメントに意図しない abort 参照が残っていない。
- ADR-0003・`process_locking`・運用手順書の日本語版と英語版（`/mktrans` 反映）がフェーズ定義 `{1, 4}` に整合している。

## 11. 次のステップ

- 本計画のレビュー・承認（ステータスを `approved` に更新）後、`/runplan` でフェーズ 1〜4 を順に実装する。
- 実装中は各チェックボックスをリアルタイムで更新する（完了 `[x]`、部分完了 `[-]`）。
- 実装完了後、PR を作成しレビューに回す。
