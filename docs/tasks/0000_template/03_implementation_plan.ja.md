# 実装計画書：[機能名]

## 1. 実装概要

- **目的**: ...
- **実装原則**: `02_architecture.md` の設計に従う

---

## 2. 実装フェーズ

### フェーズ 1: [フェーズ名]

- [ ] **1.1** [タスク名]
  - ファイル: `internal/xxx/yyy.go`
  - 作業内容: ...

- [ ] **1.2** [タスク名]
  - ファイル: `internal/xxx/yyy_test.go`
  - 作業内容: テストケース XX-01 〜 XX-03 の実装

### フェーズ 2: [フェーズ名]

- [ ] **2.1** [タスク名]
  - ファイル: `internal/zzz/www.go`
  - 作業内容: ...

---

## 3. 受け入れ条件トレーサビリティ

`01_requirements.md` の各受け入れ条件とテストの対応を記録する。

**AC-1: [F-001 の条件 1]**
- テスト: `internal/xxx/yyy_test.go::TestXxx`
- 実装: `internal/xxx/yyy.go:XX-YY`

**AC-2: [F-001 の条件 2]**
- テスト: `internal/xxx/yyy_test.go::TestXxx_ErrorCase`
- 実装: `internal/xxx/yyy.go:ZZ-WW`

---

## 4. 完了条件

- [ ] `make lint` がエラーなく完了
- [ ] `make test` がすべて成功
- [ ] `01_requirements.md` の全受け入れ条件に対応するテストが存在
- [ ] `make deadcode` で不要なコードがない
