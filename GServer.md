# GServer Protocol & Implementation Guide

**A complete, language-agnostic specification for implementing a game server.**

Derived from reverse-engineering the GServer-v2 C++ codebase (OpenGraal/GS2Emu).

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Connection & Transport Layer](#2-connection--transport-layer)
3. [Login / Handshake Protocol](#3-login--handshake-protocol)
4. [Encryption Generations](#4-encryption-generations)
5. [Wire Format: GEncoding (CString)](#5-wire-format-gencoding-cstring)
6. [Packet Framing](#6-packet-framing)
7. [Client→Server Packet IDs (PLI)](#7-clientserver-packet-ids-pli)
8. [Server→Client Packet IDs (PLO)](#8-serverclient-packet-ids-plo)
9. [Player Properties System](#9-player-properties-system)
10. [NPC Properties System](#10-npc-properties-system)
11. [Level / Board / Tile System](#11-level--board--tile-system)
12. [Flags System](#12-flags-system)
13. [Weapons System](#13-weapons-system)
14. [Map / GMAP / BigMap System](#14-map--gmap--bigmap-system)
15. [RC (Remote Control) Protocol](#15-rc-remote-control-protocol)
16. [NPC Server (NC) Protocol](#16-npc-server-nc-protocol)
17. [List Server Protocol](#17-list-server-protocol)
18. [File Transfer Protocol](#18-file-transfer-protocol)
19. [requestText / sendText System](#19-requesttext--sendtext-system)
20. [Account / Save Format](#20-account--save-format)
21. [Special Triggeractions](#21-special-triggeractions)
22. [Server Options Reference](#22-server-options-reference)
23. [File Formats](#23-file-formats)
24. [Critical Implementation Notes](#24-critical-implementation-notes)

---

## 1. Architecture Overview

A GServer has four logical connections:

| Component | Purpose |
|---|---|
| **Player Socket** | TCP listener on `serverport` (default 14802). Accepts game clients, RC, NC. |
| **NPC Server** | Optional V8/JavaScript NPC scripting engine. Runs on a separate port. Players told its address via `PLO_NPCSERVERADDR`. |
| **List Server** | Persistent TCP connection to a central list server for account verification, server listing, profiles, guilds. |
| **GS2 Compiler** | Compiles GS2 scripts to bytecode via an external WASM/native compiler process. |

### Player Types (bitmask)
```
PLTYPE_AWAIT     = -1       // Pre-login, unauthenticated
PLTYPE_CLIENT    = 1 << 0   // Old client (v1.x-2.18)
PLTYPE_RC        = 1 << 1   // Remote Control (admin)
PLTYPE_NPCSERVER = 1 << 2   // NPC Server internal player
PLTYPE_NC        = 1 << 3   // NPC Control client
PLTYPE_CLIENT2   = 1 << 4   // New client (v2.19-2.21, v3)
PLTYPE_CLIENT3   = 1 << 5   // New client (v2.22+)
PLTYPE_RC2       = 1 << 6   // New RC (v2.22+)
PLTYPE_EXTERNAL  = 1 << 7   // IRC/external
PLTYPE_WEB       = 1 << 8   // Web client (WebSocket)

PLTYPE_ANYCLIENT = CLIENT | CLIENT2 | CLIENT3 | WEB
PLTYPE_ANYRC     = RC | RC2
PLTYPE_ANYNC     = NC
PLTYPE_ANYPLAYER = ANYCLIENT | ANYRC
```

### ID Ranges
- Player IDs: start at **2** (0 and 1 are invalid)
- NPC IDs: start at **10001**
- External player IDs: start at **16000** (players from other servers, IRC channels)

---

## 2. Connection & Transport Layer

The server listens on a single TCP port. All client types connect to the same port.

### WebSocket Support
If `WOLFSSL_ENABLED` is compiled in, the server also accepts WebSocket connections:
1. Client sends HTTP `GET /` with `Sec-WebSocket-Key` header
2. Server computes: `base64(sha1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))`
3. Server replies with HTTP 101 Switching Protocols
4. All subsequent data uses WebSocket binary framing

If no WebSocket key is present, serve a simple HTML welcome page.

---

## 3. Login / Handshake Protocol

The very first packet from any client is **unencrypted, uncompressed** and terminated by `\n`.

```
First packet = type_char\n
```

The `type_char` determines the client type and encryption generation:

| type_char | Type | Encryption Gen |
|---|---|---|
| 0 | CLIENT (old) | GEN_2 (zlib only) |
| 1 | RC | GEN_3 |
| 2 | NPCSERVER | GEN_3 |
| 3 | NC | GEN_3 |
| 4 | CLIENT2 (2.19-2.21) | GEN_4 |
| 5 | CLIENT3 (2.22+) | GEN_5 |
| 6 | RC2 (2.22+) | GEN_5 |
| 8 | WEB | GEN_1 (no encryption) |

### Full Login Packet Structure (after type_char)

For **CLIENT type** (type 0, v1.3):
```
CHAR type                    // Always 0
CHAR[8] version              // 8-char version string (e.g. "v2.31\0\0\0")
```
If version is unrecognized, server re-reads with GEN_3 format:
```
CHAR[8] old_version          // Re-read 8 chars
```

For **CLIENT2/CLIENT3/RC2** (types 4, 5, 6) and some RC:
```
CHAR type
CHAR iterator_key            // Single byte encryption key
CHAR[8] version              // 8-char version string
CHAR account_len
CHAR[account_len] account
CHAR password_len
CHAR[password_len] password
STRING identity              // Platform identity: "win,md5hash,md5hash,6.2 9200"
```

For **NC** (type 3):
```
CHAR type
CHAR[8] version
CHAR account_len
CHAR[account_len] account
CHAR password_len
CHAR[password_len] password
```

### Server Login Response

After verifying credentials via the List Server:

```
PLO_SIGNATURE(25) CHAR 73    // 73 = >8 players supported
```

For login servers:
```
PLO_FULLSTOP(176)
PLO_GHOSTICON(174) CHAR 1
```

For clients with NPC server:
```
PLO_HASNPCSERVER(44)          // Prevents client from modifying NPC props
PLO_UNKNOWN168(168)
```

For game clients, the server then sends:
1. Player's login properties (see [Player Properties](#9-player-properties-system))
2. Server flags
3. Player's flags
4. Weapon list
5. Class list (v4+)
6. Level data
7. RPG welcome message
8. Server message

For RC/RC2, the server does **not** run the game-world login tail and does not warp the
connection into a level. RC2 uses the same keyed GEN_5 framing as CLIENT3. The RC login
tail should stay control-oriented, such as `PLO_RC_MAXUPLOADFILESIZE` and
`PLO_RC_CHAT` welcome/status lines, followed by a one-way player list from online game clients to the RC.
RC/RC2 connections are not iterable world players: do not send them to game clients as
physical level occupants. They should still be visible in player lists/listserver player
rows unless a server option explicitly hides staff. The pseudo `(npcserver)` player is
always listed and should never be hidden by staff visibility options.
`PLO_RC_ADMINMESSAGE` and `PLO_RC_CHAT` carry raw text after the packet id. Do not encode
either as a GString or String8; RC clients display those length bytes as stray characters.
Avoid `PLO_RC_ADMINMESSAGE` for normal RC login/status text because clients label it as an
admin message. `PLO_RC_CHAT` should be used for RC login/status text and relayed back to
connected RCs as raw text. If an RC chat message begins with `/`, treat it as an internal
RC command and do not relay it as chat. Known commands include `/help`, `/open`,
`/openacc`, `/opencomments`, `/openban`, `/openrights`, and `/reset`.

---

## 4. Encryption Generations

| Gen | Name | Compression | Encryption | Used By |
|---|---|---|---|---|
| 0 (GEN_1) | None | None | None | Web clients |
| 1 (GEN_2) | Zlib | Zlib | None | Old clients (v1.x-2.18) |
| 2 (GEN_3) | Zlib | Zlib | Single-byte insertion per-packet | RC, NC, v1.41-2.18 |
| 3 (GEN_4) | BZ2 | BZ2 | XOR with iterator (limited) | v2.19-2.21 |
| 4 (GEN_5) | Zlib/BZ2/None | Dynamic per-packet | XOR with iterator (limited) | v2.22+ |

### GEN_3 Decryption (Single-byte insertion)
```python
iterator *= 0x8088405
iterator += key
pos = (iterator & 0xFFFF) % buffer_length
remove byte at position `pos`
```

### GEN_4 / GEN_5 Decryption (XOR)
```python
# Iterator starts at 0x04A80B38 (GEN_4/GEN_5)
iterator_bytes = iterator.to_bytes(4, 'little')

for i in range(len(buffer)):
    if i % 4 == 0:
        if limit == 0: break
        iterator = (iterator * 0x8088405 + key) & 0xFFFFFFFF
        if limit > 0: limit -= 1
        iterator_bytes = iterator.to_bytes(4, 'little')
    buffer[i] ^= iterator_bytes[i % 4]
```

### GEN_5 Compression Type
The first byte of the decrypted payload specifies compression:
- `0x02` = Uncompressed
- `0x04` = Zlib
- `0x06` = BZ2

After identifying the type, set the encryption limit based on it:

| Compression | Limit |
|---|---|
| 0x02 (none) | 0x0C (12) |
| 0x04 (zlib) | 0x04 (4) |
| 0x06 (bz2) | 0x04 (4) |

The limit controls how many 4-byte blocks of the stream get encrypted before the rest is plaintext.

### Iterator Start Values
```
GEN_1: 0
GEN_2: 0
GEN_3: 0x04A80B38
GEN_4: 0x04A80B38
GEN_5: 0x04A80B38
GEN_6: 0
```

---

## 5. Wire Format: GEncoding (CString)

Graal uses a custom binary encoding called **GEncoding** throughout. Do not treat every multi-byte field as little-endian. Classic `CString::writeShort()` / frame lengths are big-endian (`high byte`, then `low byte`), while Graal `GShort` / `GInt` / `GInt4` / `GInt5` are base-128 encoded bytes with `+32` applied to each byte.

### Read Functions
| Function | Bytes | Description |
|---|---|---|
| `readGUChar()` | 1 | Unsigned char (0-255) |
| `readGChar()` | 1 | Signed char |
| `readShort()` | 2 | Normal big-endian signed 16-bit |
| `readGUShort()` / `readGShort()` | 2 | Graal base-128 short (`((b0-32) << 7) \| (b1-32)`) |
| `readGUInt()` / `readGInt()` | 3 | Graal base-128 int |
| `readGInt4()` | 4 | Graal base-128 4-byte int |
| `readGInt5()` | 5 | Graal base-128 5-byte timestamp / 32-bit value |
| `readChars(n)` | n | Raw n bytes |
| `readString(term)` | var | Read until terminator char/string |
| `readGString()` | var | Length-prefixed string: `CHAR len` + `len` bytes |

### Write Functions (same but write)
```
writeGUChar(v)   // 1 byte
writeGChar(v)    // 1 byte
writeShort(v)    // 2 bytes big-endian
writeGUShort(v)  // 2 Graal base-128 bytes
writeGShort(v)   // 2 Graal base-128 bytes
writeGUInt(v)    // 3 Graal base-128 bytes
writeGInt(v)     // 3 Graal base-128 bytes
writeGInt4(v)    // 4 Graal base-128 bytes
writeGInt5(v)    // 5 Graal base-128 bytes
```

### GString (length-prefixed string)
```
CHAR length
CHAR[length] data
```

### GTokenize / GUntokenize
`gtokenize()` converts newline-separated editable text into comma-separated fields. Lines
containing spaces, commas, slashes, control bytes, or quotes are wrapped in quotes; `\` is
escaped as `\\`, and `"` is escaped as `""`. `guntokenize()` reverses that process by
turning commas outside quotes back into newlines.

---

## 6. Packet Framing

### Outgoing (Server→Client)
Each logical packet ends with `\n`. Multiple packets are concatenated then:
1. Combined into a buffer
2. Compressed (zlib for GEN_2/3, bz2 for GEN_4, dynamic for GEN_5)
3. Encrypted (GEN_4/5)
4. Prefixed with 2-byte big-endian length. This length excludes the 2 length bytes.

**Wire format:**
```
SHORT_BE total_payload_length
BYTE[payload_length] compressed_and_or_encrypted_data
```

For GEN_5 outgoing:
```
SHORT_BE length          // 1 + len(encrypted_payload), excludes the 2 length bytes
BYTE compression_type    // 0x02 none, 0x04 zlib, 0x06 bz2
BYTE[...] encrypted_payload
```
Then decompress, then the individual `\n`-delimited packets are visible.

### Incoming (Client→Server)
```
SHORT_BE packet_length
BYTE[packet_length] data
```
Then process based on gen:
- GEN_1: raw, parse `\n`-delimited
- GEN_2: zlib decompress, then parse
- GEN_3: zlib decompress, then per-packet decrypt, then parse
- GEN_4: bz2 decompress after decrypt
- GEN_5: read compression byte, decrypt, decompress

### Raw Data Packets
Some packets use a raw data mode. After a `PLI_RAWDATA` packet:
```
PLI_RAWDATA(50) GINT raw_size
```
The next `raw_size` bytes are read as a single raw packet (not `\n`-delimited).

---

## 7. Client→Server Packet IDs (PLI)

Each sub-packet starts with `readGUChar()` for the ID.

| ID | Name | Format | Description |
|---|---|---|---|
| 0 | PLI_LEVELWARP | `CHAR x*2, CHAR y*2, STRING level` | Warp to level |
| 1 | PLI_BOARDMODIFY | `CHAR x, CHAR y, CHAR w, CHAR h, STRING tiles` | Modify board tiles |
| 2 | PLI_PLAYERPROPS | `prop_data...` | Set player properties |
| 3 | PLI_NPCPROPS | `GUINT npc_id, STRING props` | Set NPC properties |
| 4 | PLI_BOMBADD | `CHAR x, CHAR y, CHAR player_power, CHAR timer` | Place bomb |
| 5 | PLI_BOMBDEL | `data` | Remove bomb |
| 6 | PLI_TOALL | `CHAR len, CHAR[len] message` | Chat to all |
| 7 | PLI_HORSEADD | `CHAR x, CHAR y, CHAR dir_bushes, STRING image` | Add horse |
| 8 | PLI_HORSEDEL | `CHAR x, CHAR y` | Remove horse |
| 9 | PLI_ARROWADD | `data` | Fire arrow |
| 10 | PLI_FIRESPY | `data` | Fire spy |
| 11 | PLI_THROWCARRIED | `data` | Throw carried NPC |
| 12 | PLI_ITEMADD | `CHAR x, CHAR y, CHAR item_type` | Drop item |
| 13 | PLI_ITEMDEL | `CHAR x, CHAR y` | Take/delete item |
| 14 | PLI_CLAIMPKER | `GUSHORT killer_id` | Claim kill |
| 15 | PLI_BADDYPROPS | `CHAR id, STRING props` | Set baddy props |
| 16 | PLI_BADDYHURT | `data` | Hurt baddy |
| 17 | PLI_BADDYADD | `CHAR x, CHAR y, CHAR type, CHAR power, STRING image` | Add baddy |
| 18 | PLI_FLAGSET | `STRING flag_name=value` | Set flag |
| 19 | PLI_FLAGDEL | `STRING flag_name` | Delete flag |
| 20 | PLI_OPENCHEST | `CHAR x, CHAR y` | Open chest |
| 21 | PLI_PUTNPC | `GSTRING image, GSTRING code, CHAR x, CHAR y` | Put NPC |
| 22 | PLI_NPCDEL | `GUINT npc_id` | Delete NPC |
| 23 | PLI_WANTFILE | `STRING filename` | Request file |
| 24 | PLI_SHOWIMG | `data` | Show image/effect for nearby players |
| 26 | PLI_HURTPLAYER | `GUSHORT victim, CHAR dx, CHAR dy, CHAR power, GUINT npc` | Hurt player |
| 27 | PLI_EXPLOSION | `CHAR radius, CHAR x, CHAR y, CHAR power` | Explosion |
| 28 | PLI_PRIVATEMESSAGE | `GUSHORT count, GUSHORT[count] ids, STRING message` | PM |
| 29 | PLI_NPCWEAPONDEL | `STRING weapon` | Delete NPC weapon |
| 30 | PLI_LEVELWARPMOD | `GINT5 modtime, CHAR x*2, CHAR y*2, STRING level` | Warp with modtime |
| 31 | PLI_PACKETCOUNT | `GUSHORT count` | Packet count sync |
| 32 | PLI_ITEMTAKE | `CHAR x, CHAR y` | Take item (same handler as ITEMDEL) |
| 33 | PLI_WEAPONADD | `CHAR type, ...` | Add weapon |
| 34 | PLI_UPDATEFILE | `GINT5 modtime, STRING filename` | Check file freshness |
| 35 | PLI_ADJACENTLEVEL | `GINT5 modtime, STRING level` | Request adjacent level |
| 36 | PLI_HITOBJECTS | `CHAR power, CHAR x, CHAR y, [GUINT npc_id]` | Hit objects (sword) |
| 37 | PLI_LANGUAGE | `STRING language` | Set language |
| 38 | PLI_TRIGGERACTION | `GUINT npc_id, CHAR x, CHAR y, STRING action` | Trigger action |
| 39 | PLI_MAPINFO | `STRING data` | Map info |
| 40 | PLI_SHOOT | `GINT unknown, CHAR x, CHAR y, CHAR z, CHAR angle, CHAR zangle, CHAR speed, GSTRING gani, CHAR param_len, STRING params` | Shoot (old format) |
| 41 | PLI_SERVERWARP | `STRING servername` | Warp to another server |
| 44 | PLI_PROCESSLIST | `STRING processes` | Process list |
| 47 | PLI_VERIFYWANTSEND | `GINT5 crc32, STRING filename` | Verify file |
| 48 | PLI_SHOOT2 | `GUSHORT x, GUSHORT y, GUSHORT z, CHAR offx, CHAR offy, CHAR angle, CHAR zangle, CHAR speed, CHAR gravity, GUSHORT gani_len, GSTRING gani, CHAR param_len, STRING params` | Shoot (new format) |
| 50 | PLI_RAWDATA | `GINT raw_size` | Next packet is raw data |
| 51-98 | PLI_RC_* | Various | RC control packets |
| 103-119 | PLI_NC_* | Various | NPC control packets |
| 130 | PLI_REQUESTUPDATEBOARD | `CHAR level_len, level, GINT5 modtime, GSHORT x, GSHORT y, GSHORT w, GSHORT h` | Request board update |
| 152 | PLI_REQUESTTEXT | `GSTRING type, [params]` | Request data from server |
| 154 | PLI_SENDTEXT | `GSTRING type, [params]` | Send data to server |
| 157 | PLI_UPDATEGANI | `data` | GANI update |
| 158 | PLI_UPDATESCRIPT | `STRING script` | Request script |
| 159 | PLI_UPDATEPACKAGEREQUESTFILE | `CHAR order, STRING package, GINT5 modtime` | Update package |
| 161 | PLI_UPDATECLASS | `GINT5 modtime, STRING name` | Request class |
| 162 | PLI_RC_UNKNOWN162 | (blank) | Unknown RC3 |

---

## 8. Server→Client Packet IDs (PLO)

| ID | Name | Format | Description |
|---|---|---|---|
| 0 | PLO_LEVELBOARD | `tile_data` | Level board (64×64 tiles, 2 bytes each) |
| 1 | PLO_LEVELLINK | `link_data` | Level links |
| 2 | PLO_BADDYPROPS | `CHAR id, STRING props` | Baddy properties |
| 3 | PLO_NPCPROPS | `GUINT npc_id, STRING props` | NPC properties |
| 4 | PLO_LEVELCHEST | `chest_data` | Chest data |
| 5 | PLO_LEVELSIGN | `sign_data` | Sign data |
| 6 | PLO_LEVELNAME | `STRING level_name` | Current level name |
| 7 | PLO_BOARDMODIFY | `CHAR x, CHAR y, CHAR w, CHAR h, STRING tiles` | Board delta |
| 8 | PLO_OTHERPLPROPS | `GUSHORT player_id, prop_data` | Other player's props |
| 9 | PLO_PLAYERPROPS | `prop_data` | Own player props |
| 10 | PLO_ISLEADER | (blank) | You are now level leader |
| 11 | PLO_BOMBADD | `GUSHORT player_id, ...` | Bomb placed |
| 12 | PLO_BOMBDEL | `data` | Bomb removed |
| 13 | PLO_TOALL | `GUSHORT player_id, CHAR len, CHAR[len] message` | Chat to all |
| 14 | PLO_PLAYERWARP | `CHAR x*2, CHAR y*2, STRING level` | Warp player |
| 15 | PLO_WARPFAILED | `STRING level_name` | Warp failed |
| 16 | PLO_DISCMESSAGE | `STRING message` | Disconnect message |
| 17 | PLO_HORSEADD | `data` | Horse added |
| 18 | PLO_HORSEDEL | `data` | Horse removed |
| 19 | PLO_ARROWADD | `GUSHORT player_id, ...` | Arrow fired |
| 20 | PLO_FIRESPY | `GUSHORT player_id, ...` | Spy fired |
| 21 | PLO_THROWCARRIED | `GUSHORT player_id, ...` | Thrown NPC |
| 22 | PLO_ITEMADD | `CHAR x, CHAR y, CHAR item` | Item added |
| 23 | PLO_ITEMDEL | `CHAR x, CHAR y` | Item removed |
| 24 | PLO_NPCMOVED | `data` | NPC moved/hidden |
| 25 | PLO_SIGNATURE | `CHAR value` | Server signature (73 = >8 players) |
| 26 | PLO_NPCACTION | `data` | NPC action |
| 27 | PLO_BADDYHURT | `data` | Baddy hurt |
| 28 | PLO_FLAGSET | `STRING name=value` | Set flag |
| 29 | PLO_NPCDEL | `data` | NPC deleted |
| 30 | PLO_FILESENDFAILED | `STRING filename` | File not found |
| 31 | PLO_FLAGDEL | `STRING name` | Flag deleted |
| 32 | PLO_SHOWIMG | `GUSHORT player_id, ...` | Show image/effect from player |
| 33 | PLO_NPCWEAPONADD | `CHAR name_len, name, CHAR 0, CHAR img_len, img, CHAR 1, GSHORT script_len, script` | Add NPC weapon |
| 34 | PLO_NPCWEAPONDEL | `STRING weapon_name` | Delete NPC weapon |
| 35 | PLO_RC_ADMINMESSAGE | `raw message bytes` | Admin message |
| 36 | PLO_EXPLOSION | `GUSHORT player_id, CHAR radius, CHAR x, CHAR y, CHAR power` | Explosion |
| 37 | PLO_PRIVATEMESSAGE | `GUSHORT from_id, STRING type, message` | Private message |
| 38 | PLO_PUSHAWAY | `data` | Push away |
| 39 | PLO_LEVELMODTIME | `GINT5 modtime` | Level modification time |
| 40 | PLO_HURTPLAYER | `GUSHORT attacker, CHAR dx, CHAR dy, CHAR power, GUINT npc` | Hurt player |
| 41 | PLO_STARTMESSAGE | `STRING message` | Welcome/start message |
| 42 | PLO_NEWWORLDTIME | `GINT4 time` | New world time |
| 43 | PLO_DEFAULTWEAPON | `data` | Default weapon |
| 44 | PLO_HASNPCSERVER | (blank) | NPC server present |
| 45 | PLO_FILEUPTODATE | `STRING filename` | File is current |
| 46 | PLO_HITOBJECTS | `GUSHORT id, CHAR power, CHAR x, CHAR y, [GUINT npc_id]` | Hit objects |
| 47 | PLO_STAFFGUILDS | `guild1,guild2,...` | Staff guild list |
| 48 | PLO_TRIGGERACTION | `GUSHORT player_id, ...` | Forward triggeraction |
| 49 | PLO_PLAYERWARP2 | `CHAR x*2, CHAR y*2, CHAR z*2, CHAR mapX, CHAR mapY, STRING gmap_name` | Warp with gmap coords |
| 55 | PLO_ADDPLAYER | `GUSHORT id, CHAR name_len, name, props...` | Add player to client view |
| 56 | PLO_DELPLAYER | `GUSHORT id` | Remove player |
| 65 | PLO_RC_FILEBROWSER_DIRLIST | `data` | File browser listing |
| 68 | PLO_LARGEFILESTART | `STRING filename` | Large file start |
| 69 | PLO_LARGEFILEEND | `STRING filename` | Large file end |
| 74 | PLO_RC_CHAT | `raw message bytes` | RC chat message |
| 79 | PLO_NPCSERVERADDR | `CHAR 0, CHAR 2, STRING ip,port` | NPC server address |
| 82 | PLO_SERVERTEXT | (blank or data) | requestText/sendText response |
| 84 | PLO_LARGEFILESIZE | `GLONGLONG size` | Large file total size |
| 100 | PLO_RAWDATA | `GINT size` | Raw data prefix |
| 101 | PLO_BOARDPACKET | `board_data` | Raw board data |
| 102 | PLO_FILE | `[GINT5 modtime], CHAR filename_len, filename, data` | File transfer |
| 103 | PLO_RC_MAXUPLOADFILESIZE | `GINT5 size` | Max upload size |
| 107 | PLO_BOARDLAYER | `layer_data` | Additional board layer |
| 131 | PLO_NPCBYTECODE | `GINT3 id, bytecode` | Compiled NPC script |
| 140 | PLO_NPCWEAPONSCRIPT | `GINT2 info_len, script` | NPC weapon script |
| 150 | PLO_NPCDEL2 | `CHAR level_len, level, GINT3 npc_id` | Delete NPC (new format) |
| 153 | PLO_SAY2 | `STRING text` | Sign message / say2 |
| 154 | PLO_FREEZEPLAYER2 | (blank) | Freeze player |
| 155 | PLO_UNFREEZEPLAYER | (blank) | Unfreeze player |
| 156 | PLO_SETACTIVELEVEL | `STRING level_name` | Set active level for data |
| 158 | PLO_NC_NPCADD | `GINT id, props...` | NC: Add database NPC |
| 159 | PLO_NC_NPCDELETE | `GINT id` | NC: Delete database NPC |
| 160 | PLO_NC_NPCSCRIPT | `GINT id, GSTRING script` | NC: NPC script |
| 161 | PLO_NC_NPCFLAGS | `GINT id, GSTRING flags` | NC: NPC flags |
| 162 | PLO_NC_CLASSGET | `CHAR name_len, name, GSTRING script` | NC: Class script |
| 163 | PLO_NC_CLASSADD | `STRING class_name` | NC: Add class |
| 167 | PLO_NC_WEAPONLISTGET | `name1, name2, ...` | NC: Weapon list |
| 168 | PLO_UNKNOWN168 | (blank) | Login server blank packet |
| 171 | PLO_BIGMAP | `maptext,mapimage,x,y` | Big map |
| 172 | PLO_MINIMAP | `maptext,mapimage,x,y` | Mini map |
| 174 | PLO_GHOSTICON | `CHAR enabled` | Ghost icon toggle |
| 175 | PLO_SHOOT | `GUSHORT player_id, shoot_data` | Shoot (old) |
| 176 | PLO_FULLSTOP | (blank) | Full stop (login server) |
| 178 | PLO_SERVERWARP | `data` | Server warp |
| 179 | PLO_RPGWINDOW | `STRING message` | RPG window message |
| 180 | PLO_STATUSLIST | `status1,status2,...` | Playerlist status options |
| 182 | PLO_LISTPROCESSES | (blank) | Request process list (legacy clients only; do not send to G3D/v6) |
| 188 | PLO_NC_CLASSDELETE | `STRING class` | NC: Class deleted |
| 190 | PLO_UNKNOWN190 | (blank) | Triggers IRC/text request sequence |
| 191 | PLO_SHOOT2 | `GUSHORT player_id, shoot_data` | Shoot (new) |
| 192 | PLO_NC_WEAPONGET | `CHAR name_len, name, CHAR img_len, img, script` | NC: Weapon data |
| 194 | PLO_CLEARWEAPONS | (blank) | Clear weapon list |

### Packet Behavior Notes

- `PLI_SHOWIMG` is not echoed only to the sender. The server prepends the sender's `GUSHORT player_id`, converts it to `PLO_SHOWIMG`, and forwards it to players in the same level/area excluding the sender. This is used by emoji/thought-bubble effects.
- `PLI_BOARDMODIFY` should alter the server's level tile state, broadcast `PLO_BOARDMODIFY` to players in the level, and store old tiles for configured respawn when the original tile is a respawning object such as a bush, grass/swamp tile, or vase. The live `PLI/PLO_BOARDMODIFY` tile payload is a stream of Graal `GSHORT` tile ids, not raw big-endian shorts; tile id `0` is encoded as `0x20 0x20`.
- Destroyed bush-like tiles can spawn items when `bushitems=true`; `tiledroprate` is the percentage chance, and the item is chosen from item ids 0-5. Vase tile `0x2ac` drops a heart when `vasesdrop=true`. Older pre-2.1 clients handle these drops client-side.
- `PLI_ITEMDEL` removes the visual item from the level. `PLI_ITEMTAKE` shares the same payload but also applies the removed item's player props to the taker, sends updated `PLO_PLAYERPROPS` back to that client, forwards visible prop changes, and persists the account.
- `PLI_OPENCHEST` should ignore chests already present in the player's saved chest list. For a new chest, apply the chest item to the player, send `PLO_LEVELCHEST` with open state, append the chest key to the account, and save. Chest keys follow the current C++ server format `x:y:levelname`.
- Movement prop forwarding must be compatible with the receiving client. Legacy clients before v2.30 should receive `PLPROP_X/Y/Z`; modern clients v2.30+ and G3D/v6 should receive precise `PLPROP_X2/Y2/Z2`. Sending both formats to every client can make mixed-version sessions show animation without position updates.
- Account weapons whose names map to built-in item weapons, such as `bomb` and `bow`, should be sent as `PLO_DEFAULTWEAPON` with the item's numeric id. They do not require weapon script files in `weapons/`.
- `PLO_LISTPROCESSES` is a legacy anti-cheat/process-list request. It is useful for older clients such as v2.22, but G3D/v6 clients can crash if they receive it during login.
- `PLO_UNKNOWN190` is intentionally sent during modern login; it triggers the client's IRC/requestText sequence such as bantypes, PM guilds, PM servers, global PMs, buddy tracking, and related data.

---

## 9. Player Properties System

Properties are encoded as a stream: `CHAR prop_id` followed by the property-specific data. Multiple props are concatenated in a single packet.

### Property IDs (0-82, total 83 props)

| ID | Name | Wire Format | Notes |
|---|---|---|---|
| 0 | PLPROP_NICKNAME | `CHAR len, data` | Display name. Max 223 chars. |
| 1 | PLPROP_MAXPOWER | `CHAR maxhp` | Max hearts |
| 2 | PLPROP_CURPOWER | `CHAR hp*2` | Current hearts (float * 2) |
| 3 | PLPROP_RUPEESCOUNT | `GINT gralats` | Money (max 9999999) |
| 4 | PLPROP_ARROWSCOUNT | `CHAR arrows` | Arrow count (0-99) |
| 5 | PLPROP_BOMBSCOUNT | `CHAR bombs` | Bomb count (0-99) |
| 6 | PLPROP_GLOVEPOWER | `CHAR glove` | Glove power (0-3) |
| 7 | PLPROP_BOMBPOWER | `CHAR power` | Bomb power (0-3) |
| 8 | PLPROP_SWORDPOWER | `CHAR power+30, CHAR img_len, img` | If ≤4: default sword (sword{N}.png). If >30: custom img follows. |
| 9 | PLPROP_SHIELDPOWER | `CHAR power+10, CHAR img_len, img` | If ≤3: default shield. If >10: custom img follows. |
| 10 | PLPROP_GANI | `CHAR len, gani` | Current animation. Pre-2.1: bow power/image instead. |
| 11 | PLPROP_HEADGIF | `CHAR len+100, head_img` | If <100: default head N (head{N}.png). If >100: custom image. If ==100: blank/null. |
| 12 | PLPROP_CURCHAT | `CHAR len, message` | Chat bubble text (max 223) |
| 13 | PLPROP_COLORS | `5× CHAR` | colors[0..4]: skin, coat, sleeves, shoes, belt |
| 14 | PLPROP_ID | `GUSHORT id` | Player ID |
| 15 | PLPROP_X | `GUCHAR x` | X position. Encode: `pixel_x / 8` (= `tile_x * 2`). Decode: `value * 8` pixels. |
| 16 | PLPROP_Y | `GUCHAR y` | Y position. Same encoding as X. |
| 17 | PLPROP_SPRITE | `CHAR sprite` | Direction. sprite % 4 gives direction (0=up,1=left,2=down,3=right). |
| 18 | PLPROP_STATUS | `CHAR flags` | Status bitmask (see below) |
| 19 | PLPROP_CARRYSPRITE | `BYTE sprite` | Carried sprite; payload is a raw byte, not a GCHAR. Default/no carried sprite is `0xFF`. |
| 20 | PLPROP_CURLEVEL | `CHAR len, level_name` | Current level. For GMAP: gmap name. For singleplayer: name.singleplayer. |
| 21 | PLPROP_HORSEGIF | `CHAR len, image` | Horse image (max 219) |
| 22 | PLPROP_HORSEBUSHES | `CHAR count` | Horse bomb count |
| 23 | PLPROP_EFFECTCOLORS | `CHAR 0` | (unused, always 0) |
| 24 | PLPROP_CARRYNPC | `GUINT npc_id` | Carried NPC ID (0 = none) |
| 25 | PLPROP_APCOUNTER | `GUSHORT counter+1` | AP timer counter |
| 26 | PLPROP_MAGICPOINTS | `CHAR mp` | Magic points (max 100) |
| 27 | PLPROP_KILLSCOUNT | `GINT kills` | Kill count |
| 28 | PLPROP_DEATHSCOUNT | `GINT deaths` | Death count |
| 29 | PLPROP_ONLINESECS | `GINT seconds` | Online time |
| 30 | PLPROP_IPADDR | `GINT5 ip` | IPv4 address as a 32-bit integer in GINT5 encoding; saved as `IP` in account files |
| 31 | PLPROP_UDPPORT | `GINT port` | UDP port |
| 32 | PLPROP_ALIGNMENT | `CHAR ap` | AP (0-100) |
| 33 | PLPROP_ADDITFLAGS | `CHAR flags` | Additional flags bitmask |
| 34 | PLPROP_ACCOUNTNAME | `CHAR len, account` | Account name |
| 35 | PLPROP_BODYIMG | `CHAR len, body_img` | Body image |
| 36 | PLPROP_RATING | `GINT rating` | Packed Glicko: bits 9-20 = rating(12bit), bits 0-8 = deviation(9bit) |
| 37 | PLPROP_GATTRIB1 | `CHAR len, value` | Gani attribute 1 |
| 38 | PLPROP_GATTRIB2 | `CHAR len, value` | Gani attribute 2 |
| 39 | PLPROP_GATTRIB3 | `CHAR len, value` | Gani attribute 3 |
| 40 | PLPROP_GATTRIB4 | `CHAR len, value` | Gani attribute 4 |
| 41 | PLPROP_GATTRIB5 | `CHAR len, value` | Gani attribute 5 |
| 42 | PLPROP_ATTACHNPC | `CHAR type(0), GUINT npc_id` | Attached NPC |
| 43 | PLPROP_GMAPLEVELX | `CHAR x` | GMAP level X coordinate |
| 44 | PLPROP_GMAPLEVELY | `CHAR y` | GMAP level Y coordinate |
| 45 | PLPROP_Z | `GUCHAR z` | Z position. Encode: `clamp(z_pixels/8, -50, 170) + 50`. Decode: `(value - 50) * 8` pixels. |
| 46 | PLPROP_GATTRIB6 | `CHAR len, value` | Gani attribute 6 |
| 47 | PLPROP_GATTRIB7 | `CHAR len, value` | Gani attribute 7 |
| 48 | PLPROP_GATTRIB8 | `CHAR len, value` | Gani attribute 8 |
| 49 | PLPROP_GATTRIB9 | `CHAR len, value` | Gani attribute 9 |
| 50 | PLPROP_JOINLEAVELVL | `CHAR 1or0` | 1=join level, 0=leave level. Used in player prop forwarding. |
| 51 | PLPROP_PCONNECTED | (empty) | Player connected signal |
| 52 | PLPROP_PLANGUAGE | `CHAR len, language` | Player language (default "English") |
| 53 | PLPROP_PSTATUSMSG | `CHAR status_index` | Playerlist status icon index |
| 54 | PLPROP_GATTRIB10 | `CHAR len, value` | Gani attribute 10 |
| 55-63 | PLPROP_GATTRIB11-19 | `CHAR len, value` | Gani attributes 11-19 |
| 64 | PLPROP_GATTRIB20 | `CHAR len, value` | Gani attribute 20 |
| 65-73 | PLPROP_GATTRIB21-29 | `CHAR len, value` | Gani attributes 21-29 |
| 74 | PLPROP_GATTRIB30 | `CHAR len, value` | Gani attribute 30 |
| 75 | PLPROP_OSTYPE | `CHAR len, os` | OS type string (2.19+). Windows = "wind" |
| 76 | PLPROP_TEXTCODEPAGE | `GINT codepage` | Text codepage (2.19+). E.g. 1252 |
| 77 | PLPROP_UNKNOWN77 | (unknown) | Unknown |
| 78 | PLPROP_X2 | `GUSHORT val` | Precise X (2.3+). Bit 0 = negative flag, bits 1-15 = abs value in pixels. |
| 79 | PLPROP_Y2 | `GUSHORT val` | Precise Y (2.3+) |
| 80 | PLPROP_Z2 | `GUSHORT val` | Precise Z (2.3+). Clamped to -400 to 1360 pixels. |
| 81 | PLPROP_UNKNOWN81 | `GUCHAR flag` | Unknown. Bit 0 = playerlist, bit 1 = servers tab, bit 2 = channels tab (unconfirmed). |
| 82 | PLPROP_COMMUNITYNAME | `CHAR len, name` | Community name / account alias (Graal v5+) |

**Total props: 83**

### Attribute Packet ID → Attribute Index Mapping
```
Player attr packets: [37,38,39,40,41,46,47,48,49,54,55,56,57,58,59,60,61,62,63,64,65,66,67,68,69,70,71,72,73,74]
NPC attr packets:    [36,37,38,39,40,44,45,46,47,53,54,55,56,57,58,59,60,61,62,63,64,65,66,67,68,69,70,71,72,73]
```
These map attr1-attr30 respectively.

### Status Flags (PLPROP_STATUS, ID 18)
```
0x01 = PLSTATUS_PAUSED
0x02 = PLSTATUS_HIDDEN
0x04 = PLSTATUS_MALE
0x08 = PLSTATUS_DEAD
0x10 = PLSTATUS_ALLOWWEAPONS
0x20 = PLSTATUS_HIDESWORD
0x40 = PLSTATUS_HASSPIN
```
Default status value on new accounts: 20 (0x14 = MALE | ALLOWWEAPONS)

### Additional Flags (PLPROP_ADDITFLAGS, ID 33)
```
0x01 = PLFLAG_NOMASSMESSAGE
0x02 = PLFLAG_ONLYSHOWONELEVEL
0x04 = PLFLAG_NOTOALL
0x08 = PLFLAG_VOICEDISABLED
```

### Player Rights Bitmask (stored in account)
```
0x00001 = PLPERM_WARPTO
0x00002 = PLPERM_WARPTOPLAYER
0x00004 = PLPERM_SUMMON
0x00008 = PLPERM_UPDATELEVEL
0x00010 = PLPERM_DISCONNECT
0x00020 = PLPERM_VIEWATTRIBUTES
0x00040 = PLPERM_SETATTRIBUTES
0x00080 = PLPERM_SETSELFATTRIBUTES
0x00100 = PLPERM_RESETATTRIBUTES
0x00200 = PLPERM_ADMINMSG
0x00400 = PLPERM_SETRIGHTS
0x00800 = PLPERM_BAN
0x01000 = PLPERM_SETCOMMENTS
0x02000 = PLPERM_INVISIBLE
0x04000 = PLPERM_MODIFYSTAFFACCOUNT
0x08000 = PLPERM_SETSERVERFLAGS
0x10000 = PLPERM_SETSERVEROPTIONS
0x20000 = PLPERM_SETFOLDEROPTIONS
0x40000 = PLPERM_SETFOLDERRIGHTS
0x80000 = PLPERM_NPCCONTROL
0xFFFFFF = PLPERM_ANYRIGHT
```

### Sending Props to Other Players
```
PLO_OTHERPLPROPS(8) GUSHORT player_id, [prop_data...]
```

### Sending Props to Self
```
PLO_PLAYERPROPS(9) [prop_data...]
```

---

## 10. NPC Properties System

NPC props follow the same stream pattern as player props. Total: 78 properties (NPCPROP_COUNT).

### NPC Property IDs (0-77)

| ID | Name | Wire Format | Notes |
|---|---|---|---|
| 0 | NPCPROP_IMAGE | `CHAR len, image` | NPC image filename |
| 1 | NPCPROP_SCRIPT | `GSHORT len, script` | Client-side GS1 script (§-tokenized). Max 0x3FFF (16383) bytes. |
| 2 | NPCPROP_X | `GCHAR x` | Pixel X / 8 |
| 3 | NPCPROP_Y | `GCHAR y` | Pixel Y / 8 |
| 4 | NPCPROP_POWER | `CHAR hp*2` | Hearts |
| 5 | NPCPROP_RUPEES | `GINT gralats` | Money |
| 6 | NPCPROP_ARROWS | `CHAR arrows` | Arrow count |
| 7 | NPCPROP_BOMBS | `CHAR bombs` | Bomb count |
| 8 | NPCPROP_GLOVEPOWER | `CHAR glove` | Glove power |
| 9 | NPCPROP_BOMBPOWER | `CHAR power` | Bomb power |
| 10 | NPCPROP_SWORDIMAGE | `CHAR power+30, CHAR img_len, img` | 0 = no sword. >30 = custom. |
| 11 | NPCPROP_SHIELDIMAGE | `CHAR power+10, CHAR img_len, img` | 0 = no shield. >10 = custom. |
| 12 | NPCPROP_GANI | `CHAR len, gani` | Current animation. Pre-2.1: bow power/image. |
| 13 | NPCPROP_VISFLAGS | `CHAR flags` | Visibility flags (see below) |
| 14 | NPCPROP_BLOCKFLAGS | `CHAR flags` | Blocking flags (see below) |
| 15 | NPCPROP_MESSAGE | `CHAR len, message` | Chat text above NPC |
| 16 | NPCPROP_HURTDXDY | `CHAR hurtX*32+32, CHAR hurtY*32+32` | Hurt pushback direction |
| 17 | NPCPROP_ID | `GUINT id` | NPC ID |
| 18 | NPCPROP_SPRITE | `CHAR sprite` | Direction (sprite % 4) |
| 19 | NPCPROP_COLORS | `5× CHAR` | colors[0..4] |
| 20 | NPCPROP_NICKNAME | `CHAR len, nick` | NPC nickname |
| 21 | NPCPROP_HORSEIMAGE | `CHAR len, image` | Horse image |
| 22 | NPCPROP_HEADIMAGE | `CHAR len+100, head` | Same encoding as PLPROP_HEADGIF |
| 23-32 | NPCPROP_SAVE0-9 | `CHAR value` | 10 save slots (save[0] through save[9]) |
| 33 | NPCPROP_ALIGNMENT | `CHAR ap` | Alignment points |
| 34 | NPCPROP_IMAGEPART | `6 bytes` | Image part offset: 2 GShort (offsetx, offsety) + 2 GChar (width, height) |
| 35 | NPCPROP_BODYIMAGE | `CHAR len, body` | Body image |
| 36-40 | NPCPROP_GATTRIB1-5 | `CHAR len, value` | Gani attributes 1-5 |
| 41 | NPCPROP_GMAPLEVELX | `CHAR x` | GMAP level X |
| 42 | NPCPROP_GMAPLEVELY | `CHAR y` | GMAP level Y |
| 43 | NPCPROP_Z | `CHAR z` | Pixel Z / 8 + 50 (same as player Z encoding) |
| 44-47 | NPCPROP_GATTRIB6-9 | `CHAR len, value` | Gani attributes 6-9 |
| 48 | NPCPROP_UNKNOWN48 | (unknown) | Unknown |
| 49 | NPCPROP_SCRIPTER | `CHAR len, name` | Scripter name (DB NPCs) |
| 50 | NPCPROP_NAME | `CHAR len, name` | NPC name (DB NPCs only) |
| 51 | NPCPROP_TYPE | `CHAR len, type` | Script type |
| 52 | NPCPROP_CURLEVEL | `CHAR len, level` | Current level (DB NPCs) |
| 53-73 | NPCPROP_GATTRIB10-30 | `CHAR len, value` | Gani attributes 10-30 |
| 74 | NPCPROP_CLASS | `GSHORT len, class_list` | Comma-separated joined class names |
| 75 | NPCPROP_X2 | `GUSHORT val` | Precise X (same as player X2) |
| 76 | NPCPROP_Y2 | `GUSHORT val` | Precise Y |
| 77 | NPCPROP_Z2 | `GUSHORT val` | Precise Z |

### NPC Visibility Flags (NPCPROP_VISFLAGS, ID 13)
```
0x01 = NPCVISFLAG_VISIBLE
0x02 = NPCVISFLAG_DRAWOVERPLAYER
0x04 = NPCVISFLAG_DRAWUNDERPLAYER
```

### NPC Block Flags (NPCPROP_BLOCKFLAGS, ID 14)
```
0x00 = NPCBLOCKFLAG_BLOCK    (default - blocks player movement)
0x01 = NPCBLOCKFLAG_NOBLOCK  (dontblock)
```

### NPC Move Flags
```
0x00 = NPCMOVEFLAG_NOCACHE
0x01 = NPCMOVEFLAG_CACHE
0x02 = NPCMOVEFLAG_APPEND
0x04 = NPCMOVEFLAG_BLOCKCHECK
0x08 = NPCMOVEFLAG_EVENTWHENDONE
0x10 = NPCMOVEFLAG_APPLYDIR
```

### NPC Event Flags (NPC Server)
```
0x01 = NPCEVENTFLAG_CREATED
0x02 = NPCEVENTFLAG_TIMEOUT
0x04 = NPCEVENTFLAG_PLAYERCHATS
0x08 = NPCEVENTFLAG_PLAYERENTERS
0x10 = NPCEVENTFLAG_PLAYERLEAVES
0x20 = NPCEVENTFLAG_PLAYERTOUCHSME
0x40 = NPCEVENTFLAG_PLAYERLOGIN
0x80 = NPCEVENTFLAG_PLAYERLOGOUT
0x100 = NPCEVENTFLAG_NPCWARPED
```
Default event mask: 0xFF (all except NPCWARPED)

### NPC Props Always Sent on Initial Load
These props have modTime set to current time at creation:
```
IMAGE, SCRIPT, X, Y, VISFLAGS, ID, SPRITE, MESSAGE, GMAPLEVELX, GMAPLEVELY, X2, Y2
```

### NPC Props Delta System
NPC props are only sent when their modTime is newer than the client's cached time. The server tracks per-property modification timestamps and sends `getProps(clientCachedTime)` which only includes props changed since that time.

### NPC Props Packet
```
PLO_NPCPROPS(3) GUINT npc_id, [prop_data...]
```

### NPC Move Packet (server→client)
```
PLO_MOVE2(189) GINT npc_id, GUSHORT start_x, GUSHORT start_y, GUSHORT delta_x, GUSHORT delta_y, GUSHORT time_in_0.05s_steps, CHAR options
```
Values use the same sign-bit encoding as X2/Y2/Z2.

### NPC Warp Packet
```
PLO_NPCMOVED(24) GINT npc_id    // Sent to all players to hide NPC during warp
// Then NPC props sent to new level area
```

---

## 11. Level / Board / Tile System

Levels are 64×64 tiles. Each tile is 2 bytes (unsigned short):
- Lower bits: tile type (background, wall, water, bush, etc.)
- Specific tiles: `0x002`=bush, `0x1A4`=grass, `0x1FF`=swamp, `0x2AC`=vase, `0x3FF`=special

### Board Packet (PLO_LEVELBOARD)
Sent via raw data:
```
PLO_RAWDATA(100) GINT(64*64*2 + 2)  // 8194 bytes
BYTE[8192] tiles                     // 64×64 tiles, 2 bytes each
BYTE newline
```

### Board Layers (PLO_BOARDLAYER)
```
PLO_RAWDATA(100) GINT layer_length
layer_data
```

### Board Modify (PLO_BOARDMODIFY)
```
PLO_BOARDMODIFY(7) CHAR x, CHAR y, CHAR w, CHAR h, STRING tiles
```
The tiles string is `w*h` tiles, each 2 bytes.

### Level ModTime
```
PLO_LEVELMODTIME(39) GINT5 modtime
```

### Level Name
```
PLO_LEVELNAME(6) STRING level_name
```

### Level Links
Level links are sent as a series of link entries with width, height, new level, x, y.

### Level Signs
Signs include x, y, and text. Text uses `#b` as newline separator when sent to client.

### Board Changes
Tracked over time. Players receive board changes that happened since their last cached modtime.

### Item Types (LevelItemType enum)

| Byte | Enum | Name | Effect |
|---|---|---|---|
| 0 | GREENRUPEE | Green Rupee | +1 gralat |
| 1 | BLUERUPEE | Blue Rupee | +5 gralats |
| 2 | REDRUPEE | Red Rupee | +30 gralats |
| 3 | BOMBS | Bombs | +5 bombs |
| 4 | DARTS | Darts | +5 arrows |
| 5 | HEART | Heart | +1 heart |
| 6 | GLOVE1 | Glove1 | Glove power 1 |
| 7 | BOW | Bow | Adds bow weapon |
| 8 | BOMB | Bomb | Adds bomb weapon |
| 9 | SHIELD | Shield | Shield power 1 |
| 10 | SWORD | Sword | Sword power 1 |
| 11 | FULLHEART | Full Heart | +1 max heart |
| 12 | SUPERBOMB | Super Bomb | Adds superbomb |
| 13 | BATTLEAXE | Battle Axe | Sword power 2 |
| 14 | GOLDENSWORD | Golden Sword | Sword power 4 |
| 15 | MIRRORSHIELD | Mirror Shield | Shield power 2 |
| 16 | GLOVE2 | Glove2 | Glove power 2 |
| 17 | LIZARDSHIELD | Lizard Shield | Shield power 3 |
| 18 | LIZARDSWORD | Lizard Sword | Sword power 3 |
| 19 | GOLDRUPEE | Gold Rupee | +100 gralats |
| 20 | FIREBALL | Fireball | Adds fireball |
| 21 | FIREBLAST | Fireblast | Adds fireblast |
| 22 | NUKESHOT | Nuke Shot | Adds nukeshot |
| 23 | JOLTBOMB | Jolt Bomb | Adds joltbomb |
| 24 | SPINATTACK | Spin Attack | Enables spin attack |

### Baddy Properties

| ID | Name | Format |
|---|---|---|
| 0 | BDPROP_ID | `CHAR id` |
| 1 | BDPROP_X | `CHAR x` |
| 2 | BDPROP_Y | `CHAR y` |
| 3 | BDPROP_TYPE | `CHAR type` |
| 4 | BDPROP_POWERIMAGE | `CHAR power, CHAR img_len, img` |
| 5 | BDPROP_MODE | `CHAR mode` |
| 6 | BDPROP_ANI | `CHAR ani` |
| 7 | BDPROP_DIR | `CHAR dir` |
| 8 | BDPROP_VERSESIGHT | `STRING text` |
| 9 | BDPROP_VERSEHURT | `STRING text` |
| 10 | BDPROP_VERSEATTACK | `STRING text` |

### Baddy Modes
```
0  = BDMODE_WALK
1  = BDMODE_LOOK
2  = BDMODE_HUNT
3  = BDMODE_HURT
4  = BDMODE_BUMPED
5  = BDMODE_DIE
6  = BDMODE_SWAMPSHOT
7  = BDMODE_HAREJUMP
8  = BDMODE_OCTOSHOT
9  = BDMODE_DEAD
```
Baddy IDs start at 1 (ID 0 breaks the client). Power max is 12 (6 hearts).

### Level Link Format
Level links define warp zones between levels.
```
LINK x y width height newlevel newx newy
```
- `x, y`: Top-left position of the link zone in the level (tile coords)
- `width, height`: Size of the link zone
- `newlevel`: Destination level name
- `newx, newy`: Destination position (or "playerx"/"playery" for relative)

### Level Chest Format
```
PLO_LEVELCHEST(4) CHAR is_open, CHAR x, CHAR y
```
Chest contains a LevelItemType and an optional sign index. Stored per-account as `levelname,x,y`.

### Level Sign Format
Signs display text when a player walks into them. Text uses `#b` as newline separator when sent to client via `PLO_SAY2`.

---

## 12. Flags System

Flags are key-value pairs stored per-player or per-server.

### Flag Naming Conventions
| Prefix | Scope | Mutable by |
|---|---|---|
| `client.` | Player (client-side) | Client |
| `clientr.` | Player (read-only) | Server/NPC-server only |
| `server.` | Server global | Server/NPC-server |
| `serverr.` | Server global (read-only) | Server startup only |
| `this.` | NPC-local | Never sent to server |
| `gr.` | Special | System flags |

### Flag Set Packet
```
PLO_FLAGSET(28) STRING name=value
```
If value is empty: `PLO_FLAGSET(28) STRING name` (no `=`)

### Flag Delete Packet
```
PLO_FLAGDEL(31) STRING name
```

### Special `gr.` Flags
- `gr.x`, `gr.y`, `gr.z` - Movement position for old clients (pre-2.3)
- `gr.ip` - Player IP (if `flaghack_ip` enabled)
- `gr.fileerror`, `gr.filedata` - File hack results

---

## 13. Weapons System

### Weapon Add (NPC weapon, PLO_NPCWEAPONADD)

For **default weapons** (built-in items):
```
PLO_DEFAULTWEAPON(43) CHAR weapon_type_id
```

For **NPC/scripted weapons**:
```
PLO_NPCWEAPONADD(33)
CHAR name_length
STRING name
CHAR NPCPROP_IMAGE(0)          # prop type byte
CHAR image_length
STRING image
```

Then depending on client version and weapon type:

**GS1 script (client version < 5.07):**
```
CHAR NPCPROP_SCRIPT(1)
GSHORT script_length
STRING script         # §-tokenized GS1 client-side code
```

**GS2 bytecode (client version >= 4.0211):**
```
CHAR NPCPROP_CLASS(74)
GSHORT 0                        # Empty joined class list
"\n"
PLO_UNKNOWN197(197)
STRING bytecode_header         # From compiled GS2 header
",
GLONGLONG modtime              # Current time
"\n"
```

The GS2 bytecode header is extracted from the compiled bytecode:
```
GSHORT header_length            # Read from bytecode
STRING header_data              # First header_length bytes
```

**GS2 disabled (client > 5.07, no bytecode):**
Only image is sent, no script.

### Weapon Delete (PLO_NPCWEAPONDEL)
```
PLO_NPCWEAPONDEL(34) STRING weapon_name
```

### Clear Weapons
```
PLO_CLEARWEAPONS(194)
```

---

## 14. Map / GMAP / BigMap System

### Map Types
| Type | Description |
|---|---|
| BIGMAP | Classic `.txt` map file with level grid layout |
| GMAP | Modern gmap - player moves seamlessly between adjacent levels |

### GMAP Warp (PLO_PLAYERWARP2)
```
PLO_PLAYERWARP2(49)
CHAR x*2        // X position
CHAR y*2        // Y position
CHAR z*2+50     // Z position
CHAR map_x      // Level X coordinate on gmap
CHAR map_y      // Level Y coordinate on gmap
STRING gmap_name
```

### GMAP Level Navigation
Players on gmaps move between levels by setting `PLPROP_GMAPLEVELX` or `PLPROP_GMAPLEVELY`. This triggers a level change to the adjacent gmap cell.

### Set Active Level
```
PLO_SETACTIVELEVEL(156) STRING level_or_gmap_name
```
Tells the client which level to associate chests, baddies, NPCs with.

### BigMap
```
PLO_BIGMAP(171) maptext_file,mapimage_file,default_x,default_y
```

### MiniMap
```
PLO_MINIMAP(172) maptext_file,mapimage_file,default_x,default_y
```

---

## 15. RC (Remote Control) Protocol

RC packets (IDs 51-98) are admin operations:

### Key RC Packets
| ID | Name | Description |
|---|---|---|
| 51 | SERVEROPTIONSGET | Get server options |
| 52 | SERVEROPTIONSSET | Set server options |
| 59 | PLAYERPROPSGET | Get player properties |
| 60 | PLAYERPROPSSET | Set player properties |
| 61 | DISCONNECTPLAYER | Disconnect a player |
| 62 | UPDATELEVELS | Reload levels |
| 63 | ADMINMESSAGE | Send admin message |
| 65 | LISTRCS | List online RCs |
| 68 | SERVERFLAGSGET | Get server flags |
| 69 | SERVERFLAGSSET | Set server flags |
| 70 | ACCOUNTADD | Create account |
| 71 | ACCOUNTDEL | Delete account |
| 72 | ACCOUNTLISTGET | List accounts |
| 77 | ACCOUNTGET | Get account data |
| 78 | ACCOUNTSET | Set account data |
| 79 | RC_CHAT | RC chat message |
| 82 | WARPPLAYER | Warp a player |
| 83 | PLAYERRIGHTSGET | Get player rights |
| 84 | PLAYERRIGHTSSET | Set player rights |
| 87 | PLAYERBANGET | Get ban info |
| 88 | PLAYERBANSET | Set ban |
| 89 | FILEBROWSER_START | Start file browser |
| 90 | FILEBROWSER_CD | Change directory |
| 92 | FILEBROWSER_DOWN | Download file |
| 93 | FILEBROWSER_UP | Upload file |

`PLO_RC_SERVEROPTIONSGET` and `PLO_RC_FOLDERCONFIGGET` return the complete editable text
blob as `gtokenize()` comma-text after the packet id. Do not wrap these payloads in
`String8`/`GString`, and do not send raw newline-delimited text; older RC editors parse
those newlines as packet boundaries and open corrupt extra windows.
`SERVEROPTIONSGET` should tokenize the raw `config/serveroptions.txt` file contents, not
the parsed settings map, so comments, blank lines, ordering, and spacing remain visible.
`SERVEROPTIONSSET` should save the untokenized editable text back to the file before
reloading settings.

RC file browser packets follow the old RC string conventions. `PLO_RC_FILEBROWSER_DIRLIST`
is raw `gtokenize()` folder-right lines after the packet id. `PLO_RC_FILEBROWSER_DIR`
starts with a raw one-byte folder-name length, the folder name, then repeated file entries:
space, raw one-byte entry length, raw one-byte filename length plus filename, raw one-byte
rights length plus rights, `GINT5` file size, and `GINT5` mod time. `PLO_RC_FILEBROWSER_MESSAGE`
is raw text after the packet id. Client requests use raw tails for folder changes,
downloads, deletes, large-file markers, and folder deletes; uploads use a raw one-byte
filename length followed by filename and raw file bytes; move and rename use raw one-byte
length-prefixed name fields.

If an RC account has staff/full folder-management rights but no explicit account
`FOLDERRIGHT` lines, the file browser may derive editable folder rights from
`config/foldersconfig.txt`. Root wildcard entries such as `level *.nw` map to the
root folder with wildcard `*.nw`, not to a folder literally named `*.nw`. For the
default server layout, RC file-browser reads/list/stat operations resolve configured
asset folders through `world/` when there is no direct server-root folder, and the
browser root itself maps to `world/`.

### Player Rights (bitmask)
```
PLPERM_MODIFYSTAFFACCOUNT
PLPERM_SETRIGHTS
PLPERM_WARPTO
PLPERM_WARPTOPLAYER
PLPERM_SUMMON
PLPERM_UPDATELEVEL
```

---

## 16. NPC Server (NC) Protocol

When `V8NPCSERVER` is enabled, the server runs V8 JavaScript for NPC scripting.

### NC Login Sequence
1. Client connects as type 3 (NC)
2. The server treats this as `PLTYPE_NC`, not `PLTYPE_NPCSERVER`; `PLTYPE_NPCSERVER`
is only the pseudo-player shown on the listserver when `serverside=true`.
3. Server sends database NPC list:
```
PLO_NC_NPCADD(158) GINT npc_id, NPCPROP_NAME, NPCPROP_TYPE, NPCPROP_CURLEVEL
```
4. Server sends class list:
```
PLO_NC_CLASSADD(163) class_name\n
```

### NC Packets
| ID | Name | Format |
|---|---|---|
| 100 | LISTNPCS | (blank), server responds with database NPC `PLO_NC_NPCADD` packets |
| 103 | NPCGET | `GINT id` |
| 104 | NPCDELETE | `GINT id` |
| 105 | NPCRESET | `GINT id` |
| 106 | NPCSCRIPTGET | `GINT id` |
| 107 | NPCWARP | `GINT id, CHAR x*2, CHAR y*2, level` |
| 108 | NPCFLAGSGET | `GINT id` |
| 109 | NPCSCRIPTSET | `GINT id, GSTRING script` |
| 110 | NPCFLAGSSET | `GINT id, GSTRING flags` |
| 111 | NPCADD | `GSTRING info` (name,id,type,scripter,level,x,y) |
| 112 | CLASSEDIT | `STRING class_name` |
| 113 | CLASSADD | `CHAR name_len, name, GSTRING script` |
| 114 | LOCALNPCSGET | `STRING level` |
| 115 | WEAPONLISTGET | (blank) |
| 116 | WEAPONGET | `STRING weapon_name` |
| 117 | WEAPONADD | `CHAR weapon_len, weapon, CHAR img_len, img, code` |
| 118 | WEAPONDELETE | `STRING weapon_name` |
| 119 | CLASSDELETE | `STRING class_name` |

### NPC Server Address Notification
RCs with NPC-control rights request the NC/NPC-server connection target with `PLI_NPCSERVERQUERY`:
```
PLI_NPCSERVERQUERY(94) GSHORT npcserver_id, STRING "location"
```

When an NPC server is running, the server replies:
```
PLO_NPCSERVERADDR(79) GSHORT npcserver_id, STRING "ip,port"
```

### NPC Scripting Events

NPCs respond to these events (queued via V8 script engine):

| Event | Trigger |
|---|---|
| `npc.created` | NPC first loaded/created |
| `npc.timeout` | NPC timeout counter reaches 0 |
| `npc.playerchats` | A player chats in the NPC's level |
| `npc.playerenters` | A player enters the NPC's level |
| `npc.playerleaves` | A player leaves the NPC's level |
| `npc.playertouchsme` | A player touches the NPC |
| `npc.playerlogin` | A player logs into the server |
| `npc.playerlogout` | A player logs out of the server |
| `npc.warped` | NPC warped to a new level |
| `npc.trigger` | triggeraction received by this NPC |

### NPC Scripting Objects (V8)

**Server object:**
- `findlevel(name)`, `findnpc(id/name)`
- `flags['key']`, `serverflags['key']`
- `players`, `npcs` arrays
- `timevar`, `timevar2`
- `savelog(file, msg)`, `sendtonc(msg)`, `sendtorc(msg)`

**Player object:**
- Properties: `id`, `account`, `nick`, `x`, `y`, `hearts`, `rupees`, `ap`, `guild`, etc.
- Functions: `addweapon()`, `removeweapon()`, `setlevel2()`, `freezeplayer()`, `setani()`, `triggeraction()`
- `flags['key']` for client flags

**NPC object:**
- Properties: `id`, `x`, `y`, `image`, `chat`, `hearts`, `dir`, etc.
- Functions: `message()`, `move()`, `setimg()`, `setshape()`, `join()`, `destroy()`
- `flags['key']` for NPC flags
- `registerTrigger(action, function)` for triggeractions
- `setpm(function)` for Control-NPC PM handling

**Level object:**
- `findareanpcs(x,y,w,h)`, `findnearestplayers(x,y)`
- `putexplosion(radius, x, y)`, `putnpc(x, y, script)`
- `onwall(x, y)`, `players`, `npcs`

---

## 17. List Server Protocol

The GServer maintains a persistent TCP connection to a list server for authentication.

### Server→ListServer (SVO)
| ID | Name | Description |
|---|---|---|
| 0 | SETNAME | Set server name |
| 1 | SETDESC | Set description |
| 5 | SETIP | Set server IP |
| 6 | SETPORT | Set port |
| 7 | SETPLYR | Set player count |
| 8 | VERIACC | Verify account (deprecated) |
| 9 | VERIGUILD | Verify guild |
| 11 | NICKNAME | Set nickname |
| 12 | GETPROF | Get profile |
| 13 | SETPROF | Set profile |
| 14 | PLYRADD | Player added |
| 15 | PLYRREM | Player removed |
| 17 | VERIACC2 | Verify account (v2) |
| 21 | GETFILE3 | Request file from player |
| 25 | SERVERINFO | Get server info for warp |
| 29 | PMPLAYER | PM to external player |
| 31 | SENDTEXT | Send text data |

### ListServer→Server (SVI)
| ID | Name | Description |
|---|---|---|
| 0 | VERIACC | Account verification result |
| 1 | VERIGUILD | Guild verification result |
| 5 | VERSIONOLD | Client version too old |
| 6 | VERSIONCURRENT | Version OK |
| 7 | PROFILE | Profile data |
| 8 | ERRMSG | Error message |
| 11 | VERIACC2 | Account verification result (v2) |
| 18 | SERVERINFO | Server info for warp |
| 19 | REQUESTTEXT | Request text |
| 20 | SENDTEXT | Send text |
| 29 | PMPLAYER | PM from external player |
| 30 | ASSIGNPCID | Assign player client ID |
| 99 | PING | Keepalive ping |
| 100 | RAWDATA | Raw data |

### Listserver Behavior Notes

- After the initial registration switches to the compressed listserver codec, outbound SVO packets must still be encoded through that active codec. Do not enqueue raw packet bytes directly.
- `SVO_SENDTEXT` carries raw text immediately after the encoded packet id. Do not use a length-prefixed string for text such as `Listserver,settings,allowedversions,...`.
- Allowed client versions are reported with `SVO_SENDTEXT` using `Listserver,settings,allowedversions,` followed by the comma-separated values loaded from `config/allowedversions.txt`.
- Per-player listserver text requests such as `SVO_REQUESTLIST` and `SVO_REQUESTSVRINFO` carry `GUShort player_id` immediately after the encoded packet id, followed by the tokenized text payload. Matching `SVI_REQUESTTEXT` replies also begin with `GUShort player_id`, then the text payload relayed to that player as `PLO_SERVERTEXT`.
- Login-server `requestText("lister", "simplelist")` is forwarded to the listserver as a per-player `SVO_REQUESTLIST` with text fields `GraalEngine`, `lister`, `simpleserverlist`.
- `PLI_REQUESTTEXT` payloads may arrive as Graal comma-tokenized text such as `-ServerListScreen,pmservers,""` or as separator-delimited text. Decode either form before selecting `weapon`, `type`, and `option`.
- The hub/listserver sends `SVI_PING` for latency measurement; respond with the existing ping response path. To keep an idle socket from going stale without skewing latency, periodically resend `SVO_SETIP` using the configured `serverip` value or `AUTO`.

---

## 18. File Transfer Protocol

### Simple File Request (PLI_WANTFILE)
```
Client → PLI_WANTFILE(23) STRING filename
```

### File Response (PLO_FILE)
For small files:
```
PLO_RAWDATA(100) GINT(packet_size)
PLO_FILE(102) [GINT5 modtime] CHAR filename_len STRING filename BYTE[file_data_size] data \n
```

Note: v1.41 clients (CLVER < 2.1) do not send/receive modtime.

For large files (>32000 bytes):
```
PLO_LARGEFILESTART(68) STRING filename
PLO_LARGEFILESIZE(84) GLONGLONG total_size
// Multiple chunks:
PLO_RAWDATA(100) GINT(chunk_size)
PLO_FILE(102) ... chunk_data ...
PLO_LARGEFILEEND(69) STRING filename
```

### File Not Found
```
PLO_FILESENDFAILED(30) STRING filename
```

### File Up To Date
```
PLO_FILEUPTODATE(45) STRING filename
```

### Default Files
These files are never sent (client has them built-in):
- Standard ganis: `walk.gani`, `idle.gani`, `sword.gani`, etc.
- Default images: `body.png`, `sword1.png`, `shield1.png`, `pics1.png`
- Sound effects: `sword.wav`, `bomb.wav`, etc.

---

## 19. requestText / sendText System

This is a general-purpose query/response mechanism between client scripts and the server.

### Client Request
```
PLI_REQUESTTEXT(152) GSTRING request_type, [GSTRING param1, GSTRING param2, ...]
```

### Server Response
```
PLO_SERVERTEXT(82) GSTRING request_type, GSTRING param1, [GSTRING param2, ...]
```

### Client Sends Data
```
PLI_SENDTEXT(154) GSTRING type, [params...]
```

### Common requestText Types

| Type | Param | Returns | Description |
|---|---|---|---|
| `clientrc` | `1/0` | - | Open/close Client-RC |
| `list` | - | serverlist | Server list |
| `simplelist` | - | simplified list | Simplified server list |
| `pmservers` | - | server list | Public servers for playerlist |
| `pmserverplayers` | servername | player list | Players on a server |
| `pmguilds` | - | guild list | Active guild tags |
| `pmguildplayers` | guildname | player list | Players in a guild |
| `weaponlist` | - | weapon list | Server weapons (RC) |
| `classlist` | - | class list | Server classes (RC) |
| `npclist` | - | DB NPC list | Server DB NPCs (RC) |
| `weapon` | weaponname | weapon script | Get weapon script (RC) |
| `class` | classname | class script | Get class script (RC) |
| `npc` | npcname/id | NPC script | Get DB NPC script (RC) |
| `npcflags` | npcname/id | flag list | DB NPC flags (RC) |
| `options` | - | server options | Get server options (RC) |
| `folderconfig` | - | folder config | Get folder config (RC) |
| `serverflags` | - | flag list | Get server flags (RC) |
| `rights` | accountname | rights data | Player rights (RC) |
| `comments` | accountname | comments | Player comments (RC) |
| `playerflags` | accountname | flags | Player script flags (RC) |
| `playerweapons` | accountname | weapon list | Player weapons (RC) |
| `playerchests` | accountname | chest list | Player chests (RC) |
| `playerattributes` | accountname | attributes | Player attributes (RC) |
| `bantypes` | - | ban types | Available ban types (RC) |
| `localbans` | - | ban list | Local bans (RC) |
| `upgradeinfo` | - | upgrade info | Current player upgrade info |
| `nextdbnpcid` | - | int | Next available DB NPC ID (RC) |
| `scripthelp` | searchterm | help text | Script help search (RC) |

### Common sendText Types

| Type | Params | Description |
|---|---|---|
| `adminmessage` | playerid, message | Send admin message |
| `disconnect` | playerid, reason | Disconnect player |
| `resetnpc` | npcname/id | Reset DB NPC |
| `deleteweapon` | weaponname | Delete weapon |
| `deleteclass` | classname | Delete class |
| `deletenpc` | npcname | Delete DB NPC |
| `irc` | action, params | IRC operations |
| `syncoptions` | "distance", {h, v} | Sync display distance |

---

## 20. Account / Save Format

Account files are stored in `accounts/` as `.txt` files.

### Format (key=value pairs, newline delimited)
```
account=USERNAME
nick=*USERNAME
x=30
y=30
maxpower=3
rupees=100
arrows=30
bombs=30
glovepower=1
swordpower=0
shieldpower=0
ap=100
swordimage=sword1.png
shieldimage=shield1.png
headimage=head0.png
bodyimage=body.png
colors=0,0,0,0,0
statusflags=20
ip=*.*.*.*
rating=1500,350
kills=0
deaths=0
onlinetime=0
```

### Flag Storage
Flags stored in account file:
```
FLAG flagname=value
FLAG flagname2=value2
```

### Chest Storage
```
CHEST levelname,x,y
```

### Weapon Storage
```

### Account Save Timing

Loaded player accounts should save on disconnect and during periodic server saves. Login should use the account's saved `LEVEL`, `X`, and `Y` when present; only accounts without a saved level should fall back to `startlevel`/`unstickmelevel` at the default start coordinates.
WEAPON Weapon Name
WEAPON Another Weapon
```

### IP Format
IP addresses use glob patterns: `*.*.*.*`, `192.168.1.*`, etc.

---

## 21. Special Triggeractions

These triggeractions are processed server-side when `triggerhack_*` settings are enabled:

### Weapons (triggerhack_weapons)
```
triggeraction 0,0,gr.addweapon,weapon1,weapon2,weapon3;
triggeraction 0,0,gr.deleteweapon,weapon1,weapon2,weapon3;
```

### Guilds (triggerhack_guilds)
```
triggeraction 0,0,gr.addguildmember,guild,account,nickname;
triggeraction 0,0,gr.removeguildmember,guild,account;
triggeraction 0,0,gr.removeguild,guild;
triggeraction 0,0,gr.setguild,guild,account;
```

### Groups (triggerhack_groups)
```
triggeraction 0,0,gr.setgroup,group;
triggeraction 0,0,gr.setlevelgroup,group;
triggeraction 0,0,gr.setplayergroup,account,group;
```

### Files (triggerhack_files)
```
triggeraction 0,0,gr.appendfile,filename,text;
triggeraction 0,0,gr.writefile,filename,text;
triggeraction 0,0,gr.readfile,filename,line_pos;
// Returns: gr.fileerror and gr.filedata flags
```

### Props (triggerhack_props)
```
triggeraction 0,0,gr.attr1,data;   // gr.attr1 through gr.attr30
triggeraction 0,0,gr.fullhearts,amount;
```

### Levels (triggerhack_levels)
```
triggeraction 0,0,gr.updatelevel;
triggeraction 0,0,gr.updatelevel,levelname;
```

### RC Chat (triggerhack_rc)
```
triggeraction 0,0,gr.rcchat,Some chat text;
```

### Exec Scripts (triggerhack_execscript)
```
triggeraction 0,0,gr.es_clear;
triggeraction 0,0,gr.es_set,param1,param2,...;
triggeraction 0,0,gr.es_append,phrase;
triggeraction 0,0,gr.es,account,script_name;
```

### NPC Movement (always enabled)
```
triggeraction 0,0,gr.npc.move,id,dx,dy,duration,options;
triggeraction 0,0,gr.npc.setpos,id,x,y;
```

---

## 22. Server Options Reference

Config file: `config/serveroptions.txt`

| Option | Default | Description |
|---|---|---|
| `name` | My Server | Server display name |
| `serverip` | AUTO | Public IP (AUTO = detect) |
| `serverport` | 14802 | Listen port |
| `localip` | AUTO | LAN IP for local connections |
| `listip` | listserver.graal.in | List server host |
| `listport` | 14900 | List server port |
| `maxplayers` | 128 | Max concurrent players |
| `debugmode` | false | Enable high-level debug logs such as packet names and login flow |
| `packetdebugmode` | false | Enable raw packet bytes, encryption iterator, and compression trace logs |
| `staff` | (Manager),YOURACCOUNT | Comma-separated staff accounts |
| `staffguilds` | Server,Manager,... | Guilds shown as "Staff" in playerlist |
| `staffhead` | head25.png | Head image for RC players |
| `onlystaff` | false | Staff-only mode |
| `apsystem` | true | Enable alignment points |
| `aptime0`-`aptime4` | 30-1200 | AP recharge times per bracket |
| `jaillevels` | police2.graal,... | Levels where PM/warp is restricted |
| `unstickmelevel` | onlinestartlocal.nw | Unstick destination |
| `bushitems` | true | Items drop from bushes |
| `vasesdrop` | true | Vases drop hearts |
| `dropitemsdead` | true | Drop items on death |
| `tiledroprate` | 50 | % chance of bush item drops |
| `noexplosions` | false | Disable explosions |
| `defaultweapons` | true | Allow default weapons |
| `putnpcenabled` | true | Allow putnpc command |
| `serverside` | false | Server-side collision (experimental) |
| `globalguilds` | true | Allow global guild verification |
| `flaghack_movement` | true | Enable gr.x/gr.y/gr.z movement hack |
| `flaghack_ip` | false | Enable gr.ip flag |
| `triggerhack_*` | various | Enable triggeraction hacks |
| `disconnectifnotmoved` | true | Idle disconnect |
| `maxnomovement` | 1200 | Idle timeout (seconds) |
| `maps` | (empty) | Bigmap files |
| `gmaps` | (empty) | GMAP files |
| `groupmaps` | (empty) | Group-instanced maps |
| `playerlisticons` | Online,Away,... | Playerlist status options |
| `profilevars` | (see config) | Profile display variables |
| `protectedweapons` | (empty) | Weapons given on every connect |
| `cropflags` | true | Crop flags to 223 chars |
| `warptoforall` | false | Allow warp for non-staff |

---

## 23. File Formats

### Level File Formats
Levels can be stored in multiple formats:

**NW Format** (`.nw` files, text-based):
```
BOARDn            # Board tiles, newline-delimited rows
LINK x y w h newlevel newx newy   # Level links
SIGN x y text     # Signs
CHEST x y item signidx  # Chests
NPC ...           # NPC definitions
BADDY x y type   # Baddies
```

**Graal Binary Format** (`.graal` files):
Binary format with header, tiles, links, signs, etc.

**Zelda Format** (old format, deprecated):
Legacy binary format.

### Weapon File Format
```
GRAWP001\r\n
REALNAME weaponname\r\n
IMAGE imagefile.png\r\n
BYTECODE bytecode_filename\r\n    # Optional, compiled GS2
SCRIPT\r\n
... weapon script code ...\r\n
SCRIPTEND\r\n
```
If BYTECODE is present, it loads compiled bytecode from `weapon_bytecode/filename`. If both SCRIPT and BYTECODE are present, bytecode takes priority.

### NPC File Format (Database NPCs)
```
GRNPC001\r\n
NAME npcname\r\n
ID npcid\r\n
TYPE scripttype\r\n
SCRIPTER scriptername\r\n
IMAGE image.png\r\n
STARTLEVEL levelname\r\n
STARTX x\r\n
STARTY y\r\n
STARTZ z\r\n
LEVEL levelname\r\n          # Current level
X x\r\n
Y y\r\n
Z z\r\n
NICK nickname\r\n
ANI gani\r\n
HP hearts\r\n
GRALATS amount\r\n
ARROWS amount\r\n
BOMBS amount\r\n
GLOVEP power\r\n
SWORDP power\r\n
SHIELDP power\r\n
BOWP power\r\n
BOW image\r\n
HEAD image\r\n
BODY image\r\n
SWORD image\r\n
SHIELD image\r\n
HORSE image\r\n
COLORS c0,c1,c2,c3,c4\r\n
SPRITE sprite\r\n
AP alignment\r\n
TIMEOUT seconds\r\n
LAYER 0\r\n
SHAPETYPE 0\r\n
SHAPE width height\r\n
DONTBLOCK 1\r\n            # Optional
SAVEARR s0,s1,...,s9\r\n
ATTR1 value\r\n          # ATTR1 through ATTR30
FLAG name=value\r\n      # NPC flags
CANWARP 1\r\n           # Optional
NPCSCRIPT\r\n
... NPC script code ...\r\n
NPCSCRIPTEND\r\n
```

### NPC Script Format (GS1/GS2)

NPC scripts are split into server-side and client-side sections:
```
// Server-side code here
//#CLIENTSIDE
// Client-side code here
```

If `gs2default` is enabled in server options, scripts are treated as GS2 by default.

**Client-side scripts are sent §-tokenized:**
- Newlines (`\n`) are replaced with `\xa7` (§)
- `//#CLIENTSIDE` prefix is prepended if missing
- Each line is trimmed
- Max size: 0x705F (28767) bytes

**join command:**
```
join classname;
```
Appends the contents of `classname.txt` from the filesystem to the NPC/weapon script.

**toweapons command:**
```
toweapons WeaponName;
```
Extracts the weapon name from client-side NPC code. The NPC becomes a weapon with this name.

### Map File Formats

**BIGMAP format** (`.txt`):
Text grid where each character represents a level position. Levels are listed by name.

**GMAP format** (`.gmap`):
```
BOARDn
level1.nw level2.nw ...
```
GMAPs allow seamless movement between adjacent levels. Player position tracked via PLPROP_GMAPLEVELX/Y.

### Account File Format
```
account=USERNAME
nick=*USERNAME
x=30
y=30.5
maxpower=3
rupees=100
arrows=30
bombs=30
glovepower=1
swordpower=0
shieldpower=0
ap=100
swordimage=sword1.png
shieldimage=shield1.png
headimage=head0.png
bodyimage=body.png
bowimage=
horseimage=
colors=0,0,0,0,0
statusflags=20
mp=0
ip=*.*.*.*
rating=1500,350
kills=0
deaths=0
onlinetime=0
communityname=
language=English
rights=0
comments=
email=
banned=false
isguest=false
FLAG flagname=value
WEAPON Weapon Name
CHEST levelname,x,y
```

### Server Configuration Files

| File | Purpose |
|---|---|
| `config/serveroptions.txt` | Main server configuration |
| `config/adminconfig.txt` | Admin/RC configuration |
| `config/foldersconfig.txt` | Filesystem folder configuration |
| `config/allowedversions.txt` | Allowed client versions |
| `config/ipbans.txt` | IP ban list |
| `config/rchelp.txt` | RC help text |
| `config/rcmessage.txt` | RC welcome message |
| `config/rules.txt` | Server rules |
| `config/servermessage.html` | Server welcome HTML message |
| `serverflags.txt` | Server flag defaults |

### Folder Configuration Types
```
FS_ALL = 0     # All files
FS_FILE = 1    # General files
FS_LEVEL = 2   # Level files (.nw, .graal)
FS_HEAD = 3    # Head images
FS_BODY = 4    # Body images
FS_SWORD = 5   # Sword images
FS_SHIELD = 6  # Shield images
```

### ServerList File Request Types
```
SVF_HEAD = 0
SVF_BODY = 1
SVF_SWORD = 2
SVF_SHIELD = 3
SVF_FILE = 4
```

---

## 24. Critical Implementation Notes

### New World Time (NWTime)
The server maintains a "new world time" counter that represents elapsed time. Sent as `GINT4`:
```
PLO_NEWWORLDTIME(42) GINT4 nwtime
```

### Level Leader
The first player in a level is the "leader". The leader handles baddy AI and is authoritative for certain level state. On leader disconnect, the next player becomes leader:
```
PLO_ISLEADER(10)
```

### Player Visibility / Level Area
Players can see other players in:
1. The same level
2. Adjacent levels on a GMAP (±1 in both X and Y)
3. Not across group boundaries on group maps

### Precise vs Non-Precise Movement
- Pre-2.3 clients: `PLPROP_X/Y/Z` (single byte, tile units × 2)
- 2.3+ clients: `PLPROP_X2/Y2/Z2` (unsigned short, bit 0 = negative flag)
- Both are sent simultaneously so old and new clients can see each other

### Disconnect Handling
```
PLO_DISCMESSAGE(16) STRING reason
```
Must be sent before closing the socket so the client sees the reason.

### Tokenization
Editable multiline blobs sent over comma-delimited packet fields should use
`gtokenize()` before sending and `guntokenize()` after receiving. This is comma-text
quoting, not a `\xa7` newline replacement.

### Version Detection
The server maps version strings to internal IDs:
- `CLVER_1_411` = 1.41
- `CLVER_2_1` = 2.1
- `CLVER_2_3` = 2.3
- `CLVER_2_31` = 2.31
- `CLVER_4_0211` = 4.x
- `CLVER_5_07` = 5.07
- `CLVER_5_12` = 5.12
- `CLVER_6_015` = 6.015
- `CLVER_UNKNOWN` = unknown

Version affects:
- File modtime support (2.1+)
- Precise movement (2.3+)
- Class support (4.0211+)
- Shoot2 format (5.07+)
- Process list request (pre-6.015)

### Rating System (Glicko)
Spar rating packed into `PLPROP_RATING`:
```
packed = ((rating & 0xFFF) << 9) | (deviation & 0x1FF)
```
Rating range: 0-4000. Deviation range: 50-350.

### Singleplayer Levels
Levels with the `singleplayer` NPC command become per-player instances. The server clones the level for each player.

### Group Maps
Players in groups only see group members on group maps. Managed via `gr.setgroup` and `gr.setlevelgroup` triggeractions.

### Bomb Data Format
```
PLO_BOMBADD(11) GUSHORT player_id, CHAR x, CHAR y, CHAR player_power, CHAR timer
```
- `player_power`: bits 7-2 = player id, bits 1-0 = power
- `timer`: increments of 0.05 seconds (default 55 = 2.75s)

### Shoot V1 (old clients < 5.07)
```
GINT shoot_id
CHAR x/16
CHAR y/16
CHAR z/16+50
CHAR angle
CHAR zangle
CHAR speed
CHAR gani_len, gani
CHAR param_len, params
```

### Shoot V2 (clients ≥ 5.07)
```
GUSHORT x
GUSHORT y
GUSHORT z
CHAR offset_x+32
CHAR offset_y+32
CHAR angle
CHAR zangle
CHAR speed
CHAR gravity
GUSHORT gani_len, gani
CHAR param_len, params
```

### Item Types
| Byte | Item |
|---|---|
| 0 | Green Rupee (1 gralat) |
| 1 | Blue Rupee (5 gralats) |
| 2 | Red Rupee (30 gralats) |
| 3 | Bombs (5) |
| 4 | Darts/Arrows (5) |
| 5-6 | Various weapons |
| 19 | Gold Rupee (100 gralats) |
| 40+ | Heart |

### Server Flags vs Client Flags
- `server.flagname` → stored in server's global flag map, broadcast to all players
- `client.flagname` → stored per-player, not broadcast (client manages locally)
- Flags without prefix → per-player client flags, broadcast to other players

---

*End of document. This specification is derived from GServer-v2 source code. Implementation in any language requires careful attention to the binary encoding, encryption, and the exact byte-level packet structures described above.*
