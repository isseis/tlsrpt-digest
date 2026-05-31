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
| `internal/store/recovery.go` | 実装変更 | （1）定数 `resetPhaseDataStaged=2`・`resetPhaseEmailsStaged=3` を削除する。（2）`advanceResetPhases` 内の中間チェックポイント書き込み（フェーズ 2・3 の `writeResetManifest` 呼び出し）を削除し、ステージング 2 操作とコミットを連続実行に変更する。（3）コミット前判定条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する。（4）関数コメント（"Forward: 1 → 2 → 3 → 4"）・型定義コメントを新定義に整合させる。 |
| `internal/store/recovery_test.go` | テスト変更・追加 | `resetPhaseDataStaged` / `resetPhaseEmailsStaged` を参照する 5 つのテスト（下表）で定数をリテラル値 `resetPhase(2)` / `resetPhase(3)` に置き換える。レガシー後方互換テスト（AC-05・AC-06）を新規追加する。 |
| `internal/store/store_test.go` | テスト変更 | `resetPhaseEmailsStaged` を参照する 2 箇所（line 377・395）を `resetPhase(3)` に置き換える。 |
| `internal/store/store.go` | コメント変更 | `HasPendingReset` コメント（line 99）の "phases 1–3 or 5" を新定義に整合した表現に更新する。 |
| `docs/dev/adr/0003_reset_phase_design.ja.md` | ドキュメント改訂 | §2–§7 のフェーズ 2・3 への言及を新フェーズ定義 `{1, 4, 5}` に整合させる（AC-07 参照）。後方互換の扱いを明記する（AC-08）。 |
| `docs/dev/adr/0003_reset_phase_design.md` | 翻訳反映 | `/mktrans` で日本語版から英語版へ反映する（AC-09）。 |

#### `recovery_test.go` の定数参照テスト一覧

