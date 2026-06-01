# 運用手順書：レガシーリセットマニフェストが残存するストアのアップグレード手順

## 対象読者

本手順書は、`tlsrpt-digest` をバージョン 0081 以降（`AbortReset` 廃止・有効フェーズ `{1, 4}` 確定）にアップグレードするアップグレード作業者を対象とする。

## 適用条件

以下のいずれかに該当するストアが存在する場合に本手順書が適用される。

- **フェーズ 2 または フェーズ 3** のリセットマニフェスト（`.tlsrpt-digest-reset-manifest.json`）が残存するストア（旧バージョンが `recover --mode discard-old --yes` の途中でクラッシュし、チェックポイントフェーズを書いた状態で停止している）
- **フェーズ 5** のリセットマニフェストが残存するストア（旧バージョンが `recover --abort-reset --yes` の途中でクラッシュし、中断 WAL エントリを書いた状態で停止している）

**通常のケース**（マニフェストなし、フェーズ 1、またはフェーズ 4 のみ）はそのままアップグレードして問題ない。本手順書の対応は不要である。

---

## 背景

task 0081 以降、`validateManifestPhase` はフェーズ値 `{1, 4}` のみを有効と見なす。旧バージョンが書いた値 2・3・5 を検出した場合、新バージョンは `ErrResetManifestPhaseUnknown` を返して停止する（fail-closed）。これはデータ破損を防ぐための安全側倒し（fail-closed）設計であり、オペレーターの手動対応を要求する。

---

## アップグレード前の確認方法

アップグレード前に以下のコマンドでマニフェストのフェーズ値を確認する。

```bash
cat "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
```

出力例（フェーズ 2 の場合）：

```json
{"version":1,"curr_uid_validity":12345678,"phase":2}
```

`"phase"` の値が `2`・`3`・`5` であれば本手順書の対応が必要である。`"phase"` が `1` または `4`、またはファイルが存在しない場合は対応不要である。

---

## フェーズ 2 または フェーズ 3 のマニフェストが残存する場合

### 概要

フェーズ 2・3 は旧バージョンが書いていたチェックポイントフェーズである（task 0080 で廃止）。これらのフェーズは `recover --mode discard-old --yes` の途中でクラッシュしたことを意味する。旧バージョンではコミット前として扱われ、再実行で収束していた。

### 対処手順

アップグレード前に**旧バージョン**で以下を実行し、リセットを完了させる。

```bash
tlsrpt-digest recover --mode discard-old --yes
```

コマンドが成功したことを確認する（exit code 0 で完了し、マニフェストファイルが削除されること）。

```bash
ls "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
# ls: ... No such file or directory  ← 正常（マニフェスト削除済み）
```

マニフェストが削除されたことを確認してからアップグレードを実施する。

---

## フェーズ 5 のマニフェストが残存する場合

### 概要

フェーズ 5 は旧バージョンの `AbortReset`（`recover --abort-reset --yes`）が使用していた中断 WAL エントリである（task 0081 で廃止）。このフェーズは `AbortReset` がステージング内のファイルを元の場所に戻す途中でクラッシュしたことを意味する。旧バージョンでは `AbortReset` を再実行することで正しく収束していた。

### 対処手順

アップグレード前に**旧バージョン**で以下を実行し、中断処理を完了させる。

```bash
tlsrpt-digest recover --abort-reset --yes
```

コマンドが成功したことを確認する（exit code 0 で完了し、マニフェストファイルが削除されること）。

```bash
ls "${STORE_ROOT_DIR}/.tlsrpt-digest-reset-manifest.json"
# ls: ... No such file or directory  ← 正常（マニフェスト削除済み）
```

マニフェストが削除されたことを確認してからアップグレードを実施する。

---

## アップグレード後の確認

### レガシーマニフェストが残存したままアップグレードした場合

レガシーマニフェスト（フェーズ 2・3・5）が残存したままアップグレードした場合、新バージョンは `fetch` / `gc` / `recover` などのストア書き込み操作で以下のエラーを返して停止する。

```
store: unknown reset manifest phase: got=N
```

（`N` は検出されたフェーズ値）

この状態では以下が保証される。

- **ステージングディレクトリとマニフェストファイルは削除されない**（fail-closed 設計）。
- ストアの整合性は保たれている。

旧バージョンに一時的にロールバックし、上記の事前作業（`--mode discard-old --yes` または `--abort-reset --yes`）を完了させてからアップグレードをやり直す。

### 正常アップグレード後の動作確認

アップグレード後、次のコマンドで動作を確認する。

```bash
tlsrpt-digest recover
```

`No recovery required: store is in a consistent state.` または recovery-required の状態表示が出力されれば正常である。

---

## 関連ドキュメント

- [ADR-0003: ResetForRecovery のフェーズ設計](../dev/adr/0003_reset_phase_design.ja.md)
- [プロセス間ロック設計ガイドライン](../dev/developer_guide/process_locking.ja.md)
