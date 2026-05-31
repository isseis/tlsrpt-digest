# 実装計画書：ResetForRecovery チェックポイントフェーズの簡略化

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-31 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 実装の全体像

### 1.1 目的

`resetPhase` のフェーズ 2（`resetPhaseDataStaged`）・フェーズ 3（`resetPhaseEmailsStaged`）を廃止し、`advanceResetPhases` をコミット前から一括実行へ簡略化する。設計の詳細は [02_architecture.ja.md](02_architecture.ja.md) を参照。

### 1.2 実装原則

- `internal/store/recovery.go` の変更に閉じる。新しいパッケージ・型・公開 API は追加しない（アーキテクチャ §2.1）。
- フェーズ 4・5 の数値・意味・役割は変更しない（AC-04）。
- コミット前判定は `phase < resetPhaseCommitted`（`[1, 4)`）の範囲式で表現し、値 2・3 を名指ししない（アーキテクチャ §3.2）。
- 既存テストは削除するのではなく、レガシー値のリテラル（`resetPhase(2)` / `resetPhase(3)`）で構築し直して意味を維持する（アーキテクチャ §3.4）。

### 1.3 既存コード調査結果

#### 変更が必要なコード

| ファイル | 変更種別 | 変更箇所 |
|---|---|---|
| `internal/store/recovery.go` | 実装変更 | （1）定数 `resetPhaseDataStaged=2`・`resetPhaseEmailsStaged=3` を削除する。（2）`advanceResetPhases` 内の中間チェックポイント書き込み（フェーズ 2・3 の `writeResetManifest` 呼び出し）を削除し、ステージング 2 操作とコミットを連続実行に変更する。（3）コミット前判定条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する。（4）関数コメント（"Forward: 1 → 2 → 3 → 4"）・型定義コメントを新定義に整合させる。（5）公開 API を増やさず、テストからマニフェスト書き込みフェーズと `advanceResetPhases` 内の操作順序を観測できる package-private の差し替え点を追加する。 |
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

- `os.MkdirAll(stagingPath, dirPerm)`・`stageDataFile`・`stageEmailsDir`・`commitReset` の本体処理は変更しない。AC-02 の順序検証のため、`advanceResetPhases` から呼ぶ関数を package-private 変数経由にするか、同等にテストから呼び出し順序を観測できる最小の差し替え点を置く。
- `writeResetManifest` の本体処理は変更しない。AC-01 の書き込み列検証のため、呼び出し元から使う package-private 変数を追加し、テストでフェーズ列を記録できるようにする。`validateManifestPhase`・`readResetManifest` は変更しない。
- `AbortReset` のロジックはフェーズ 2・3 を単一値比較の対象にしておらず、コミット前の範囲として扱う既存の構造が維持される。

---

## 2. 実装フェーズ

### Phase 1: `internal/store/recovery.go` の実装変更（AC-01・AC-02・AC-04）

- [ ] **1.1** フェーズ定数を `{1, 4, 5}` のみに整理する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: 定数 `resetPhaseDataStaged = 2` と `resetPhaseEmailsStaged = 3` の定義を削除する。以下 4 箇所のコメントを内容に従って更新する。
    - `resetPhase` 型定義のコメント: "Forward: 1 → 2 → 3 → 4" → "Forward: 1 → 4" に更新する。
    - `ResetForRecovery` の関数コメント: "Drives phases 1→2→3→4→cleanup" → "Drives phases 1→4→cleanup" に更新する。
    - `ResetForRecovery` 内インラインコメント: "A pre-commit manifest (phases 1–3)" → "A pre-commit manifest (phase < resetPhaseCommitted, or legacy phases 2–3)" に更新する。
    - `AbortReset` の関数コメント: "Valid for pre-commit phases (1–3)" → "Valid for pre-commit phases (phase < resetPhaseCommitted, or legacy phases 2–3)" に更新する。
  - 完了基準: 定数定義とコメントが更新済みであること。`make build` は Phase 1.3 で production 参照を除去した後に実行する。

