# ADR-0003: ResetForRecovery のフェーズ設計とコミット後クリーンアップの扱い

| 項目 | 内容 |
|---|---|
| 番号 | ADR-0003 |
| ステータス | 採択 |
| 決定日 | 2026-05-25 |
| 関連タスク | 0070_entrypoint |

---

## 1. コンテキスト

### UIDVALIDITY 変化と手動復旧

IMAP サーバーが UIDVALIDITY を変更すると、既存の UID と新しい UID の対応が保証されなくなる。本システムはこの変化を検出した時点で `recovery_required` をセンチネルに記録し、以後の fetch/summary を停止する。オペレーターは `recover` サブコマンドで以下のいずれかを選択する。

- **keep-old** (`ApplyRecovery`): 旧データを保持したまま新 UIDVALIDITY に移行する。
- **discard-old** (`ResetForRecovery`): 旧データをすべて破棄し、空ストアで再スタートする。

`ResetForRecovery` は複数のファイル操作を伴うため、途中でクラッシュした場合でも安全に再開または取り消しができる必要がある。

### 要件（02_architecture.md より）

| 要件 | 内容 |
|---|---|
| AC-crash-safe | `ResetForRecovery` はいずれの段階でクラッシュしても「旧データ保持 + recovery_required 残存」または「空ストア + 新 UIDVALIDITY + recovery_required 解消」のどちらかに収束する |
| AC-abort | `AbortReset` はコミット前の pending reset を取り消し、旧データを保持した状態に戻せる |
| AC-fail-closed | コミット前の pending reset がある場合、通常の `Open(OpenReadWrite)` は fail-closed する |
| AC-cleanup | コミット後の cleanup 失敗は通常データパスへ影響させず、後続の `Open` または `ResetForRecovery` で再 cleanup 可能にする |

---

## 2. フェーズ設計の概要

`ResetForRecovery` はファイル操作の進捗を `resetPhase`（整数値）としてリセットマニフェスト（`.tlsrpt-digest-reset-manifest.json`）に記録する。マニフェストはリセット操作の進捗台帳であり、センチネルファイル（`.tlsrpt-digest-meta.json`）はユーザー可視の確定状態（UIDValidity・recovery_required の現在値）を保持する。

```
マニフェスト（進捗台帳）
  ↓  記録
resetPhase（整数） ─── フェーズに応じてコードが再開・中断を判断

センチネル（確定状態）
  ↓  記録
UIDValidity / recovery_required ─── 操作の「コミット済みか否か」の真の根拠
```

---

## 3. フェーズ一覧と役割

| 定数名 | 値 | 記録タイミング | 意味・役割 |
|---|---|---|---|
| *(なし / legacy)* | 0 | 旧コードがステージング完了後に書いた（フィールド自体が存在しない） | 後方互換のために受け付ける。`ResetForRecovery` では `emails_staged` (3) と同等に扱い、`AbortReset` ではセンチネルを確認して判断する |
| `resetPhaseManifestWritten` | 1 | ステージング開始前（先書き） | **WAL エントリ**。この時点からマニフェストが存在するため `Open(OpenReadWrite)` は `ErrPendingReset` を返す。`AbortReset` によるロールバックが可能になる |
| `resetPhaseDataStaged` | 2 | `tlsrpt.json` のステージング完了後（チェックポイント） | データファイルのリネームが完了したことを記録する。クラッシュ後の再開でこのフェーズから再実行しても `stageDataFile` は冪等（ファイル不在は no-op） |
| `resetPhaseEmailsStaged` | 3 | `emails/` のステージング完了後（チェックポイント） | メールディレクトリのリネームが完了したことを記録する。同様に冪等 |
| `resetPhaseCommitted` | 4 | センチネル保存直後（コミットマーカー） | センチネルへの書き込み（recovery_required クリア・新 UIDVALIDITY 設定）が完了したことを記録する。この後はマニフェストとステージングディレクトリのみが残存するため、クリーンアップ失敗は通常データパスに影響しない |
| `resetPhaseAborting` | 5 | `restoreFromStaging` 実行前（中断 WAL エントリ） | **中断操作の WAL エントリ**。`AbortReset` がファイルを元の場所に戻す前にこのフェーズを書く。以降のクラッシュでマニフェストが残存しても、`ResetForRecovery` はこのフェーズを見て操作を拒否し、`AbortReset` の再実行を促す |

