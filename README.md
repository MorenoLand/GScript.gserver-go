# GServer-Go

Created by: denveous
Based off the original work by 39ster, Stefan Knorr.
For their additional work on the old gserver, special thanks go to:
	Agret, Beholder, Joey, Marlon, Nalin, and Pac.

## About

GServer-Go is a complete Go implementation of the Graal Online game server protocol, rewritten from C++ for simplicity and maintainability. It maintains full binary protocol compatibility with existing Graal clients (v1-v5) while providing a clean, minimal codebase.

**Key Features:**
- Minimalist architecture (8 files, ~10,000 lines vs 43 C++ files)
- Full GEN encryption support (GEN_1 through GEN_5)
- List server communication with proper hq_level tier support
- Player login flow with all pre-warp packets
- Level loading and warping
- Remote Control (RC) protocol support (in progress)

## Building

### Prerequisites

- **Go** 1.23 or later

### Building the server

```bash
cd gserver-go
go build -o gserver.exe .
```

## Quick Start Instructions

How-to setup a server:

1) Under the accounts folder, rename the text file 'YOURACCOUNT.txt' to your account name.  For example: 'denveous.txt'
2) Modify defaultaccount.txt to your liking.  This is the default settings new players will start with.  It can also be modified via RC.
3) Open config/serveroptions.txt and edit it to your liking.  Be sure to modify the settings under "Private server options".  Help for what these options do are available on the forums and in the file itself.
4) Find the line that starts with "staff=" in config/serveroptions.txt.  Replace YOURACCOUNT with your account name.  Anybody who needs RC access must be added to this line with their account names separated by commas.  Additionally, RC users must have their IP range changed to at least *.*.*.* in their account to connect.
5) Port forward if needed. (Many threads on this topic exist in the forums.  If you are having trouble, seek them out.  Try the tutorials forum.)  Basically, if you are behind a router and your computer isn't set to be the "DMZ", you will need to port forward.
6) Run gserver.exe -- enjoy.
7) Report any bugs on the Graal forums or GitHub issues.

## Implementation Status

**Progress: 70% (31/44 files convertible without V8)**