- [ ] **1.2** `advanceResetPhases` から中間チェックポイント書き込みを削除する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `advanceResetPhases` 内で、`stageDataFile` 後の `writeResetManifest(phase=2)` 呼び出しと `stageEmailsDir` 後の `writeResetManifest(phase=3)` 呼び出しを削除する。フェーズ 1（コミット前）から、ステージング領域確保 → `stageDataFile` → `stageEmailsDir` → `commitReset` を順に実行するフローにする（アーキテクチャ §2.2）。関数コメントを更新する。
  - 完了基準: 関数内に `writeResetManifest` の呼び出しがなく（`commitReset` 内の書き込みは除く）、Phase 1.2a の差し替え点経由で `commitReset` が呼ばれること。

- [ ] **1.2a** フェーズ書き込み列と操作順序をテストから観測できるようにする
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `writeResetManifest`、ステージング領域確保、`stageDataFile`、`stageEmailsDir`、`commitReset` の本体は維持し、呼び出し側だけを unexported の hook 構造体または package-private 関数変数経由にする。これはフェーズ 2・3 が「書かれない」ことと操作順序をファイル最終状態だけでは証明できないための最小限の production seam とする。公開 API・永続フォーマットは変えず、production code から再代入しない。差し替えるテストでは `t.Parallel` を使わず、`t.Cleanup` で必ず元に戻す。
  - 完了基準: 公開 API・永続フォーマット・本体処理を増やさず、Phase 2.3a のテストでフェーズ書き込み列と `advanceResetPhases` の操作順序を記録できること。

- [ ] **1.3** コミット前判定条件を範囲式に変更する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `ResetForRecovery` 内の残存マニフェスト検出条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する（アーキテクチャ §3.2・§6.2）。
  - 完了基準: `rg -n -e resetPhaseEmailsStaged -e resetPhaseDataStaged internal/store/recovery.go internal/store/store.go` が該当なしになり、`make build` でコンパイルエラーが出ないこと（テスト参照は Phase 2.1・2.2 で解消する）。

- [ ] **1.4** `store.go` のコメントを更新する
  - ファイル: `internal/store/store.go`
  - 作業内容: `HasPendingReset` のコメント "phases 1–3 or 5" を、フェーズ 2・3 を能動的な書き込み対象として記述せず、レガシー値として読み取り互換で扱われる旨が読み取れる表現に更新する（例: "pre-commit phases (1, or legacy 2–3) and aborting phase (5)"）。
  - 完了基準: 更新後のコメントが新フェーズ定義（コミット前は `phase < resetPhaseCommitted`、レガシー値はコミット前として読み取り互換）を正確に反映しており、フェーズ 2・3 を能動的な書き込み対象として記述していないこと。

### Phase 2: テストの更新と新規追加

- [ ] **2.1** `recovery_test.go` の廃止定数参照を置き換える（AC-01・AC-02・AC-03・AC-04 の回帰）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 1.3 の表に示した 8 つのテストで `resetPhaseDataStaged` → `resetPhase(2)` に、`resetPhaseEmailsStaged` → `resetPhase(3)` に置き換える。Go ソース内のコメントは英語で更新し、legacy phase manifest からの収束、または legacy values でも pending reset として扱う検証であることを説明する。
  - 完了基準: `rg -n -e resetPhaseDataStaged -e resetPhaseEmailsStaged internal/store/recovery_test.go` が該当なしになり、各テストが `make test` で通ること。

- [ ] **2.2** `store_test.go` の廃止定数参照を置き換える（AC-04 の回帰）
  - ファイル: `internal/store/store_test.go`
  - 作業内容: `TestOpen_PendingReset_FailsClosedForReadWrite` および `TestOpen_PendingReset_OpenRecoverResetSucceeds` 内の `resetPhaseEmailsStaged` を `resetPhase(3)` に置き換える。
  - 完了基準: `make test` で 2 件のテストが通ること。

