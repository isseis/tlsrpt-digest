# 実装計画書：ResetForRecovery チェックポイントフェーズの簡略化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `approved` |
| 作成日 | 2026-05-31 |
| レビュー日 | 2026-05-31 |
| レビュアー | isseis |
| コメント | - |

---

## 1. 実装の全体像

### 1.1 目的

`resetPhase` のフェーズ 2（`resetPhaseDataStaged`）・フェーズ 3（`resetPhaseEmailsStaged`）を廃止し、`advanceResetPhases` をコミット前から一括実行へ簡略化する。設計の詳細は [02_architecture.md](02_architecture.md) を参照。

### 1.2 実装原則

- `internal/store/recovery.go` の変更に閉じる。新しいパッケージ・型・公開 API は追加しない（アーキテクチャ §2.1）。
- フェーズ 4・5 の数値・意味・役割は変更しない（AC-04）。
- コミット前判定は `phase < resetPhaseCommitted`（`[1, 4)`）の範囲式で表現し、値 2・3 を名指ししない（アーキテクチャ §3.2）。
- 既存テストは削除するのではなく、レガシー値のリテラル（`resetPhase(2)` / `resetPhase(3)`）で構築し直して意味を維持する（アーキテクチャ §3.4）。

### 1.3 既存コード調査結果

#### 変更が必要なコード

| ファイル | 変更種別 | 変更箇所 |
|---|---|---|
| `internal/store/recovery.go` | 実装変更 | （1）定数 `resetPhaseDataStaged=2`・`resetPhaseEmailsStaged=3` を削除する。（2）`advanceResetPhases` 内の中間チェックポイント書き込み（フェーズ 2・3 の `writeResetManifest` 呼び出し）を削除し、ステージング 2 操作とコミットを連続実行に変更する。（3）コミット前判定条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する。（4）フェーズ 2・3 を参照する陳腐化コメント計 7 箇所（Phase 1.1 に列挙）を新定義に整合させる。テスト観測用の production seam・公開 API は追加しない（アーキテクチャ §2.1・§3.1）。 |
| `internal/store/recovery_test.go` | テスト変更・追加 | `rg -n -e resetPhaseDataStaged -e resetPhaseEmailsStaged internal/store` で検出される全参照を対象に、`resetPhaseDataStaged` / `resetPhaseEmailsStaged` をリテラル値 `resetPhase(2)` / `resetPhase(3)` に置き換える。対象テストは下表の 8 件。レガシー値の読み取り互換テスト（AC-05・AC-06）と、フェーズ書き込み列・操作順序の観測テスト（AC-01・AC-02）を新規追加する。 |
| `internal/store/store_test.go` | テスト変更 | `rg -n "resetPhaseEmailsStaged" internal/store/store_test.go` で検出される 2 箇所を `resetPhase(3)` に置き換える。 |
| `internal/store/store.go` | コメント変更 | `HasPendingReset` コメントの "phases 1–3 or 5" を新定義に整合した表現に更新する。 |
| `docs/dev/adr/0003_reset_phase_design.ja.md` | ドキュメント改訂 | アーキテクチャ §7.3 が列挙する ADR-0003 の更新対象に従い、フェーズ 2・3 への言及を新フェーズ定義 `{1, 4, 5}` に整合させる。後方互換の扱いを明記する（AC-08）。 |
| `docs/dev/adr/0003_reset_phase_design.md` | 翻訳反映 | `/mktrans` で日本語版から英語版へ反映する（AC-09）。 |

#### `recovery_test.go` の定数参照テスト一覧