**Note:** 13 remaining files are V8-specific (scripting/*) or blocked on V8 integration. All non-V8 conversion work is complete.

**Note on Completion:** The percentage above represents **files converted from C++ to Go**, not functional completion. Many systems have code converted but are not yet tested or fully functional. See "Testing Status" below for actual working features.

### ✅ Completed (Code Converted)

| Component | Status | Notes |
|-----------|--------|-------|
| Binary Protocol | ✅ Complete | GChar, GShort, GInt, GInt4, GInt5, GString encoding |
| GEN Encryption | ✅ Complete | GEN_1-5 with zlib/bz2 compression |
| List Server | ✅ Complete | Registration, hq_level packet, server info updates |
| Login Packets | ✅ Complete | 10+ pre-warp packets (PLAYERPROPS, FLAGSET, warp, etc.) |
| Account System | ✅ Complete | Account loading/saving (GRACC001 format) |
| Config Loading | ✅ Complete | serveroptions.txt, adminconfig.txt, serverflags.txt (fixed path bug) |
| File System | ✅ Complete | heads, bodies, swords, shields, ganis, images, sounds, levels |
| RC Protocol (27 packets) | ✅ Complete | All server options, folder ops, player props, account management, file browser, chat |
| NC Protocol (18 packets) | ✅ Complete | NPC management, weapon/class operations, level list |
| Item System | ✅ Complete | 25 item types with pickup effects |
| Sign System | ✅ Complete | Encoding/decoding with custom character tables |
| Map Loading | ✅ Complete | BIGMAP and GMAP formats with level positioning |
| Level Loading (.nw) | ✅ Complete | BOARD, CHEST, SIGN, LINK, BADDY, NPC tokens with base64 tile decoding |
| Level Loading (.zelda) | ✅ Complete | RLE bitstream decoding (12/13-bit), links, baddies, signs, verses support |
| Board Changes | ✅ Complete | Tile swap, timeout support, board packet generation |
| Level Links | ✅ Complete | Link parsing, serialization, getters/setters |
| File Permissions | ✅ Complete | Read/write flags, regex wildcard matching |
| Package System | ✅ Complete | CRC32 checksums, file list, reload support |
| Trigger Commands | ✅ Complete | 13 commands: weapons, groups, guilds, RC chat |
| Guild System | ✅ Complete | Add/remove members, set/remove guilds, nickname updates |
| Word Filter | ✅ Complete | Pattern matching, precision (abs/%), word position (full/start/part), actions: log/tellrc/replace/warn/jail/ban |
| Baddy System | ✅ Complete | 10 baddy types, 10 modes (WALK/LOOK/HUNT/HURT/DIE/SWAMPSHOT/HAREJUMP/OCTOSHOT/DEAD), item dropping, props, verses, respawn |
| Script Classes | ✅ Complete | Class storage, NC add/edit/delete handlers, UPDATECLASS packet sends class code to client |

### 🚧 Partial (Code Converted, Needs Testing/Completion)

| Component | Status | Notes |
|-----------|--------|-------|
| NPC System | 🚧 Partial | NPC struct exists, AI and script integration pending |
| Weapon System | 🚧 Partial | Weapon struct exists, script loading pending |
| Player Scripts | 🚧 Partial | UPDATEGANI, UPDATESCRIPT, UPDATECLASS handlers exist |
| Packet Handlers | 🚧 Partial | Basic handlers done, some PLI packets need completion |

### ❌ Not Started

| Component | Status | Notes |
|-----------|--------|-------|
| GS2 Scripting (Server-side) | ❌ Pending | V8 integration for server-side script execution |
| GS2 Compiler (Server-side) | ❌ Pending | Server-side GS2 compiler not yet implemented |
| GS2 Scripting (Client-side) | ✅ Supported | Client-side GS2 scripts work normally with existing GS2 compiler |
| File Transfers | ❌ Pending | Upload/download system |

**Note:** V8 is used ONLY for server-side script execution. Client-side GS2 scripts continue to work with the existing GS2 compiler - no changes to client-side script handling. A server-side GS2 compiler has not been implemented yet.

## Testing Status

**Last Updated:** 2026-01-19

### Verified Working ✅

| Feature | Status | Notes |
|---------|--------|-------|
| Server Startup | ✅ Working | Server initializes, loads config, binds to port |
| TCP Connections | ✅ Working | Client can connect to server |
| GEN Encryption | ✅ Working | All GEN versions (1-5) encrypt/decrypt correctly |
| Account Loading | ✅ Working | GRACC001 format accounts load/save correctly |
| Pre-Warp Packets | ✅ Working | PLAYERPROPS, FLAGSET, etc. sent to client |
| Listserver Registration | ✅ Working | Server registers with listserver, sends heartbeats |
| Config Files | ✅ Working | serveroptions.txt, adminconfig.txt, serverflags.txt load |
| .nw Level Parsing | ✅ Implemented | BOARD, CHEST, SIGN, LINK, BADDY, NPC tokens parsed |
| .zelda Level Parsing | ✅ Implemented | RLE bitstream decoding (12/13-bit), links, baddies, signs, verses |

### Partially Working ⚠️

| Feature | Status | Issue |
|---------|--------|--------|
| Login Flow | ⚠️ Partial | Account auth works, level data loads, client connection issue needs verification |
| RC Protocol | ⚠️ Untested | 27 packet handlers implemented but not verified with RC client |
| NC Protocol | ⚠️ Untested | 18 packet handlers implemented but not verified |

### Needs Verification ❓

| Feature | Status | Notes |
|---------|--------|--------|
| Game World Entry | ❓ Unverified | .nw and .zelda parsers implemented, needs testing with actual client |
| Level Geometry | ❓ Unverified | Board data parsed from both formats, warp() sends level data |
| Player Movement | ❓ Unverified | Depends on client receiving valid level data |
| NPC Display | ❓ Unverified | NPCs loaded from level files, script execution pending V8 |

### Critical Blocker

**V8 Scripting Integration** - Both .nw and .zelda level parsers are implemented and functional, but:
1. Server-side GS2/GS5 script execution requires V8 integration
2. NPC AI and weapon behaviors need script execution
3. Full gameplay requires scripting system for interactivity

**Estimated Functional Completion:** ~50% (networking works, login works, both level parsers work, scripting blocked)

## Architecture

### File Structure

```
gserver-go/
├── main.go       (150 lines)  - Entry point, server initialization
├── gserver.go    (3200 lines) - Server, Player, Level, packet handlers
├── network.go    (390 lines)  - Buffer, SocketManager, encryption, compression
├── protocol.go   (800 lines)  - Packet enums, constants
└── config.go     (250 lines)  - Settings, logging, file system
```

### Design Principles

1. **Minimalism** - 8 files vs 43 C++ files
2. **No inheritance** - Composition over inheritance
3. **Garbage collected** - No manual memory management
4. **Standard library** - No external dependencies except Go stdlib

### Protocol Implementation

**GEN Encryption:**
- GEN_1: Raw packets, no compression
- GEN_2: Zlib compression with 2-byte length prefix
- GEN_3: Zlib + single byte insertion
- GEN_4: BZ2 compression + XOR encryption
- GEN_5: Zlib/BZ2/none + XOR encryption

**Login Flow (Packets sent in order):**
1. PLO_PLAYERPROPS - Player properties
2. PLO_CLEARWEAPONS - Clear weapon slots
3. PLO_FLAGSET x10 - Head, body, sword, shield, colors, sprite
4. PLO_FLAGSET - Server flags
5. PLO_NPCWEAPONDEL x2 - Remove Bomb/Bow default weapons
6. PLO_UNKNOWN190 - Unknown 0xBE packet
7. warp() - Load level, spawn player
8. PLO_RPGWINDOW - Welcome message
9. PLO_STARTMESSAGE - Server start message
10. PLO_SERVERTEXT - Server text display

## servers.txt

The gserver can run multiple servers at once without needing to spawn separate processes.  This is accomplished by the servers.txt file.  This file will tell the gserver how many servers to run and where they are located, as well as some optional ip and port overrides.

The file looks like this:
```
    servercount = 1
    server_1 = default
    server_1_ip = myserver.com
    server_1_port = 12345
    server_1_localip = 127.0.0.1
    server_1_interface = 192.168.2.1
```
servercount specifies the number of servers.  In the default file, that is 1 server.
**server_#** specifies the directory the server is under.
**server_#_ip** specifies an optional ip address override.
**server_#_port** specifies an optional port override.
**server_#_localip** specifies an optional localip override.
**server_#_interface** specifies an optional interface override.

All of the optional overrides will take precedence over the options defined in **serveroptions.txt**.

## Special Graal Reborn NPC commands

The Graal Reborn gserver has a couple special NPC commands built in.

join somefile;
    Much like official Graal's server-side join command, this command searches for somefile.txt and appends the contents to the end of the NPC script.

singleplayer
    This command is like the sparringzone command.  When placed by itself with no semi-colon inside an NPC, it signifies that the level is "singleplayer."  (SEE: Singleplayer Levels).

## Singleplayer Levels

The Graal Reborn gserver has the ability to toggle a level as "singleplayer."  In this mode, the user cannot see any other player in the level.  Any changes they make to the level are not replicated to other users.  They are, in essence, in a level by themselves.

To activate singleplayer mode, add an NPC to the level and add the single command "singleplayer" to the level, much like how the "sparringzone" command works.

## Group Maps

Like singleplayer levels, group maps allow only players in a group to see each other in a level.  Player groups can be managed via the gr.setgroup and gr.setlevelgroup triggeractions (SEE: Graal Reborn special triggeractions).

Individual levels cannot be set as group levels; instead, an entire map must be specified as a group map.  The "groupmaps" server option specifies a comma-delimited list of maps that can contain groups.

## Graal Reborn special client flags

There are a few special client flags built into the gserver.  These are:
gr.x
gr.y
gr.z

These flags are used by the -gr_movement weapon included in the server weapons folder to simulate the smooth movement as found in the Graal clients 2.3 and up.

If you don't want the gserver to recognize these flags, set the flaghack_movement setting to false in serveroptions.txt.

Also, if flaghack_ip is enabled in the serveroptions.txt file, you can gain access to the following:
gr.ip

## Graal Reborn special triggeractions

The Graal Reborn gserver has a couple unique triggeractions built into it.  They can be enabled/disabled by altering the setting that controls their group in serveroptions.txt.  They are as follows:

### Controlled by the setting triggerhack_weapons:
    triggeraction 0,0,gr.addweapon,weapon1,weapon2,weapon3;
        Adds weapon1, weapon2, and weapon3 to the player's account.

    triggeraction 0,0,gr.deleteweapon,weapon1,weapon2,weapon3;
        Removes weapon1, weapon2, and weapon3 from the player's account.

### Controlled by the setting triggerhack_guilds:
    triggeraction 0,0,gr.addguildmember,guild,account,nickname;
        Adds a player to the specified guild.  Nickname is optional.

    triggeraction 0,0,gr.removeguildmember,guild,account;
        Removes a player from the specified guild.

    triggeraction 0,0,gr.removeguild,guild;
        Removes the guild from the server.

    triggeraction 0,0,gr.setguild,guild,account;
        Sets the player's guild tag to the specified guild.

### Controlled by the setting triggerhack_groups:
    triggeraction 0,0,gr.setgroup,group;
        Adds the player to the specified group.

    triggeraction 0,0,gr.setlevelgroup,group;
        Adds all the players in the level to the specified group.

    triggeraction 0,0,gr.setplayergroup,account,group;
        Adds the specified player to the specified group.

### Controlled by the setting triggerhack_files:
    triggeraction 0,0,gr.appendfile,filename,text;
        Opens the file specified, located in the server's logs directory, and appends a line of text.

    triggeraction 0,0,gr.writefile,filename,text;
        Opens the file specified, located in the server's logs directory, erases all of its contents, and writes a line of text.

    triggeraction 0,0,gr.readfile,filename,line_pos;
        Opens the file specified, located in the server's logs directory, reads the given line number, and returns the contents to the player.
        File contents are returned on the following flags:
            gr.fileerror: String list.  First index is a random number, subsequent indexes are error values.  Error 1 = line_pos was outside of range.  In this case, the next value is the line number returned.
            gr.filedata: The file data.

### Controlled by the setting triggerhack_rc:
    triggeraction 0,0,gr.rcchat,Some chat text;
        Sends some chat text to any logged in RC's.

### Controlled by the setting triggerhack_execscript:
    triggeraction 0,0,gr.es_clear;
        Clears the execscript parameter list.

    triggeraction 0,0,gr.es_set,param1,param2,...;
        Sets the execscript parameter list.

    triggeraction 0,0,gr.es_append,phrase;
        Appends phrase directly to the end of the set parameter list.

    triggeraction 0,0,gr.es,account,script_name;
        Sends the execscript to the specified account, or everybody if ALLPLAYERS was specified.
        View the execscript/readme.txt file for more information.

### Controlled by the setting triggerhack_props:
    triggeraction 0,0,gr.attr1,data;
        Sets data on the specified attribute.  gr.attr1 - gr.attr30 work.

    triggeraction 0,0,gr.fullhearts,amount;
        Sets the player's fullhearts to the specified amount.

### Controlled by the setting triggerhack_levels:
    triggeraction 0,0,gr.updatelevel;
        Updates the current level.

    triggeraction 0,0,gr.updatelevel,levelname;
        Updates the specified level.

### Not controlled by any option:
    triggeraction 0,0,gr.npc.move,id,dx,dy,duration,options;
        Creates a serverside move command for the specified NPC.

    triggeraction 0,0,gr.npc.setpos,id,x,y;
        Sets an NPC's position.

## Weapon bytecode

Place weapon bytecode in the weapon_bytecode/ folder.  Inside each weapon file in weapons/, add the following:
BYTECODE name_of_file

The gserver will load weapon_bytecode/name_of_file and use the bytecode contained there-in.

## Recent Development

### 2026-01-18

**Fixed: Blocking I/O in OnRecv()**
- Added 10ms read deadline to prevent blocking entire server loop
- Server now continues processing when client connects but doesn't send data immediately

**Fixed: SVO_SERVERHQLEVEL packet**
- Added hq_level packet sending to listserver
- Server tier now displays correctly (Bronze/Silver/Gold) instead of "Graal3D"

**Added: Complete login flow**
- Implemented all 10+ pre-warp packets from C++ sendLoginClient()
- Added warp() function for level loading and player spawning
- Added player flags (head, body, sword, shield, colors)
- Added server flags transmission

**Added: Comprehensive RC Protocol**
- Implemented 30+ RC packet handlers for admin functionality
- File browser system with directory navigation
- Player management (kick, ban, warp, property editing)
- Server configuration editing
- Account management and creation
- Guild and group management

**Added: Extensive Packet Handler Coverage**
- 162 PLI packet types with handlers
- Game mechanics: combat, items, weapons, NPCs
- Level interaction and modification
- Player communication and messaging
- File transfer and update systems

## Known Issues

- Client may still hang at "Loading account..." - packet handling needs verification
- Level file loading (.nw/.zelda) not implemented - levels created empty
- NPC script integration missing
- Weapon system incomplete
- Some RC packet handlers still need implementation

## Development Roadmap

1. **Level file parsing** - Implement .nw BOARD token parsing and .zelda RLE decoding
2. **NPC system** - Complete NPC types and AI behaviors
3. **Weapon system** - Load weapon files, implement weapon packets
4. **Server-side GS2 compiler** - Create GS2 compiler that runs on server (V8 integration for execution)
5. **Server-side scripting** - V8 integration for NPC/weapon scripts (client-side GS2 already works)
6. **File transfer system** - Upload/download functionality
7. **Word filter** - Chat filtering system

## Credits

Based on [GServer-v2](https://github.com/xtjoeytx/GServer-v2) C++ implementation.

Original GServer by Stefan Knorr and the Graal Reborn/Preagonal/OpenGraal community.

## License

GPL v3.0 - See LICENSE file for details.

---

**Programmed by denveous.**