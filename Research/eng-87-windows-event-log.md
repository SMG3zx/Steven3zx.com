# Reading the Windows Event Log incrementally from the Go host agent

Research date: 2026-10-19. Linear: ENG-87 (map ENG-80, project Origin Admin).

Terms follow `portfolio/admin/GLOSSARY.md`: the **host agent** runs on the **origin host** and supplies **log lines**; the admin server pulls them and keeps them in Turso for 7 days.

## Answer in brief

- **API:** `wevtapi.dll`, using **pull** reads: `EvtQuery` + `EvtNext` per channel on each server pull, with an `EvtSubscribe` signal event only as an optional "new events waiting" hint. Do not use the push callback model.
- **Library:** `golang.org/x/sys/windows` has **no** `Evt*` bindings, so write a small in-repo wrapper (about 12 procs through `windows.NewLazySystemDLL("wevtapi.dll")`). Use OpenTelemetry's Apache-2.0 `stanza/operator/input/windows` package as the reference implementation. Do not import it or Beats.
- **Cursor:** the **server owns the cursor**. It stores one `(channel, lastRecordId)` per channel in Turso, in the same transaction as the log lines. The agent stays stateless and queries `EventRecordID > cursor`. It reports gaps when the circular log has already overwritten the next record.
- **Channels:** System, Application, Security, and `Microsoft-Windows-Windows Defender/Operational` by default. TaskScheduler/Operational is **disabled on this origin host**, so it yields nothing unless someone enables it. Security needs `LocalSystem` or the Administrators group: a non-elevated session could not even open it here.
- **Volume on this origin host:** about 800 events/day across System, Application, and Defender at all levels, or about **60/day at Warning or worse**. One crash-looping service produced **29,884 System events in a single day**, so per-source rate limiting is required.
- **Log-line shape:** one rendered message line (`EvtFormatMessage`, first line only) plus structured fields taken from `EvtRender(EvtRenderEventXml)`, with the raw XML kept as well.

---

## 1. Which API, and which Go library

### wevtapi: push compared with pull

`EvtSubscribe` supports two models. In the **push** model, you pass a `Callback` that the service invokes for each event. In the **poll** model, you pass a `SignalEvent`. When it is signalled, you call `EvtNext` on the subscription handle, then reset the event and wait again ([EvtSubscribe](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtsubscribe)). `SignalEvent` and `Callback` are mutually exclusive. A subscription can only target Admin or Operational channels, not Analytic or Debug ones (same page).

