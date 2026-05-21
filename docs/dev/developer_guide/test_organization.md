# Test Helper File Organization

Test helper files follow a two-tier classification system based on their scope and dependencies:

## Classification A: `testutil/` Subdirectory (Cross-Package Helpers)

**Use for**: Test helpers and mocks used across multiple packages or that only use public APIs

```
<package>/
├── <implementation>.go
├── <implementation>_test.go
└── testutil/
    ├── mocks.go              # Lightweight mocks (no external dependencies)
    ├── testify_mocks.go      # testify-based mocks (for complex scenarios)
    ├── mocks_test.go         # Tests for mock implementations
    └── helpers.go            # Test utility functions
```

**File Naming Rules:**
- **`testutil/mocks.go`**: Simple mock implementations without external library dependencies
- **`testutil/testify_mocks.go`**: Advanced mocks using stretchr/testify framework
- **`testutil/mocks_test.go`**: Unit tests for mock implementations
- **`testutil/helpers.go`**: Common test utility functions and setup helpers

**Package Naming:**
- Use a domain-prefixed package name within the `testutil/` subdirectory: `package <domain>testutil`
  - Examples: `package commontestutil`, `package securitytestutil`, `package verificationtestutil`
- Import without an alias: `<module>/internal/<package>/testutil`
- The unique package name eliminates the need for import aliases at call sites, preventing alias drift across the codebase

**Exception:** The repository-root `internal/testutil` package uses `package tu` for readability, since its helpers (e.g., `tu.Int32Ptr`) are used heavily for inline test data construction.

## Classification B: Package-Level `test_helpers.go` (Internal Helpers)

**Use for**: Test helpers that must remain in the same package due to:
- Adding methods to package-internal types
- Using non-exported (private) package APIs
- Avoiding circular dependencies

```
<package>/
├── <implementation>.go
├── <implementation>_test.go
└── test_helpers.go           # Package-internal test helpers
```

**File Naming Rules:**
- **`test_helpers.go`**: Single file for package-internal test helpers
- If multiple helper categories needed: `test_helpers_<category>.go` (e.g., `test_helpers_group.go`)

**Package Naming:**
- Use the same package name as the production code
- Always include `//go:build test` build tag

## Guidelines for New Test Helpers

When adding new test helper code, follow this decision tree:

1. **Does the helper use only public APIs?**
   - Yes → Continue to step 2 (Classification A)
   - No → Continue to step 4 (likely Classification B)

2. **What type of test helper are you creating?** (Classification A - `testutil/` subdirectory)
   - **Mock implementation** → Choose based on complexity:
     - Simple mock (no external dependencies) → `testutil/mocks.go`
     - Complex mock (using testify/mock) → `testutil/testify_mocks.go`
   - **Helper function** (setup, utilities, fixtures) → `testutil/helpers.go`
   - **Mock tests** → `testutil/mocks_test.go`

3. **Is the helper used by tests in other packages?**
   - Yes → Ensure it uses only public APIs, then place in appropriate `testutil/` file (step 2)
   - No → Continue to step 4

4. **Package-internal considerations** (Classification B - `test_helpers.go`)
   Place in `test_helpers.go` if the helper:
   - Adds methods to package-internal types
   - Uses non-exported (private) package APIs
   - Would create circular dependencies if placed in `testutil/` subdirectory
   - If multiple helper categories exist: use `test_helpers_<category>.go` (e.g., `test_helpers_group.go`)

**Build Tags:**
- All test helper files must include `//go:build test` at the top
- This ensures they are only compiled during test builds, not in production binaries

**Examples:**
- Mock interface implementation → `testutil/mocks.go` or `testutil/testify_mocks.go`
- Test setup helper function → `testutil/helpers.go`
- Method on internal type → `test_helpers.go`
- Factory function using private constructor → `test_helpers.go`

## Test Data Files: `testdata/` vs Inline Constants

### When to use `testdata/`

Use `testdata/` for data that originates **outside the codebase** — files received from external systems that are used as-is to exercise real parsing or processing logic.

**Examples of what belongs in `testdata/`:**
- Real TLSRPT report emails (`.eml`, `.json.gz`) captured from an actual mail server
- Recorded HTTP responses or protocol captures used for integration testing

### When to embed data inline

Embed test data as constants or string literals directly in the test function when the data is **artificially constructed** for the purpose of the test.

**Examples of what belongs inline:**
- Self-signed TLS certificates generated solely to test certificate validation logic
- Minimal TOML snippets constructed to trigger a specific validation error
- Small JSON payloads hand-crafted to test a parser edge case

**Why inline is preferred for artificial data:**
- The test is self-contained: the reader sees the exact input without opening a separate file.
- There is no ambiguity about whether the file represents a real-world artifact or a test fixture.
- Write the file to `t.TempDir()` at test setup time when a file path is needed (e.g., for `os.ReadFile`-based code under test).