| テスト名 | 参照している廃止定数 | 変更後の構築方法 |
|---|---|---|
| `TestApplyRecovery_RefusesPendingReset` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え（`ApplyRecovery` がフェーズ 2 の in-flight reset を拒否すること、すなわちレガシー値でも保留と判定されることを検証するテストとして維持） |
| `TestResetForRecovery_IdempotentAfterCrashBeforeCommit` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え（レガシー値フェーズ 3 から冪等に収束することを検証するテストとして維持） |
| `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え（変更後はフェーズ 2 が書かれない状況を再現するのではなく、旧バージョンが書いたレガシーフェーズ 2 マニフェストからの収束テストとして維持） |
| `TestAbortReset_CrashDuringCommitRefusesAbort` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え |
| `TestAbortReset_RestoresOldData` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え |
| `TestOpen_CleansUpAfterCommitCrashWindow` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え（C4: センチネル確定済み・マニフェストはレガシーフェーズ 3 の収束確認として維持） |
| `TestOpen_BlockedByPreCommitReset` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え（レガシー値でも `OpenReadWrite` が保留リセットを fail-closed することを維持） |
| `TestResetForRecovery_CommitCrashWindow_ZeroUID` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え（C4 の `currUIDValidity == 0` クリーンアップ経路を維持） |

#### 再利用できる既存資産

- `os.MkdirAll(stagingPath, dirPerm)`・`stageDataFile`・`stageEmailsDir`・`commitReset`・`writeResetManifest`・`validateManifestPhase`・`readResetManifest` の本体処理は変更しない。`advanceResetPhases` 内の中間 `writeResetManifest`（フェーズ 2・3）呼び出しの削除と、呼び出し元の判定条件のみを変更する。
- AC-01・AC-02 はテスト観測用の seam を導入せず、ディスク上のファイル配置と最終状態で検証する（アーキテクチャ §5.2 の設計思想「進捗はファイル配置から導出可能」に沿う）。「フェーズ 2・3 を新規に書かない」ことは、(a) 中間 `writeResetManifest`（フェーズ 2・3）呼び出しを Task 1.2 で削除し、定数を Task 1.1 で削除する（定数削除後は当該呼び出しがコンパイル不能になるため `make build` の成功が呼び出し除去を保証し、Phase 1.3・2.5 の `rg` が定数の不在を確認する）、(b) マニフェストをフェーズ 1 のままにした各ファイル配置状態から `ResetForRecovery` が収束することを確認（Phase 2.3）、の 2 点で担保する。
- `AbortReset` のロジックはフェーズ 2・3 を単一値比較の対象にしておらず、コミット前の範囲として扱う既存の構造が維持される。`AbortReset` がフェーズ 5（aborting）を書く挙動は、既存の `TestAbortReset_ResumesFromAbortingPhase`・`TestResetForRecovery_RefusesAbortingPhase`（フェーズ 5 マニフェストを構築して再開・拒否を検証）で担保され、seam なしで確認できる。

---

## 2. 実装フェーズ

### Phase 1: `internal/store/recovery.go` の実装変更（AC-01・AC-02・AC-04）

- [x] **1.1** フェーズ定数を `{1, 4, 5}` のみに整理する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: 定数 `resetPhaseDataStaged = 2` と `resetPhaseEmailsStaged = 3` の定義を削除する。フェーズ 2・3 を参照する以下 7 箇所のコメントを内容に従って更新する（行番号は現状の `recovery.go`）。
    - 38 行（`resetPhase` 型定義コメント）: "Forward: 1 (manifest_written, WAL) → 2 (data_staged) → 3 (emails_staged) → 4 (committed)." → "Forward: 1 (manifest_written, WAL) → 4 (committed)." に更新する。
    - 486 行（`ResetForRecovery` 関数コメント）: "Drives phases 1→2→3→4→cleanup" → "Drives phases 1→4→cleanup" に更新する。
    - 522 行（`ResetForRecovery` 内インラインコメント）: "A pre-commit manifest (phases 1–3)" → "A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)" に更新する。
    - 596–597 行（`advanceResetPhases` 関数コメント）: "writing a checkpoint manifest after each idempotent file operation." と "See ADR-0003 §3–4 for the WAL/checkpoint pattern and idempotence invariants." を、中間チェックポイントを書かず一括実行する新挙動に合わせて書き換える。
    - 661 行（`AbortReset` 関数コメント）: "Valid for pre-commit phases (1–3)" → "Valid for pre-commit phases (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3)" に更新する。
    - 691–693 行（`AbortReset` 内インラインコメント）: "the commit already happened even if the manifest is still at phase 3." → "...even if the manifest is still pre-commit (phase 1, or legacy 2–3)." に更新する。
    - 755 行（`HasPendingReset` 実装コメント）: "Returns true only for active-phase resets (phases 1–3 or 5)." → "Returns true for any non-committed manifest: pre-commit (phase 1, or legacy 2–3) and aborting (phase 5)." に更新する。
  - 完了基準: 定数定義が削除され、上記 7 箇所のコメントが更新済みであること。`make build` は Phase 1.3 で production 参照を除去した後に実行する。

- [x] **1.2** `advanceResetPhases` を無条件の一括実行に書き換える
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `advanceResetPhases` 内の **3 つの `if phase <= X` ブロック全体を削除し**、`os.MkdirAll`・`stageDataFile`・`stageEmailsDir`・`commitReset` を引数なしで無条件に順番に呼び出す形へ置き換える（アーキテクチャ §2.2 のフローチャート参照）。具体的には次の変換を行う。
    - 削除: `if phase <= resetPhaseManifestWritten { … }` ブロック全体（`MkdirAll`・`stageDataFile`・`writeResetManifest(phase=2)` を含む）
    - 削除: `if phase <= resetPhaseDataStaged { … }` ブロック全体（`stageEmailsDir`・`writeResetManifest(phase=3)` を含む）
    - 削除: `if phase <= resetPhaseEmailsStaged { … }` ブロック全体（`commitReset` を含む）
    - 追加: ガードなしで `MkdirAll` → `stageDataFile` → `stageEmailsDir` → `commitReset` を無条件に呼び出す単一のシーケンス
    - これにより、フェーズ 1 のマニフェストから呼ばれた場合も、レガシー値 2・3 のマニフェストから呼ばれた場合も、常に全操作（ステージング + コミット）を冪等に再実行する。
    - `phase` 引数はブロック削除後に不要になるため、関数シグネチャからも削除し、呼び出し元 `executeResetFromManifest` の呼び出し箇所も合わせて変更する（アーキテクチャ §3.3）。コメント（596–597 行）は Phase 1.1 で更新する。
  - 完了基準: `advanceResetPhases` 関数内に `writeResetManifest` の直接呼び出しがなく（フェーズ確定は `commitReset` 内の 1 回のみ）、`if phase <= X` の条件分岐が存在しないこと。`make build` は Phase 1.3 後（テスト参照除く）に確認する。

- [x] **1.3** コミット前判定条件を範囲式に変更する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `ResetForRecovery` 内の残存マニフェスト検出条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する（アーキテクチャ §3.2・§6.2）。
  - 完了基準: **ステップ 1.1〜1.3 はテストファイル（`recovery_test.go`・`store_test.go`）がまだ削除済み定数を参照するため、単体では `make test` を通せない。`make build`（プロダクションバイナリのみ）は通るが、`make test` は 2.1・2.2 完了まで保留する。** ステップ 2.2 完了後に `rg -n -e resetPhaseEmailsStaged -e resetPhaseDataStaged internal/store` が該当なしになり、`make test` でエラーがないことを確認する。ステップ 1.1〜1.3 は 2.1・2.2 と合わせて PR-1 の一体的なコンパイル単位を形成する。

- [x] **1.4** `store.go` のインターフェースコメントを更新する
  - ファイル: `internal/store/store.go`
  - 作業内容: `Store.HasPendingReset` のインターフェースコメント（99 行）"reports whether an active reset is in progress (phases 1–3 or 5)" を更新する。フェーズ 2・3 を能動的な書き込み対象として記述せず、レガシー値として読み取り互換で扱われる旨が読み取れる表現にする（例: "an active reset is in progress (pre-commit phase 1 or legacy 2–3, or aborting phase 5)"）。これは `recovery.go:755` の実装コメント（Phase 1.1 で対応）とは別の重複コメントである。
  - 完了基準: 更新後のコメントが新フェーズ定義（コミット前は `phase < resetPhaseCommitted`、レガシー値はコミット前として読み取り互換）を正確に反映しており、フェーズ 2・3 を能動的な書き込み対象として記述していないこと。

### Phase 2: テストの更新と新規追加

- [x] **2.1** `recovery_test.go` の廃止定数参照を置き換える（AC-01・AC-02・AC-03・AC-04 の回帰）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 1.3 の表に示した 8 つのテストで `resetPhaseDataStaged` → `resetPhase(2)` に、`resetPhaseEmailsStaged` → `resetPhase(3)` に置き換える。Go ソース内のコメントは英語で更新し、legacy phase manifest からの収束、または legacy values でも pending reset として扱う検証であることを説明する。
  - 完了基準: `rg -n -e resetPhaseDataStaged -e resetPhaseEmailsStaged internal/store/recovery_test.go` が該当なしになり、各テストが `make test` で通ること。

- [x] **2.2** `store_test.go` の廃止定数参照を置き換える（AC-04 の回帰）
  - ファイル: `internal/store/store_test.go`
  - 作業内容: `TestOpen_PendingReset_FailsClosedForReadWrite` および `TestOpen_PendingReset_OpenRecoverResetSucceeds` 内の `resetPhaseEmailsStaged` を `resetPhase(3)` に置き換える。
  - 完了基準: `make test` で 2 件のテストが通ること。

### PR-1 作成ポイント: core phase simplification in store

**対象ステップ**: 1.1 / 1.2 / 1.3 / 1.4 / 2.1 / 2.2

**推奨タイトル**: `chore(store): simplify ResetForRecovery to phase set {1,4,5}`

**レビュー観点**: `advanceResetPhases` が `if phase <= X` 条件分岐を持たず、`MkdirAll`→`stageDataFile`→`stageEmailsDir`→`commitReset` を無条件に呼ぶこと（レガシー値 2・3 でも全操作が実行されること） / 定数削除とコミット前判定の範囲式への変更 / 既存テストの定数リテラル置換が意味を維持していること / `validateManifestPhase` の値域が `[1,5]` のまま変わっていないこと

- [x] `make test && make lint` がグリーンであることを確認した
- [x] PR を作成した
- [x] PR がマージされた
- [x] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

- [x] **2.3** 新実装の C3 クラッシュシナリオテストを追加する（AC-03）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetForRecovery_CrashAfterBothFilesStaged` を新規追加する。`rootDir` にレポートと emails を植え、`stagingPath` に `tlsrpt.json` と `emails/` を移動済みにし、マニフェストを `resetPhaseManifestWritten`（フェーズ 1）で書いた状態から `ResetForRecovery(200)` を呼び出す。空ストア（`tlsrpt.json` と `emails/` が存在しないこと）・新 UIDVALIDITY・`recovery_required` 解消（`HasPendingReset` が `false` を返すこと）・マニフェスト削除・ステージング領域削除へ収束することを確認する。クラッシュ地点の定義はアーキテクチャ §5.2 を参照する。
  - 完了基準: テストが `make test` で通ること。

