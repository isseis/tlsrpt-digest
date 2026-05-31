# 要件定義書：AbortReset・フェーズ 5 の廃止およびフェーズ 2・3 フォールバックの削除

## ドキュメントステータス

| 項目 | 内容 |
|---|---|
| ステータス | `draft` |
| 作成日 | 2026-05-31 |
| レビュー日 | - |
| レビュアー | - |
| コメント | - |

---

## 1. 背景と目的

### 1.1 背景

ADR-0003 は `ResetForRecovery`（`recover --mode discard-old --yes`）の進捗を `resetPhase`（整数 1–5）としてリセットマニフェストに記録する。task 0080 でフェーズ 2・3 を廃止（書き込み停止）してフェーズ集合を `{1, 4, 5}` に縮小したが、フェーズ 5 は引き続き `AbortReset`（`recover --abort-reset --yes`）の WAL エントリとして残存している。また task 0080 では、旧バージョンが書いたフェーズ 2・3 のマニフェストをフェーズ 1 相当として読み取る後方互換コードを残していた。

task 0081 の設計調査（2026-05-31）で以下の事実が確認された。

#### 調査結果 1：フェーズ 1 マニフェストはクラッシュ後にのみ存在する

`ResetForRecovery` は同期的に完走する。フェーズ 1 マニフェストを書いてからステージング・コミットまで一括実行するため、プロセスのクラッシュ（または SIGKILL・OOM 等）以外にフェーズ 1 マニフェストがディスクに残存する経路がない。オペレーターが `recover --mode discard-old --yes` を実行中に「一時停止して abort を選ぶ」ことはできない。

#### 調査結果 2：AbortReset が受け付けるケースは 2 種類

1. **フェーズ 1 + クラッシュ後**（C1/C2/C3 いずれも）：クラッシュ地点によってステージング状態は異なるが、`restoreFromStaging`（冪等）でいずれも正しく復元する。
2. **フェーズ 5 からの再開**：AbortReset 自体がクラッシュしてフェーズ 5 マニフェストが残った場合に、再実行で再開する。

#### 調査結果 3：AbortReset 固有の複雑性

- `AbortReset` はファイルを元の場所に戻す前にフェーズ 5 を WAL エントリとして書く（クラッシュ後も `ResetForRecovery` が誤って上書きしないよう保護するため）。
- フェーズ 5 の存在により `ResetForRecovery` にフェーズ 5 拒否チェックが必要になっている。
- `HasPendingReset` はフェーズ 5 を保留リセットとして扱うためにフェーズ 4 以外を真とする実装になっている。
- `restoreFromStaging` 関数は `AbortReset` からのみ呼ばれる。

#### 調査結果 4：削除可能なコードの概算

| 対象 | 規模 |
|---|---|
| `AbortReset()` 関数本体 | 約 90 行 |
| `TestAbortReset_*` テスト群 | 約 250 行 |
| `resetPhaseAborting` 定数 | 1 行 |
| `ErrResetAbortInProgress` エラー型 | 数行 |
| `restoreFromStaging` 関数 | 約 20 行 |
| `recover --abort-reset` CLI サブコマンド | 複数ファイル |

#### 調査結果 5：フェーズ 5 レガシー値の扱い（案 A を採用）

旧バージョンが書いたフェーズ 5 マニフェストをアップグレード後に読み込んだ場合の挙動について、以下の 2 案を検討し **案 A（fail-closed）を採用する**。

- **案 A（fail-closed、採用）**：`validateManifestPhase` がフェーズ 5 を未知値として拒否し、オペレーターに手動対応を促す。旧バージョンで `AbortReset` を実行中にアップグレードした場合に安全側に倒せる。
- **案 B（自動収束、不採用）**：フェーズ 5 をコミット前として扱い `advanceResetPhases` で discard-old に収束させる。ただし、旧コードが `restoreFromStaging` でファイルを root に戻しかけている状態では、root に旧データが一部残っているままステージングしてコミットする危険があるため採用しない。

