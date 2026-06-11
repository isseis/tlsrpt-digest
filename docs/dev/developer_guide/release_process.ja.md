# リリース手順

このドキュメントは `tlsrpt-digest` のリリース手順を説明します。タグを push すると GitHub Actions が自動的にバイナリをビルドして GitHub Release を作成します。

---

## 概要

1. main ブランチの最新コミットを確認する
2. バージョンタグを作成する（`git tag -a vX.Y.Z HEAD`）
3. タグを push する（`git push origin vX.Y.Z`）
4. GitHub Actions の Release ワークフローが起動する
5. GoReleaser が linux/amd64・linux/arm64 バイナリをビルドし、tar.gz アーカイブと checksums.txt を生成する
6. GitHub Release が作成され、アーカイブと changelog が添付される

---

## 前提条件

- `main` ブランチの CI がすべてグリーンであること。
- `go.mod` / `go.sum` が最新であること（`go mod tidy` 後に差分がないこと）。GoReleaser はリリース前にこれを検証し、差分があれば失敗します。

---

## リリース手順

### 1. main ブランチの最新状態を確認する

```bash
git checkout main
git pull origin main
make test && make lint
```

### 2. バージョンを決める

本プロジェクトは [Semantic Versioning](https://semver.org/) に従います。

| 変更の種類 | バージョン変更 | 例 |
|---|---|---|
| 後方互換のバグ修正 | パッチ（Z を +1） | `v1.2.3` → `v1.2.4` |
| 後方互換の新機能追加 | マイナー（Y を +1、Z をリセット） | `v1.2.3` → `v1.3.0` |
| 後方互換でない変更 | メジャー（X を +1、Y・Z をリセット） | `v1.2.3` → `v2.0.0` |

直前のリリースタグを確認するには次のコマンドを使います。

```bash
git tag --list 'v*' --sort=-version:refname | head -5
```

### 3. タグを作成して push する

```bash
git tag -a vX.Y.Z HEAD -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

タグのフォーマットは `v[数字].[数字].[数字]` です（例：`v1.0.0`、`v0.2.1`）。このパターンに合致しないタグ（`vtest` など）はリリースワークフローを起動しません。

### 4. GitHub Actions のワークフローを確認する

[Actions タブ](https://github.com/isseis/tlsrpt-digest/actions) で「Release」ワークフローが起動したことを確認します。ワークフローが完了すると [Releases ページ](https://github.com/isseis/tlsrpt-digest/releases) に新しいリリースが作成されます。

---

## リリース成果物

GitHub Release には次のファイルが添付されます。

| ファイル | 内容 |
|---|---|
| `tlsrpt-digest_vX.Y.Z_linux_amd64.tar.gz` | Linux (x86-64) バイナリと付属ファイル |
| `tlsrpt-digest_vX.Y.Z_linux_arm64.tar.gz` | Linux (ARM64) バイナリと付属ファイル |
| `checksums.txt` | 全アーカイブの SHA-256 チェックサム |

各 tar.gz アーカイブには次のファイルが含まれます。

- `tlsrpt-digest`（バイナリ）
- `LICENSE`
- `README.md`
- `README.ja.md`

---

## changelog の生成ルール

GoReleaser は前回タグから今回タグまでの git log をもとに changelog を自動生成します。以下のプレフィックスで始まるコミット（スコープ付きも含む）は changelog から除外されます。

| 除外パターン | 例 |
|---|---|
| `docs:` / `docs(…):` | `docs(readme): update installation guide` |
| `test:` / `test(…):` | `test(imap): add edge case` |
| `chore:` / `chore(…):` | `chore(deps): bump golangci-lint` |

---

## タグを誤って push した場合

ビルドが開始する前であれば、タグを削除してリリースをキャンセルできます。

```bash
git tag -d vX.Y.Z
git push origin :refs/tags/vX.Y.Z
```

GitHub Release が作成済みの場合は、GitHub の Releases ページから手動で削除してください。

---

## 関連ファイル

| ファイル | 内容 |
|---|---|
| [`.goreleaser.yml`](../../../.goreleaser.yml) | GoReleaser 設定（ビルド・アーカイブ・changelog） |
| [`.github/workflows/release.yml`](../../../.github/workflows/release.yml) | リリースワークフロー定義 |