- [x] **2.3a** 新規リセットの一括遷移を行動で検証する（AC-01・AC-02）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 既存の `TestResetForRecovery_ClearsDataAndSentinel` を強化し、(1) リセット後にストアが空であること（`GetAllReports` が空）、(2) `rootDir` 直下に `tlsrpt.json`・`emails/` が存在しないことのアサートを追加する。両ファイルが root から消えていることは、コミット前に両方がステージングへ退避され（AC-02 の「ステージング 2 操作 → コミット」順序の行動的証跡）、フェーズ 1 から committed・クリーンアップへ収束したこと（AC-01 の 1→4 遷移）を示す。
  - 補足: 「フェーズ 2・3 を新規に書かない」ことの直接観測はテスト用 seam を導入せず、(a) 中間 `writeResetManifest` 呼び出しと定数の削除を `rg` で確認（Phase 1.3・2.5）、(b) マニフェストをフェーズ 1 に固定したまま両ファイルを退避済みにした状態から収束する Phase 2.3 のテスト、で担保する（§1.3 の方針）。
  - 完了基準: 強化したテストが `make test` で通り、リセット後にストアが空で、root にステージング対象ファイルが残らないことを確認すること。

- [x] **2.3b** C4 クラッシュウィンドウとステージング領域境界を検証する（AC-02・AC-03）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 以下 3 つのテストを追加する。いずれも空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除への収束をアサートする。
    1. `TestOpen_CleansUpAfterCommitCrashWindowManifestWritten`: 新設計の C4（センチネル確定済み・マニフェストは `resetPhaseManifestWritten` のまま）を `OpenReadWrite` 経由でクリーンアップする。
    2. `TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID`: 同じ C4 状態を `ResetForRecovery(0)` 経由でクリーンアップする。
    3. `TestResetForRecovery_Phase1MissingStagingDirConverges`: フェーズ 1 マニフェストはあるがステージング領域が存在しない状態から `ResetForRecovery` を実行し、ステージング領域が再作成されて収束する（AC-02 の境界値）。
  - 補足: 既存の phase 3 版（`TestOpen_CleansUpAfterCommitCrashWindow`・`TestResetForRecovery_CommitCrashWindow_ZeroUID`）は Phase 2.1 で `resetPhase(3)` に置換し、レガシー値の読み取り互換テストとして維持する。
  - 完了基準: 3 つのテストが `make test` で通ること。