採用理由：安全側に倒すことが保守性・運用の観点で優先される。フェーズ 5 レガシー値が発生するのは `AbortReset` 実行中にクラッシュしてからアップグレードした稀なケースに限られ、自動収束の複雑性とデータ破損リスクに見合わない。

### 1.2 目的

1. **主目的**：`AbortReset` 機能を廃止し、フェーズ 1 からは `recover --mode discard-old --yes` による前進のみを許容する。フェーズ 2・3 の後方互換フォールバックも削除し、有効フェーズを `{1, 4}` のみに確定する。状態空間と制御フローをさらに単純化する。
2. **副次的目的**：ADR-0003 を新フェーズ定義に整合させ、フェーズ 5 の設計根拠・状態遷移図・不変条件表を更新する。

---

## 2. スコープ

### 対象範囲（In Scope）

- `AbortReset()` メソッドの廃止（`Store` インターフェースと `fileStore` 実装を含む）
- `resetPhaseAborting` 定数（値 5）の削除
- `ErrResetAbortInProgress` エラー型の削除
- `ErrResetNotPending` エラー型の削除（`AbortReset` 専用であり、廃止後は未使用になる）
- `restoreFromStaging` 関数の削除
- `ResetForRecovery` 内のフェーズ 5 拒否チェックの削除
- `validateManifestPhase` の有効値域を `{1, 4}` の 2 値に限定（値 2・3・5 以上はすべて `ErrResetManifestPhaseUnknown`）
- フェーズ 5 レガシー値を fail-closed で扱う（案 A、上記 §1.1 調査結果 5）
- フェーズ 2・3 後方互換フォールバックの削除：`validateManifestPhase` での受理、`ResetForRecovery` の「`phase < resetPhaseCommitted`」判定コメント、`HasPendingReset` のコメント、`advanceResetPhases` のコメントにある「legacy 2–3」への言及
- `recover --abort-reset` CLI フラグおよび関連処理の削除：`--abort-reset` フラグ定義・`RecoverAbort` オプション・`runAbortReset`・検証ロジック（`errAbortResetRequiresYes`、`errAbortAndModeExclusive`）・`recoverStoreOpenMode` の abort 分岐（`cmd/tlsrpt-digest/recover.go`、`cmd/tlsrpt-digest/main.go`）
- abort 廃止に伴う CLI メッセージ・案内文の更新：保留リセット検出時の「Roll back reset」案内（`recover.go`）、`ErrPendingReset` のエラーメッセージ（`boot.go`）、`errYesRequiresModeOrAbort` の文言（`--yes requires --mode`）
- 既存テストの削除・更新（`TestAbortReset_*` 全件、フェイクストアの abort 関連フィールド、レガシー値 2・3 を前提とするテスト群）
- ADR-0003 の改訂（フェーズ 5・フェーズ 2・3 の設計根拠節削除、状態遷移図・不変条件表の更新）

### 対象外（Out of Scope）

- フェーズ 1・4 の役割変更
- `ResetForRecovery` の処理フロー変更（フェーズ 5 拒否チェックおよびレガシー値フォールバック削除以外）
- `cleanupCompletedReset` のセンチネルベース判定ロジックの変更
- ストレージ技術の移行

### 影響を受けるコンポーネント

- **直接変更**：`internal/store/recovery.go`、`internal/store/store.go`（インターフェース）、`internal/store/errors.go`（エラー型）、`cmd/tlsrpt-digest/recover.go`、`cmd/tlsrpt-digest/main.go`（フラグ定義・検証ロジック）、`cmd/tlsrpt-digest/boot.go`（`ErrPendingReset` メッセージ）
- **間接的影響**：`internal/store/recovery_test.go`、`cmd/tlsrpt-digest/recover_test.go`（フェイクストア）、`docs/dev/adr/0003_reset_phase_design.ja.md` / `.md`