---

## 4. 各フェーズの詳細設計根拠

### フェーズ 1（WAL エントリ）をステージング前に書く理由

マニフェストの書き込みは `Open(OpenReadWrite)` に対する「操作中フラグ」を兼ねる。このフラグを最初に書くことで：

- `Open(OpenReadWrite)` が常に fail-closed できる（AC-fail-closed）
- `AbortReset` がどのフェーズからでもロールバックできる

フラグを書く前にクラッシュした場合はマニフェストが存在しないため、次回実行は "fresh start" として扱われ、センチネルの recovery_required を確認して正常に再開する。

### フェーズ 2・3（チェックポイント）をリネーム後に書く理由

`rename(2)` は POSIX の保証する原子操作であるため、成功した場合のみファイルが移動している。チェックポイントをリネームの後に書くことで、「チェックポイントが書かれている = リネームは確実に完了している」という推論が成立する。クラッシュして再開した場合：

```
[フェーズ N のチェックポイントがない]
  → 「フェーズ N の操作は完了済みかもしれないし、未実行かもしれない」
  → 冪等な操作を再実行する（ファイルが不在なら no-op）
```

逆にリネームの前に書いた場合、クラッシュするとチェックポイントは書かれたがリネームは未完了という状態になり、再開時に「完了済み」と誤判断するリスクがある。本設計では操作後チェックポイントパターンを選んだ。

> **注意**: この設計は AC-crash-safe を担保するが、「フェーズ N の書き込みが完了した = フェーズ N の操作は完了した」という不変条件を前提にしている。各ステージング関数（`stageDataFile`・`stageEmailsDir`）の冪等性はこの不変条件を維持するために必須である。

### センチネルがコミットの真の根拠である理由

フェーズ 4（`resetPhaseCommitted`）のマーカーは `commitReset` がセンチネルを保存した**後**に書く。つまり「センチネルに recovery_required がない」は「コミットが完了している」と等価であり、「マニフェストがフェーズ 4 である」よりも信頼性が高い（フェーズ 4 への更新が完了する前にクラッシュする可能性があるため）。

この性質を利用して以下の判断を行う。

| 判断箇所 | 使用する根拠 | 理由 |
|---|---|---|
| `AbortReset` のロールバック可否 | `sentinel.recovery_required != nil` | 「コミット後の abort」を防ぐ |
| `Open(OpenReadWrite)` のクリーンアップ可否 | `sentinel.recovery_required == nil` | 「コミット後の cleanup 失敗」を検出してデータパスをブロックしない |

### フェーズ 5（中断 WAL エントリ）を設ける理由

`AbortReset` がファイルを元の場所に戻す（`restoreFromStaging`）操作は途中でクラッシュしうる。クラッシュ後の状態は「マニフェストはフェーズ 3 のまま、ファイルは root に復元済み」になる可能性がある。この状態で `ResetForRecovery` を実行するとフェーズ 3 として扱われ、空のステージングへのコミットが行われてしまう（「新 UIDVALIDITY + recovery_required クリア + 旧データが root に残存」という矛盾状態）。

この問題を回避するため、`AbortReset` はファイルを動かす前に必ずフェーズ 5（aborting）に更新する。`ResetForRecovery` はフェーズ 5 を見た場合に `ErrResetAbortInProgress` を返し、`AbortReset` の完了を要求する。

```
[フェーズ 5 検出時の動作]
  ResetForRecovery → 拒否 (ErrResetAbortInProgress)
  AbortReset       → 再開（restoreFromStaging は冪等）→ クリーンアップ
```

---

## 5. Open(OpenReadWrite) でのクリーンアップ設計

### 設計選択肢

| 選択肢 | 方針 | 課題 |
|---|---|---|
| A. フェーズ値のみで判断 | フェーズ 4 のみクリーンアップ | コミット後クラッシュウィンドウ（フェーズ 3 + センチネルコミット済み）や旧コードのフェーズ 0 に対応できない |
| B. センチネル値で判断（採択） | `recovery_required == nil` ならクリーンアップ | すべてのフェーズ・旧コード互換・コミットウィンドウに統一対応できる |

### クリーンアップロジック（`cleanupCompletedReset`）

