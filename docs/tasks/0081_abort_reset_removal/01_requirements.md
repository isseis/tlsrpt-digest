# 要件定義書：AbortReset とフェーズ 5 の廃止

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

ADR-0003 は `ResetForRecovery`（`recover --mode discard-old --yes`）の進捗を `resetPhase`（整数 1–5）としてリセットマニフェストに記録する。task 0080 でフェーズ 2・3 を廃止してフェーズ集合を `{1, 4, 5}` に縮小したが、フェーズ 5 は引き続き `AbortReset`（`recover --abort-reset --yes`）の WAL エントリとして残存している。

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

#### 調査結果 5：フェーズ 5 レガシー値の扱い（設計決定が必要）

旧バージョンが書いたフェーズ 5 マニフェストをアップグレード後に読み込んだ場合の挙動は、以下の 2 案のうちどちらを採用するか、アーキテクチャ設計書で決定する必要がある。

- **案 A（fail-closed）**：`validateManifestPhase` がフェーズ 5 を未知値として拒否し、オペレーターに手動対応を促す。旧バージョンで `AbortReset` を実行中にアップグレードした場合に安全側に倒せる。
- **案 B（自動収束）**：フェーズ 5 をコミット前として扱い `advanceResetPhases` で discard-old に収束させる。ただし、旧コードが `restoreFromStaging` でファイルを root に戻しかけている状態では、root に旧データが一部残っているままステージングしてコミットする危険がある。

案 A の採用を推奨する（安全側に倒すことが保守性・運用の観点で優先される）。

### 1.2 目的

1. **主目的**：`AbortReset` 機能を廃止し、フェーズ 1 からは `recover --mode discard-old --yes` による前進のみを許容する。フェーズ集合を `{1, 4}` に縮小し、状態空間と制御フローをさらに単純化する。
2. **副次的目的**：ADR-0003 を新フェーズ定義に整合させ、フェーズ 5 の設計根拠・状態遷移図・不変条件表を更新する。

---

## 2. スコープ

### 対象範囲（In Scope）

- `AbortReset()` メソッドの廃止（`Store` インターフェースと `fileStore` 実装を含む）
- `resetPhaseAborting` 定数（値 5）の削除
- `ErrResetAbortInProgress` エラー型の削除
- `restoreFromStaging` 関数の削除
- `ResetForRecovery` 内のフェーズ 5 拒否チェックの削除
- `validateManifestPhase` の有効値域を `[1, 4]` に縮小
- フェーズ 5 レガシー値の読み取り時挙動の定義（上記 §1.1 調査結果 5 の設計決定）
- `recover --abort-reset` CLI サブコマンドの削除（`cmd/tlsrpt-digest/recover.go`）
- 既存テストの削除・更新（`TestAbortReset_*` 全件）
- ADR-0003 の改訂（フェーズ 5 の設計根拠節削除、状態遷移図・不変条件表の更新）

### 対象外（Out of Scope）

- フェーズ 1・4 の役割変更
- `ResetForRecovery` の処理フロー変更（フェーズ 5 拒否チェック以外）
- `cleanupCompletedReset` のセンチネルベース判定ロジックの変更
- ストレージ技術の移行

### 影響を受けるコンポーネント

- **直接変更**：`internal/store/recovery.go`、`internal/store/store.go`（インターフェース）、`cmd/tlsrpt-digest/recover.go`
- **間接的影響**：`internal/store/recovery_test.go`、`cmd/tlsrpt-digest/recover_test.go`（フェイクストア）、`docs/dev/adr/0003_reset_phase_design.ja.md` / `.md`

---

## 3. 機能要件

### `F-001`：`AbortReset` の廃止

`AbortReset()` を `Store` インターフェースおよび `fileStore` 実装から削除する。`recover --abort-reset` CLI サブコマンドも削除する。

**受け入れ条件**：

- `AC-01`：`Store` インターフェースに `AbortReset` メソッドが存在しない。
- `AC-02`：`fileStore` に `AbortReset` の実装が存在しない。
- `AC-03`：`recover --abort-reset --yes` コマンドを実行すると、未知のサブコマンドとして CLI がエラーを返す（`flag.Parse` 失敗または `unknown subcommand` 相当）。
- `AC-04`：`restoreFromStaging` 関数が削除されている（`make deadcode` で未使用として検出されない）。
- `AC-05`：`ErrResetAbortInProgress` エラー型が削除されている。

