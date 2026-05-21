# ADR-0001: Selecting the Timestamp for `.eml` File GC Determination

| Item | Content |
|---|---|
| Number | ADR-0001 |
| Status | Adopted |
| Decision Date | 2026-05-20 |
| Related Tasks | 0040_store, 0041_store_gc_simplify |

---

## 1. Context

### Role of `.eml` GC in the System

This system fetches TLSRPT report emails from an IMAP mailbox and stores the raw email files as `.eml` files at `{root_dir}/emails/{uidvalidity}/{YYYYMM}/{padded_uid}.eml`. The `{YYYYMM}` component is derived from the reception timestamp (`INTERNALDATE`) set by the IMAP server, structuring the files to make it easy to manually delete old files by date.

`DeleteEmailsBefore` periodically GCs `.eml` files that have exceeded the retention period. The system has the following four types of timestamps available as GC determination criteria.

### Available Timestamps and Their Characteristics

| Timestamp | Source | Controlling Party | Reliability |
|---|---|---|---|
| **Sent time** (SentAt) | Email `Date:` header | Sender (external) | Low: can be set arbitrarily by external parties |
| **IMAP reception time** (INTERNALDATE) | IMAP server | Mail server (external) | Medium: server-dependent |
| **Download time** (SavedAt) | File inode change time (ctime) | This system (local) | High: cannot be tampered with externally |
| **Report period end date** (report_end_date) | `date-range.end-datetime` in TLSRPT report | Sender (external) | Low: can be set arbitrarily by external parties |

---

## 2. Design Considerations

### Risks of Using Externally Controlled Values as GC Criteria

`SentAt` and `report_end_date` are values set by the sender. There is a risk that GC will stop functioning due to misconfiguration with far-future dates (e.g., year 3000) or intentional attacks. If `.eml` files continue to accumulate without being deleted, disk usage will grow without limit.

`SavedAt` (ctime), on the other hand, is a locally controlled value recorded by this system and cannot be tampered with externally, making it highly reliable as a GC criterion.

### Relationship Between File Path Structure and GC Reference Timestamp

The `{YYYYMM}` component in the `.eml` path is derived from `INTERNALDATE` (IMAP server reception time). The timestamp used for GC deletion determination (download time, period end date, etc.) can differ from `INTERNALDATE` in the month (e.g., received in January 2025, downloaded in June 2025), so an approach that directly compares directory names with GC reference timestamps for deletion carries a risk of erroneous deletion.

### Problems with Using `SentAt` (`Date:` Header) for Path Determination

Using `SentAt` for `{YYYYMM}` determination has the following problems:

- **Dependency on externally controlled values**: The `Date:` header is a value that the sender can set arbitrarily and is unreliable.
- **Path instability**: When the `Date:` header is absent, the system falls back to `SavedAt`; however, if the same email is re-fetched during recovery, `SavedAt` changes and the path changes, creating a risk of duplicate files.

The `INTERNALDATE` set by the IMAP server (a mandatory field per RFC 3501) always exists and is not changed once the server sets it, making it suitable for stable path determination without needing a fallback.

### Responsibility Placement for Validation

Adding an upper-limit check on `end-datetime` to `internal/tlsrpt.Parse()` as a countermeasure against far-future `report_end_date` values is one option to consider. However, `internal/tlsrpt` is responsible for faithfully converting RFC 8460 JSON into Go structs. The decision of "whether to process this date-range" is an application-level judgment; embedding business logic in the parser mixes responsibilities and also requires injecting `now time.Time`, which reduces testability. This judgment is the responsibility of the entry point.

---

## 3. Options Considered

### Option A: Perform GC determination using both `report_end_date` and `saved_at` criteria, preventing erroneous deletion during directory sweeps

GC determination is performed using two criteria: `report_end_date < reportCutoff` (external control) and `saved_at < savedAtCutoff` (local control). Directory sweeps are cross-referenced against surviving indices to prevent erroneous deletion.

**Reason for rejection**: Using the sender-controlled `report_end_date` for GC determination leaves an attack vector via far-future dates. Managing two criteria makes the code complex.

### Option B: Derive the `{YYYYMM}` directory name from `SavedAt` (download time)

Using `{savedAt.YYYYMM}` as the base ensures consistency between directory names and GC reference timestamps.

**Reason for rejection**:

- **Lower path stability than `INTERNALDATE`**: Since `SavedAt` is the download time, if the same email is re-fetched during recovery and the month changes, it would be written to a different path, creating a risk of duplicate files. `INTERNALDATE` (a mandatory field per RFC 3501) is set once by the server and never changed, so it is always stable.
- **Semantic issue**: Directories are partitioned by "download period" rather than "report period," making it harder to manually locate emails for a specific period.
- **Root problem unresolved**: Although `SavedAt` is a locally controlled value and appropriate as a GC criterion, `INTERNALDATE` is superior in both stability and reliability for path determination.

### Option C: Use only `INTERNALDATE` as the GC criterion and delete empty directories after GC (Adopted)

- Use `DeleteEmailsBefore(cutoff time.Time)` with `internal_date < cutoff` as the only deletion condition.
- Do not include `report_end_date` in the email index.
- Do not directly compare directory names with GC reference timestamps; instead, delete `{uidvalidity}/{YYYYMM}` and `{uidvalidity}` directories that become empty after GC.

Reason for adopting `INTERNALDATE` rather than `SavedAt` as the GC criterion: The purpose of GC is to "delete old report data," and the "age" of data is semantically correct to measure by the IMAP server reception time (`INTERNALDATE`, an approximation of the sender's transmission time), not by when we downloaded it (`SavedAt`). Using `SavedAt` as the criterion would shorten the effective retention period when fetch is delayed due to network failures. When re-fetching old emails that have exceeded the retention period during recovery, they would immediately become GC targets, but this is the correct behavior (reprocessing data beyond the retention period is unnecessary).

Cleanup of truly orphaned `.eml` files (files not present in the index) is naturally handled by the `reprocess` subcommand, which recursively traverses all `.eml` files, so no directory sweep is needed.

### Option D: Add date-range validation to `internal/tlsrpt.Parse()`

Validate `end-datetime <= now + 48h` or similar at parse time.

**Reason for rejection**: As described in Section 2 "Responsibility Placement for Validation," this exceeds the scope of the parser's responsibility and should be handled at the entry point.

---

## 4. Decision

### 4.1 GC Reference Timestamp: Adopt Option C

**To be implemented as task 0041_store_gc_simplify**. Only `INTERNALDATE` (IMAP server reception time) is used for `.eml` GC determination.

### 4.2 Path Determination Timestamp: Adopt `INTERNALDATE`

**To be implemented simultaneously as a prerequisite for task 0041_store_gc_simplify**. `INTERNALDATE` is used for `{YYYYMM}` determination; `SentAt` (`Date:` header) is not included in the data model.

- `EmailMeta` and `internalEmailIndexEntry` have an `InternalDate` (IMAP INTERNALDATE) field and do not have a `SentAt` field.
- `LoadedEmail` does not have a `SentAt` field (when the sent time is needed, it can be accessed via `Message.Header.Get("Date")`).
- `Store.SaveEmail` takes `internalDate` as an argument and does not take `savedAt` as an argument.
- If `INTERNALDATE` is a zero value, an error is returned (`INTERNALDATE` is a mandatory field per RFC 3501, and a zero value indicates a serious specification violation by the IMAP server).

| Timestamp | Role in `.eml` GC | Role in Path Determination | Role in Report GC |
|---|---|---|---|
| `SentAt` | Not used | Not used | Not used |
| `INTERNALDATE` | **The sole criterion for deletion determination** | **The basis for `{YYYYMM}` determination** | Not used |
| `SavedAt` | Not used | Not used | Not used |
| `report_end_date` | Not used (not included in email index) | Not used | Criterion for deletion determination |

`report_end_date` is used for GC of report records (the `reports` array in `tlsrpt.json`) (`DeleteReportsBefore`). Report records have no `saved_at` equivalent timestamp, and the semantics of "excluding data whose target period has ended from aggregation targets" is correct.

Countermeasures against far-future `end-datetime` values are implemented as a retention period upper limit at the entry point (task 0070) and are not added to `internal/tlsrpt`.

---

## 5. Resulting Trade-offs

| Gained | Lost |
|---|---|
| GC is based on content age (`INTERNALDATE`), so the retention period is consistent regardless of fetch delays | Truly orphaned `.eml` files may remain until `reprocess` is executed |
| `DeleteEmailsBefore` evaluates only a single deletion condition (`internal_date`), making the implementation simple | — |
| `DeleteEmailsBefore` deletes `{uidvalidity}/{YYYYMM}` directories that become empty after GC | — |
| `SaveReports` handles only report records, with clearly separated responsibilities from the email index | — |
| No risk of erroneous deletion since directory names and GC reference timestamps are not directly compared | — |
| `INTERNALDATE` is set by the server and never changed, so paths are always stable and do not depend on the presence or absence of the `Date:` header | — |
| Both path determination and GC determination are unified around `INTERNALDATE`, making the design simple | — |
