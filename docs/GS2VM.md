# GS2 VM Documentation

Central documentation for the native Go GS2 VM.

This documents implemented behavior only. Planned parity work belongs in issues or implementation notes, not in this support matrix.

## Core Docs

- [Syntax And Runtime](GS2VM_SYNTAX.md): parser/translator behavior, event state, loops, arrays, strings, and GS2 concat tokens.
- [Globals And API](GS2VM_API.md): globals, host functions, player objects, server flags, file helpers, NPC helpers, and TSocket support.
- [GServer Integration](GS2VM_GSERVER.md): how VM results are applied by the Go GServer and NPC-server runtime.

## Quick Map

| Area | Doc |
| --- | --- |
| `//#CLIENTSIDE`, `this.`, `temp.`, `params` | [Syntax And Runtime](GS2VM_SYNTAX.md) |
| `SPC`, `TAB`, `NL`, `@`, `@=` | [Syntax And Runtime](GS2VM_SYNTAX.md) |
| arrays, foreach, dynamic calls, enums | [Syntax And Runtime](GS2VM_SYNTAX.md) |
| `player`, `client`, `clientr`, `server`, `serverr`, `serveroptions` | [Globals And API](GS2VM_API.md) |
| `findplayer`, `sendpm`, `setlevel2`, `addweapon` | [Globals And API](GS2VM_API.md) |
| `loadstring`, `loadlines`, `savestring`, `findfiles` | [Globals And API](GS2VM_API.md) |
| `setshape`, `setshape2`, `move`, `warpto`, `triggerclient` | [Globals And API](GS2VM_API.md) |
| socket bind/send/events | [Globals And API](GS2VM_API.md) |
| script loading, class expansion, result application | [GServer Integration](GS2VM_GSERVER.md) |

## Runtime Scope

- Runs server-side script text before `//#CLIENTSIDE`.
- Backs weapon, DB NPC, level NPC, and socket event execution.
- Produces host actions for player flags, server flags, PMs, weapon add/remove, player warps, NPC updates, client triggers, sockets, and scheduled events.
- Client-side bytecode compilation and delivery are separate from this VM.

## Boundary

- This VM is a compatibility runtime for server-side GS2 behavior used by the Go GServer.
- It is backed by goja and translates the supported GS2 syntax into executable JavaScript.
- It is not a full Graal client VM.