```
1. マニフェストを読む
   ├─ 不在 → 何もしない（正常）
   ├─ バージョン不一致 → エラー（fail-closed）
   └─ 不明フェーズ → エラー（fail-closed）

2. センチネルを読む
   ├─ 不在 → ErrPendingReset（非正規状態・fail-closed）
   ├─ recovery_required あり → ErrPendingReset（操作進行中）
   └─ recovery_required なし → コミット済み → クリーンアップ実行
       ├─ os.RemoveAll(staging) ── best-effort
       └─ os.Remove(manifest)   ── 必須（失敗すると次回も試みる）
```

### カバーするシナリオ

| シナリオ | マニフェストフェーズ | sentinel.recovery_required | 結果 |
|---|---|---|---|
| 操作進行中（コミット前） | 1〜3 | あり | ErrPendingReset |
| abort 中断 | 5 | あり | ErrPendingReset |
| コミット後クリーンアップ失敗（新コード） | 4 | なし | クリーンアップして通常 Open |
| コミット後クリーンアップ失敗（旧コード） | 0 | なし | クリーンアップして通常 Open |
| コミットウィンドウクラッシュ（フェーズ 3 + センチネル確定） | 3 | なし | クリーンアップして通常 Open |

---

## 6. フェーズ間の不変条件まとめ

| 不変条件 | 担保箇所 |
|---|---|
| フェーズ 1 が書かれている間は `Open(OpenReadWrite)` が ErrPendingReset を返す | `cleanupCompletedReset` が recovery_required を確認 |
| フェーズ 2 が書かれている ⟹ `tlsrpt.json` はステージングに存在する | `stageDataFile` が冪等・フェーズ 2 はリネーム後に書く |
| フェーズ 3 が書かれている ⟹ `emails/` はステージングに存在する | `stageEmailsDir` が冪等・フェーズ 3 はリネーム後に書く |
| フェーズ 4 または `recovery_required == nil` ⟹ センチネルはコミット済み | `commitReset` がセンチネル保存後にフェーズ 4 を書く |
| フェーズ 5 が書かれている ⟹ `AbortReset` のみが続行できる | `ResetForRecovery` がフェーズ 5 を拒否 |

---

## 7. 将来の変更・拡張方針

### フェーズを追加する場合

1. 新しい `resetPhase` 定数を定義する（値は既存の最大値 + 1）
2. `validateManifestPhase` の上限を更新する
3. `advanceResetPhases`・`AbortReset` に新フェーズの処理を追加する
4. `cleanupCompletedReset` がコミット判断にセンチネルを使っているため、フェーズ数が増えても影響を受けない

### ステージングの対象ファイルが増える場合

- `stageDataFile`・`stageEmailsDir` に倣い、対応する `stageXxx` 関数を追加して冪等性を保つ
- 新しいチェックポイントフェーズを追加する（上記「フェーズを追加する場合」に準じる）

### コミット操作が複数ステップになる場合

現在の `commitReset` はセンチネル保存を 1 ステップで行っているため、センチネル保存の完了 = コミット確定となっている。複数ステップのコミットが必要になった場合は、「最後のステップが完了 = コミット確定」という不変条件を維持するように設計する必要がある。センチネル以外のファイルがコミット根拠になる場合は `cleanupCompletedReset` の判断ロジックの更新が必要になる。

### `AbortReset` の中断ロジックが複雑になる場合

現在の中断処理はフェーズ 5（aborting）の 1 ステップのみである。中断操作が複数ファイルにまたがる非原子操作になる場合は、同様にサブフェーズを導入することを検討する。その際も「フェーズ 5 系 = ResetForRecovery 禁止」の不変条件は維持する。

---

## 8. 関連ファイル

| ファイル | 役割 |
|---|---|
| `internal/store/recovery.go` | `resetPhase`・`resetManifest`・`ResetForRecovery`・`AbortReset`・`cleanupCompletedReset` の実装 |
| `internal/store/store.go` | `Open` 関数での `cleanupCompletedReset` 呼び出し |
| `internal/store/errors.go` | `ErrPendingReset`・`ErrResetNotPending`・`ErrResetManifestVersionMismatch`・`ErrResetManifestPhaseUnknown`・`ErrResetAbortInProgress` の定義 |
| `internal/store/recovery_test.go` | フェーズごとのクラッシュシナリオテスト |
| `internal/store/store_test.go` | `Open` 時のクリーンアップ動作テスト |
