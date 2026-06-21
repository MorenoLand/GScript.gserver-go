# GS2 VM GServer Integration

## Script Sources

The gserver feeds the native VM from:

- weapon scripts
- DB NPC scripts
- level NPC scripts
- joined server-side class scripts
- socket event scripts

## Class Expansion

- `join className;` is detected before VM execution.
- `join("className");` is detected before VM execution.
- Joined class server-side text is expanded into the owning script runtime.
- Classes do not execute by themselves.

## Runtime Result Handling

The VM returns structured results. The gserver applies them after execution.

Handled result groups:

- NC/RC echo output
- client triggers
- player flag updates
- server flag updates
- player PMs
- add/remove weapon actions
- player warps
- NPC actions
- socket actions
- socket context updates
- scheduled events
- exported `this.` state

## Client Triggers

- `triggerclient(type, target, args...)` emits a trigger result.
- The gserver converts that result into the client trigger packet format.
- Weapon and GUI targets are routed back to the player that triggered the server-side event.

## Player Actions

- `sendpm()` and `sendplayer()` send NPC-server PMs.
- `setlevel()` and `setlevel2()` call the player warp path.
- `addweapon()` updates the player account weapon list and sends the weapon.
- `removeweapon()` removes the account weapon and sends deletion.
- `client.` and `clientr.` mutations are collected and sent back to players.

## Server Flags

- `server.` and `serverr.` mutations update the gserver server flag table.
- Deleting a flag removes it from the server flag table.
- Updated server flags are broadcast to connected players through the gserver flag packet path.

## NPC Actions

- `setshape()` updates NPC rectangular blocking data.
- `setshape2()` updates NPC tile-shape data.
- Setting `chat` updates the NPC chat/message property.
- `showcharacter()` switches the NPC to character-style rendering and sends character props.
- `warpto(level, x, y)` attaches the NPC to the target level and sends NPC prop updates.
- Level NPC triggeractions map `leftmouse`, `rightmouse`, `middlemouse`, and `doublemouse` to `onActionLeftMouse`, `onActionRightMouse`, `onActionMiddleMouse`, and `onActionDoubleMouse`.

## Socket Actions

- `new TSocket(name)` creates VM-side socket intent.
- `.bind()` requests a server-managed listener.
- `.send()` writes to accepted socket clients.
- `.close()` and `.destroy()` request socket shutdown.
- Accepted clients call back into dotted socket event functions.
- Socket object state is preserved through socket context updates.

## Scheduled Events

- `scheduleevent(delay, event)` queues a delayed VM event; `scheduleEvent(...)` and `this.scheduleEvent(...)` are accepted aliases.
- Event names without `on` are resolved against matching `on...` handlers.
- Scheduled events reuse the script type, script name, source, player context, NPC ID, and exported `this.` state from the original run.
- Applying or reloading a weapon/NPC script invalidates previously scheduled events from the old script runtime.

## Lifecycle Rules

- Server-side VM execution is gated by the NPC-server runtime.
- If the NPC-server is offline, server-side script execution stops.
- If the NPC-server is offline, weapon bytecode compiling and delivery stop.
- Restarting the NPC-server runs startup initialization for active server-side scripts.

## Error Output

Runtime errors are normalized before RC output.

Preferred RC style:

- `Compiler error for Weapon -gr_movement:`
- `error: Cannot read property 'sendpm' of undefined or null at line 5`

Debug-only output includes raw bytecode, raw packet dumps, and huge hex dumps.