- [ ] **2.3** 新実装の C3 クラッシュシナリオテストを追加する（AC-03）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetForRecovery_CrashAfterBothFilesStaged` を新規追加する。`rootDir` にレポートと emails を植え、`stagingPath` に `tlsrpt.json` と `emails/` を移動済みにし、マニフェストを `resetPhaseManifestWritten`（フェーズ 1）で書いた状態から `ResetForRecovery(200)` を呼び出す。空ストア（`tlsrpt.json` と `emails/` が存在しないこと）・新 UIDVALIDITY・`recovery_required` 解消（`HasPendingReset` が `false` を返すこと）・マニフェスト削除・ステージング領域削除へ収束することを確認する。クラッシュ地点の定義はアーキテクチャ §5.2 を参照する。
  - 完了基準: テストが `make test` で通ること。

- [ ] **2.3a** フェーズ書き込み列と操作順序を検証する（AC-01・AC-02）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetForRecovery_WritesOnlyManifestAndCommittedPhases` を追加し、Phase 1.2a の差し替え点で `writeResetManifest` が受け取る `Phase` を記録する。新規リセット実行時の書き込み列が `[resetPhaseManifestWritten, resetPhaseCommitted]` だけで、`resetPhase(2)`・`resetPhase(3)` が現れないことをアサートする。併せて `TestAdvanceResetPhases_RunsStagesBeforeCommit` を追加し、差し替え点でステージング領域確保 → `stageDataFile` → `stageEmailsDir` → `commitReset` の呼び出し順序を記録して完全一致をアサートする。
  - 完了基準: 2 つのテストが `make test` で通り、AC-01 はフェーズ列、AC-02 は呼び出し順序で検証されること。

- [ ] **2.3b** C4 クラッシュウィンドウとステージング領域境界を検証する（AC-02・AC-03）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 新設計の C4（センチネル確定済み・マニフェストは `resetPhaseManifestWritten` のまま）を明示する `TestOpen_CleansUpAfterCommitCrashWindowManifestWritten` と `TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID` を追加する。それぞれ `OpenReadWrite` 経由のクリーンアップと `ResetForRecovery(0)` 経由のクリーンアップが、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除へ収束することを確認する。既存の phase 3 版はレガシー値の読み取り互換テストとして維持する。
  - 作業内容: `TestResetForRecovery_Phase1MissingStagingDirConverges` を追加し、フェーズ 1 マニフェストがあるがステージング領域が存在しない状態から `ResetForRecovery` を実行しても、ステージング領域確保が先に行われ、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除へ収束することを確認する。
  - 完了基準: 3 つのテストが `make test` で通ること。