`EvtQuery` returns a result set that you walk with `EvtNext`. The result set "is not a snapshot": events raised while you enumerate are included ([Consuming Events](https://learn.microsoft.com/en-us/windows/win32/wes/consuming-events)). Call `EvtNext` until it returns `ERROR_NO_MORE_ITEMS`, and `EvtClose` every event handle it returns ([EvtNext](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtnext)).

**Recommendation: pull.** The push callback runs on a thread the service owns and calls back into Go through `syscall.NewCallback`. That only adds risk, because the admin server pulls anyway. Every mature Go collector reviewed here uses the signal-event plus `EvtNext` pull model:

- OpenTelemetry passes a `windows.CreateEvent` handle as `SignalEvent` with a null callback, then calls `EvtNext` in batches ([subscription.go](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/stanza/operator/input/windows/subscription.go)).
- Telegraf passes a signal event, then loops `EvtNext` until `ERROR_NO_MORE_ITEMS` ([win_eventlog.go](https://github.com/influxdata/telegraf/blob/master/plugins/inputs/win_eventlog/win_eventlog.go)).

Because the admin server initiates each read, the simplest correct design is **`EvtQuery` per pull** (section 2). The subscription is not required. If the agent later wants to push a "new log lines available" hint, it can keep a signalled `EvtSubscribe(..., EvtSubscribeToFutureEvents)` purely as a wake-up.

### Go library options

| Option | Licence | Maintenance | Verdict |
|---|---|---|---|
| `golang.org/x/sys/windows` (already v0.49.0 in `agent/go.mod`) | BSD-3 | Active (v0.49.0, 2026-10-06) | Has **no `Evt*` functions**. It only covers the legacy writer side (`RegisterEventSource`, `ReportEvent`, `DeregisterEventSource`) ([pkg.go.dev](https://pkg.go.dev/golang.org/x/sys/windows)). A grep of the v0.49.0 module source finds no `wevtapi` or `EvtSubscribe` either. Use it for `NewLazySystemDLL`, `CreateEvent`, `UTF16PtrFromString`, and errors. |
| OpenTelemetry `pkg/stanza/operator/input/windows` | Apache-2.0 ([LICENSE](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/LICENSE)) | Very active | Best **reference**. It loads 11 procs through `windows.NewLazySystemDLL("wevtapi.dll")` ([api.go](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/stanza/operator/input/windows/api.go)) and has a publisher-metadata cache, bookmark persistence, and batch halving on `RPC_S_INVALID_BOUND` ([input.go](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/stanza/operator/input/windows/input.go)). Importing it would pull in the whole stanza/pdata dependency tree, so copy patterns (with attribution) instead. |
| `github.com/google/winops/winlog` + `/wevtapi` | Apache-2.0 | Published 2026-07-23 ([pkg.go.dev](https://pkg.go.dev/github.com/google/winops/winlog/wevtapi)) | A thin, clean binding with 21 `Evt*` functions, including `EvtSeek`, `EvtFormatMessage`, and `EvtOpenChannelConfig`, built on `x/sys/windows`. Its helper `GetRenderedEvents` indexes `events[returned-1]` and would panic on a zero-event batch when a bookmark is set ([winlog.go](https://github.com/google/winops/blob/master/winlog/winlog.go)). It is acceptable as a dependency for the low-level `wevtapi` package only. Leave out the helpers. |
| `github.com/elastic/beats/v7/winlogbeat/sys/wineventlog` | pkg.go.dev shows licence "UNKNOWN" and hides the docs ([pkg.go.dev](https://pkg.go.dev/github.com/elastic/beats/v7/winlogbeat/sys/wineventlog)) | Active, but internal to Beats | Avoid: the licence status is unclear and it is a heavy module. |
| `github.com/ofcoursedude/gowinlog` | MIT | Last tag v1.0.5, 2022-01-15 ([pkg.go.dev](https://pkg.go.dev/github.com/ofcoursedude/gowinlog)) | Avoid: unmaintained fork chain. |
| Telegraf `win_eventlog` | MIT (repo) | Active | Plugin-internal and not importable. A useful reference for `only_first_line_of_message` and the 64 KB `event_size_limit` ([win_eventlog.go](https://github.com/influxdata/telegraf/blob/master/plugins/inputs/win_eventlog/win_eventlog.go)). |

**Recommendation:** add `portfolio/admin/agent/eventlog_windows.go` with a hand-rolled binding to these procs: `EvtQuery`, `EvtNext`, `EvtRender`, `EvtClose`, `EvtCreateRenderContext`, `EvtOpenPublisherMetadata`, `EvtFormatMessage`, `EvtOpenLog`, `EvtGetLogInfo`, and optionally `EvtSubscribe`. This is about 150 lines, adds no new dependency, and keeps the licence BSD and in-repo. Alternatively, depend on `google/winops/winlog/wevtapi` for the raw signatures only.

## 2. Resume exactly where we left off when the server pulls

### What a bookmark actually is

A bookmark is created with `EvtCreateBookmark`, advanced with `EvtUpdateBookmark(event)`, and persisted as XML through `EvtRender(EvtRenderBookmark)`. It is restored by passing that XML back to `EvtCreateBookmark`. It can then be used with `EvtQuery`+`EvtSeek(EvtSeekRelativeToBookmark)` or with `EvtSubscribe(EvtSubscribeStartAfterBookmark)` ([Bookmarking Events](https://learn.microsoft.com/en-us/windows/win32/wes/bookmarking-events)). On this origin host, the bookmark for the newest System event renders as:

```xml
<BookmarkList Direction='backward'>
  <Bookmark Channel='System' RecordId='38309' IsCurrent='true'/>
</BookmarkList>
```

So, for a single channel, **a bookmark is just `(channel, EventRecordID)`**. Storing the integer record ID per channel loses nothing and is easier to query and compare in Turso.

### Pitfalls of bookmark seeking

- Without `EvtSeekStrict`, a seek past the end of the result set "will return the last record within the boundary" ([EVT_SEEK_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_seek_flags)). Seeking to "bookmark + 1" when nothing new has arrived would therefore re-deliver the last event as a duplicate.
- Without `EvtSubscribeStrict`, a missing bookmarked event makes the subscription silently start "with the event that is after the event that is closest to the bookmarked event". With `EvtSubscribeStrict`, `EvtSubscribe` fails with `ERROR_NOT_FOUND`, and the callback is notified of missing records ([EVT_SUBSCRIBE_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_subscribe_flags)).

### Recommended cursor design: server-owned and agent-stateless

1. **Turso holds the cursor.** Use a table `eventlog_cursor(channel TEXT PRIMARY KEY, last_record_id INTEGER, updated_at)`. When the server stores a batch of log lines, it upserts the cursor **in the same transaction**. That gives exactly-once delivery across restarts of either side: if the server dies before commit, it simply re-requests the same range.
2. **The pull request carries the cursor.** For example, `GET /eventlog?System=38309&Application=8468&...&limit=500`. A missing cursor means first contact, which defaults to "newest minus N" or "last 24 h" so the server does not backfill weeks of history into a 7-day store.
3. **The agent runs one forward `EvtQuery` per channel**, combining the cursor with the source filter in XPath. `EventRecordID` is an ordinary `System` element, and XPath supports `<`, `>` and `and` ([Consuming Events](https://learn.microsoft.com/en-us/windows/win32/wes/consuming-events)):
   ```
   *[System[EventRecordID > 38309 and (Level <= 3 or ...)]]
   ```
   It then calls `EvtNext` in batches until `limit` is reached or `ERROR_NO_MORE_ITEMS`, and returns the lines plus `nextCursor = last returned EventRecordID`. With no new events, it returns an empty list and the same cursor. Nothing is duplicated and no seek is needed.
4. **Gap detection.** Before querying, the agent calls `EvtOpenLog` + `EvtGetLogInfo(EvtLogOldestRecordNumber)` ([EVT_LOG_PROPERTY_ID](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_log_property_id)). If `cursor + 1 < oldest`, the circular log overwrote records the server never pulled. The agent then returns a synthetic gap log line (`"N records lost: log wrapped"`) so the loss is visible rather than silent. If `cursor > newest record`, the log was cleared or reset, so the agent restarts from the oldest record and emits a gap line. Record-ID behaviour after a clear was **not verified** against a primary source, so treat this case defensively.
5. **Order caveat.** Events written by different threads on different CPUs "may appear out of order" ([Consuming Events](https://learn.microsoft.com/en-us/windows/win32/wes/consuming-events)), but record IDs are assigned at write time. "Greater than last record ID" is therefore the correct monotonic predicate. Do **not** use `TimeCreated` as the cursor.

**Headroom:** the System log here is 14.1 MB of a 20 MB circular file holding 36 days. A server outage of hours or even days loses nothing, but the 30k/day crash-loop burst shows a full wrap is possible within a few days. The Security log has a 20 MB default too, and its volume depends on the audit policy.

## 3. Channels worth collecting by default

Observed on this origin host (Windows 11 Pro 10.0.26300) with `Get-WinEvent -ListLog`, run in a non-elevated session:

| Channel | Enabled | Records | Size / max | Span | Default? |
|---|---|---|---|---|---|
| System | yes | 38,309 | 14.1 / 20 MB | 2026-09-13 → today | **Yes** |
| Application | yes | 8,468 | 5.1 / 20 MB | 2026-09-13 → today | **Yes** |
| Security | ? | ? | ? | ? | **Yes** (needs elevation, see below) |
| Microsoft-Windows-Windows Defender/Operational | yes | 2,440 | 3.0 / 16 MB | 2026-09-13 → today | **Yes** |
| Microsoft-Windows-TaskScheduler/Operational | **no** | 0 | 0 / 10 MB | none | Opt-in. It is disabled by default here. Enabling it is a system setting change, which is the admin's call. |
| Microsoft-Windows-WindowsUpdateClient/Operational | yes | 796 | 1.0 / 1 MB (full) | 2026-09-13 → today | Optional. System already carries WindowsUpdateClient 19/20/43. |
| Microsoft-Windows-Windows Firewall With Advanced Security/Firewall | yes | 977 | 1.0 / 1 MB (full) | 4 days | Optional (rule changes) |
| Microsoft-Windows-PowerShell/Operational | yes | 1,708 | 15 / 15 MB (full) | 8 days | No, because script-block logging is noisy and sensitive |
| Microsoft-Windows-Hyper-V-VmSwitch-Operational | yes | 37,714 | | | No (noise from Docker/WSL networking) |
| Microsoft-Windows-Hyper-V-Compute-Operational | ? | ? | | | Needs elevation even to list. Consider it later for Docker container start/stop. |

The machine exposes 465 channels, 384 of them enabled and 128 non-empty, holding 196,517 records in total.

**Elevation:**
- System, Application, and Defender/Operational all carry the same channel SDDL here: `O:BAG:SYD:(A;;0xf0007;;;SY)(A;;0x7;;;BA)(A;;0x3;;;BO)(A;;0x5;;;SO)(A;;0x1;;;IU)(A;;0x3;;;SU)(A;;0x1;;;S-1-5-3)(A;;0x2;;;S-1-5-33)(A;;0x1;;;S-1-5-32-573)`. That grants read to interactive users (`IU`), services (`SU`), and **Event Log Readers** (`S-1-5-32-573`), whose members "can read event logs from local computers" ([Security groups](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-security-groups#event-log-readers)). These channels therefore need no elevation.
- **Security** failed in this session even for `-ListLog`, with *"To access the 'Security' log start PowerShell with elevated user rights"*. Viewing it is tied to the **Manage auditing and security log** right (`SeSecurityPrivilege`), held by Administrators by default on client computers ([Manage auditing and security log](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/security-policy-settings/manage-auditing-and-security-log)). The planned elevated host agent service, running as `LocalSystem`, will be able to read it. I did not try to elevate, so the Security ACL and counts here are **unmeasured**.

## 4. Volume and source filters

### Measured on this origin host (last 7 days, 2026-10-12 → 2026-10-19)

| Channel | All levels, events/day | Critical+Error+Warning in 7 days | Noisiest sources (7 days) |
|---|---|---|---|
| System | **~371/day** (63–491) | 210 (79 Error, 131 Warning) | SCM #7040 247, FilterManager #6 127, Hyper-V-VmSwitch #291 127, HttpService #112 118, DistributedCOM #10016 103 (Warning) |
| Application | **~356/day** (122–516) | 202 (153 Error, 49 Warning) | Security-SPP #16384/#16394 355 each, RestartManager #10000/#10001 ~146 each, WER #1001 125, SideBySide #33 107 (Error) |
| Defender/Operational | **~76/day** (32–96) | 2 | #1150/#1151 148 each (health heartbeat), #5007 136 (config change), #2010/#2000 (signature updates) |

- **Warning and worse across System+Application: 425 events in 7 days, about 61/day.** Two benign sources make up half of that: DCOM 10016 and SideBySide 33. Microsoft says DCOM 10016 events "can be safely ignored" and are "by design" ([Event ID 10016](https://learn.microsoft.com/en-us/troubleshoot/windows-client/application-management/event-10016-logged-when-accessing-dcom)).
- **Burst:** across all 35 days of System history, the median day had **126** events, but **2026-09-15 had 30,135**, of which **29,884 were Service Control Manager #7031** (a service terminated unexpectedly, in a restart loop). Any default must survive that.
- **Average size:** event XML plus rendered message is about **1,078 characters** (sampled from 300 System events). 800 events/day for 7 days is about 5,600 rows and roughly 6 MB in Turso before compression, which is trivial. A 30k/day burst unfiltered would be about 32 MB/day.
- **Security:** not measurable without elevation. Microsoft rates *Audit Logon* (4624/4625/4648) volume as "Low on a client computer" ([Audit Logon](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/auditing/audit-logon)). *Audit Filtering Platform Connection* (5156/5158 etc.) is "High", one event per connection, and Microsoft recommends against Success auditing for it ([Audit Filtering Platform Connection](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/auditing/audit-filtering-platform-connection)). On an origin host serving web traffic through Caddy, 5156 would be thousands per day if it were ever enabled. **Plan for low hundreds to low thousands per day of 4624/4672 service and system logons. Measure from the elevated agent before finalising.**

### Recommended source filters

Event IDs are only unique **per provider**. On this origin host, ID 1 comes from 5 different providers and 7036 from `VfpExt` as well as SCM. Filters must therefore match `(Provider, EventID)`, not the ID alone. Levels are 1 Critical, 2 Error, 3 Warning, 4 Informational, 5 Verbose ([LevelType](https://learn.microsoft.com/en-us/windows/win32/wes/eventmanifestschema-leveltype-complextype)). Classic providers also log **Level 0**: 137 Application events here had no level name. Use `Level <= 3` and handle 0 deliberately (treat it as Informational), rather than `Level >= 1 and Level <= 3`.

Use a structured XML query, which is required when you have more than 20 expressions or several paths. It supports `<Suppress>` ([Consuming Events](https://learn.microsoft.com/en-us/windows/win32/wes/consuming-events)).

- **System:** `Level <= 3`, plus an Informational allowlist:
  - EventLog 6005/6006/6008 (log start/stop/unexpected shutdown)
  - User32 1074 (who restarted)
  - Kernel-General 12/13 (OS start/stop)
  - Kernel-Power 41
  - SCM 7045 (service installed)
  - WindowsUpdateClient 19/20/43

  Suppress DCOM 10016. Measured cost is about 30/day plus about 25/day.
- **Application:** `Level <= 3`. Suppress SideBySide 33 if it stays noisy. Measured cost is about 29/day.
- **Defender/Operational:** `Level <= 3` plus:
  - 1006/1116/1117 (malware detected/action)
  - 5001 (real-time protection disabled)
  - 5007 (config changed)
  - 2001/2003 (update failures)

  Drop 1150/1151 heartbeats. Cost is a few per day.
- **Security:** an allowlist only, never `*`:
  - 4624 (logon), 4625 (failed logon), 4648 (explicit credentials), 4672 (special privileges)
  - 4720/4722/4725/4726/4738 (account changes)
  - 4732 (added to local group)
  - 1102 (audit log cleared)
  - 4697 (service installed)

  Consider suppressing 4624 for `S-1-5-18` system logons if volume proves high.
- **Rate limit at the agent.** Allow at most N lines per `(channel, provider, id)` per minute (say 20). Fold the excess into one summary log line (`"SCM 7031 repeated 29,864 times between t1 and t2"`). Advance the cursor past the suppressed records anyway. This turns the 30k burst into a few hundred rows.

## 5. Rendering a log line

**Structured fields** come from `EvtRender(NULL, event, EvtRenderEventXml)`, parsed with `encoding/xml`. The `<System>` block carries `Provider@Name`, `EventID`, `Level`, `Task`, `Keywords`, `TimeCreated@SystemTime`, `EventRecordID`, `Channel`, `Computer` and `Security@UserID`, as in this real sample from this origin host:

```xml
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Microsoft-Windows-Kernel-Power' .../><EventID>566</EventID><Version>0</Version><Level>4</Level><Task>268</Task>...<TimeCreated SystemTime='2026-10-19T22:51:12.7403400Z'/><EventRecordID>38309</EventRecordID>...<Channel>System</Channel><Computer>WIN-MAIN-01</Computer><Security UserID='S-1-5-18'/></System><EventData><Data Name='BootId'>23</Data>...</EventData></Event>
```

Alternatively, `EvtCreateRenderContext(EvtRenderContextSystem)` + `EvtRender(EvtRenderEventValues)` returns the system properties as typed variants without XML parsing. That path is faster but needs more unsafe code. XML is fine at about 800 events/day.

**Readable text** comes from `EvtFormatMessage(publisherMetadata, event, 0, 0, NULL, EvtFormatMessageEvent, ...)`. The `PublisherMetadata` handle comes from `EvtOpenPublisherMetadata(NULL, providerName, NULL, locale, 0)` ([EvtFormatMessage](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtformatmessage)).

- **Cache publisher handles** per provider name, as OTel's `publishercache.go` does. Cache "not found" too, because some providers have no metadata.
- Use the two-call sizing pattern: `ERROR_INSUFFICIENT_BUFFER` returns the required size in characters.
- Treat `ERROR_EVT_MESSAGE_NOT_FOUND` / `ERROR_EVT_MESSAGE_ID_NOT_FOUND` as "no message", not as an error ([Formatting Event Messages](https://learn.microsoft.com/en-us/windows/win32/wes/formatting-event-messages)). Fall back to `"<Provider> <EventID>: k=v, k=v"` built from `EventData`.
- `EvtFormatMessageLevel` / `Task` / `Opcode` / `Keyword` give localised names. `EvtFormatMessageXml` returns the event XML with a `RenderingInfo` section containing all strings in one call ([EVT_FORMAT_MESSAGE_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_format_message_flags)). That is a reasonable single-call alternative.
- Messages are multi-line. For example, the Kernel-Power 566 message rendered here as 94 characters over 4 lines. Store the full message but use the **first line** as the log line's `text`, as Telegraf's `only_first_line_of_message` does. Cap rendered size at 64 KB per event (also Telegraf's default).
- Resolving `UserID` SIDs to names (`windows.SID.LookupAccount`) is optional and should be cached. Keep the SID either way.

### Proposed log-line shape (agent → server JSON)

```json
{
  "source": "eventlog",
  "channel": "System",
  "recordId": 38309,
  "time": "2026-10-19T22:51:12.7403400Z",
  "level": 4,
  "levelName": "Information",
  "provider": "Microsoft-Windows-Kernel-Power",
  "eventId": 566,
  "task": 268,
  "keywords": "0x8000000000000604",
  "computer": "WIN-MAIN-01",
  "userSid": "S-1-5-18",
  "user": "NT AUTHORITY\\SYSTEM",
  "text": "The system session has transitioned from 22 to 24.",
  "message": "The system session has transitioned from 22 to 24.\r\n\r\nReason InputAccelerometer\r\n\r\nBootId: 23",
  "data": { "BootId": "23", "Reason": "31", "PreviousSessionId": "22" },
  "xml": "<Event ...>…</Event>"
}
```

The pull response is `{ "lines": [...], "cursors": { "System": 38309, ... }, "gaps": [{ "channel": "System", "from": 1201, "to": 1450, "reason": "wrapped" }] }`. The natural key in Turso is `(channel, recordId)`, which makes re-delivery idempotent (`INSERT OR IGNORE`). This is a second safety net beside the transactional cursor. `xml` and `data` could be dropped after parsing if storage matters. At about 1 KB/event they do not.

## Open points for the map

- Measure Security volume from the elevated agent once it exists, before finalising the Security allowlist.
- Decide whether to enable TaskScheduler/Operational on the origin host. It is off by default, and enabling it is a system change.
- Confirm `EventRecordID` behaviour after a log is cleared (not found in primary docs). The gap logic above handles either outcome.

## Sources

- Microsoft Learn: [EvtSubscribe](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtsubscribe), [EVT_SUBSCRIBE_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_subscribe_flags), [EvtNext](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtnext), [EVT_SEEK_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_seek_flags), [EVT_LOG_PROPERTY_ID](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_log_property_id), [Bookmarking Events](https://learn.microsoft.com/en-us/windows/win32/wes/bookmarking-events), [Consuming Events](https://learn.microsoft.com/en-us/windows/win32/wes/consuming-events), [EvtFormatMessage](https://learn.microsoft.com/en-us/windows/win32/api/winevt/nf-winevt-evtformatmessage), [EVT_FORMAT_MESSAGE_FLAGS](https://learn.microsoft.com/en-us/windows/win32/api/winevt/ne-winevt-evt_format_message_flags), [Formatting Event Messages](https://learn.microsoft.com/en-us/windows/win32/wes/formatting-event-messages), [LevelType](https://learn.microsoft.com/en-us/windows/win32/wes/eventmanifestschema-leveltype-complextype), [Manage auditing and security log](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/security-policy-settings/manage-auditing-and-security-log), [Event Log Readers](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-security-groups#event-log-readers), [Audit Logon](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/auditing/audit-logon), [Audit Filtering Platform Connection](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/auditing/audit-filtering-platform-connection), [Event ID 10016](https://learn.microsoft.com/en-us/troubleshoot/windows-client/application-management/event-10016-logged-when-accessing-dcom).
- Go: [golang.org/x/sys/windows](https://pkg.go.dev/golang.org/x/sys/windows), [google/winops wevtapi](https://pkg.go.dev/github.com/google/winops/winlog/wevtapi), [google/winops winlog.go](https://github.com/google/winops/blob/master/winlog/winlog.go), [OTel stanza windows input](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/pkg/stanza/operator/input/windows), [Telegraf win_eventlog](https://github.com/influxdata/telegraf/blob/master/plugins/inputs/win_eventlog/win_eventlog.go), [Beats wineventlog](https://pkg.go.dev/github.com/elastic/beats/v7/winlogbeat/sys/wineventlog), [gowinlog](https://pkg.go.dev/github.com/ofcoursedude/gowinlog).
- Local, read-only measurements on the origin host: `Get-WinEvent -ListLog`, `Get-WinEvent -FilterHashtable` (last 7 days and the full System history), and the channel `SecurityDescriptor`, all run 2026-10-19 without elevation.
