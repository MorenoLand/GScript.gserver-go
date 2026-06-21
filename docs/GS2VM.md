# GS2 VM

Server-side GS2 runtime notes for the NPC-server-backed VM. This document tracks implemented behavior and parity gaps only.

## Contents

- [Script Splitting And Persistence](#script-splitting-and-persistence)
- [Event Runtime](#event-runtime)
- [Globals And Host Functions](#globals-and-host-functions)
- [Player Lookup And PMs](#player-lookup-and-pms)
- [Triggers](#triggers)
- [NPC-Server Lifecycle](#npc-server-lifecycle)
- [Errors](#errors)
- [Parity Gaps](#parity-gaps)

## Script Splitting And Persistence

- `//#CLIENTSIDE` splits one weapon or NPC script into server-side and client-side portions.
- The server-side VM runs only the text before `//#CLIENTSIDE`.
- The client compiler and bytecode sender use the client-side text starting at `//#CLIENTSIDE`.
- NC Apply saves the submitted script text exactly as sent, including indentation, blank lines, spacing, and section marker placement.
- Reapplying a weapon/class/NPC script resets that script's `this.` state and recompiles/sends client bytecode where applicable.
- Player login compiles and sends weapons the player already has when the NPC-server is online.
- `join className;` and `join("className");` append server-side class code into a weapon or DB NPC runtime.
- Classes do not execute on their own; class code runs only through a weapon or DB NPC that joins it.

## Event Runtime

- Event handlers execute with `this` bound to the owning script instance.
- `this.` values persist across events until the script instance is reloaded.
- `temp.` values are event-frame locals and do not survive into the next event.
- `temp.foo = value;` also exposes `foo` as a bare alias in the same event frame.
- Function parameters declared as `temp.name` are locals and should also be visible as `name`.
- `params` is the current trigger/event argument list.
- `params[0]` and normal indexed access work.
- Event lookup is case-insensitive for `on...` handlers.
- `onCreated()` runs when a weapon or DB NPC script is applied/updated.
- `onInitialized()` runs when the NPC-server starts.
- `onPlayerLogin()` and `onPlayerLogout()` run for active weapon and DB NPC server-side scripts.
- `SPC` concatenates values with a single space.
- `@` concatenates values without adding a space.
- `@=` appends to strings.
- `TAB` and `NL` are available as tab and newline constants.
- Basic `enum { A, B, C }` blocks are translated to numeric constants starting at `0`.
- Simple GS2 array assignment literals such as `this.health = {5, 7};` are supported.
- `new[size]` array construction is supported.
- Classic `for (...)` loops and GS2 foreach loops using `for (temp.item : list)` or `for (temp.item in list)` are supported.
- `.size()` is translated to the host array/string length property.
- Comma-separated server option values are exposed as list-like values for indexed access.

## Globals And Host Functions

Implemented globals:

- `name`
- `params`
- `temp`
- `this`
- `player`
- `client`
- `clientr`
- `server`
- `serverr`
- `serveroptions`
- `screenwidth`
- `screenheight`

Implemented global functions:

- `echo(value...)`
- `base64encode(value)`
- `base64decode(value)`
- `findplayer(value)`
- `triggerclient(type, target, args...)`
- `showimg(index, image, x, y)`
- `findimg(index)`
- `getimgwidth(image)`
- `getimgheight(image)`
- `loadstring(filename)`
- `loadlines(filename)`
- `savestring(filename, value, mode)`
- `savelines(filename, lines, mode)`

Implemented object behavior:

- `player.client.*` and bare `client.*` both refer to client flags on the current player.
- `player.clientr.*` and bare `clientr.*` both refer to clientr flags on the current player.
- Assigning `client.` or `clientr.` updates the player's flags and queues the matching flag update packet.
- `server.` and `serverr.` expose server flags and assignments update the gserver flag table.
- `serveroptions.` exposes server options by option name as read-only VM input.
- `showimg()` creates or updates an image object for the current VM run.
- `findimg()` returns that image object or `null`.
- Image objects currently expose at least `index`, `image`, `x`, `y`, and `rotation`.
- String values support `.savestring(filename, mode)` and `.loadstring(filename)`.
- Array values support `.savelines(filename, mode)` and `.loadlines(filename)`.

## Player Lookup And PMs

- `findplayer(str)` returns a player object or `null`.
- Lookup accepts account name, current nickname, and PCID-style guest identities such as `pc:763`.
- Lookup can see normal game clients, eligible RC/NC control connections, and the NPC-server where applicable.
- Lookup is case-insensitive.
- Player objects expose `account`, `nick`, `nickname`, `level`, `client`, `clientr`, `sendpm()`, and `sendplayer()`.
- `sendpm(str)` sends a PM to the player object it is called on.
- `sendplayer(str)` is treated as the compatible alias.
- `setlevel(level)` warps the player object to a level.
- `setlevel2(level, x, y)` warps the player object to a level at tile coordinates.
- Bare `setlevel(level)` and `setlevel2(level, x, y)` target the player that triggered the current server-side event.
- Bare `addweapon(name)` and `removeweapon(name)` target the player that triggered the current server-side event.
- Player objects returned by `findplayer()` support `addweapon(name)` and `removeweapon(name)`.
- PMs sent from server-side scripts are sent as NPC-Server messages.

Supported forms:

- `temp.pl = findplayer("SomePlayer"); temp.pl.sendpm("hey");`
- `temp.pl = findplayer("SomePlayer"); pl.sendpm("hey");`
- `findplayer("SomePlayer").sendpm("hey");`

## Triggers

- `triggerServer("gui", name, args...)` and `triggerServer("weapon", name, args...)` arrive as client triggeraction packets.
- Server-side dispatch routes those to the matching weapon/GUI/NPC script.
- Server-side weapon trigger handlers accept capitalization variants such as `onActionServerside` and `onActionServerSide`.
- Trigger args are exposed through `params`.
- `triggerclient(type, target, args...)` queues a client-side trigger packet back to the intended player.
- The same client action should only run the server-side handler once.

## NPC-Server Lifecycle

- Server-side GS2 execution belongs to the NPC-server module.
- If the NPC-server is shut down or disconnected, server-side GS2 execution stops.
- If the NPC-server is shut down or disconnected, client bytecode compiling/delivery for weapons stops.
- `/npcshutdown` saves map NPCs, stops watchers, removes the NPC-Server player, and disables script runtime side effects.
- `/npckill` immediately disables the NPC-server runtime path.
- Bringing the NPC-server back online restores compile/delivery/runtime behavior and updates the NPC-Server player state.

## Errors

- NC-facing GS2 errors use the same style family as compiler feedback.
- Runtime errors are normalized before reaching RC.
- Host-language type errors should not leak directly to RC.
- Full bytecode blobs, huge hex dumps, and raw VM exception payloads are debug-only.

Preferred style:

- `Compiler error for Weapon -gr_movement:`
- `error: Cannot read property 'sendpm' of undefined or null at line 5`

## Parity Gaps

- More player object properties: position, chat, hearts, rupees, bombs, arrows, head, body, sword, shield, gani, attr, and account metadata.
- More player methods: freeze/unfreeze, rights helpers, and scripted stat/item helpers.
- Broader class and NPC-db state.
- Timers, waits, `scheduleevent`, and delayed event dispatch.
- `getstringkeys()` for persistent variable prefixes.
- Full drawing/image object parity beyond the current lightweight `showimg`/`findimg` object.
- Full trigger target routing beyond the currently implemented player return path.
- Broader GS2 collection/object edge cases beyond simple assignment literals.