---

## 3. 機能要件

### `F-001`：`AbortReset` の廃止

`AbortReset()` を `Store` インターフェースおよび `fileStore` 実装から削除する。`recover --abort-reset` CLI フラグおよび関連処理も削除する。

**受け入れ条件**：

- `AC-01`：`Store` インターフェースに `AbortReset` メソッドが存在しない。
- `AC-02`：`fileStore` に `AbortReset` の実装が存在しない。
- `AC-03`：`recover --abort-reset --yes` を実行すると、`--abort-reset` が未定義フラグとして `flag.Parse` がエラーを返す（`flag provided but not defined: -abort-reset` 相当）。
- `AC-04`：`restoreFromStaging` 関数が削除され、削除後に `make deadcode` で新たな未使用関数が検出されない。
- `AC-05`：`ErrResetAbortInProgress` エラー型が削除されている。
- `AC-06`：`ErrResetNotPending` エラー型が削除されている（`AbortReset` 専用であり廃止後は未使用となるため）。
- `AC-07`：CLI の案内文・エラーメッセージから abort への言及が削除されている。具体的には、(a) 保留リセット検出時に「Roll back reset」を案内しない、(b) `ErrPendingReset` 由来のメッセージが `recover --abort-reset --yes to roll back` を含まない、(c) `--yes` 単独指定時のエラーが `--yes requires --mode`（`or --abort-reset` を含まない）になる。

### `F-002`：フェーズ 5 定数の廃止・レガシー値の扱い、フェーズ 2・3 フォールバックの削除

`resetPhaseAborting`（値 5）を廃止し、フェーズ 2・3 の後方互換フォールバックも削除する。有効フェーズを `{1, 4}` の 2 値のみとする。旧バージョンが書いたフェーズ 2・3・5 のマニフェストはすべて fail-closed で扱う。

**受け入れ条件**：

- `AC-08`：`resetPhaseAborting` 定数が `recovery.go` に存在しない。
- `AC-09`：`validateManifestPhase` は値 `{1, 4}` のみを有効とし、それ以外（0・2・3・5・6・99 など）はすべて `ErrResetManifestPhaseUnknown` を返す。
- `AC-10`：値 2・3 のマニフェストが存在する状態で `ResetForRecovery` または `Open(OpenReadWrite)` を呼び出すと、`ErrResetManifestPhaseUnknown` を返して fail-closed する（ステージングやマニフェストを削除しない）。
- `AC-11`：フェーズ 5 のマニフェストが存在する状態で `ResetForRecovery` または `Open(OpenReadWrite)` を呼び出すと、`ErrResetManifestPhaseUnknown` を返して fail-closed する（ステージングやマニフェストを削除しない）。
- `AC-12`：`ResetForRecovery` 内にフェーズ 5 への単値比較（`mfst.Phase == resetPhaseAborting`）が存在しない。
- `AC-13`：`recovery.go` のコメントおよびロジックからフェーズ 2・3 への言及（`legacy 2–3`・`legacy 2·3`・`phase < resetPhaseCommitted` の「2・3 を含む」旨の記述）が削除されている。

### `F-003`：ADR-0003 の改訂

ADR-0003 を新フェーズ定義 `{1, 4}` に整合させる。

**受け入れ条件**：

- `AC-14`：ADR-0003（日本語版・英語版）のフェーズ一覧表からフェーズ 2・3・5 の行が削除されている。
- `AC-15`：「フェーズ 5（recovery_required リセットマーカー）を設ける理由」節が削除または「廃止の経緯」として更新されている。
- `AC-16`：状態遷移図から P5 ノードおよびその遷移（P1→P5、P5→RR）が削除されている。
- `AC-17`：不変条件表の「フェーズ 5 が書かれている ⟹ `AbortReset` のみが続行できる」行が削除されている。
- `AC-18`：ユーザー操作時の挙動表から `recover --abort-reset --yes` 列が削除されている（または「廃止済み」として更新されている）。
- `AC-19`：英語版は `/mktrans` で日本語版から反映する（CLAUDE.md 翻訳規約に従う）。