- [x] **2.3c** フェーズ 4・5 の永続数値と aborting マーカーを検証する（AC-04）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetPhasePersistedNumericValues` を追加し、`resetPhaseCommitted == resetPhase(4)` と `resetPhaseAborting == resetPhase(5)` をリテラル値でアサートする（再採番を検出する）。`AbortReset` がフェーズ 5 を書いてから中断復元へ進む挙動は、既存の `TestAbortReset_ResumesFromAbortingPhase`（フェーズ 5 マニフェストから復元を再開）と `TestResetForRecovery_RefusesAbortingPhase`（フェーズ 5 で `ResetForRecovery` が `ErrResetAbortInProgress` を返す）が seam なしで担保するため、新規の seam テストは追加しない。
  - 完了基準: `TestResetPhasePersistedNumericValues` が `make test` で通り、フェーズ 4・5 の再採番がテストで検出されること。

- [x] **2.4** レガシー値の読み取り互換テストを追加する（AC-05・AC-06）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容:
    - AC-05: `validateManifestPhase` のテーブル駆動テスト `TestValidateManifestPhaseRange` を追加する。受理値は `1, 2, 3, 4, 5`、拒否値は境界値 `0, 6` と既存の代表値 `99` とし、レガシー値 2・3 が拒否されないことと値域境界を同時に確認する。
    - AC-05: Phase 2.1 で `resetPhase(2)` に置き換える `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate` を強化する。フェーズ 2 のレガシーマニフェスト（ステート: `tlsrpt.json` がステージングに移動済み・`emails/` は rootDir に残存）から `ResetForRecovery` を実行し、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除をアサートする。
    - AC-05: Phase 2.1 で `resetPhase(3)` に置き換える `TestResetForRecovery_IdempotentAfterCrashBeforeCommit` を強化する。フェーズ 3 のレガシーマニフェスト、退避済み `tlsrpt.json`、退避済み `emails/`、旧 `recovery_required` を用意して `ResetForRecovery` を呼び出し、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除をアサートする。
    - AC-06: テーブル駆動テスト `TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` を追加する。フェーズ 2・3 のレガシーマニフェスト（`CurrUIDValidity` が現在の `recovery_required` の値と不一致）を持つストアで `ResetForRecovery` を呼び出したとき、残存マニフェストと判定されて（a）旧マニフェストが削除されること、（b）最終的に空ストア・新 UIDVALIDITY・`recovery_required` 解消へ収束することを確認する（旧マニフェストの削除は最終状態でマニフェストが存在しないことで確認する）。
  - 完了基準: 追加したテストが `make test` で通ること。

- [x] **2.5** `make fmt && make lint && make test` を通す
  - 完了基準: `rg -n -e resetPhaseDataStaged -e resetPhaseEmailsStaged internal/store` が該当なしになり、`make fmt`・`make lint`・`make test`・`go test -race -tags test ./internal/store` がいずれもエラーなく完了すること。

### PR-2 作成ポイント: crash-safety and legacy-compat tests

**対象ステップ**: 2.3 / 2.3a / 2.3b / 2.3c / 2.4 / 2.5

**推奨タイトル**: `test(store): add crash-safety and legacy-compat tests for phase set {1,4,5}`

**レビュー観点**: フェーズ 1 固定マニフェストから C3 クラッシュが収束すること / 新設計 C4 クラッシュウィンドウの収束確認 / レガシー値 2・3 マニフェストがコミット前として収束すること / `go test -race` が通ること

- [x] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

### Phase 3: ADR-0003 の改訂（AC-07・AC-08・AC-09）

- [ ] **3.1** ADR-0003 日本語版を新フェーズ定義に整合させる
  - ファイル: `docs/dev/adr/0003_reset_phase_design.ja.md`
  - 作業内容: アーキテクチャ §7.3 の対象範囲に従って ADR を改訂する。計画書には設計詳細を再掲せず、各節の更新作業だけを実施する。後方互換の正規化方針では「レガシー値 2・3 をコミット前として解釈する」ことを明記する（AC-08）。
  - 完了基準: `rg -n -e "フェーズ ?2" -e "フェーズ ?3" -e "phase ?2" -e "phase ?3" -e data_staged -e emails_staged -e resetPhaseDataStaged -e resetPhaseEmailsStaged -e チェックポイント -e checkpoint -e "1〜3" -e "1–3" -e "1-3" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` を実行し、能動的なフェーズ 2・3 書き込み説明が残っていないことを確認する。レガシー値の読み取り互換説明として残るヒットは、確認結果に理由を添えて残す。

- [ ] **3.2** 英語版 ADR を `/mktrans` で反映する
  - ファイル: `docs/dev/adr/0003_reset_phase_design.md`
  - 作業内容: `/mktrans` コマンドを使い、日本語版の変更内容を英語版に反映する。CLAUDE.md の翻訳規約に従い、日本語版を原本として英語版に適用する。
  - 完了基準: 日英 ADR の見出し一覧を比較する（例: `rg -n "^#{1,4} " docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md`）。見出し構造が対応し、英語版にも AC-08 のレガシー値説明が反映されていることを確認する。

- [ ] **3.3** ADR 改訂の検証結果を記録する（AC-07・AC-08・AC-09）
  - ファイル: `docs/tasks/0080_reset_phase_simplify/03_implementation_plan.md`
  - 作業内容: Phase 3.1 と 3.2 の確認コマンド、残存ヒットの理由、日英見出し比較の結果を「受け入れ条件トレーサビリティ」セクションへ追記する。
  - 完了基準: AC-07・AC-08・AC-09 が「目視確認」だけでなく、実行した確認コマンドと確認観点に紐づいていること。

### PR-3 作成ポイント: ADR-0003 update for phase simplification

**対象ステップ**: 3.1 / 3.2 / 3.3

**推奨タイトル**: `docs(adr): update reset phase design for simplified phase set {1,4,5}`

**レビュー観点**: フェーズ 2・3 の能動的書き込み説明が ADR から削除されていること / レガシー値の読み取り互換方針（コミット前として解釈）が明記されていること / 日英両版の見出し構造が対応していること

- [ ] `make test && make lint` がグリーンであることを確認した
- [ ] PR を作成した
- [ ] PR がマージされた
- [ ] 次のブランチへ切り替えた（次ステップは新しいブランチで作業する）

---

## 3. 受け入れ条件トレーサビリティ

| AC | 実装箇所 | 検証タスク | 検証方法 |
|---|---|---|---|
| AC-01 | `internal/store/recovery.go::advanceResetPhases`、`commitReset` | `TestResetForRecovery_ClearsDataAndSentinel`（強化, Phase 2.3a）、`TestResetForRecovery_CrashAfterBothFilesStaged`（新規, Phase 2.3） | 新規リセットが空ストア・committed・クリーンアップへ収束（1→4 遷移）すること、マニフェストをフェーズ 1 に固定したまま両ファイル退避済みの状態からも収束することを確認する。「フェーズ 2・3 を新規に書かない」ことは Task 1.2 の cascade 削除＋`make build`（PR-1 で担保済み）および Phase 2.5 の `rg`（PR-2 で最終確認。定数は PR-1 時点で既に不在なため PR-2 でも必ず通る）で構造的に担保する（seam は導入しない）。 |
| AC-02 | `internal/store/recovery.go::advanceResetPhases`、ステージング領域確保、`stageDataFile`、`stageEmailsDir`、`commitReset` | `TestResetForRecovery_ClearsDataAndSentinel`（強化, Phase 2.3a）、`TestResetForRecovery_Phase1MissingStagingDirConverges`（新規, Phase 2.3b）、`TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`（既存）、`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`（強化, Phase 2.4） | 両ステージング操作がコミット前に完了する（リセット後に root から両ファイルが消える）こと、ステージング領域欠落時も再作成され収束すること、対象ファイル不在時にステージング操作が no-op で収束すること（absent `tlsrpt.json`・absent `emails/`）を確認する。 |
| AC-03 | `internal/store/recovery.go::ResetForRecovery`、`advanceResetPhases`、`cleanupCompletedReset` | Primary（新設計のファイル配置モデル）: `TestResetForRecovery_CrashAtPhaseManifestWritten`（既存）、`TestResetForRecovery_CrashAfterBothFilesStaged`（新規, Phase 2.3）、`TestOpen_CleansUpAfterCommitCrashWindowManifestWritten`（新規, Phase 2.3b）、`TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID`（新規, Phase 2.3b）。Compatibility（レガシー値の収束）: `TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`（既存）、`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`（強化, Phase 2.4）、`TestResetForRecovery_IdempotentAfterCrashBeforeCommit`（強化, Phase 2.4） | 各ファイル配置状態からの再実行が空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・（該当時）ステージング削除へ収束することをアサートする。 |
| AC-04 | `internal/store/recovery.go` の `resetPhaseCommitted`・`resetPhaseAborting`・`commitReset`・`AbortReset` | `TestResetPhasePersistedNumericValues`（新規, Phase 2.3c）、`TestAbortReset_ResumesFromAbortingPhase`（既存）、`TestResetForRecovery_RefusesAbortingPhase`（既存）、`TestAbortReset_AfterCommit`（既存）、`internal/store/store_test.go::TestOpen_PendingReset_FailsClosedForReadWrite`（Phase 2.2 で定数をリテラル置換） | `resetPhaseCommitted == resetPhase(4)`・`resetPhaseAborting == resetPhase(5)` をリテラルでアサート（再採番検出）。`AbortReset` が phase 5 を書いて honor する挙動と phase 4 の committed 挙動が既存テストで通ることを確認する。 |
| AC-05 | `internal/store/recovery.go::validateManifestPhase`、`ResetForRecovery`、`advanceResetPhases` | `internal/store/recovery_test.go::TestValidateManifestPhaseRange`、`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`（strengthened）、`TestResetForRecovery_IdempotentAfterCrashBeforeCommit`（strengthened） | `1..5` が受理され `0`・`6`・`99` が拒否されること、レガシー値 2・3 のマニフェストがコミット前として収束することを確認する。 |
| AC-06 | `internal/store/recovery.go::ResetForRecovery` の残存マニフェスト検出条件 | `internal/store/recovery_test.go::TestResetForRecovery_LegacyPreCommitStaleManifestRestarts` | phase 2/3 と `CurrUIDValidity` 不一致のテーブル駆動テストで、旧マニフェスト削除、新規リセット開始、最終収束を確認する。 |
| AC-07 | `docs/dev/adr/0003_reset_phase_design.ja.md`、`docs/dev/adr/0003_reset_phase_design.md` | Phase 3.1・3.3 | `rg -n -e "フェーズ ?2" -e "フェーズ ?3" -e "phase ?2" -e "phase ?3" -e data_staged -e emails_staged -e resetPhaseDataStaged -e resetPhaseEmailsStaged -e チェックポイント -e checkpoint -e "1〜3" -e "1–3" -e "1-3" docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` の結果を記録し、能動的なフェーズ 2・3 書き込み説明が残っていないことを確認する。 |
| AC-08 | `docs/dev/adr/0003_reset_phase_design.ja.md`、`docs/dev/adr/0003_reset_phase_design.md` | Phase 3.1・3.2・3.3 | `rg -n -e レガシー値 -e legacy -e コミット前 -e pre-commit docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` で日英両方に読み取り互換方針があることを確認する。 |
| AC-09 | `docs/dev/adr/0003_reset_phase_design.md` | Phase 3.2・3.3 | `/mktrans` 実行後、`rg -n "^#{1,4} " docs/dev/adr/0003_reset_phase_design.ja.md docs/dev/adr/0003_reset_phase_design.md` で見出し構造を比較し、差分が翻訳上の表記差に限られることを確認する。 |

---

## 4. 実装順序とマイルストーン

| マイルストーン | 内容 | 含むフェーズ |
|---|---|---|
| M1 | コア実装 | Phase 1 |
| M2 | テスト整合・全テスト通過 | Phase 2 |
| M3 | ドキュメント改訂 | Phase 3 |

Phase 1 と Phase 2 は密接に依存するため、1.1→1.2→1.3→1.4→2.1→2.2→2.3→2.3a→2.3b→2.3c→2.4→2.5 の順で連続して完結させる。Phase 3 はコードの動作確認が完了した後に 3.1→3.2→3.3 の順で実施する。

### PR 構成

| PR | 対象ステップ | 主な変更内容 |
|---|---|---|
| PR-1 | 1.1 / 1.2 / 1.3 / 1.4 / 2.1 / 2.2 | `recovery.go` の定数削除・中間書き込み削除・範囲式変更・コメント更新、既存テストの定数リテラル置換 |
| PR-2 | 2.3 / 2.3a / 2.3b / 2.3c / 2.4 / 2.5 | 新設計の C3/C4 クラッシュシナリオテスト・一括遷移検証・レガシー値読み取り互換テストの追加 |
| PR-3 | 3.1 / 3.2 / 3.3 | ADR-0003 の日英両版をフェーズ定義 `{1, 4, 5}` に整合させる |

---

## 5. テスト戦略

### 単体テスト

- **一括遷移（フェーズ 2・3 を書かない）**（AC-01）: `TestResetForRecovery_ClearsDataAndSentinel`（強化, Phase 2.3a）が新規リセットの 1→4 収束を行動で確認する。マニフェストをフェーズ 1 に固定したまま両ファイル退避済みの `TestResetForRecovery_CrashAfterBothFilesStaged`（Phase 2.3）が、中間チェックポイントなしで収束することを示す。「2・3 を書かない」ことの構造的担保は Task 1.2 の cascade 削除＋`make build`（PR-1 で確立）と Phase 2.5 の `rg`（PR-2 で最終確認。PR-1 時点で定数は既に不在のため必ず通る）で行う（テスト用 seam は導入しない）。
- **ステージング順序・冪等**（AC-02）: `TestResetForRecovery_ClearsDataAndSentinel`（強化）がリセット後に root から両ファイルが消えること（=コミット前に両方退避済み）を確認する。`TestResetForRecovery_Phase1MissingStagingDirConverges`（Phase 2.3b）がステージング領域欠落時の境界値を、`TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`（既存）・`...CrashAfterStageEmailsBeforeManifestUpdate`（強化）が対象ファイル不在時の no-op 冪等を確認する。
- **クラッシュ収束**（AC-03）: ファイル配置で表現される C1・C2・C3・C4 状態からの再実行 → 空ストア収束。Phase 2.1 で更新する既存テスト、Phase 2.3 の C3 テスト、Phase 2.3b の新設計 C4 テストが担う。
- **レガシー値の読み取り互換**（AC-05・AC-06）: Phase 2.1 で更新する既存テストを強化し、Phase 2.4 で境界値と残存マニフェストのテーブル駆動テストを追加する。フェーズ 2・3 のリテラルマニフェストを JSON で直接組み立てるか `resetPhase(2)` / `resetPhase(3)` で構築する。
- **フェーズ 4・5 不変**（AC-04）: `TestResetPhasePersistedNumericValues` のリテラル数値アサーション（4・5 の再採番検出）と、既存の aborting フェーズテスト（`TestAbortReset_ResumesFromAbortingPhase`・`TestResetForRecovery_RefusesAbortingPhase`）・committed テスト（`TestAbortReset_AfterCommit`）・Phase 2.2 で更新する `store_test.go` で担保する。
- **race 確認**: `go test -race -tags test ./internal/store` を Phase 2.5 で実行し、テスト追加によるデータ競合がないことを確認する。

### 統合テスト

- `cmd/tlsrpt-digest/recover_test.go` は CLI が `ResetForRecovery` を正しい条件で呼び出すことを確認するファサード回帰として実行する。公開 API・CLI フローは変更しないため、同ファイルは原則変更しない。
- ファイル配置、センチネル更新、マニフェスト削除の主証跡は `internal/store/recovery_test.go` のファイルストア実体テストで確認する。CLI テストは AC の主証跡ではなく、コマンド層が既存の呼び出し契約を保つことの回帰確認として扱う。

### 読み取り互換テスト

- レガシー値 2・3 のマニフェストは、旧バージョンが残した永続データとして `internal/store/recovery_test.go` で明示的に構築する。Phase 2.4 の phase 2/3 収束テストと残存マニフェストテーブル駆動テストが後方互換性の証跡になる。

### テストヘルパー

既存の `openRecoverResetStore`・`writeResetManifest`・`makeFullReport` を再利用する。新規テストヘルパーファイルは不要。レガシー値のマニフェストはテスト関数内でリテラル値または `resetPhase(2)` / `resetPhase(3)` を使って構築する。

### テストデータ

人工的なフィクスチャはすべてテスト関数内にインラインで記述する（`testdata/` への追加不要）。

---

## 6. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| `AbortReset` 内のコミット前判定に `resetPhaseEmailsStaged` への暗黙的な依存がある場合 | `AbortReset` が誤動作する | Phase 1.3 までに production 参照をすべて解消し、`make build` でコンパイルエラーがないことを確認してから Phase 2 へ進む |
| `cleanupCompletedReset` のシナリオ表（§5）が廃止定数を参照している場合 | ADR 改訂漏れ | Phase 3.1 で §5 のシナリオ表も明示的に確認する |
| 既存の 8 つの定数参照テストが、定数削除後に想定と異なる挙動を検証しているケース | テストが false positive になる | Phase 2.1 の置き換えと同時に、Go ソース内コメントは英語で更新し、legacy phase manifest convergence または pending reset handling for legacy values としてテスト意図を明示する |
| `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate` はテスト名が「StageEmails」だがマニフェストはフェーズ 2（data_staged）で構築される | 実装者が誤って `resetPhase(3)` に「修正」する | Phase 2.1 で `resetPhase(2)` に置換する際、名称はクラッシュ地点・フェーズ値は直前チェックポイントを指す点をコメントで明示する |
| ADR 日英同期に想定以上の時間がかかる場合 | Phase 3 が遅延する | コード変更とテスト通過を先に完了させ、ADR 翻訳は日本語版の節構造確定後にまとめて実行する |

---

## 7. 実装チェックリスト

- [ ] PR-1 マージ済み（対象ステップ: 1.1 / 1.2 / 1.3 / 1.4 / 2.1 / 2.2）
- [ ] PR-2 マージ済み（対象ステップ: 2.3 / 2.3a / 2.3b / 2.3c / 2.4 / 2.5）
- [ ] PR-3 マージ済み（対象ステップ: 3.1 / 3.2 / 3.3）
- [ ] `make deadcode` が本タスク起因の未使用コードを報告しない
- [ ] `cmd/tlsrpt-digest/recover_test.go` のテストが引き続き通ること（ファサード変更なし・回帰確認）

---

## 8. 完了基準

- `resetPhaseDataStaged` / `resetPhaseEmailsStaged` の定数がコードに残っていないこと
- 新規リセットでフェーズ 2・3 が書かれないことが、定数・中間 `writeResetManifest` 呼び出しの不在（`rg`）と、フェーズ 1 固定で両ファイル退避済みの状態からの収束テスト（`TestResetForRecovery_CrashAfterBothFilesStaged`）で担保されていること
- `TestResetForRecovery_ClearsDataAndSentinel`（強化）が、リセット後にストアが空で root にステージング対象ファイルが残らないことを確認していること
- `TestResetForRecovery_Phase1MissingStagingDirConverges` が、ステージング領域欠落時にも収束することを確認していること
- コミット前判定が `mfst.Phase < resetPhaseCommitted` の範囲式で表現されていること
- 全 AC（AC-01〜AC-09）に対応するテスト、確認コマンド、または検証結果の記録が「受け入れ条件トレーサビリティ」に紐づいていること
- `make fmt`・`make lint`・`make test` がすべて通ること
- ADR-0003 の日本語版・英語版について、Phase 3.1〜3.3 の確認コマンド結果が新フェーズ定義との整合を示していること

---

## 9. 次のステップ

- 実装完了後、PR を作成する。
- 将来ステージング対象が増えた場合は、`stageXxx` 冪等関数の追加のみで対応できる。チェックポイントフェーズの追加は不要（アーキテクチャ §9）。
