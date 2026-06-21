# GS2 VM Globals And API

## Core Globals

- `name`
- `params`
- `temp`
- `this`
- `player`
- `client`
- `clientr`
- `chat`
- `server`
- `serverr`
- `serveroptions`
- `allplayers`
- `weapons`
- `screenwidth`
- `screenheight`
- `TAB`
- `NL`
- `NULL`

`TAB` and `NL` also work as GS2 concat tokens between expressions.

## Core Functions

- `echo(value...)`
- `trace(value...)`
- `int(value)`
- `float(value)`
- `double(value)`
- `strtofloat(value)`
- `abs(value)`
- `ceil(value)`
- `floor(value)`
- `sin(value)`
- `cos(value)`
- `tan(value)`
- `random(min, max)`
- `char(code)`
- `strlen(value)`
- `hideimgs(start, count)`
- `keycode(code)`
- `isObject(value)`
- `strequals(left, right)`
- `strcontains(value, search)`
- `contains(value, search)`
- `startswith(value, prefix)`
- `endswith(value, suffix)`
- `uppercase(value)`
- `lowercase(value)`
- `replacetext(value, search, replacement)`
- `toJson(value)`
- `base64encode(value)`
- `base64decode(value)`
- `openurl(value)`
- `sleep(value)`

## Class And Scheduling Functions

- `loadclass(name)` is accepted as a no-op runtime call.
- `join(name)` and `leave(name)` are accepted as no-op runtime calls after server-side class expansion has already happened; `this.join(name)` and `this.leave(name)` are accepted aliases.
- `scheduleevent(delay, event)` queues a delayed event; `scheduleEvent(...)` and `this.scheduleEvent(...)` are accepted aliases.

## Player Functions

- `findplayer(value)`
- `setlevel(level)`
- `setlevel2(level, x, y)`
- `addweapon(name)`
- `removeweapon(name)`
- `sendpm(account, message)`
- `sendplayer(account, message)`

Bare `setlevel`, `setlevel2`, `addweapon`, and `removeweapon` target the player that triggered the current server-side event.
Bare `sendpm` and `sendplayer` route to the supplied account.

## Player Objects

Player objects are returned by `findplayer()` and appear in `player` and `allplayers`.

Supported fields:

- `account`
- `nick`
- `nickname`
- `level`
- `client`
- `clientr`

Supported methods:

- `sendpm(message)`
- `sendplayer(message)`
- `setlevel(level)`
- `setlevel2(level, x, y)`
- `addweapon(name)`
- `removeweapon(name)`

`sendplayer()` is treated as a compatible alias for `sendpm()`.

Supported lookup keys:

- account name
- current nickname
- PCID-style guest identity such as `pc:763`

Supported call forms:

- `temp.pl = findplayer("moondeath"); temp.pl.sendpm("hey");`
- `temp.pl = findplayer("moondeath"); pl.sendpm("hey");`
- `findplayer("moondeath").sendpm("hey");`

## Player Flags

- `player.client.flag`
- `player.clientr.flag`
- `client.flag`
- `clientr.flag`

Assignments to `client.` and `clientr.` update the owning player's flags and queue matching flag updates for the gserver bridge.

## Server Flags

- `server.flag`
- `serverr.flag`

Assignments update server flags and deletes remove server flags.
Comma-separated flag values are exposed as indexed values, and `true`/`false` parts are typed as booleans.

Examples:

- `server.foo = "bar";`
- `serverr.secret = true;`
- `serverr.poopybutthole[0] == true`
- `delete server.oldflag;`

## Server Options

- `serveroptions.optionname`

Server options are exposed read-only.

Comma-separated option values are exposed as list-like values for indexed access.

Example:

- `echo(serveroptions.staff[1]);`

## Player And Weapon Lists

- `allplayers` is an array of player objects visible to the VM.
- `weapons` is an array of weapon objects.

Weapon object fields:

- `name`
- `image`

## Triggers

- `triggerclient(type, target, args...)`

The VM queues a client trigger result for the gserver bridge.

Client `triggerServer("gui", name, args...)` and `triggerServer("weapon", name, args...)` arrive through triggeraction handling and dispatch to matching server-side script events.

Level NPC triggeractions dispatch to server-side NPC events. Generic actions dispatch to `onAction<Name>`. Mouse actions dispatch to the Graal event names: `onActionLeftMouse`, `onActionRightMouse`, `onActionMiddleMouse`, and `onActionDoubleMouse`.

## Drawing Functions

- `showimg(index, image, x, y)`
- `findimg(index)`
- `getimgwidth(image)`
- `getimgheight(image)`

Image objects currently expose:

- `index`
- `image`
- `x`
- `y`
- `rotation`

## File Functions

- `loadstring(filename)`
- `loadlines(filename)`
- `savestring(filename, value, mode)`
- `savelines(filename, lines, mode)`
- `findfiles(pattern, recursive)`

Save mode accepts overwrite by default and append when mode is `1`, `true`, or `append`.

File operations are rooted to the configured VM file root and reject absolute paths or paths escaping that root.

## NPC Functions

- `showcharacter()`
- `setshape(shapeType, width, height)`
- `setshape2(width, height, tileTypes)`
- `warpto(level, x, y)`
- `move(dx, dy, time, options)`
- `hide()`
- `show()`
- `destroy()`
- `dontblock()`
- `blockagain()`
- `drawoverplayer()`
- `drawunderplayer()`
- `drawaslight()`
- `canbecarried()` / `cannotbecarried()`
- `canbepulled()` / `cannotbepulled()`
- `canbepushed()` / `cannotbepushed()`
- `canwarp()` / `canwarp2()` / `cannotwarp()`

These only emit NPC actions when the VM run has an NPC ID.

Current NPC scripts can also set `this.image`, `this.chat`, `this.dir`, `this.ani`, `this.nick`, `this.head`, `this.headimg`, `this.body`, `this.bodyimg`, `this.shieldimg`, `this.horseimg`, `this.hearts`, `this.gralats`, `this.arrows`, `this.bombs`, `this.darts`, `this.glovepower`, `this.shieldpower`, `this.ap`, and `this.colors[0..4]`. Bare `chat`, `image`, and the same property names are collected for the current NPC too.

`showcharacter()` marks the NPC as character-style. Character NPCs use the same visible character fields as players: nick, head/body/shield images, direction, gani, and color indexes.

## TSocket Functions And Objects

- `new TSocket(name)`
- socket `.bind(port, ssl)`
- socket `.send(data)`
- socket `.close()`
- socket `.destroy()`
- socket `.join(name)`
- bare `send(data)` in socket events
- bare `close()` in socket events

Socket object fields:

- `name`
- `objecttype`
- `address`
- `error`
- `ipaddress`
- `isconnected`
- `port`
- `parent`
- `data`
- `packagedelimiter`
- `enablessl`

Socket event globals:

- `outdatalength`
- `isconnected`

Supported socket events are routed by dotted function names, including:

- `SocketName.onBind`
- `SocketName.onBindFailed`
- `SocketName.onNewClient`
- `SocketName.onReceiveDataPackage`
- `SocketName.onDisconnect`

## Compatibility No-Ops

These functions exist so scripts can run while host behavior is implemented elsewhere or intentionally ignored:

- `loadclass`
- `join`
- `leave`
- `openurl`
- `Adventure_setAllowedPortsBind`
- `sleep`