- [ ] **2.3c** フェーズ 4・5 の永続数値をリテラルで検証する（AC-04）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetPhasePersistedNumericValues` を追加し、`resetPhaseCommitted == resetPhase(4)` と `resetPhaseAborting == resetPhase(5)` をリテラル値でアサートする。
  - 作業内容: `TestAbortReset_WritesAbortingPhase` を追加し、Phase 1.2a の書き込み差し替え点で `AbortReset` が `resetPhaseAborting`（永続値 5）を書いてから中断復元へ進むことを確認する。
  - 完了基準: 2 つのテストが `make test` で通り、フェーズ 4・5 の再採番と `AbortReset` の phase 5 書き込み漏れがテストで検出されること。

- [ ] **2.4** レガシー値の読み取り互換テストを追加する（AC-05・AC-06）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容:
    - AC-05: `validateManifestPhase` のテーブル駆動テストを追加する。受理値は `1, 2, 3, 4, 5`、拒否値は境界値 `0, 6` と既存の代表値 `99` とし、レガシー値 2・3 が拒否されないことと値域境界を同時に確認する。
    - AC-05: Phase 2.1 で `resetPhase(2)` に置き換える `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate` を強化する。フェーズ 2 のレガシーマニフェスト（ステート: `tlsrpt.json` がステージングに移動済み・`emails/` は rootDir に残存）から `ResetForRecovery` を実行し、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除をアサートする。
    - AC-05: Phase 2.1 で `resetPhase(3)` に置き換える `TestResetForRecovery_IdempotentAfterCrashBeforeCommit` を強化する。フェーズ 3 のレガシーマニフェスト、退避済み `tlsrpt.json`、退避済み `emails/`、旧 `recovery_required` を用意して `ResetForRecovery` を呼び出し、空ストア・新 UIDVALIDITY・`recovery_required` 解消・マニフェスト削除・ステージング領域削除をアサートする。
    - AC-06: フェーズ 2・3 のレガシーマニフェスト（`CurrUIDValidity` が現在の `recovery_required` の値と不一致）を持つストアで `ResetForRecovery` を呼び出したとき、残存マニフェストと判定されて（a）旧マニフェストが削除されること、（b）最終的に空ストア・新 UIDVALIDITY・`recovery_required` 解消へ収束することを確認するテーブル駆動テストを追加する（旧マニフェストの削除は最終状態でマニフェストが存在しないことで確認する）。
  - 完了基準: 追加したテストが `make test` で通ること。

- [ ] **2.5** `make fmt && make lint && make test` を通す
  - 完了基準: `rg -n -e resetPhaseDataStaged -e resetPhaseEmailsStaged internal/store` が該当なしになり、`make fmt`・`make lint`・`make test`・`go test -race ./internal/store` がいずれもエラーなく完了すること。

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

---

## 3. 受け入れ条件トレーサビリティ

| AC | 実装箇所 | 検証タスク | 検証方法 |
|---|---|---|---|
| AC-01 | `internal/store/recovery.go::initResetManifest`、`advanceResetPhases`、`commitReset` | `internal/store/recovery_test.go::TestResetForRecovery_WritesOnlyManifestAndCommittedPhases`（Phase 2.3a） | package-private の書き込み差し替え点で `writeResetManifest` の `Phase` 列を記録し、`[1, 4]` の完全一致と `2`・`3` が一度も書かれないことをアサートする。 |
| AC-02 | `internal/store/recovery.go::advanceResetPhases`、ステージング領域確保、`stageDataFile`、`stageEmailsDir`、`commitReset` | `internal/store/recovery_test.go::TestAdvanceResetPhases_RunsStagesBeforeCommit`（Phase 2.3a）、`TestResetForRecovery_Phase1MissingStagingDirConverges`（Phase 2.3b） | 差し替え点で呼び出し列を記録し、ステージング領域確保 → `stageDataFile` → `stageEmailsDir` → `commitReset` の完全一致をアサートする。ステージング領域が欠けていても再作成され収束することを確認する。 |
| AC-03 | `internal/store/recovery.go::ResetForRecovery`、`advanceResetPhases`、`cleanupCompletedReset` | Primary: `TestResetForRecovery_CrashAtPhaseManifestWritten`、`TestResetForRecovery_CrashAfterBothFilesStaged`、`TestOpen_CleansUpAfterCommitCrashWindowManifestWritten`、`TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID`。Compatibility regression: `TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`、`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`、`TestResetForRecovery_IdempotentAfterCrashBeforeCommit` | Primary tests confirm the new C1・C3・C4 file-placement model. Compatibility tests confirm legacy phase 2/3 manifests still converge. All convergence tests assert empty store, new UIDVALIDITY, `recovery_required` cleared, manifest removed, and staging removed where applicable. |
| AC-04 | `internal/store/recovery.go` の `resetPhaseCommitted`・`resetPhaseAborting`・`commitReset`・`AbortReset` | `internal/store/recovery_test.go::TestResetPhasePersistedNumericValues`（Phase 2.3c）、`TestAbortReset_WritesAbortingPhase`（Phase 2.3c）、`TestAbortReset_AfterCommit`、`TestAbortReset_NoPendingReset`、`TestAbortReset_CrashDuringCommitRefusesAbort`、`internal/store/store_test.go::TestOpen_PendingReset_FailsClosedForReadWrite` | `resetPhaseCommitted == resetPhase(4)`、`resetPhaseAborting == resetPhase(5)` をリテラルでアサートし、`AbortReset` が phase 5 を書くことと phase 4/5 の既存挙動が通ることを確認する。 |
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

Phase 1 と Phase 2 は密接に依存するため、1.1→1.2→1.2a→1.3→2.1→2.2→2.3→2.3a→2.3b→2.3c→2.4→2.5 の順で連続して完結させる。Phase 3 はコードの動作確認が完了した後に 3.1→3.2→3.3 の順で実施する。

---

## 5. テスト戦略

### 単体テスト

- **フェーズ書き込み列**（AC-01）: `TestResetForRecovery_WritesOnlyManifestAndCommittedPhases`（Phase 2.3a）が、新規リセットのマニフェスト書き込み列を `[1, 4]` として直接確認し、レガシー値 2・3 が新規に書かれないことを検証する。
- **操作順序**（AC-02）: `TestAdvanceResetPhases_RunsStagesBeforeCommit`（Phase 2.3a）が、`advanceResetPhases` の呼び出し列「ステージング領域確保 → `stageDataFile` → `stageEmailsDir` → `commitReset`」を直接確認する。`TestResetForRecovery_Phase1MissingStagingDirConverges`（Phase 2.3b）がステージング領域欠落時の境界値を確認する。
- **クラッシュ収束**（AC-03）: ファイル配置で表現される C1・C2・C3・C4 状態からの再実行 → 空ストア収束。Phase 2.1 で更新する既存テスト、Phase 2.3 の C3 テスト、Phase 2.3b の新設計 C4 テストが担う。
- **レガシー値の読み取り互換**（AC-05・AC-06）: Phase 2.1 で更新する既存テストを強化し、Phase 2.4 で境界値と残存マニフェストのテーブル駆動テストを追加する。フェーズ 2・3 のリテラルマニフェストを JSON で直接組み立てるか `resetPhase(2)` / `resetPhase(3)` で構築する。
- **フェーズ 4・5 不変**（AC-04）: リテラル数値アサーション、`AbortReset` の phase 5 書き込み観測、既存テストで担保する。Phase 2.2 で更新した `store_test.go` を含む。
- **hook 差し替え点の race 確認**: `go test -race ./internal/store` を Phase 2.5 で実行し、package-private hook の復元漏れや並列実行リスクがないことを確認する。

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
| package-private の関数変数差し替え点が mutable global になり、並列テストや production code からの再代入で不安定化する場合 | 回復処理のテストが順序依存になる | 差し替え点は unexported に閉じ、production code では再代入しない。差し替えるテストでは `t.Parallel` を使わず、`t.Cleanup` で必ず元に戻す |
| ADR 日英同期または hook 差し替え点の実装調整に想定以上の時間がかかる場合 | Phase 3 または Phase 2.3a〜2.3c が遅延する | コード変更とテスト通過を先に完了させ、ADR 翻訳は日本語版の節構造確定後にまとめて実行する。hook 実装が過大になる場合は、公開 API を増やさない範囲で unexported hook 構造体へ切り替える |

---

## 7. 実装チェックリスト

- [ ] Phase 1: `recovery.go` のコア実装変更
- [ ] Phase 2: テストの更新・追加・全件通過
- [ ] Phase 3: ADR-0003 改訂（日英両版）
- [ ] `make fmt` 実行済み
- [ ] `make lint` がエラーなく完了する
- [ ] `make test` が全テストで通過する
- [ ] `make deadcode` が本タスク起因の未使用コードを報告しない
- [ ] `cmd/tlsrpt-digest/recover_test.go` のテストが引き続き通ること（ファサード変更なし・回帰確認）

---

## 8. 完了基準

- `resetPhaseDataStaged` / `resetPhaseEmailsStaged` の定数がコードに残っていないこと
- `TestResetForRecovery_WritesOnlyManifestAndCommittedPhases` が、新規リセットでフェーズ 2・3 が書かれないことを確認していること
- `TestAdvanceResetPhases_RunsStagesBeforeCommit` が、`advanceResetPhases` の操作順序を確認していること
- `TestResetForRecovery_Phase1MissingStagingDirConverges` が、ステージング領域欠落時にも収束することを確認していること
- コミット前判定が `mfst.Phase < resetPhaseCommitted` の範囲式で表現されていること
- 全 AC（AC-01〜AC-09）に対応するテスト、確認コマンド、または検証結果の記録が「受け入れ条件トレーサビリティ」に紐づいていること
- `make fmt`・`make lint`・`make test` がすべて通ること
- ADR-0003 の日本語版・英語版について、Phase 3.1〜3.3 の確認コマンド結果が新フェーズ定義との整合を示していること

---

## 9. 次のステップ

- 実装完了後、PR を作成する。
- 将来ステージング対象が増えた場合は、`stageXxx` 冪等関数の追加のみで対応できる。チェックポイントフェーズの追加は不要（アーキテクチャ §9）。