---

## 4. 非機能要件

### 保守性

- 有効フェーズを `{1, 4}` の 2 値に限定することで、`HasPendingReset` の判定（`mfst.Phase != resetPhaseCommitted`）が「フェーズ 1 のみ」を意味するようになり、フェーズ 5（aborting）の特殊扱いが不要になる。また `ResetForRecovery` の分岐も `phase == resetPhaseManifestWritten` に単純化できる。
- `AbortReset` 廃止とレガシー値フォールバック削除により、テストコードが約 350 行削減される。

### 後方互換性

- フェーズ 2・3・5 のレガシー値はすべて fail-closed で扱い、自動収束させない。フェーズ 2・3 のマニフェストが残存するストアは移行期間終了とみなし、アップグレード前に旧バージョンで `recover --mode discard-old --yes` を完了するよう運用ドキュメントに記載する。フェーズ 5 については旧バージョンで `AbortReset` を完了してからアップグレードするよう記載する。

### パフォーマンス

- `AbortReset` は稀な例外イベントの手動操作であり、廃止による性能要件への影響はない。

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）。
- フェーズ 1・4 の数値・意味・役割は変更しない（再採番しない）。

---

## 6. テスト方針

### 削除するテスト

- `TestAbortReset_*` 全件（`internal/store/recovery_test.go`）
- `TestResetForRecovery_RefusesAbortingPhase`（フェーズ 5 拒否テスト）
- フェーズ 2・3 のレガシーマニフェストを前提とするテスト群：
  - `TestResetForRecovery_ResumesLegacyPhase3Manifest`（フェーズ 3 からの再開テスト）
  - `TestResetForRecovery_LegacyPhase2Manifest*`（フェーズ 2 マニフェストテスト群）
  - その他 `resetPhase(2)` / `resetPhase(3)` を植え込むテストケース（フェーズ 5 追加後の fail-closed テストとして書き換え対象外のもの）

### 更新するテスト

- `TestValidateManifestPhaseRange`（task 0080 で追加）：有効値を `{1, 4}` のみとし、値 2・3・5 はすべて拒否値に変更する。
- `TestResetPhasePersistedNumericValues`（task 0080 で追加）：`resetPhaseAborting` の数値アサーションを削除する。
- `TestRecover_YesAlone`：エラーメッセージのアサーションを `--yes requires --mode`（`or --abort-reset` を含まない）に更新する。
- `TestApplyRecovery_*` のうちフェーズ 2 マニフェストを植え込んでいるもの：フェーズ 2 が fail-closed になることを確認するか、植え込みをフェーズ 1 に変更する。

### 新規追加するテスト

- フェーズ 2・3 のマニフェストが存在する状態で `ResetForRecovery` および `Open(OpenReadWrite)` を呼び出すと fail-closed することを検証する（AC-10）。
- フェーズ 5 のマニフェストが存在する状態で `ResetForRecovery` および `Open(OpenReadWrite)` を呼び出すと fail-closed することを検証する（AC-11）。

### 統合テスト

- `cmd/tlsrpt-digest/recover_test.go` の `AbortReset` 関連テスト（`TestRecover_AbortReset*`）を削除する。`recoverStoreOpenMode` など `RecoverAbort` を用いるテストケースから abort 行を削除する。
- フェイクストアから `AbortReset` メソッドおよび `AbortResetCallCount`・`AbortResetErr` フィールドを削除する（`Store` インターフェースから `AbortReset` が消えるため）。
- `--abort-reset` フラグ削除後、`recover --abort-reset --yes` が `flag.Parse` でエラーになることを検証する（AC-03、必要に応じて新規追加）。