### `F-002`：フェーズ 5 定数の廃止とレガシー値の扱い

`resetPhaseAborting`（値 5）を廃止し、新規に書き込むフェーズを `{1, 4}` のみとする。旧バージョンが書いたフェーズ 5 マニフェストは fail-closed（案 A）で扱う。

**受け入れ条件**：

- `AC-06`：`resetPhaseAborting` 定数が `recovery.go` に存在しない。
- `AC-07`：`validateManifestPhase` は値 5 を未知値として `ErrResetManifestPhaseUnknown` を返す（有効値域 `[1, 4]`）。
- `AC-08`：値 0 および値 5 以上（5・6・99 など）を `validateManifestPhase` に渡すと `ErrResetManifestPhaseUnknown` が返る。値 1・2・3・4 は引き続き受理される（レガシー値 2・3 の互換性は task 0080 で確立済み）。
- `AC-09`：フェーズ 5 のマニフェストが存在する状態で `ResetForRecovery` または `Open(OpenReadWrite)` を呼び出すと、`ErrResetManifestPhaseUnknown` を返して fail-closed する（ステージングやマニフェストを削除しない）。
- `AC-10`：`ResetForRecovery` 内にフェーズ 5 への単値比較（`mfst.Phase == resetPhaseAborting`）が存在しない。

### `F-003`：ADR-0003 の改訂

ADR-0003 を新フェーズ定義 `{1, 4}` に整合させる。

**受け入れ条件**：

- `AC-11`：ADR-0003（日本語版・英語版）のフェーズ一覧表からフェーズ 5 の行が削除されている。
- `AC-12`：「フェーズ 5（recovery_required リセットマーカー）を設ける理由」節が削除または「廃止の経緯」として更新されている。
- `AC-13`：状態遷移図から P5 ノードおよびその遷移（P1→P5、P5→RR）が削除されている。
- `AC-14`：不変条件表の「フェーズ 5 が書かれている ⟹ `AbortReset` のみが続行できる」行が削除されている。
- `AC-15`：ユーザー操作時の挙動表から `recover --abort-reset --yes` 列が削除されている（または「廃止済み」として更新されている）。
- `AC-16`：英語版は `/mktrans` で日本語版から反映する（CLAUDE.md 翻訳規約に従う）。

---

## 4. 非機能要件

### 保守性

- フェーズ集合を `{1, 4}` に縮小し、`HasPendingReset` の実装を `mfst.Phase == resetPhaseCommitted` の単一値比較で表現できるようにする。
- `AbortReset` 廃止により、テストコードが約 250 行削減される。

### 後方互換性

- フェーズ 5 レガシー値は fail-closed で扱い、自動収束させない（§1.1 調査結果 5 の案 A）。フェーズ 5 マニフェストが残存するストアへのアップグレードは、旧バージョンで `AbortReset` を完了してからアップグレードするよう運用ドキュメントに記載する。

### パフォーマンス

- `AbortReset` は稀な例外イベントの手動操作であり、廃止による性能要件への影響はない。

---

## 5. 制約

- 使用言語は Go とする（Go 1.26 以上）。
- フェーズ 4 の数値・意味・役割は変更しない（再採番しない）。
- レガシー値 2・3 の読み取り互換は task 0080 で確立済みであり、本タスクでは変更しない。

---

## 6. テスト方針

### 削除するテスト

- `TestAbortReset_*` 全件（`internal/store/recovery_test.go`）
- `TestResetForRecovery_RefusesAbortingPhase`（フェーズ 5 拒否テスト）

### 更新するテスト

- `TestValidateManifestPhaseRange`（task 0080 で追加）：値 5 が拒否値に移動する。
- `TestResetPhasePersistedNumericValues`（task 0080 で追加）：`resetPhaseAborting` の数値アサーションを削除する。

### 新規追加するテスト

- フェーズ 5 マニフェストが存在する状態で `ResetForRecovery` および `Open(OpenReadWrite)` を呼び出すと fail-closed することを検証する（AC-09）。

### 統合テスト

- `cmd/tlsrpt-digest/recover_test.go` の `AbortReset` 関連テストを削除する。`--abort-reset` フラグのテストがあれば削除または「未知サブコマンド」として更新する。