| テスト名 | 参照している廃止定数 | 変更後の構築方法 |
|---|---|---|
| `TestApplyRecovery_RefusesPendingReset` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え（`ApplyRecovery` がフェーズ 2 の in-flight reset を拒否すること、すなわちレガシー値でも保留と判定されることを検証するテストとして維持） |
| `TestResetForRecovery_IdempotentAfterCrashBeforeCommit` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え（レガシー値フェーズ 3 から冪等に収束することを検証するテストとして維持） |
| `TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え（変更後はフェーズ 2 が書かれない状況を再現するのではなく、旧バージョンが書いたレガシーフェーズ 2 マニフェストからの収束テストとして維持） |
| `TestAbortReset_CrashDuringCommitRefusesAbort` | `resetPhaseEmailsStaged` | `resetPhase(3)` に置き換え |
| `TestAbortReset_RestoresOldData` | `resetPhaseDataStaged` | `resetPhase(2)` に置き換え |

#### 再利用できる既存資産

- `stageDataFile`・`stageEmailsDir`・`commitReset` は変更なし。`advanceResetPhases` からの呼び出し順序を変えるだけ。
- `writeResetManifest`・`validateManifestPhase`・`readResetManifest` は変更なし。
- `AbortReset` のロジックはフェーズ 2・3 を単一値比較の対象にしておらず、コミット前の範囲として扱う既存の構造が維持される。

---

## 2. 実装フェーズ

### Phase 1: `internal/store/recovery.go` の実装変更（AC-01・AC-02・AC-04）

- [ ] **1.1** フェーズ定数を `{1, 4, 5}` のみに整理する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: 定数 `resetPhaseDataStaged = 2` と `resetPhaseEmailsStaged = 3` の定義を削除する。以下 5 箇所のコメントを内容に従って更新する。
    - `resetPhase` 型定義のコメント: "Forward: 1 → 2 → 3 → 4" → "Forward: 1 → 4" に更新する。
    - `ResetForRecovery` の関数コメント（line 486）: "Drives phases 1→2→3→4→cleanup" → "Drives phases 1→4→cleanup" に更新する。
    - `ResetForRecovery` 内インラインコメント（line 522）: "A pre-commit manifest (phases 1–3)" → "A pre-commit manifest (phase < resetPhaseCommitted, or legacy phases 2–3)" に更新する。
    - `AbortReset` の関数コメント（line 661）: "Valid for pre-commit phases (1–3)" → "Valid for pre-commit phases (phase < resetPhaseCommitted, or legacy phases 2–3)" に更新する。
    - `HasPendingReset` の関数コメント（line 755）: "Returns true only for active-phase resets (phases 1–3 or 5)" → "Returns true only for active-phase resets (pre-commit phases or aborting phase 5)" に更新する。
  - 完了基準: `make build` でコンパイルエラーが出ないこと（この時点でテストは壊れてよい）。

- [ ] **1.2** `advanceResetPhases` から中間チェックポイント書き込みを削除する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `advanceResetPhases` 内で、`stageDataFile` 後の `writeResetManifest(phase=2)` 呼び出しと `stageEmailsDir` 後の `writeResetManifest(phase=3)` 呼び出しを削除する。フェーズ 1（コミット前）から `stageDataFile` → `stageEmailsDir` → `commitReset` を順に実行するフローにする（アーキテクチャ §2.2）。関数コメントを更新する。
  - 完了基準: 関数内に `writeResetManifest` の呼び出しがなく（`commitReset` 内の書き込みは除く）、`commitReset` が直接呼ばれること。

- [ ] **1.3** コミット前判定条件を範囲式に変更する
  - ファイル: `internal/store/recovery.go`
  - 作業内容: `ResetForRecovery` 内の stale manifest 検出条件 `mfst.Phase <= resetPhaseEmailsStaged` を `mfst.Phase < resetPhaseCommitted` に変更する（アーキテクチャ §3.2・§6.2）。
  - 完了基準: 変更前後の範囲が `[1, 4)` で等価であることを差分で確認できること、かつ Phase 2.4 の AC-06 テストが `make test` で通ること。

- [ ] **1.4** `store.go` のコメントを更新する
  - ファイル: `internal/store/store.go`
  - 作業内容: `HasPendingReset` のコメント "phases 1–3 or 5" を、フェーズ 2・3 を能動的な書き込み対象として記述せず、レガシー値として読み取り互換で扱われる旨が読み取れる表現に更新する（例: "pre-commit phases (1, or legacy 2–3) and aborting phase (5)"）。
  - 完了基準: 更新後のコメントが新フェーズ定義（コミット前は `phase < resetPhaseCommitted`、レガシー値はコミット前として読み取り互換）を正確に反映しており、フェーズ 2・3 を能動的な書き込み対象として記述していないこと。

### Phase 2: テストの更新と新規追加

- [ ] **2.1** `recovery_test.go` の廃止定数参照を置き換える（AC-01・AC-02・AC-03・AC-04 の回帰）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: 1.3 の表に示した 5 つのテストで `resetPhaseDataStaged` → `resetPhase(2)` に、`resetPhaseEmailsStaged` → `resetPhase(3)` に置き換える。各テストのコメントを「レガシーフェーズ値を持つマニフェストからの収束」を検証するテストとして意味を整合させる。
  - 完了基準: コンパイルエラーが解消し、各テストが `make test` で通ること。

- [ ] **2.2** `store_test.go` の廃止定数参照を置き換える（AC-04 の回帰）
  - ファイル: `internal/store/store_test.go`
  - 作業内容: `TestOpen_PendingReset_FailsClosedForReadWrite` および `TestOpen_PendingReset_OpenRecoverResetSucceeds` 内の `resetPhaseEmailsStaged` を `resetPhase(3)` に置き換える。
  - 完了基準: `make test` で 2 件のテストが通ること。

- [ ] **2.3** 新実装の C3 クラッシュシナリオテストを追加する（AC-01・AC-02・AC-03）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容: `TestResetForRecovery_CrashAfterBothFilesStaged` を新規追加する。`rootDir` にレポートと emails を植え、`stagingPath` に `tlsrpt.json` と `emails/` を移動済みにし、マニフェストを `resetPhaseManifestWritten`（フェーズ 1）で書いた状態から `ResetForRecovery(200)` を呼び出す。空ストア（`tlsrpt.json` と `emails/` が存在しないこと）・新 UIDVALIDITY・`recovery_required` 解消（`HasPendingReset` が `false` を返すこと）へ収束することを確認する。このテストは「`advanceResetPhases` がフェーズ 1 から開始し、両ファイルのリネームを no-op として扱い、`commitReset` まで到達できること」を間接的に検証し、AC-01（中間チェックポイントなし）と AC-03（C3 状態からの収束）を担保する。AC-01 の最終的な根拠は `advanceResetPhases` のコードレビューで確認する。
  **注**: 中間フェーズが書かれないことをブラックボックステストで直接証明するには `writeResetManifest` 呼載時に呼び出されるテストフックが必要であり、現時点ではその実装コストは対効果に見合わない。コードレビューで `advanceResetPhases` 内の `writeResetManifest` 呼び出しが除去されていることを目視確認する（AC-01 の証明細目）。
  - 完了基準: テストが `make test` で通ること。

- [ ] **2.4** レガシー値後方互換テストを追加する（AC-05・AC-06）
  - ファイル: `internal/store/recovery_test.go`
  - 作業内容:
    - AC-05: `validateManifestPhase` がフェーズ 2・3 を拒否しないことを確認するテストを追加する（`validateManifestPhase(resetPhase(2))` と `validateManifestPhase(resetPhase(3))` が `nil` を返すこと）。
    - AC-05: フェーズ 2 のレガシーマニフェスト（ステート: `tlsrpt.json` がステージングに移動済み・`emails/` は rootDir に残存・マニフェストが `resetPhase(2)` で記録済み）を持つストアで `ResetForRecovery` を実行したとき、`validateManifestPhase` が通過し、コミット前として一括実行で収束（空ストア + 新 UIDVALIDITY + recovery_required 解消）することを確認するテストを追加する。
    - AC-06: フェーズ 3 のレガシーマニフェスト（`CurrUIDValidity` が現在の `recovery_required` の値と不一致）を持つストアで `ResetForRecovery` を呼び出したとき、stale manifest と判定されて（a）旧マニフェストが削除されること、（b）最終的に空ストア・新 UIDVALIDITY・`recovery_required` 解消へ収束することを確認するテストを追加する（旧マニフェストの削除は最終状態でマニフェストが存在しないことで確認する）。境界値確認のためフェーズ 2 のレガシーマニフェストで同様のセットアップを行うテストも追加する。
  - 完了基準: 追加したテストが `make test` で通ること。

- [ ] **2.5** `make fmt && make lint && make test` を通す
  - 完了基準: いずれもエラーなく完了すること。

### Phase 3: ADR-0003 の改訂（AC-07・AC-08・AC-09）

- [ ] **3.1** ADR-0003 日本語版を新フェーズ定義に整合させる
  - ファイル: `docs/dev/adr/0003_reset_phase_design.ja.md`
  - 作業内容: アーキテクチャ §3.4 が列挙する箇所を改訂する。具体的な対象範囲は以下のとおり（AC-07 参照）。
    - §2: `resetPhase`（整数 1–5）のフェーズ値域記述を新定義 `{1, 4, 5}` とレガシー値 2・3 の説明に更新する。フェーズ一覧表からフェーズ 2・3 の行を削除または「レガシー（読み取り専用）」行に変更する。フェーズ別ファイル配置表のフェーズ 2・3 行を同様に更新する。状態遷移図を新定義に整合させる。
    - §3: 設計パターン注記（フェーズ 2・3 を「後書き（チェックポイント）」と説明する記述）を削除し、「コミット前からの一括実行」に更新する。
    - §4: 「フェーズ 2・3（チェックポイント）をリネーム後に書く理由」の節は新設計と矛盾するため削除または全面改訂する。「チェックポイントフェーズ（フェーズ 2・3）廃止の判断」節を実施済みの記述へ更新し、後方互換の正規化方針（レガシー値 2・3 をコミット前として解釈）を明記する（AC-08）。
    - §5: クリーンアップシナリオ表の「フェーズ（1〜3）」表記を新定義（コミット前）に更新する。
    - §6: 不変条件まとめ表のフェーズ 2・3 に関する行を削除または「レガシー値は範囲判定でコミット前として扱われる」旨の記述に変更する。
    - §7: 将来拡張方針の「新しいチェックポイントフェーズを追加する」等の記述を「チェックポイントフェーズは廃止済みであり、ステージング対象の追加は冪等関数の追加で十分」に更新する。
  - 完了基準: フェーズ 2・3 への言及が能動的な書き込み対象として残っていないこと（レガシー後方互換の説明は除く）。

- [ ] **3.2** 英語版 ADR を `/mktrans` で反映する
  - ファイル: `docs/dev/adr/0003_reset_phase_design.md`
  - 作業内容: `/mktrans` コマンドを使い、日本語版の変更内容を英語版に反映する。CLAUDE.md の翻訳規約に従い、日本語版を原本として英語版に適用する。
  - 完了基準: 英語版が日本語版と構造一致していること（AC-09）。

---

## 3. 受け入れ条件トレーサビリティ

| AC | 内容 | 実装タスク | 検証テスト |
|---|---|---|---|
| AC-01 | 新規リセットはフェーズ 2・3 を書かずフェーズ 1 → 4 へ遷移する | Phase 1.1・1.2 | Phase 2.3 で追加するテスト（`advanceResetPhases` が中間書き込みなしでフェーズ 4 へ遷移すること） |
| AC-02 | `advanceResetPhases` がコミット前から `stageDataFile`・`stageEmailsDir`・`commitReset` を順に冪等実行する | Phase 1.2 | Phase 2.3 で追加するテスト。既存の `TestResetForRecovery_CrashAtPhaseManifestWritten`・`TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate` も継続して通ること |
| AC-03 | 任意の中間状態でクラッシュした後の再実行が空ストア + 新 UIDVALIDITY + recovery_required 解消へ収束する | Phase 1.2 | Phase 2.1 で更新した各クラッシュテスト（`TestResetForRecovery_CrashAtPhaseManifestWritten`・`TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate`・`TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate`・`TestResetForRecovery_IdempotentAfterCrashBeforeCommit`）、および Phase 2.3 で追加する `TestResetForRecovery_CrashAfterBothFilesStaged` |
| AC-04 | フェーズ 4・5 の意味・数値・役割が変更されない | Phase 1.1 | Phase 2.2 で更新した `store_test.go` のテスト。既存の `TestAbortReset_AfterCommit`・`TestAbortReset_NoPendingReset` が通ること。Phase 2.1 で更新した `TestAbortReset_CrashDuringCommitRefusesAbort`（センチネルコミット済み + レガシー phase=3 マニフェスト → `AbortReset` が `ErrResetNotPending` を返すこと）が通ること |
| AC-05 | レガシーフェーズ 2・3 マニフェストが `validateManifestPhase` に拒否されず、コミット前として冪等収束する | Phase 1.3 | Phase 2.4 で追加するレガシー後方互換テスト |
| AC-06 | レガシーフェーズ 2・3 マニフェストに対する stale manifest 検出（`CurrUIDValidity` 不一致）が正しく動作する | Phase 1.3 | Phase 2.4 で追加する AC-06 テスト |
| AC-07 | ADR-0003 内のフェーズ 2・3 への言及が新定義に整合している | Phase 3.1 | ドキュメントレビューによる目視確認（注：当 AC はソースコードではなく文書内容の変更を対象としており、`*_test.go` による自動検証が不適切なため目視確認とする） |
| AC-08 | 後方互換の正規化方針（レガシー値 2・3 をコミット前として解釈）が ADR に明記される | Phase 3.1 | ドキュメントレビューによる目視確認（注：同上） |
| AC-09 | 英語版 ADR が日本語版と構造一致している | Phase 3.2 | ドキュメントレビューによる目視確認（注：同上） |

---

## 4. 実装順序とマイルストーン

| マイルストーン | 内容 | 含むフェーズ |
|---|---|---|
| M1 | コア実装 | Phase 1 |
| M2 | テスト整合・全テスト通過 | Phase 2 |
| M3 | ドキュメント改訂 | Phase 3 |

Phase 1 と Phase 2 は密接に依存するため、1.1→1.2→1.3→2.1→2.2→2.3→2.4→2.5 の順で連続して完結させる。Phase 3 はコードの動作確認が完了した後に実施する。

---

## 5. テスト戦略

### 単体テスト

- **一括遷移と C3 クラッシュ収束**（AC-01・AC-02・AC-03）: `TestResetForRecovery_CrashAfterBothFilesStaged`（Phase 2.3 で追加）が、「フェーズ 1 マニフェスト + 両ファイルステージング済み」という C3 状態から `ResetForRecovery` が冪等に収束することを直接確認する。これにより中間チェックポイントが書かれないことの間接証明にもなる。
- **その他のクラッシュ収束**（AC-03）: ファイル配置で表現される C1・C2 状態（`tlsrpt.json` 退避前・退避後）でのクラッシュ → 再実行 → 空ストア収束。Phase 2.1 で更新する既存テストがこれを担う。
- **レガシー後方互換**（AC-05・AC-06）: Phase 2.4 で新規追加。フェーズ 2・3 のリテラルマニフェストを JSON で直接組み立てるか `resetPhase(2)` / `resetPhase(3)` で構築する。
- **フェーズ 4・5 不変**（AC-04）: 既存テストが通ることで担保。Phase 2.2 で更新した `store_test.go` を含む。

### テストヘルパー

既存の `openRecoverResetStore`・`writeResetManifest`・`makeFullReport` を再利用する。新規テストヘルパーファイルは不要。レガシー値のマニフェストはテスト関数内でリテラル値または `resetPhase(2)` / `resetPhase(3)` を使って構築する。

### テストデータ

人工的なフィクスチャはすべてテスト関数内にインラインで記述する（`testdata/` への追加不要）。

---

## 6. リスク管理

| リスク | 影響 | 対策 |
|---|---|---|
| `AbortReset` 内のコミット前判定に `resetPhaseEmailsStaged` への暗黙的な依存がある場合 | `AbortReset` が誤動作する | Phase 1.1 の変更後に `make build` でコンパイルエラーを確認し、残存参照をすべて解消してから Phase 2 へ進む |
| `cleanupCompletedReset` のシナリオ表（§5）が廃止定数を参照している場合 | ADR 改訂漏れ | Phase 3.1 で §5 のシナリオ表も明示的に確認する |
| 既存の 5 つのクラッシュテストが、定数削除後に想定と異なる挙動を検証しているケース | テストが false positive になる | Phase 2.1 の置き換えと同時に、各テストのコメントを「レガシー値からの収束」として意味を再定義し、テスト意図を明示する |

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
- `advanceResetPhases` が中間チェックポイントを書かないこと
- コミット前判定が `mfst.Phase < resetPhaseCommitted` の範囲式で表現されていること
- 全 AC（AC-01〜AC-09）に対応するテストまたはドキュメントレビューが存在すること
- `make fmt`・`make lint`・`make test` がすべて通ること
- ADR-0003 の日本語版・英語版が新フェーズ定義に整合していること

---

## 9. 次のステップ

- 実装完了後、PR を作成する。
- 将来ステージング対象が増えた場合は、`stageXxx` 冪等関数の追加のみで対応できる。チェックポイントフェーズの追加は不要（アーキテクチャ §9）。
