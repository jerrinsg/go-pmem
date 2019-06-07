## Introduction
This document details the design of the undo log used in the runtime package to
enable swizzling support.

The following image shows the layout of the arena header which is placed in the
beginning of each arena.

<img src="img/arenaLayout.svg" alt="Arena layout" height=800>

`numLogEntries` and the `log entry` data members help implement a minimal
per-arena undo log. `numLogEntries` stores how many entries in the data section
is valid. The maximum number of entries supported is `maxLogEntries`, whose
value currently is two.

Each data item stores the logged value as a signed
integer and its address as a `uintptr`. Since arena map address can change on
each invocation of the application, rather than storing the absolute address of
each data item, the offset of the data from the beginning of the arena header
is stored.

## The log APIs are:

```func (pa *pArena) logEntry(addr unsafe.Pointer)```

Function to log an item in the arena header. Each arena supports logging up
to `maxLogEntries` number of entries.

```func (pa *pArena) revertLog()```

Copies all logged data back back to its original location in memory. Once all
data items have been copied, it sets `numLogEntries` in the arena header as 0.

```func (pa *pArena) resetLog()```

Discards all log entries without copying any data. It does this by setting
`numLogEntries` as 0 without zeroing any of the data present in the log.

```func (pa *pArena) commitLog()```

Discards the log entries by setting numLogEntries as 0. It also flushes the
persistent memory addresses into which new data was written.