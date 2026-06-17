package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSettingsControlsPacketDebugSeparately(t *testing.T) {
	oldDebugMode := DEBUG_MODE
	oldPacketDebugMode := PACKET_DEBUG_MODE
	t.Cleanup(func() {
		DEBUG_MODE = oldDebugMode
		PACKET_DEBUG_MODE = oldPacketDebugMode
	})

	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}

	writeOptions := func(contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(configDir, "serveroptions.txt"), []byte(contents), 0644); err != nil {
			t.Fatalf("write serveroptions: %v", err)
		}
	}

	server := &Server{logger: NewLogger("", false), config: NewFileSystem(dir), settings: NewSettings()}
	writeOptions("debugmode = true\npacketdebugmode = false\n")
	server.loadSettings()
	if !DEBUG_MODE {
		t.Fatalf("DEBUG_MODE = false, want true")
	}
	if PACKET_DEBUG_MODE {
		t.Fatalf("PACKET_DEBUG_MODE = true, want false")
	}

	writeOptions("debugmode = false\npacketdebugmode = true\n")
	server.loadSettings()
	if DEBUG_MODE {
		t.Fatalf("DEBUG_MODE = true, want false")
	}
	if !PACKET_DEBUG_MODE {
		t.Fatalf("PACKET_DEBUG_MODE = false, want true")
	}
}

func TestGS2CompilerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GS2_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || len(args) < sep+3 {
		fmt.Fprintln(os.Stderr, "missing input/output args")
		os.Exit(2)
	}
	inputPath := args[sep+1]
	outputPath := args[sep+2]
	data, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if strings.Contains(string(data), "bad syntax") {
		fmt.Fprintln(os.Stderr, "parser error occurred near line 1: bad syntax")
		os.Exit(2)
	}
	if strings.Contains(string(data), "silent output failure") {
		fmt.Fprintln(os.Stdout, " -> [ERROR] malformed input at line 1: silent output failure")
		os.Exit(0)
	}
	payload := "bytecode:" + os.Getenv("GS2_SCRIPT_TYPE") + ":" + os.Getenv("GS2_SCRIPT_NAME")
	if err := os.WriteFile(outputPath, []byte(payload), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNewServerCreatesNPCServerRuntime(t *testing.T) {
	server := NewServer("Test")
	if server.npcServer == nil {
		t.Fatal("NewServer did not initialize npc-server runtime")
	}
	if server.npcServer.host != server {
		t.Fatalf("npc-server runtime host = %#v, want server", server.npcServer.host)
	}
}

func TestLoadSettingsSyncsNPCServerPlayer(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	writeOptions := func(contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(configDir, "serveroptions.txt"), []byte(contents), 0644); err != nil {
			t.Fatalf("write serveroptions: %v", err)
		}
	}
	countNPCServers := func(server *Server) int {
		t.Helper()
		count := 0
		for _, player := range server.players {
			if player != nil && player.playerType == PLTYPE_NPCSERVER {
				count++
			}
		}
		return count
	}

	server := NewServer("Test")
	server.config = NewFileSystem(dir)
	server.logger = NewLogger("", false)

	writeOptions("serverside = true\nnickname = NPC-Server\n")
	server.loadSettings()
	if got := countNPCServers(server); got != 1 {
		t.Fatalf("NPC server count = %d, want 1", got)
	}
	npc := server.players[1]
	if npc == nil || npc.accountName != "(npcserver)" || npc.character.nickName != "NPC-Server (Server)" {
		t.Fatalf("NPC server player = %#v", npc)
	}

	server.loadSettings()
	if got := countNPCServers(server); got != 1 {
		t.Fatalf("NPC server count after reload = %d, want 1", got)
	}

	writeOptions("serverside = false\n")
	server.loadSettings()
	if got := countNPCServers(server); got != 0 {
		t.Fatalf("NPC server count after disabling = %d, want 0", got)
	}
}

func TestLoadConfigFilesHonorsListserverFalse(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "serveroptions.txt"), []byte("name = LAN Test\nlistserver = false\n"), 0644); err != nil {
		t.Fatalf("write serveroptions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "adminconfig.txt"), []byte(""), 0644); err != nil {
		t.Fatalf("write adminconfig: %v", err)
	}

	server := NewServer("Test")
	server.config = NewFileSystem(dir)
	server.logger = NewLogger("", false)

	if err := server.loadConfigFiles(); err != nil {
		t.Fatalf("loadConfigFiles: %v", err)
	}
	if server.name != "LAN Test" {
		t.Fatalf("server name = %q, want LAN Test", server.name)
	}
	if server.serverList == nil {
		t.Fatal("serverList was nil")
	}
	if server.serverList.enabled {
		t.Fatal("serverList.enabled = true, want false when listserver=false")
	}
}

func TestLoadConfigFilesBuildsMultipleListservers(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	options := "listserver = true\nlistip = listserver.graal.in, listserver.moreno.land\nlistport = 14900, 14901\n"
	if err := os.WriteFile(filepath.Join(configDir, "serveroptions.txt"), []byte(options), 0644); err != nil {
		t.Fatalf("write serveroptions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "adminconfig.txt"), []byte(""), 0644); err != nil {
		t.Fatalf("write adminconfig: %v", err)
	}

	server := NewServer("Test")
	server.config = NewFileSystem(dir)
	server.logger = NewLogger("", false)

	if err := server.loadConfigFiles(); err != nil {
		t.Fatalf("loadConfigFiles: %v", err)
	}
	if len(server.serverLists) != 2 {
		t.Fatalf("serverLists len = %d, want 2", len(server.serverLists))
	}
	if server.serverLists[0].host != "listserver.graal.in" || server.serverLists[0].port != "14900" {
		t.Fatalf("first endpoint = %s:%s", server.serverLists[0].host, server.serverLists[0].port)
	}
	if server.serverLists[1].host != "listserver.moreno.land" || server.serverLists[1].port != "14901" {
		t.Fatalf("second endpoint = %s:%s", server.serverLists[1].host, server.serverLists[1].port)
	}
	if server.serverList != server.serverLists[0] {
		t.Fatal("primary serverList is not first configured endpoint")
	}
}

func TestServerListTimedEventsSkipsWhenDisabled(t *testing.T) {
	server := NewServer("Test")
	server.logger = NewLogger("", false)
	sl := server.serverList
	sl.enabled = false
	sl.connected = false
	sl.nextConnectionAttempt = time.Time{}

	sl.doTimedEvents()

	if !sl.lastTimer.IsZero() {
		t.Fatalf("lastTimer = %v, want zero when disabled", sl.lastTimer)
	}
}

func TestLoginServerModeRequiresExplicitOption(t *testing.T) {
	server := NewServer("Login")
	server.settings = NewSettings()
	if server.shouldUseLoginServerMode() {
		t.Fatal("login server mode enabled without loginserver option")
	}

	server.settings.Set("loginserver", "true")
	if !server.shouldUseLoginServerMode() {
		t.Fatal("login server mode disabled with loginserver=true")
	}
}

func TestNPCServerLoadsPseudoPlayerAccount(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "config/serveroptions.txt", "serverside = true\n")
	writeTestFile(t, server.config.GetBasePath(), "accounts/(npcserver).txt", ""+
		"GRACC001\n"+
		"NICK NPC-Server (Server)\n"+
		"COMMUNITYNAME (npcserver)\n"+
		"LEVEL \n"+
		"X 30.00\n"+
		"Y 30.50\n"+
		"MAXHP 3\n"+
		"HP 3\n"+
		"IP -2115681208\n")

	server.loadSettings()

	npc := server.players[1]
	if npc == nil {
		t.Fatalf("NPC server pseudo-player was not created")
	}
	if got, want := int(npc.x/8), 60; got != want {
		t.Fatalf("NPC server list x = %d, want %d", got, want)
	}
	if got, want := int(npc.y/8), 61; got != want {
		t.Fatalf("NPC server list y = %d, want %d", got, want)
	}
	if npc.accountIp != 0 {
		t.Fatalf("NPC server accountIp = %d, want 0", npc.accountIp)
	}
	wantIP := NewBuffer().WriteGInt5(0).Bytes()
	if got := npc.getProp(PLPROP_IPADDR); !bytes.Equal(got, wantIP) {
		t.Fatalf("NPC server IP prop = % X, want % X", got, wantIP)
	}
}

func TestNPCServerUsesServerStaffGuild(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("serverside", "true")
	server.settings.Set("staffguilds", "Server,Manager,Owner")

	server.initNPCServer()

	npc := server.GetPlayer(1)
	if npc == nil {
		t.Fatal("npc-server was not initialized")
	}
	if got := npc.guild; got != "Server" {
		t.Fatalf("npc-server guild = %q, want Server", got)
	}
}

func TestLoadNpcsLoadsControlNPCWithSavedID(t *testing.T) {
	server := newLoginTestServer(t)
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server.levels[level.levelName] = level
	writeTestFile(t, server.config.GetBasePath(), "npcs/npcControl-NPC.txt", ""+
		"GRNPC001\n"+
		"NAME Control-NPC\n"+
		"ID 10000\n"+
		"TYPE CONTROL\n"+
		"STARTLEVEL onlinestartlocal.nw\n"+
		"STARTX 30.00\n"+
		"STARTY 30.50\n"+
		"NPCSCRIPT\n"+
		"function onCreated() {\n"+
		"  server.sendtorc(\"Script Server Initialized!\");\n"+
		"}\n"+
		"NPCSCRIPTEND\n")

	server.loadNpcs(false)

	npc := server.GetNPC(10000)
	if npc == nil {
		t.Fatalf("Control-NPC was not loaded at saved id 10000")
	}
	if npc.npcName != "Control-NPC" || npc.scriptType != "CONTROL" {
		t.Fatalf("loaded npc name/type = %q/%q", npc.npcName, npc.scriptType)
	}
	if !strings.Contains(npc.script, "Script Server Initialized") {
		t.Fatalf("loaded npc script = %q", npc.script)
	}
	if npc.level != level || level.npcs[npc.id] != npc {
		t.Fatalf("loaded npc was not attached to saved start level")
	}
	if server.npcIdGen != 10001 {
		t.Fatalf("npcIdGen = %d, want 10001", server.npcIdGen)
	}
}

func TestNCListNPCsSendsDatabaseNPCsToNCConnection(t *testing.T) {
	server := newLoginTestServer(t)
	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc
	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.scriptType = "CONTROL"
	npc.level = &Level{levelName: "onlinestartlocal.nw"}
	if !server.AddNPC(npc) {
		t.Fatalf("AddNPC returned false")
	}

	nc.msgPLI_NC_LISTNPCS([]byte{PLI_NC_LISTNPCS})

	want := NewBuffer()
	want.WriteByte(PLO_NC_NPCADD + 32)
	want.WriteGInt(10000)
	want.WriteGChar(NPCPROP_NAME)
	want.WriteString8Encoded("Control-NPC")
	want.WriteGChar(NPCPROP_TYPE)
	want.WriteString8Encoded("CONTROL")
	want.WriteGChar(NPCPROP_CURLEVEL)
	want.WriteString8Encoded("onlinestartlocal.nw")
	want.WriteByte('\n')
	if !bytes.Contains(nc.outQueue, want.Bytes()) {
		t.Fatalf("NC list payload = % X, want to contain % X", nc.outQueue, want.Bytes())
	}
}

func TestNCNpcGetParsesPayloadAfterPacketID(t *testing.T) {
	server := newLoginTestServer(t)
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.scriptType = "CONTROL"
	npc.saves[0] = 7
	if !server.AddNPC(npc) {
		t.Fatalf("AddNPC returned false")
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_NPCGET)
	packet.WriteGInt(10000)
	if !nc.msgPLI_NC_NPCGET(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCGET returned false")
	}

	wantDump := "Variables dump from npc Control-NPC\n\n" +
		"Control-NPC.type: CONTROL\n" +
		"\nAttributes:\n" +
		"Control-NPC.id: 10000\n" +
		"Control-NPC.name: Control-NPC\n" +
		"Control-NPC.type: CONTROL\n" +
		"Control-NPC.save[0]: 7\n"
	want := append([]byte{PLO_NC_NPCATTRIBUTES + 32}, []byte(gtokenizeText(wantDump))...)
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC npc attributes payload = % X, want % X", nc.outQueue, want)
	}
}

func TestNCNpcScriptSetSavesDatabaseNPC(t *testing.T) {
	server := newLoginTestServer(t)
	level := &Level{levelName: "onlinestartlocal.nw"}
	server.AddLevel(level)
	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.scriptType = "CONTROL"
	npc.level = level
	if !server.AddNPC(npc) {
		t.Fatalf("AddNPC returned false")
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_NPCSCRIPTSET)
	packet.WriteGInt(npc.id)
	packet.Write([]byte(gtokenizeText("function onCreated() {\n  echo(\"saved\");\n}")))
	if !nc.msgPLI_NC_NPCSCRIPTSET(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCSCRIPTSET returned false")
	}

	data, err := server.config.LoadFile("npcs/npcControl-NPC.txt")
	if err != nil {
		t.Fatalf("NPC script was not saved: %v", err)
	}
	if !strings.Contains(string(data), "echo(\"saved\")") {
		t.Fatalf("saved NPC file = %q", string(data))
	}
}

func TestNCNpcFlagsGetSetRoundTrip(t *testing.T) {
	server := newLoginTestServer(t)
	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.flagList["old"] = "1"
	if !server.AddNPC(npc) {
		t.Fatalf("AddNPC returned false")
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	setPacket := NewBuffer()
	setPacket.WriteByte(PLI_NC_NPCFLAGSSET)
	setPacket.WriteGInt(npc.id)
	setPacket.Write([]byte(gtokenizeText("flag.one=alpha\nflag.two=beta\n")))
	if !nc.msgPLI_NC_NPCFLAGSSET(setPacket.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCFLAGSSET returned false")
	}
	if got := npc.flagList["flag.one"]; got != "alpha" {
		t.Fatalf("npc flag flag.one = %q, want alpha", got)
	}
	if _, ok := npc.flagList["old"]; ok {
		t.Fatalf("old flag was not replaced: %#v", npc.flagList)
	}
	data, err := server.config.LoadFile("npcs/npcControl-NPC.txt")
	if err != nil {
		t.Fatalf("NPC flags were not saved: %v", err)
	}
	if !strings.Contains(string(data), "FLAG flag.one=alpha") || !strings.Contains(string(data), "FLAG flag.two=beta") {
		t.Fatalf("saved NPC flags file = %q", string(data))
	}

	nc.outQueue = nil
	getPacket := NewBuffer()
	getPacket.WriteByte(PLI_NC_NPCFLAGSGET)
	getPacket.WriteGInt(npc.id)
	if !nc.msgPLI_NC_NPCFLAGSGET(getPacket.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCFLAGSGET returned false")
	}
	want := append([]byte{PLO_NC_NPCFLAGS + 32}, NewBuffer().WriteGInt(npc.id).Bytes()...)
	want = append(want, []byte(gtokenizeText("flag.one=alpha\nflag.two=beta\n"))...)
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC npc flags payload = % X, want % X", nc.outQueue, want)
	}
}

func TestNCWeaponListUsesGByteNameLengths(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{
		"zweapon":       {name: "ZWeapon", image: "z.png", script: "function onCreated() {}"},
		"ControlWeapon": {name: "ControlWeapon", image: "control.png", script: "function onCreated() {}"},
		"controlweapon": {name: "ControlWeapon", image: "control.png", script: "function onCreated() {}"},
		"Bomb":          {name: "Bomb", defPlayer: true},
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	nc.msgPLI_NC_WEAPONLISTGET([]byte{PLI_NC_WEAPONLISTGET})

	want := NewBuffer()
	want.WriteByte(PLO_NC_WEAPONLISTGET + 32)
	want.WriteString8Encoded("ControlWeapon")
	want.WriteString8Encoded("ZWeapon")
	want.WriteByte('\n')
	if !bytes.Equal(nc.outQueue, want.Bytes()) {
		t.Fatalf("NC weapon list payload = % X, want % X", nc.outQueue, want.Bytes())
	}
}

func TestNCWeaponListAfterWeaponAddIsSingleCleanPacket(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.loaded = true
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("test")))
	add.Write([]byte("test"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("function onCreated() {\xa7  echo(\"hi\");\xa7}"))
	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}

	nc.outQueue = nil
	if !nc.msgPLI_NC_WEAPONLISTGET([]byte{PLI_NC_WEAPONLISTGET}) {
		t.Fatalf("msgPLI_NC_WEAPONLISTGET returned false")
	}

	want := NewBuffer()
	want.WriteByte(PLO_NC_WEAPONLISTGET + 32)
	want.WriteString8Encoded("test")
	want.WriteByte('\n')
	if !bytes.Equal(nc.outQueue, want.Bytes()) {
		t.Fatalf("weapon list after add = % X, want % X", nc.outQueue, want.Bytes())
	}
}

func TestNCWeaponAddReportsGS2CompilerErrors(t *testing.T) {
	t.Setenv("GO_WANT_GS2_HELPER_PROCESS", "1")
	server := newLoginTestServer(t)
	server.settings.Set("gs2compiler", os.Args[0])
	server.settings.Set("gs2compilerargs", "-test.run=TestGS2CompilerHelperProcess --")
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("badweapon")))
	add.Write([]byte("badweapon"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("//#CLIENTSIDE\xa7bad syntax"))

	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}
	if server.GetWeapon("badweapon") != nil {
		t.Fatalf("bad weapon was added after compiler error")
	}
	wantHeader := append([]byte{PLO_RC_CHAT + 32}, []byte("Script compiler output for Weapon badweapon:")...)
	wantError := append([]byte{PLO_RC_CHAT + 32}, []byte("error: parser error occurred near line 1: bad syntax")...)
	if !bytes.Contains(nc.outQueue, wantHeader) || !bytes.Contains(nc.outQueue, wantError) {
		t.Fatalf("NC compiler error response = % X, want header % X and error % X", nc.outQueue, wantHeader, wantError)
	}
}

func TestNCWeaponAddReportsCompilerOutputWhenOutputFileMissing(t *testing.T) {
	t.Setenv("GO_WANT_GS2_HELPER_PROCESS", "1")
	server := newLoginTestServer(t)
	server.settings.Set("gs2compiler", os.Args[0])
	server.settings.Set("gs2compilerargs", "-test.run=TestGS2CompilerHelperProcess --")
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("badweapon")))
	add.Write([]byte("badweapon"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("//#CLIENTSIDE\xa7silent output failure"))

	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}
	if server.GetWeapon("badweapon") != nil {
		t.Fatalf("bad weapon was added after compiler output failure")
	}
	wantHeader := append([]byte{PLO_RC_CHAT + 32}, []byte("Script compiler output for Weapon badweapon:")...)
	wantError := append([]byte{PLO_RC_CHAT + 32}, []byte("error: malformed input at line 1: silent output failure")...)
	if !bytes.Contains(nc.outQueue, wantHeader) || !bytes.Contains(nc.outQueue, wantError) {
		t.Fatalf("NC compiler missing-output response = % X, want header % X and error % X", nc.outQueue, wantHeader, wantError)
	}
}

func TestNCWeaponAddWarnsWhenClientsideScriptCannotCompileWithoutCompiler(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("test")))
	add.Write([]byte("test"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("//#CLIENTSIDE\xa7bad syntax"))

	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}
	if server.GetWeapon("test") == nil {
		t.Fatalf("weapon was not saved when compiler was unavailable")
	}
	wantHeader := append([]byte{PLO_RC_CHAT + 32}, []byte("Script compiler output for Weapon test:")...)
	wantWarning := append([]byte{PLO_RC_CHAT + 32}, []byte("warning: gs2compiler is not configured; saved without compile feedback")...)
	if !bytes.Contains(nc.outQueue, wantHeader) || !bytes.Contains(nc.outQueue, wantWarning) {
		t.Fatalf("NC compiler warning response = % X, want header % X and warning % X", nc.outQueue, wantHeader, wantWarning)
	}
}

func TestNCWeaponAddStoresCompiledBytecodeWhenCompilerSucceeds(t *testing.T) {
	t.Setenv("GO_WANT_GS2_HELPER_PROCESS", "1")
	server := newLoginTestServer(t)
	server.settings.Set("gs2compiler", os.Args[0])
	server.settings.Set("gs2compilerargs", "-test.run=TestGS2CompilerHelperProcess --")
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("test")))
	add.Write([]byte("test"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("//#CLIENTSIDE\xa7function onCreated() {\xa7  player.chat = \"hi\";\xa7}"))

	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}
	weapon := server.GetWeapon("test")
	if weapon == nil {
		t.Fatalf("weapon was not added")
	}
	if string(weapon.bytecode) != "bytecode:weapon:test" {
		t.Fatalf("compiled bytecode = %q", weapon.bytecode)
	}
}

func TestNCWeaponAddPersistsAndReloadsCompiledBytecode(t *testing.T) {
	t.Setenv("GO_WANT_GS2_HELPER_PROCESS", "1")
	server := newLoginTestServer(t)
	server.settings.Set("gs2compiler", os.Args[0])
	server.settings.Set("gs2compilerargs", "-test.run=TestGS2CompilerHelperProcess --")
	server.weapons = map[string]*Weapon{}
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	add := NewBuffer()
	add.WriteByte(PLI_NC_WEAPONADD)
	add.WriteGChar(byte(len("test")))
	add.Write([]byte("test"))
	add.WriteGChar(byte(len("bcalarmclock.png")))
	add.Write([]byte("bcalarmclock.png"))
	add.Write([]byte("//#CLIENTSIDE\xa7function onCreated() {\xa7  player.chat = \"hi\";\xa7}"))

	if !nc.msgPLI_NC_WEAPONADD(add.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}
	weaponFile, err := os.ReadFile(filepath.Join(server.config.GetBasePath(), "weapons", "weapontest.txt"))
	if err != nil {
		t.Fatalf("read saved weapon file: %v", err)
	}
	if !bytes.Contains(weaponFile, []byte("BYTECODE weapontest.gs2bc")) {
		t.Fatalf("weapon file missing BYTECODE line:\n%s", weaponFile)
	}
	bytecode, err := os.ReadFile(filepath.Join(server.config.GetBasePath(), "weapon_bytecode", "weapontest.gs2bc"))
	if err != nil {
		t.Fatalf("read saved bytecode file: %v", err)
	}
	if string(bytecode) != "bytecode:weapon:test" {
		t.Fatalf("saved bytecode = %q", bytecode)
	}

	reloaded := NewServer("Reloaded")
	reloaded.config = server.config
	reloaded.settings = NewSettings()
	reloaded.logger = NewLogger("", false)
	reloaded.loadWeapons(false)
	loadedWeapon := reloaded.GetWeapon("test")
	if loadedWeapon == nil {
		t.Fatal("reloaded weapon not found")
	}
	if string(loadedWeapon.bytecode) != "bytecode:weapon:test" {
		t.Fatalf("reloaded bytecode = %q", loadedWeapon.bytecode)
	}
}

func TestNCWeaponGetParsesPayloadAfterPacketID(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{
		"ControlWeapon": {name: "ControlWeapon", image: "control.png", script: "line1\nline2"},
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_NC_WEAPONGET}, []byte("ControlWeapon")...)
	if !nc.msgPLI_NC_WEAPONGET(packet) {
		t.Fatalf("msgPLI_NC_WEAPONGET returned false")
	}

	want := NewBuffer()
	want.WriteByte(PLO_NC_WEAPONGET + 32)
	want.WriteString8Encoded("ControlWeapon")
	want.WriteString8Encoded("control.png")
	want.Write([]byte("line1\xa7line2"))
	want.WriteByte('\n')
	if !bytes.Contains(nc.outQueue, want.Bytes()) {
		t.Fatalf("NC weapon get payload = % X, want % X", nc.outQueue, want.Bytes())
	}
}

func TestNCWeaponGetMissingWeaponReportsToNC(t *testing.T) {
	server := newLoginTestServer(t)
	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	packet := append([]byte{PLI_NC_WEAPONGET}, []byte("MissingWeapon")...)
	if !nc.msgPLI_NC_WEAPONGET(packet) {
		t.Fatalf("msgPLI_NC_WEAPONGET returned false")
	}

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("moondeath prob: weapon MissingWeapon doesn't exist")...)
	want = append(want, '\n')
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC missing weapon error = % X, want % X", nc.outQueue, want)
	}
}

func TestNCWeaponAddNotifiesNCChatOnly(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{
		"test": {name: "test", image: "old.png", script: "old();"},
	}
	rc := NewPlayer(nil, server)
	rc.id = 2
	rc.playerType = PLTYPE_RC2
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.loaded = true
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_WEAPONADD)
	packet.WriteGChar(byte(len("test")))
	packet.Write([]byte("test"))
	packet.WriteGChar(byte(len("bcalarmclock.png")))
	packet.Write([]byte("bcalarmclock.png"))
	if !nc.msgPLI_NC_WEAPONADD(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("Weapon/GUI-script test updated by moondeath")...)
	want = append(want, '\n')
	if bytes.Contains(rc.outQueue, want) {
		t.Fatalf("RC received duplicate NC weapon update message: % X", rc.outQueue)
	}
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC did not receive NC weapon update message: % X, want % X", nc.outQueue, want)
	}
}

func TestNCWeaponAddUpdatesExistingWeaponWithBytecodeFile(t *testing.T) {
	server := newLoginTestServer(t)
	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.loaded = true
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc
	existing := NewWeapon("test")
	existing.image = "old.png"
	existing.script = "old();"
	existing.bytecodeFile = "weapon_bytecode/weapontest.gs2bc"
	server.AddWeapon(existing)

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_WEAPONADD)
	packet.WriteGChar(byte(len("test")))
	packet.Write([]byte("test"))
	packet.WriteGChar(byte(len("bcalarmclock.png")))
	packet.Write([]byte("bcalarmclock.png"))
	packet.Write([]byte("echo(1);"))

	if !nc.msgPLI_NC_WEAPONADD(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_WEAPONADD returned false")
	}

	weapon := server.GetWeapon("test")
	if weapon == nil || weapon.image != "bcalarmclock.png" || weapon.script != "echo(1);" {
		t.Fatalf("weapon was not updated: %#v", weapon)
	}
	want := append([]byte{PLO_RC_CHAT + 32}, []byte("Weapon/GUI-script test updated by moondeath")...)
	want = append(want, '\n')
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC update chat missing: % X, want % X", nc.outQueue, want)
	}
}

func TestSendPacketToTypeDoesNotHoldPlayerLockWhileSending(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.id = 2
	rc.playerType = PLTYPE_RC2
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	server.playerMu.Lock()
	done := make(chan struct{})
	go func() {
		server.sendPacketToType(PLTYPE_ANYRC, rcChatPacket("hello"))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("sendPacketToType returned while player lock was held")
	case <-time.After(20 * time.Millisecond):
	}
	server.playerMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendPacketToType did not finish after player lock was released")
	}
	if !bytes.Contains(rc.outQueue, append([]byte{PLO_RC_CHAT + 32}, []byte("hello")...)) {
		t.Fatalf("RC did not receive fanout packet: % X", rc.outQueue)
	}
}

func TestNCClassEditUsesRawNameLength(t *testing.T) {
	server := newLoginTestServer(t)
	server.classes = map[string]*ScriptClass{
		"ControlClass": {name: "ControlClass", script: "line1\nline2"},
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_NC_CLASSEDIT}, []byte("ControlClass")...)
	if !nc.msgPLI_NC_CLASSEDIT(packet) {
		t.Fatalf("msgPLI_NC_CLASSEDIT returned false")
	}

	want := NewBuffer()
	want.WriteByte(PLO_NC_CLASSGET + 32)
	want.WriteString8("ControlClass")
	want.Write([]byte(gtokenizeText("line1\nline2")))
	want.WriteByte('\n')
	if !bytes.Contains(nc.outQueue, want.Bytes()) {
		t.Fatalf("NC class edit payload = % X, want % X", nc.outQueue, want.Bytes())
	}
}

func TestNCClassAddParsesPayloadAfterPacketID(t *testing.T) {
	server := newLoginTestServer(t)
	server.classes = make(map[string]*ScriptClass)
	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_CLASSADD)
	packet.WriteGChar(byte(len("ControlClass")))
	packet.Write([]byte("ControlClass"))
	packet.Write([]byte(gtokenizeText("function onCreated() {\n  echo(\"hi\");\n}")))

	if !nc.msgPLI_NC_CLASSADD(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_CLASSADD returned false")
	}
	classObj := server.classes["ControlClass"]
	if classObj == nil {
		t.Fatalf("ControlClass was not added")
	}
	if !strings.Contains(classObj.script, "echo(\"hi\")") || strings.Contains(classObj.script, "\x01") {
		t.Fatalf("class script was not untokenized correctly: %q", classObj.script)
	}
	classFile, err := server.config.LoadFile("scripts/ControlClass.txt")
	if err != nil {
		t.Fatalf("ControlClass was not saved: %v", err)
	}
	if !strings.Contains(string(classFile), "echo(\"hi\")") {
		t.Fatalf("saved class script = %q", string(classFile))
	}
	want := []byte{PLO_NC_CLASSADD + 32}
	want = append(want, []byte("ControlClass")...)
	want = append(want, '\n')
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC class add broadcast = % X, want % X", nc.outQueue, want)
	}
}

func TestNCClassAddRefreshesClassForModernClients(t *testing.T) {
	server := newLoginTestServer(t)
	server.classes = map[string]*ScriptClass{
		"ControlClass": {name: "ControlClass", script: "old();"},
	}
	client := NewPlayer(nil, server)
	client.id = 3
	client.playerType = PLTYPE_CLIENT3
	client.versionId = 300
	client.queueOutgoing = true
	client.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[client.id] = client

	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	newScript := "function onCreated() {\n  echo(\"updated\");\n}"
	packet := NewBuffer()
	packet.WriteByte(PLI_NC_CLASSADD)
	packet.WriteGChar(byte(len("ControlClass")))
	packet.Write([]byte("ControlClass"))
	packet.Write([]byte(gtokenizeText(newScript)))

	if !nc.msgPLI_NC_CLASSADD(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_CLASSADD returned false")
	}

	payload := append([]byte{PLO_NPCWEAPONSCRIPT + 32}, []byte(newScript)...)
	expectedLen := NewBuffer().WriteGInt(uint32(len(payload))).Bytes()
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen...)
	want = append(want, '\n')
	want = append(want, payload...)
	if !bytes.Contains(client.outQueue, want) {
		t.Fatalf("client did not receive class refresh: % X, want % X", client.outQueue, want)
	}
}

func TestNCClassListPrefixesEachClassPacket(t *testing.T) {
	server := newLoginTestServer(t)
	server.classes = map[string]*ScriptClass{
		"Alpha": {name: "Alpha"},
		"Beta":  {name: "Beta"},
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	nc.sendNCClassList()

	want := []byte{PLO_NC_CLASSADD + 32}
	want = append(want, []byte("Alpha\n")...)
	want = append(want, PLO_NC_CLASSADD+32)
	want = append(want, []byte("Beta\n")...)
	if !bytes.Equal(nc.outQueue, want) {
		t.Fatalf("NC class list = % X, want % X", nc.outQueue, want)
	}
}

func TestNCLevelListGetUsesGTokenizedLevelNames(t *testing.T) {
	server := newLoginTestServer(t)
	server.levels = map[string]*Level{
		"alpha.nw": {levelName: "alpha.nw"},
		"beta.nw":  {levelName: "beta.nw"},
	}
	nc := NewPlayer(nil, server)
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)

	if !nc.msgPLI_NC_LEVELLISTGET([]byte{PLI_NC_LEVELLISTGET}) {
		t.Fatalf("msgPLI_NC_LEVELLISTGET returned false")
	}

	want := append([]byte{PLO_NC_LEVELLIST + 32}, []byte("alpha.nw,beta.nw\n")...)
	if !bytes.Equal(nc.outQueue, want) {
		t.Fatalf("NC level list = % X, want % X", nc.outQueue, want)
	}
}

func TestNCNPCAddBroadcastIsFramed(t *testing.T) {
	server := newLoginTestServer(t)
	level := &Level{levelName: "onlinestartlocal.nw"}
	server.levels[level.levelName] = level
	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	payload := gtokenizeText("Control-NPC\n0\nCONTROL\nmoondeath\nonlinestartlocal.nw\n30\n30\n")
	packet := append([]byte{PLI_NC_NPCADD}, []byte(payload)...)
	if !nc.msgPLI_NC_NPCADD(packet) {
		t.Fatalf("msgPLI_NC_NPCADD returned false")
	}

	wantPrefix := NewBuffer()
	wantPrefix.WriteByte(PLO_NC_NPCADD + 32)
	idx := bytes.Index(nc.outQueue, wantPrefix.Bytes())
	if idx < 0 {
		t.Fatalf("NC npc add broadcast missing: % X", nc.outQueue)
	}
	next := bytes.IndexByte(nc.outQueue[idx:], '\n')
	if next < 0 {
		t.Fatalf("NC npc add broadcast was not framed: % X", nc.outQueue)
	}
	frame := nc.outQueue[idx : idx+next]
	if bytes.Contains(frame, []byte{PLO_RC_CHAT + 32}) {
		t.Fatalf("NC npc add broadcast merged with following chat packet: % X", frame)
	}
}

func TestNCNPCAddAttachesNPCToLevelAndNotifiesClients(t *testing.T) {
	server := newLoginTestServer(t)
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server.levels[level.levelName] = level

	client := NewPlayer(nil, server)
	client.id = 4
	client.playerType = PLTYPE_CLIENT3
	client.currentLevel = level
	client.levelName = level.levelName
	client.queueOutgoing = true
	client.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[client.id] = client
	level.addPlayer(client)

	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	payload := gtokenizeText("Control-NPC\n10000\nCONTROL\nmoondeath\nonlinestartlocal.nw\n30\n30\n")
	packet := append([]byte{PLI_NC_NPCADD}, []byte(payload)...)
	if !nc.msgPLI_NC_NPCADD(packet) {
		t.Fatalf("msgPLI_NC_NPCADD returned false")
	}

	npc := server.GetNPC(10000)
	if npc == nil {
		t.Fatalf("database NPC was not created")
	}
	if level.npcs[npc.id] != npc {
		t.Fatalf("database NPC was not attached to level")
	}
	want := append([]byte{PLO_NPCPROPS + 32}, NewBuffer().WriteGInt(npc.id).Bytes()...)
	if !bytes.Contains(client.outQueue, want) {
		t.Fatalf("client did not receive new NPC props: % X, want prefix % X", client.outQueue, want)
	}
}

func TestNCNPCWarpMovesLevelAndNotifiesNCs(t *testing.T) {
	server := newLoginTestServer(t)
	oldLevel := NewLevel()
	oldLevel.levelName = "old.nw"
	newLevel := NewLevel()
	newLevel.levelName = "new.nw"
	server.levels[oldLevel.levelName] = oldLevel
	server.levels[newLevel.levelName] = newLevel

	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.level = oldLevel
	server.npcs[npc.id] = npc
	oldLevel.npcs[npc.id] = npc

	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_NPCWARP)
	packet.WriteGInt(npc.id)
	packet.WriteGChar(40)
	packet.WriteGChar(42)
	packet.Write([]byte("new.nw"))

	if !nc.msgPLI_NC_NPCWARP(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCWARP returned false")
	}
	if _, ok := oldLevel.npcs[npc.id]; ok {
		t.Fatalf("npc remained in old level")
	}
	if newLevel.npcs[npc.id] != npc {
		t.Fatalf("npc was not attached to new level")
	}
	if npc.level != newLevel || npc.x != 20*16 || npc.y != 21*16 {
		t.Fatalf("npc state = level %v x=%d y=%d", npc.level, npc.x, npc.y)
	}
	want := NewBuffer()
	want.WriteByte(PLO_NC_NPCADD + 32)
	want.WriteGInt(npc.id)
	want.WriteGChar(NPCPROP_CURLEVEL)
	want.WriteString8Encoded("new.nw")
	want.WriteByte('\n')
	if !bytes.Contains(nc.outQueue, want.Bytes()) {
		t.Fatalf("NC did not receive warp level update: % X, want % X", nc.outQueue, want.Bytes())
	}
}

func TestNCNPCDeleteRemovesFromLiveLevel(t *testing.T) {
	server := newLoginTestServer(t)
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server.levels[level.levelName] = level
	npc := NewNPC(DBNPC)
	npc.id = 10000
	npc.npcName = "Control-NPC"
	npc.level = level
	server.npcs[npc.id] = npc
	level.npcs[npc.id] = npc

	client := NewPlayer(nil, server)
	client.id = 4
	client.playerType = PLTYPE_CLIENT3
	client.queueOutgoing = true
	client.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[client.id] = client

	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	packet := NewBuffer()
	packet.WriteByte(PLI_NC_NPCDELETE)
	packet.WriteGInt(npc.id)
	if !nc.msgPLI_NC_NPCDELETE(packet.Bytes()) {
		t.Fatalf("msgPLI_NC_NPCDELETE returned false")
	}
	if server.GetNPC(npc.id) != nil {
		t.Fatalf("npc remained in server map")
	}
	if _, ok := level.npcs[npc.id]; ok {
		t.Fatalf("npc remained in level map")
	}
	want := append([]byte{PLO_NPCDEL + 32}, NewBuffer().WriteGInt(npc.id).Bytes()...)
	if !bytes.Contains(client.outQueue, want) {
		t.Fatalf("client did not receive NPC delete: % X, want % X", client.outQueue, want)
	}
}

func TestNCNPCAddMissingLevelReportsToNC(t *testing.T) {
	server := newLoginTestServer(t)
	server.levels = map[string]*Level{}
	nc := NewPlayer(nil, server)
	nc.id = 9
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	payload := gtokenizeText("Control-NPC\n0\nCONTROL\nmoondeath\nmissing.nw\n30\n30\n")
	packet := append([]byte{PLI_NC_NPCADD}, []byte(payload)...)
	if !nc.msgPLI_NC_NPCADD(packet) {
		t.Fatalf("msgPLI_NC_NPCADD returned false")
	}

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("Error adding database npc: Level does not exist")...)
	want = append(want, '\n')
	if !bytes.Contains(nc.outQueue, want) {
		t.Fatalf("NC missing-level error = % X, want % X", nc.outQueue, want)
	}
}

func TestLoadClassesReadsScriptsFolder(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "scripts/ControlClass.txt", "function onCreated() {\r\n  echo(\"hi\");\r\n}\r\n")
	server.classes = make(map[string]*ScriptClass)

	server.loadClasses(false)

	classObj := server.classes["ControlClass"]
	if classObj == nil {
		t.Fatalf("ControlClass was not loaded")
	}
	if !strings.Contains(classObj.script, "echo(\"hi\")") {
		t.Fatalf("loaded class script = %q", classObj.script)
	}
}

func TestRC2LoginReadsEncryptionKeyAndSkipsGameWorldLogin(t *testing.T) {
	server := newLoginTestServer(t)
	p := NewPlayer(nil, server)

	if !p.handleLogin(buildLoginPacket(t, 6, 42, "G3D0311C", "Admin", "pass", "win,test,6.037")) {
		t.Fatalf("RC2 login was rejected")
	}
	if p.playerType != PLTYPE_RC2 {
		t.Fatalf("playerType = %d, want RC2 (%d)", p.playerType, PLTYPE_RC2)
	}
	if p.encryption.gen != ENCRYPT_GEN_5 {
		t.Fatalf("encryption gen = %d, want GEN_5 (%d)", p.encryption.gen, ENCRYPT_GEN_5)
	}
	if p.outEncryption.gen != ENCRYPT_GEN_5 {
		t.Fatalf("out encryption gen = %d, want GEN_5 (%d)", p.outEncryption.gen, ENCRYPT_GEN_5)
	}
	if p.currentLevel != nil {
		t.Fatalf("RC login entered game level %q", p.levelName)
	}
}

func TestRCPostLoginTailAnnouncesRCToGameClients(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.character.nickName = "Admin"
	rc.communityName = "Admin"
	rc.loaded = true

	game := NewPlayer(nil, server)
	game.playerType = PLTYPE_CLIENT3
	game.id = 3
	game.accountName = "Player"
	game.character.nickName = "Player"
	gameServerConn, gameClientConn := net.Pipe()
	defer gameServerConn.Close()
	defer gameClientConn.Close()
	game.conn = gameServerConn
	game.queueOutgoing = true
	game.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[game.id] = game

	rc.sendRCPostLoginTail()

	wantPrefix := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(rc.id).Bytes()...)
	if !bytes.Contains(game.outQueue, wantPrefix) {
		t.Fatalf("game client did not receive RC login props: % X", game.outQueue)
	}
}

func TestRCPostLoginTailHonorsHideStaffForRC(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("hidestaff", "true")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.character.nickName = "Admin"
	rc.isStaff = true
	rc.loaded = true

	game := NewPlayer(nil, server)
	game.playerType = PLTYPE_CLIENT3
	game.id = 3
	game.accountName = "Player"
	game.character.nickName = "Player"
	gameServerConn, gameClientConn := net.Pipe()
	defer gameServerConn.Close()
	defer gameClientConn.Close()
	game.conn = gameServerConn
	game.queueOutgoing = true
	game.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[game.id] = game

	rc.sendRCPostLoginTail()

	if len(game.outQueue) != 0 {
		t.Fatalf("game client received hidden RC login packets: % X", game.outQueue)
	}
}

func TestRCPostLoginTailIncludesNPCServerWithoutSocket(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.character.nickName = "Admin"
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	npc := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_NPCSERVER,
		loaded:     true,
	}
	npc.accountName = "(npcserver)"
	npc.character.nickName = "NPC-Server (Server)"
	npc.communityName = "(npcserver)"
	server.players[npc.id] = npc

	rc.sendRCPostLoginTail()

	want := append([]byte{PLO_ADDPLAYER + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("rc did not receive npc-server addplayer entry: % X", rc.outQueue)
	}
}

func TestRCPlayerPropsSetUpdatesTargetFromLegacyPacket(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_SETATTRIBUTES
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	target := NewPlayer(nil, server)
	target.id = 7
	target.accountName = "Player"
	target.character.nickName = "OldNick"
	target.flagList = map[string]string{"oldflag": "1"}
	target.weaponList = []string{"oldweapon"}
	target.chestList = []string{"1:1:old.nw"}
	target.queueOutgoing = true
	target.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[target.id] = target

	props := NewBuffer()
	props.WriteGChar(PLPROP_NICKNAME)
	props.WriteGChar(byte(len("NewNick")))
	props.Write([]byte("NewNick"))

	packet := NewBuffer()
	packet.WriteByte(PLI_RC_PLAYERPROPSSET)
	packet.WriteGShort(target.id)
	packet.WriteGChar(byte(len("main")))
	packet.Write([]byte("main"))
	packet.WriteGChar(byte(props.Len()))
	packet.Write(props.Bytes())
	packet.WriteGShort(1)
	packet.WriteGChar(byte(len("newflag=ok")))
	packet.Write([]byte("newflag=ok"))
	packet.WriteGShort(1)
	packet.WriteGChar(byte(2 + len("newlevel.nw")))
	packet.WriteGChar(4)
	packet.WriteGChar(5)
	packet.Write([]byte("newlevel.nw"))
	packet.WriteGChar(1)
	packet.WriteGChar(byte(len("spinattack")))
	packet.Write([]byte("spinattack"))

	if !rc.msgPLI_RC_PLAYERPROPSSET(packet.Bytes()) {
		t.Fatalf("msgPLI_RC_PLAYERPROPSSET returned false")
	}
	if target.character.nickName != "NewNick" {
		t.Fatalf("target nick = %q, want NewNick", target.character.nickName)
	}
	if got := target.flagList["newflag"]; got != "ok" {
		t.Fatalf("target flag newflag = %q, want ok; flags=%#v", got, target.flagList)
	}
	if _, ok := target.flagList["oldflag"]; ok {
		t.Fatalf("old flags were not cleared: %#v", target.flagList)
	}
	if len(target.chestList) != 1 || target.chestList[0] != "4:5:newlevel.nw" {
		t.Fatalf("target chests = %#v, want 4:5:newlevel.nw", target.chestList)
	}
	if len(target.weaponList) != 1 || target.weaponList[0] != "spinattack" {
		t.Fatalf("target weapons = %#v, want spinattack", target.weaponList)
	}
}

func TestRCUpdateLevelsReloadsCachedLevel(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_UPDATELEVEL
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	levelName := "levels/reloadme.nw"
	writeTestFile(t, server.config.GetBasePath(), levelName, "GLEVNW01\n")
	level := NewLevel()
	if !level.loadLevel(server, levelName) {
		t.Fatalf("initial loadLevel failed")
	}
	server.AddLevel(level)

	writeTestFile(t, server.config.GetBasePath(), levelName, "GLEVNW02\n")
	packet := NewBuffer()
	packet.WriteByte(PLI_RC_UPDATELEVELS)
	packet.WriteGShort(1)
	packet.WriteGChar(byte(len(levelName)))
	packet.Write([]byte(levelName))

	if !rc.msgPLI_RC_UPDATELEVELS(packet.Bytes()) {
		t.Fatalf("msgPLI_RC_UPDATELEVELS returned false")
	}
	if level.fileVersion != "GLEVNW02" {
		t.Fatalf("level fileVersion = %q, want GLEVNW02", level.fileVersion)
	}
}

func TestRCPostLoginTailAnnouncesNewRCToAllRCs(t *testing.T) {
	server := newLoginTestServer(t)
	existing := NewPlayer(nil, server)
	existing.id = 2
	existing.playerType = PLTYPE_RC2
	existing.accountName = "Owner"
	existing.loaded = true
	existing.queueOutgoing = true
	existing.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[existing.id] = existing

	rc := NewPlayer(nil, server)
	rc.id = 3
	rc.playerType = PLTYPE_RC2
	rc.accountName = "moondeath"
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	rc.sendRCPostLoginTail()

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("New RC: moondeath")...)
	want = append(want, '\n')
	if !bytes.Contains(existing.outQueue, want) {
		t.Fatalf("existing RC did not receive new RC message: % X", existing.outQueue)
	}
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("new RC did not receive its own new RC message: % X", rc.outQueue)
	}
}

func TestNCPostLoginTailAnnouncesNewNCToOtherNCs(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("name", "Orion-Go")
	existing := NewPlayer(nil, server)
	existing.id = 2
	existing.playerType = PLTYPE_NC
	existing.accountName = "Owner"
	existing.loaded = true
	existing.queueOutgoing = true
	existing.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[existing.id] = existing

	nc := NewPlayer(nil, server)
	nc.id = 3
	nc.playerType = PLTYPE_NC
	nc.accountName = "moondeath"
	nc.loaded = true
	nc.queueOutgoing = true
	nc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[nc.id] = nc

	nc.sendNCPostLoginTail()

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("New NC: moondeath")...)
	want = append(want, '\n')
	if !bytes.Contains(existing.outQueue, want) {
		t.Fatalf("existing NC did not receive new NC message: % X", existing.outQueue)
	}
	welcome := append([]byte{PLO_RC_CHAT + 32}, []byte("Welcome to the NPC-Server for Orion-Go")...)
	welcome = append(welcome, '\n')
	if !bytes.Contains(nc.outQueue, welcome) {
		t.Fatalf("new NC did not receive welcome message: % X", nc.outQueue)
	}
	if bytes.Contains(nc.outQueue, want) {
		t.Fatalf("new NC received duplicate self new-NC message: % X", nc.outQueue)
	}
}

func TestRCPostLoginTailSendsStaffGuildDefinitions(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("staffguilds", "Server,Manager,Owner")
	rc := NewPlayer(nil, server)
	rc.id = 2
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	rc.sendRCPostLoginTail()

	want := append([]byte{PLO_STAFFGUILDS + 32}, []byte("\"Server\",\"Manager\",\"Owner\"")...)
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("RC post-login tail missing staffguilds: % X, want % X", rc.outQueue, want)
	}
}

func TestAddPlayerPacketUsesEncodedAccountLength(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	npc := NewPlayer(nil, server)
	npc.id = 1
	npc.accountName = "(npcserver)"
	npc.setNickname("NPC-Server (Server)")
	npc.communityName = "(npcserver)"

	if !rc.sendPLO_ADDPLAYER(npc) {
		t.Fatal("sendPLO_ADDPLAYER returned false")
	}

	payload := bytes.TrimSuffix(rc.outQueue, []byte{'\n'})
	buf := NewBufferFromBytes(payload)
	if got := buf.ReadByte(); got != PLO_ADDPLAYER+32 {
		t.Fatalf("packet id = %d, want %d", got, PLO_ADDPLAYER+32)
	}
	if got := buf.ReadGShort(); got != npc.id {
		t.Fatalf("player id = %d, want %d", got, npc.id)
	}
	account := string(buf.ReadBytes(int(buf.ReadGChar())))
	if account != "(npcserver)" {
		t.Fatalf("account = %q, want (npcserver); packet=% X", account, rc.outQueue)
	}
	if got := buf.ReadGChar(); got != PLPROP_CURLEVEL {
		t.Fatalf("first prop = %d, want PLPROP_CURLEVEL", got)
	}
}

func TestRCAdminMessageUsesRawPayload(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.sendPLO_RC_ADMINMESSAGE("Welcome")

	want := append([]byte{PLO_RC_ADMINMESSAGE + 32}, []byte("Welcome")...)
	want = append(want, '\n')
	if !bytes.Equal(rc.outQueue, want) {
		t.Fatalf("admin message packet = % X, want % X", rc.outQueue, want)
	}
}

func TestRCPlayerPropsGet2NetworkPayloadStripsPacketID(t *testing.T) {
	server := newLoginTestServer(t)
	target := NewPlayer(nil, server)
	target.id = 3
	target.playerType = PLTYPE_CLIENT3
	target.accountName = "moondeath"
	target.levelName = "onlinestartlocal.nw"
	server.players[target.id] = target

	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_VIEWATTRIBUTES
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_RC_PLAYERPROPSGET2}, NewBuffer().WriteGShort(target.id).Bytes()...)
	if !rc.msgPLI_RC_PLAYERPROPSGET2(packet) {
		t.Fatal("msgPLI_RC_PLAYERPROPSGET2 returned false")
	}

	want := append([]byte{PLO_RC_PLAYERPROPSGET + 32}, NewBuffer().WriteGShort(target.id).Bytes()...)
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("player props response = % X, want prefix % X", rc.outQueue, want)
	}
}

func TestRCPlayerPropsGet2SendsLoadedAccountProps(t *testing.T) {
	server := newLoginTestServer(t)
	target := NewPlayer(nil, server)
	target.id = 3
	target.playerType = PLTYPE_CLIENT3
	target.accountName = "moondeath"
	target.character.nickName = "moondeath"
	target.character.bodyImage = "body.png"
	target.levelName = "onlinestartlocal.nw"
	target.kills = 12
	target.deaths = 4
	target.onlineTime = 99
	target.accountIp = 0xC0A80119
	target.weaponList = []string{"Spin Attack"}
	target.SetFlag("clientr.foo", "bar")
	target.chestList = []string{"10:20:onlinestartlocal.nw"}
	server.players[target.id] = target

	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_VIEWATTRIBUTES
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_RC_PLAYERPROPSGET2}, NewBuffer().WriteGShort(target.id).Bytes()...)
	if !rc.msgPLI_RC_PLAYERPROPSGET2(packet) {
		t.Fatal("msgPLI_RC_PLAYERPROPSGET2 returned false")
	}

	payload := bytes.TrimSuffix(rc.outQueue, []byte{'\n'})
	if len(payload) == 0 || payload[0] != PLO_RC_PLAYERPROPSGET+32 {
		t.Fatalf("player props packet = % X", rc.outQueue)
	}
	buf := NewBufferFromBytes(payload[1:])
	if got := buf.ReadGShort(); got != target.id {
		t.Fatalf("player id = %d, want %d", got, target.id)
	}
	if got := string(buf.ReadBytes(int(buf.ReadGChar()))); got != "moondeath" {
		t.Fatalf("account = %q, want moondeath", got)
	}
	if got := string(buf.ReadBytes(int(buf.ReadGChar()))); got != "main" {
		t.Fatalf("world = %q, want main", got)
	}
	propsLen := int(buf.ReadGChar())
	props := buf.ReadBytes(propsLen)
	killsProp := append([]byte{PLPROP_KILLSCOUNT + 32}, NewBuffer().WriteGInt(12).Bytes()...)
	accountProp := append([]byte{PLPROP_ACCOUNTNAME + 32}, NewBuffer().WriteString8Encoded("moondeath").Bytes()...)
	bodyProp := append([]byte{PLPROP_BODYIMG + 32}, NewBuffer().WriteString8Encoded("body.png").Bytes()...)
	if !bytes.Contains(props, killsProp) || !bytes.Contains(props, accountProp) || !bytes.Contains(props, bodyProp) {
		t.Fatalf("props missing expected account data: props=% X kills=% X account=% X body=% X", props, killsProp, accountProp, bodyProp)
	}
}

func TestRCPlayerPropsGet3ParsesString8AccountName(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/moondeath.txt", ""+
		"GRACC001\n"+
		"NICK moondeath\n"+
		"BODY body.png\n"+
		"KILLS 7\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_VIEWATTRIBUTES
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_RC_PLAYERPROPSGET3}, NewBuffer().WriteString8Encoded("moondeath").Bytes()...)
	if !rc.msgPLI_RC_PLAYERPROPSGET3(packet) {
		t.Fatal("msgPLI_RC_PLAYERPROPSGET3 returned false")
	}

	if !bytes.Contains(rc.outQueue, []byte("moondeath")) || !bytes.Contains(rc.outQueue, append([]byte{PLPROP_KILLSCOUNT + 32}, NewBuffer().WriteGInt(7).Bytes()...)) {
		t.Fatalf("player props by account response missing loaded account data: % X", rc.outQueue)
	}
}

func TestRCPlayerRightsGetNetworkPayloadUsesRawAccountAndGInt5Rights(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	if !rc.msgPLI_RC_PLAYERRIGHTSGET(append([]byte{PLI_RC_PLAYERRIGHTSGET}, []byte("Admin")...)) {
		t.Fatal("msgPLI_RC_PLAYERRIGHTSGET returned false")
	}

	want := NewBuffer()
	want.WriteByte(PLO_RC_PLAYERRIGHTSGET + 32)
	want.WriteString8Encoded("Admin")
	want.WriteGInt5(1)
	want.WriteString8Encoded("")
	want.WriteGShort(0)
	want.WriteByte('\n')
	if !bytes.Contains(rc.outQueue, want.Bytes()) {
		t.Fatalf("rights response = % X, want % X", rc.outQueue, want.Bytes())
	}
}

func TestRCPlayerRightsGetResponseUsesRCEncodedFields(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/moondeath.txt", ""+
		"GRACC001\n"+
		"NICK moondeath\n"+
		"LOCALRIGHTS 255\n"+
		"IPRANGE 127.0.0.1\n"+
		"FOLDERRIGHT rw *.nw\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_SETRIGHTS
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	if !rc.msgPLI_RC_PLAYERRIGHTSGET(append([]byte{PLI_RC_PLAYERRIGHTSGET}, []byte("moondeath")...)) {
		t.Fatal("msgPLI_RC_PLAYERRIGHTSGET returned false")
	}

	payload := bytes.TrimSuffix(rc.outQueue, []byte{'\n'})
	buf := NewBufferFromBytes(payload)
	if got := buf.ReadByte(); got != PLO_RC_PLAYERRIGHTSGET+32 {
		t.Fatalf("packet id = %d, want %d", got, PLO_RC_PLAYERRIGHTSGET+32)
	}
	account := string(buf.ReadBytes(int(buf.ReadGChar())))
	rights := int(buf.ReadGInt5())
	adminIP := string(buf.ReadBytes(int(buf.ReadGChar())))
	foldersLen := int(buf.ReadGShort())
	folders := string(buf.ReadBytes(foldersLen))

	if account != "moondeath" || rights != 255 || adminIP != "127.0.0.1" || folders != "\"rw *.nw\"" {
		t.Fatalf("decoded rights packet = account=%q rights=%d ip=%q folders=%q", account, rights, adminIP, folders)
	}
}

func TestRCPlayerRightsGetTokenizesFolderRights(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/moondeath.txt", ""+
		"GRACC001\n"+
		"NICK moondeath\n"+
		"LOCALRIGHTS 255\n"+
		"IPRANGE 0.0.0.0\n"+
		"FOLDERRIGHT rw *.nw\n"+
		"FOLDERRIGHT rw levels/*.graal\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_SETRIGHTS
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	if !rc.msgPLI_RC_PLAYERRIGHTSGET(append([]byte{PLI_RC_PLAYERRIGHTSGET}, []byte("moondeath")...)) {
		t.Fatal("msgPLI_RC_PLAYERRIGHTSGET returned false")
	}

	wantFolders := []byte("\"rw *.nw\",\"rw levels/*.graal\"")
	if !bytes.Contains(rc.outQueue, wantFolders) {
		t.Fatalf("rights response missing tokenized folders: % X, want %q", rc.outQueue, wantFolders)
	}
}

func TestRCPlayerRightsSetReadsGInt5Rights(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/moondeath.txt", ""+
		"GRACC001\n"+
		"NICK moondeath\n"+
		"LOCALRIGHTS 0\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = allLocalRights()
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_RC_PLAYERRIGHTSSET)
	packet.WriteString8Encoded("moondeath")
	packet.WriteGInt5(uint64(PLPERM_NPCCONTROL | PLPERM_SETRIGHTS))
	packet.WriteString8Encoded("127.0.0.1")
	folders := gtokenizeText("rw *.nw")
	packet.WriteGShort(uint16(len(folders)))
	packet.Write([]byte(folders))
	if !rc.msgPLI_RC_PLAYERRIGHTSSET(packet.Bytes()) {
		t.Fatal("msgPLI_RC_PLAYERRIGHTSSET returned false")
	}

	target := NewPlayer(nil, server)
	if !target.LoadAccount("moondeath", false) {
		t.Fatal("load target account failed")
	}
	wantRights := PLPERM_NPCCONTROL | PLPERM_SETRIGHTS
	if target.adminRights != wantRights || target.adminIp != "127.0.0.1" {
		t.Fatalf("saved rights/ip = %d/%q, want %d/127.0.0.1", target.adminRights, target.adminIp, wantRights)
	}
	if got := strings.Join(target.folderList, "\n"); got != "rw *.nw" {
		t.Fatalf("saved folders = %q, want rw *.nw", got)
	}
}

func TestRCPlayerCommentsGetNetworkPayloadUsesRawComments(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/Admin.txt", ""+
		"GRACC001\n"+
		"NICK Admin\n"+
		"COMMENTS saved comment\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	if !rc.msgPLI_RC_PLAYERCOMMENTSGET(append([]byte{PLI_RC_PLAYERCOMMENTSGET}, []byte("Admin")...)) {
		t.Fatal("msgPLI_RC_PLAYERCOMMENTSGET returned false")
	}

	want := append([]byte{PLO_RC_PLAYERCOMMENTSGET + 32}, byte(len("Admin")+32))
	want = append(want, []byte("Admin")...)
	want = append(want, []byte("saved comment")...)
	want = append(want, '\n')
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("comments response = % X, want % X", rc.outQueue, want)
	}
}

func TestSendTextParsesCommaStyleListerCommands(t *testing.T) {
	server := newLoginTestServer(t)
	p := NewPlayer(nil, server)
	p.id = 3
	p.accountName = "moondeath"
	p.queueOutgoing = true
	p.encryption.SetGen(ENCRYPT_GEN_1)

	if !p.msgPLI_SENDTEXT(append([]byte{PLI_SENDTEXT}, []byte("GraalEngine,lister,getbanbyid,1")...)) {
		t.Fatal("msgPLI_SENDTEXT returned false")
	}

	want := append([]byte{PLO_SERVERTEXT + 32}, []byte("GraalEngine\x01lister\x01getbanbyid\x011\x01")...)
	want = append(want, '\n')
	if !bytes.Contains(p.outQueue, want) {
		t.Fatalf("sendtext fallback response = % X, want % X", p.outQueue, want)
	}
}

func TestRCLoginPayloadUsesChatForWelcomeMessages(t *testing.T) {
	server := newLoginTestServer(t)
	configDir := filepath.Join(server.config.GetBasePath(), "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "rcmessage.txt"), []byte("Welcome\nSay /help\n"), 0644); err != nil {
		t.Fatalf("write rcmessage: %v", err)
	}
	rc := NewPlayer(nil, server)
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.sendRCLoginPayload()

	if bytes.Contains(rc.outQueue, []byte{PLO_RC_ADMINMESSAGE + 32}) {
		t.Fatalf("rc login payload used admin-message packet: % X", rc.outQueue)
	}
	wantWelcome := append([]byte{PLO_RC_CHAT + 32}, []byte("Welcome")...)
	wantWelcome = append(wantWelcome, '\n')
	wantHelp := append([]byte{PLO_RC_CHAT + 32}, []byte("Say /help")...)
	wantHelp = append(wantHelp, '\n')
	if !bytes.Contains(rc.outQueue, wantWelcome) || !bytes.Contains(rc.outQueue, wantHelp) {
		t.Fatalf("rc login payload missing chat welcome messages: % X", rc.outQueue)
	}
}

func TestRCChatRelaysToConnectedRCs(t *testing.T) {
	server := newLoginTestServer(t)
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	rc.msgPLI_RC_CHAT([]byte{PLI_RC_CHAT, 'o', 'h'})

	want := append([]byte{PLO_RC_CHAT + 32}, []byte("Admin: oh")...)
	want = append(want, '\n')
	if !bytes.Equal(rc.outQueue, want) {
		t.Fatalf("rc chat packet = % X, want % X", rc.outQueue, want)
	}
}

func TestRCChatSlashHelpDoesNotEchoAsChat(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "config/rchelp.txt", "/open account\n/openrights account\n")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	rc.msgPLI_RC_CHAT(append([]byte{PLI_RC_CHAT}, []byte("/help")...))

	if bytes.Contains(rc.outQueue, []byte("Admin: /help")) {
		t.Fatalf("slash command was echoed to RC chat: % X", rc.outQueue)
	}
	if !bytes.Contains(rc.outQueue, []byte("/open account")) || !bytes.Contains(rc.outQueue, []byte("/openrights account")) {
		t.Fatalf("help command did not send rchelp lines: % X", rc.outQueue)
	}
}

func TestRCChatOpenRightsDispatchesCommand(t *testing.T) {
	server := newLoginTestServer(t)
	target := NewPlayer(nil, server)
	target.playerType = PLTYPE_CLIENT3
	target.id = 3
	target.accountName = "moondeath"
	target.adminIp = "*.*.*.*"
	server.players[target.id] = target

	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_SETRIGHTS
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	rc.msgPLI_RC_CHAT(append([]byte{PLI_RC_CHAT}, []byte("/openrights moondeath")...))

	if bytes.Contains(rc.outQueue, []byte("Admin: /openrights moondeath")) {
		t.Fatalf("openrights command was echoed to RC chat: % X", rc.outQueue)
	}
	want := append([]byte{PLO_RC_PLAYERRIGHTSGET + 32}, []byte{byte(len("moondeath") + 32)}...)
	want = append(want, []byte("moondeath")...)
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("openrights did not dispatch rights packet: % X, want prefix % X", rc.outQueue, want)
	}
}

func TestRCServerOptionsGetSendsTokenizedConfigAsSinglePacket(t *testing.T) {
	server := newLoginTestServer(t)
	configDir := filepath.Join(server.config.GetBasePath(), "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	optionsText := "# Server Options\n\nname = Orion-Go\nstaff = (Manager),moondeath\n"
	if err := os.WriteFile(filepath.Join(configDir, "serveroptions.txt"), []byte(optionsText), 0644); err != nil {
		t.Fatalf("write serveroptions: %v", err)
	}
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.msgPLI_RC_SERVEROPTIONSGET([]byte{PLI_RC_SERVEROPTIONSGET})

	if !bytes.HasPrefix(rc.outQueue, []byte{PLO_RC_SERVEROPTIONSGET + 32}) {
		t.Fatalf("serveroptions packet id = % X", rc.outQueue)
	}
	payload := rc.outQueue[1 : len(rc.outQueue)-1]
	if bytes.ContainsAny(payload, "\x01\n\r") {
		t.Fatalf("serveroptions payload contained packet-breaking separators: % X", rc.outQueue)
	}
	if !bytes.Contains(payload, []byte("\"# Server Options\"")) || !bytes.Contains(payload, []byte("\"\"")) ||
		!bytes.Contains(payload, []byte("\"name = Orion-Go\"")) || !bytes.Contains(payload, []byte("\"staff = (Manager),moondeath\"")) {
		t.Fatalf("serveroptions payload missing tokenized lines: %q", payload)
	}
}

func TestRCServerOptionsSetPreservesCommentsAndBlankLinesForFullRights(t *testing.T) {
	server := newLoginTestServer(t)
	configDir := filepath.Join(server.config.GetBasePath(), "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 2
	rc.accountName = "Admin"
	rc.adminRights = allLocalRights()
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[rc.id] = rc

	optionsText := "# Server Options\n\nname = Orion-Go\nstaff = (Manager),moondeath\n"
	packet := append([]byte{PLI_RC_SERVEROPTIONSSET}, []byte(gtokenizeText(optionsText))...)
	rc.msgPLI_RC_SERVEROPTIONSSET(packet)

	saved, err := os.ReadFile(filepath.Join(configDir, "serveroptions.txt"))
	if err != nil {
		t.Fatalf("read saved serveroptions: %v", err)
	}
	want := strings.TrimSuffix(optionsText, "\n") + "\n"
	if string(saved) != want {
		t.Fatalf("saved serveroptions = %q, want %q", saved, want)
	}
	if got := server.settings.Get("name"); got != "Orion-Go" {
		t.Fatalf("reloaded setting name = %q", got)
	}
}

func TestRCFolderConfigGetUsesFolderConfigPacketAndTokenizedText(t *testing.T) {
	server := newLoginTestServer(t)
	configDir := filepath.Join(server.config.GetBasePath(), "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "foldersconfig.txt"), []byte("level *.nw\r\nlevel levels/*.graal\r\n"), 0644); err != nil {
		t.Fatalf("write foldersconfig: %v", err)
	}
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.msgPLI_RC_FOLDERCONFIGGET([]byte{PLI_RC_FOLDERCONFIGGET})

	want := append([]byte{PLO_RC_FOLDERCONFIGGET + 32}, []byte("\"level *.nw\",\"level levels/*.graal\"")...)
	want = append(want, '\n')
	if !bytes.Equal(rc.outQueue, want) {
		t.Fatalf("folderconfig packet = % X, want % X", rc.outQueue, want)
	}
}

func TestLoadFlagsSkipsMalformedServerFlags(t *testing.T) {
	server := newLoginTestServer(t)
	configDir := filepath.Join(server.config.GetBasePath(), "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "serverflags.txt"), []byte("good=true\nbad\x00name=true\n=value\nmulti=line\x00junk\n"), 0644); err != nil {
		t.Fatalf("write serverflags: %v", err)
	}

	server.loadFlags()

	if got := server.flags["good"]; got != "true" {
		t.Fatalf("good flag = %q, want true", got)
	}
	if len(server.flags) != 1 {
		t.Fatalf("loaded flags = %#v, want only good flag", server.flags)
	}
}

func TestRCServerFlagsGetUsesSingleString8Length(t *testing.T) {
	server := newLoginTestServer(t)
	server.flags["test"] = "true"
	server.flags["bad\x00name"] = "true"

	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.msgPLI_RC_SERVERFLAGSGET([]byte{PLI_RC_SERVERFLAGSGET})

	want := []byte{PLO_RC_SERVERFLAGSGET + 32, 0x20, 0x21, byte(len("test=true") + 32)}
	want = append(want, []byte("test=true")...)
	want = append(want, '\n')
	if !bytes.Equal(rc.outQueue, want) {
		t.Fatalf("server flags packet = % X, want % X", rc.outQueue, want)
	}
}

func TestGTokenizeTextRoundTripsConfigLines(t *testing.T) {
	input := "name=Orion-Go\nstaff=(Manager),moondeath\npath=levels/*.graal\nquote=a \"quoted\" value\n"
	tokenized := gtokenizeText(input)
	want := "name=Orion-Go,\"staff=(Manager),moondeath\",\"path=levels/*.graal\",\"quote=a \"\"quoted\"\" value\""
	if tokenized != want {
		t.Fatalf("gtokenizeText = %q, want %q", tokenized, want)
	}
	if got := guntokenizeText(tokenized); got != strings.TrimSuffix(input, "\n") {
		t.Fatalf("guntokenizeText = %q, want %q", got, strings.TrimSuffix(input, "\n"))
	}
	folderConfig := "\"level *.nw\",\"level levels/*.graal\""
	if got := guntokenizeText(folderConfig); got != "level *.nw\nlevel levels/*.graal" {
		t.Fatalf("guntokenizeText quoted first field = %q", got)
	}
}

func TestRCFileBrowserStartSendsTokenizedFoldersAndDirectory(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "levels/test.nw", "level")
	rc := newRCFileBrowserTestPlayer(server)
	rc.folderList = []string{"rw levels/*.nw"}

	rc.msgPLI_RC_FILEBROWSER_START([]byte{PLI_RC_FILEBROWSER_START})

	dirListPrefix := []byte{PLO_RC_FILEBROWSER_DIRLIST + 32}
	if !bytes.HasPrefix(rc.outQueue, dirListPrefix) {
		t.Fatalf("file browser did not start with dirlist: % X", rc.outQueue)
	}
	dirListEnd := bytes.IndexByte(rc.outQueue, '\n')
	if dirListEnd < 0 {
		t.Fatalf("dirlist packet missing terminator: % X", rc.outQueue)
	}
	dirListPayload := rc.outQueue[1:dirListEnd]
	if string(dirListPayload) != "\"rw levels/*.nw\"" {
		t.Fatalf("dirlist payload = %q", dirListPayload)
	}
	if !bytes.Contains(rc.outQueue, append([]byte{PLO_RC_FILEBROWSER_MESSAGE + 32}, []byte("Welcome to the File Browser.\n")...)) {
		t.Fatalf("file browser welcome missing or malformed: % X", rc.outQueue)
	}

	wantPrefix := append([]byte{PLO_RC_FILEBROWSER_DIR + 32, byte(len("levels/"))}, []byte("levels/")...)
	dirIndex := bytes.Index(rc.outQueue, wantPrefix)
	if dirIndex < 0 {
		t.Fatalf("dir packet prefix missing from % X, want % X", rc.outQueue, wantPrefix)
	}
	dirPacket := rc.outQueue[dirIndex:]
	if !bytes.HasPrefix(dirPacket, wantPrefix) {
		t.Fatalf("dir packet prefix = % X, want % X", dirPacket, wantPrefix)
	}
	entryStart := 1 + 1 + len("levels/")
	if dirPacket[entryStart] != ' ' {
		t.Fatalf("dir entry separator = %02X, want space in % X", dirPacket[entryStart], dirPacket)
	}
	entryLen := int(dirPacket[entryStart+1])
	entry := dirPacket[entryStart+2 : entryStart+2+entryLen]
	if entry[0] != byte(len("test.nw")) || string(entry[1:1+len("test.nw")]) != "test.nw" {
		t.Fatalf("dir entry filename malformed: % X", entry)
	}
	rightsOffset := 1 + len("test.nw")
	if entry[rightsOffset] != byte(len("rw")) || string(entry[rightsOffset+1:rightsOffset+1+len("rw")]) != "rw" {
		t.Fatalf("dir entry rights malformed: % X", entry)
	}
	if len(entry) != rightsOffset+1+len("rw")+10 {
		t.Fatalf("dir entry should contain two GInt5 values, got %d bytes: % X", len(entry), entry)
	}
}

func TestRCFileBrowserRootListsFilesAndFolders(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "world/onlinestartlocal.nw", "level")
	writeTestFile(t, server.config.GetBasePath(), "world/readme.txt", "readme")
	writeTestFile(t, server.config.GetBasePath(), "world/levels/test.nw", "level")
	rc := newRCFileBrowserTestPlayer(server)
	rc.folderList = []string{"rw *.nw", "rw *.txt", "rw levels/*.nw"}
	rc.lastFolder = ""

	rc.msgPLI_RC_FILEBROWSER_START([]byte{PLI_RC_FILEBROWSER_START})

	if !bytes.Contains(rc.outQueue, []byte("onlinestartlocal.nw")) {
		t.Fatalf("root file browser output missing root level: % X", rc.outQueue)
	}
	if !bytes.Contains(rc.outQueue, []byte("levels/")) {
		t.Fatalf("root file browser output missing child folder: % X", rc.outQueue)
	}
}

func TestRCFileBrowserCDParsesRawFolderAndSendsDirectory(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "levels/test.nw", "level")
	rc := newRCFileBrowserTestPlayer(server)
	rc.folderList = []string{"rw levels/*.nw"}

	rc.msgPLI_RC_FILEBROWSER_CD(append([]byte{PLI_RC_FILEBROWSER_CD}, []byte("levels/")...))

	if rc.lastFolder != "levels/" {
		t.Fatalf("lastFolder = %q, want levels/", rc.lastFolder)
	}
	if !bytes.Contains(rc.outQueue, append([]byte{PLO_RC_FILEBROWSER_DIR + 32, byte(len("levels/"))}, []byte("levels/")...)) {
		t.Fatalf("cd did not send directory packet: % X", rc.outQueue)
	}
}

func TestRCFileBrowserUploadParsesString8FilenameAndRawData(t *testing.T) {
	server := newLoginTestServer(t)
	rc := newRCFileBrowserTestPlayer(server)
	rc.folderList = []string{"rw levels/*.nw"}
	rc.lastFolder = "levels/"

	packet := append([]byte{PLI_RC_FILEBROWSER_UP, byte(len("upload.nw"))}, []byte("upload.nw")...)
	packet = append(packet, []byte("uploaded")...)
	rc.msgPLI_RC_FILEBROWSER_UP(packet)

	got, err := os.ReadFile(filepath.Join(server.config.GetBasePath(), "levels", "upload.nw"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "uploaded" {
		t.Fatalf("uploaded file = %q", got)
	}
}

func TestRCFileBrowserDownloadWrapsFilePacketInRawData(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "levels/test.nw", "level")
	rc := newRCFileBrowserTestPlayer(server)
	rc.lastFolder = "levels/"

	rc.msgPLI_RC_FILEBROWSER_DOWN(append([]byte{PLI_RC_FILEBROWSER_DOWN}, []byte("test.nw")...))

	embedded := NewBuffer()
	embedded.WriteGChar(PLO_FILE)
	embedded.WriteGInt5(uint64(0))
	embedded.WriteGChar(byte(len("test.nw")))
	embedded.Write([]byte("test.nw"))
	embedded.Write([]byte("level"))
	embedded.WriteByte('\n')

	wantPrefix := append([]byte{PLO_RAWDATA + 32}, NewBuffer().WriteGInt(uint32(embedded.Len())).Bytes()...)
	wantPrefix = append(wantPrefix, '\n', PLO_FILE+32)
	if !bytes.HasPrefix(rc.outQueue, wantPrefix) {
		t.Fatalf("download packet prefix = % X, want prefix % X", rc.outQueue, wantPrefix)
	}
	if !bytes.Contains(rc.outQueue, []byte("test.nwlevel\n")) {
		t.Fatalf("download packet missing filename/data: % X", rc.outQueue)
	}
}

func TestAccountLoadsAndSavesIP(t *testing.T) {
	server := newLoginTestServer(t)
	accountPath := filepath.Join(server.config.GetBasePath(), "accounts", "Admin.txt")
	accountData, err := os.ReadFile(accountPath)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}
	accountData = append(accountData, []byte("IP 3232235777\n")...)
	if err := os.WriteFile(accountPath, accountData, 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	p := NewPlayer(nil, server)

	if !p.LoadAccount("Admin", false) {
		t.Fatalf("load account failed")
	}
	if p.accountIp != 3232235777 {
		t.Fatalf("account IP = %d, want 3232235777", p.accountIp)
	}
	p.accountIp = 16909060
	if !p.SaveAccount() {
		t.Fatalf("save account failed")
	}
	saved, err := os.ReadFile(accountPath)
	if err != nil {
		t.Fatalf("read saved account: %v", err)
	}
	if !bytes.Contains(saved, []byte("IP 16909060\r\n")) {
		t.Fatalf("saved account did not contain updated IP: %s", saved)
	}
}

func TestAccountIPFromRemoteAddress(t *testing.T) {
	got := accountIPFromAddr(&net.TCPAddr{IP: net.ParseIP("192.168.1.25")})
	if got != 3232235801 {
		t.Fatalf("account IP = %d, want 3232235801", got)
	}
	if got := accountIPFromAddr(fakeAddr("not an ip")); got != 0 {
		t.Fatalf("non-IP addr = %d, want 0", got)
	}
}

func TestRCLoginAppliesServerOptionsStaffRights(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("staff", "(Manager),moondeath")
	writeTestFile(t, server.config.GetBasePath(), "config/foldersconfig.txt", ""+
		"# Folder Configuration\n"+
		"level *.nw\n"+
		"level levels/*.graal\n")
	account := "" +
		"GRACC001\n" +
		"NICK moondeath\n" +
		"LOCALRIGHTS 0\n" +
		"IPRANGE 0.0.0.0\n" +
		"LEVEL onlinestartlocal.nw\n" +
		"X 4\n" +
		"Y 4\n"
	if err := os.WriteFile(filepath.Join(server.config.GetBasePath(), "accounts", "moondeath.txt"), []byte(account), 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	p := NewPlayer(nil, server)

	if !p.handleLogin(buildLoginPacket(t, 6, 42, "G3D0311C", "moondeath", "pass", "win,test,6.037")) {
		t.Fatalf("RC2 login was rejected")
	}

	wantRights := (PLPERM_NPCCONTROL << 1) - 1
	if p.adminRights != wantRights {
		t.Fatalf("adminRights = %d, want %d", p.adminRights, wantRights)
	}
	if !p.isStaff {
		t.Fatalf("staff-listed account was not marked staff")
	}
	if p.adminIp != "*.*.*.*" {
		t.Fatalf("adminIp = %q, want wildcard", p.adminIp)
	}
	if got, want := strings.Join(p.folderList, "\n"), "rw *.nw\nrw levels/*.graal"; got != want {
		t.Fatalf("folderList = %q, want %q", got, want)
	}
}

func TestRCFileBrowserUsesFolderConfigFallbackAndRootWildcard(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "config/foldersconfig.txt", ""+
		"level *.nw\n"+
		"level levels/*.graal\n")
	writeTestFile(t, server.config.GetBasePath(), "world/start.nw", "level")
	writeTestFile(t, server.config.GetBasePath(), "world/levels/test.graal", "level")
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.adminRights = allLocalRights()
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	rc.msgPLI_RC_FILEBROWSER_START([]byte{PLI_RC_FILEBROWSER_START})

	if !bytes.Contains(rc.outQueue, []byte("start.nw")) {
		t.Fatalf("file browser output missing root wildcard file: % X", rc.outQueue)
	}
	if !bytes.Contains(rc.outQueue, []byte("rw *.nw")) {
		t.Fatalf("file browser dirlist missing derived root folder right: % X", rc.outQueue)
	}
}

func TestFileSystemReadMethodsFallBackToWorldFolder(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "world/levels/test.nw", "level")
	fs := NewFileSystem(dir)

	files, err := fs.ListFiles("levels/")
	if err != nil {
		t.Fatalf("ListFiles fallback failed: %v", err)
	}
	if len(files) != 1 || files[0] != "test.nw" {
		t.Fatalf("ListFiles fallback = %v, want test.nw", files)
	}
	data, err := fs.LoadFile("levels/test.nw")
	if err != nil {
		t.Fatalf("LoadFile fallback failed: %v", err)
	}
	if string(data) != "level" {
		t.Fatalf("LoadFile fallback = %q, want level", data)
	}
	if _, err := fs.FileInfo("levels/test.nw"); err != nil {
		t.Fatalf("FileInfo fallback failed: %v", err)
	}
	if !fs.FileExists("levels/test.nw") {
		t.Fatalf("FileExists fallback = false, want true")
	}
}

func TestResolveRequestedFileUsesFolderConfigPatterns(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ganis"), 0755); err != nil {
		t.Fatalf("create ganis dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "foldersconfig.txt"), []byte("file    ganis/*.gani\nfile    *.gani\n"), 0644); err != nil {
		t.Fatalf("write foldersconfig: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ganis", "emoticon.gani"), []byte("gani data"), 0644); err != nil {
		t.Fatalf("write gani: %v", err)
	}

	server := &Server{config: NewFileSystem(dir)}
	resolved, data, err := server.resolveRequestedFile("emoticon.gani")
	if err != nil {
		t.Fatalf("resolveRequestedFile: %v", err)
	}
	if resolved != "ganis/emoticon.gani" {
		t.Fatalf("resolved = %q, want ganis/emoticon.gani", resolved)
	}
	if string(data) != "gani data" {
		t.Fatalf("data = %q, want gani data", string(data))
	}
}

func TestRCLoginClearsWorldLevelFromAccount(t *testing.T) {
	server := newLoginTestServer(t)
	account := "" +
		"GRACC001\n" +
		"NICK moondeath\n" +
		"LOCALRIGHTS 1\n" +
		"LEVEL onlinestartlocal.nw\n" +
		"X 30\n" +
		"Y 30\n"
	if err := os.WriteFile(filepath.Join(server.config.GetBasePath(), "accounts", "moondeath.txt"), []byte(account), 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	p := NewPlayer(nil, server)

	if !p.handleLogin(buildLoginPacket(t, 6, 42, "G3D0311C", "moondeath", "pass", "win,test,6.037")) {
		t.Fatalf("RC2 login was rejected")
	}

	if p.levelName != "" || p.currentLevel != nil || p.x != 0 || p.y != 0 {
		t.Fatalf("RC inherited world state: level=%q current=%v x=%d y=%d", p.levelName, p.currentLevel, p.x, p.y)
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return "fake" }
func (a fakeAddr) String() string  { return string(a) }

func newLoginTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "accounts"), 0755); err != nil {
		t.Fatalf("create accounts dir: %v", err)
	}
	account := "" +
		"GRACC001\n" +
		"NICK Admin\n" +
		"LOCALRIGHTS 1\n" +
		"LEVEL onlinestartlocal.nw\n" +
		"X 4\n" +
		"Y 4\n"
	if err := os.WriteFile(filepath.Join(dir, "accounts", "Admin.txt"), []byte(account), 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	server := NewServer("Test")
	server.config = NewFileSystem(dir)
	server.settings = NewSettings()
	server.adminSettings = NewSettings()
	server.logger = NewLogger("", false)
	return server
}

func newRCFileBrowserTestPlayer(server *Server) *Player {
	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.id = 9
	rc.accountName = "Admin"
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)
	rc.rcLargeFiles = make(map[string]string)
	return rc
}

func writeTestFile(t *testing.T, basePath, relPath, contents string) {
	t.Helper()
	fullPath := filepath.Join(basePath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("create test file dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func buildLoginPacket(t *testing.T, clientTypeByte byte, encryptionKey byte, version, account, password, identity string) []byte {
	t.Helper()
	raw := NewBuffer()
	raw.WriteGChar(clientTypeByte)
	if clientTypeByte == 4 || clientTypeByte == 5 || clientTypeByte == 6 {
		raw.WriteGChar(encryptionKey)
	}
	raw.Write([]byte(version))
	raw.WriteGChar(byte(len(account))).Write([]byte(account))
	raw.WriteGChar(byte(len(password))).Write([]byte(password))
	raw.Write([]byte(identity))
	compressed, err := ZlibCompress(raw.Bytes())
	if err != nil {
		t.Fatalf("compress login packet: %v", err)
	}
	return compressed
}

func TestSendPacketGen5LengthExcludesLengthPrefix(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	done := make(chan error, 1)
	go func() {
		p.sendPacket([]byte{PLO_SIGNATURE, 73})
		done <- nil
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	frame := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, frame); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send packet: %v", err)
	}

	wireLen := int(frame[0])<<8 | int(frame[1])
	if wireLen != len(frame)-2 {
		t.Fatalf("GEN_5 length prefix = %d, want %d", wireLen, len(frame)-2)
	}
}

func TestSendPacketGen5EncodesOutgoingPacketID(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	done := make(chan error, 1)
	go func() {
		p.sendPacket([]byte{PLO_SIGNATURE, 73})
		done <- nil
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	frame := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, frame); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send packet: %v", err)
	}

	payload := append([]byte(nil), frame[3:]...)
	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(frame[2])
	in.Decrypt(payload)

	if payload[0] != PLO_SIGNATURE+32 {
		t.Fatalf("plaintext packet ID = 0x%02X, want encoded 0x%02X", payload[0], PLO_SIGNATURE+32)
	}
}

func TestSendCompressFlushesQueuedPacketsAsOneGen5Frame(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	p.sendPacket([]byte{PLO_SIGNATURE, 73, '\n'})
	p.sendPacket([]byte{PLO_CLEARWEAPONS, '\n'})

	done := make(chan struct{}, 1)
	go func() {
		p.sendCompress(true)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	header := make([]byte, 3)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[0] != 0 || header[1] != 6 {
		t.Fatalf("GEN_5 frame length header = %02X %02X, want big-endian 00 06", header[0], header[1])
	}
	frameLen := int(header[0])<<8 | int(header[1])
	if frameLen != 1+len([]byte{PLO_SIGNATURE + 32, 73, '\n', PLO_CLEARWEAPONS + 32, '\n'}) {
		t.Fatalf("GEN_5 frame length = %d, want one uncompressed queued frame", frameLen)
	}
	encrypted := make([]byte, frameLen-1)
	if _, err := io.ReadFull(clientConn, encrypted); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	<-done

	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(header[2])
	in.Decrypt(encrypted)

	want := []byte{PLO_SIGNATURE + 32, 73, '\n', PLO_CLEARWEAPONS + 32, '\n'}
	if string(encrypted) != string(want) {
		t.Fatalf("queued plaintext = % X, want % X", encrypted, want)
	}
}

func TestPostLoginTailSendsListProcessesAfterStatusAndPlayerExchange(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	server.settings.Set("staffguilds", "Staff")
	server.settings.Set("playerlisticons", "Online,Away")
	p := &Player{
		id:         1,
		conn:       serverConn,
		server:     server,
		playerType: PLTYPE_CLIENT3,
		versionId:  222,
		encryption: *NewEncryption(),
	}
	p.accountName = "moondeath"
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPostLoginTail()
		done <- struct{}{}
	}()

	want := []byte{PLO_STAFFGUILDS + 32}
	want = append(want, []byte("\"Staff\"")...)
	want = append(want, '\n')
	want = append(want, PLO_STATUSLIST+32)
	want = append(want, []byte("Online,Away")...)
	want = append(want, '\n')
	want = append(want, PLO_LISTPROCESSES+32, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read post-login tail: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("post-login tail = % X, want % X", got, want)
	}
	if bytes.IndexByte(got, PLO_LISTPROCESSES+32) < bytes.IndexByte(got, PLO_STATUSLIST+32) {
		t.Fatalf("PLO_LISTPROCESSES was sent before PLO_STATUSLIST: % X", got)
	}
}

func TestPostLoginTailSkipsListProcessesForG3DClient(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings(), players: make(map[uint16]*Player)},
		playerType:    PLTYPE_CLIENT3,
		versionId:     300,
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	p.sendPostLoginTail()

	if bytes.Contains(p.outQueue, []byte{PLO_LISTPROCESSES + 32}) {
		t.Fatalf("G3D client received legacy PLO_LISTPROCESSES: % X", p.outQueue)
	}
}

func TestPostLoginTailExchangesAddPlayerAndOtherProps(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	p := &Player{
		id:            1,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "moondeath"
	p.character.nickName = "moondeath"
	p.communityName = "moondeath"
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:            2,
		conn:          serverConn,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	other.accountName = "Z"
	other.character.nickName = "Z"
	other.communityName = "Z"
	other.levelName = "onlinestartlocal.nw"
	server.players[p.id] = p
	server.players[other.id] = other

	p.sendPostLoginTail()

	pProps := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(other.id).Bytes()...)
	otherProps := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)

	if !bytes.Contains(p.outQueue, pProps) {
		t.Fatalf("new player did not receive existing player's PLO_OTHERPLPROPS: % X", p.outQueue)
	}
	if !containsTerminatedPacket(p.outQueue, pProps) {
		t.Fatalf("existing player's PLO_OTHERPLPROPS was not newline-terminated: % X", p.outQueue)
	}
	if !bytes.Contains(other.outQueue, otherProps) {
		t.Fatalf("existing player did not receive new player's PLO_OTHERPLPROPS: % X", other.outQueue)
	}
	if !containsTerminatedPacket(other.outQueue, otherProps) {
		t.Fatalf("new player's PLO_OTHERPLPROPS was not newline-terminated: % X", other.outQueue)
	}
}

func TestPostLoginTailSendsExistingRCAsBlankLevelPlayerListEntry(t *testing.T) {
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	p := &Player{
		id:            2,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "Z"
	p.character.nickName = "Z"
	p.levelName = "onlinestartlocal.nw"
	rc := &Player{
		id:            3,
		server:        server,
		playerType:    PLTYPE_RC2,
		versionId:     222,
		queueOutgoing: true,
		loaded:        true,
	}
	rc.accountName = "moondeath"
	rc.character.nickName = "Not Denveous"
	rc.communityName = "moondeath"
	server.players[p.id] = p
	server.players[rc.id] = rc

	p.sendPostLoginTail()

	rcPropsPrefix := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(rc.id).Bytes()...)
	if !bytes.Contains(p.outQueue, rcPropsPrefix) {
		t.Fatalf("game client did not receive RC login props packet: % X", p.outQueue)
	}
	rcAdd := append([]byte{PLO_ADDPLAYER + 32}, NewBuffer().WriteGShort(rc.id).Bytes()...)
	if bytes.Contains(p.outQueue, rcAdd) {
		t.Fatalf("game client should not receive RC PLO_ADDPLAYER during login exchange: % X", p.outQueue)
	}
}

func TestPostLoginTailSendsNPCServerPlayerListEntry(t *testing.T) {
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	p := &Player{
		id:            2,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "Z"
	p.character.nickName = "Z"
	p.levelName = "onlinestartlocal.nw"
	npc := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_NPCSERVER,
		loaded:     true,
	}
	npc.accountName = "(npcserver)"
	npc.character.nickName = "NPC-Server (Server)"
	npc.communityName = "(npcserver)"
	server.players[p.id] = p
	server.players[npc.id] = npc

	p.sendPostLoginTail()

	npcPropsPrefix := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	if !bytes.Contains(p.outQueue, npcPropsPrefix) {
		t.Fatalf("game client did not receive NPC server RC-style props packet: % X", p.outQueue)
	}
	npcAdd := append([]byte{PLO_ADDPLAYER + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	if bytes.Contains(p.outQueue, npcAdd) {
		t.Fatalf("game client should not receive NPC-server PLO_ADDPLAYER during login exchange: % X", p.outQueue)
	}
}

func TestPostLoginTailDoesNotHideNPCServerWhenHideStaffEnabled(t *testing.T) {
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	server.settings.Set("hidestaff", "true")
	p := &Player{
		id:            2,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "Z"
	p.character.nickName = "Z"
	p.levelName = "onlinestartlocal.nw"
	npc := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_NPCSERVER,
		loaded:     true,
	}
	npc.accountName = "(npcserver)"
	npc.character.nickName = "NPC-Server (Server)"
	npc.communityName = "(npcserver)"
	npc.isStaff = true
	server.players[p.id] = p
	server.players[npc.id] = npc

	p.sendPostLoginTail()

	npcPropsPrefix := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	if !bytes.Contains(p.outQueue, npcPropsPrefix) {
		t.Fatalf("game client did not receive NPC server while hidestaff=true: % X", p.outQueue)
	}
}

func TestInitNPCServerBroadcastsToOnlineGameClients(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("serverside", "true")
	game := NewPlayer(nil, server)
	game.playerType = PLTYPE_CLIENT3
	game.id = 2
	game.accountName = "Player"
	game.character.nickName = "Player"
	game.communityName = "Player"
	game.loaded = true
	gameServerConn, gameClientConn := net.Pipe()
	defer gameServerConn.Close()
	defer gameClientConn.Close()
	game.conn = gameServerConn
	game.queueOutgoing = true
	game.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[game.id] = game

	server.initNPCServer()

	npc := server.GetPlayer(1)
	if npc == nil {
		t.Fatal("npc-server was not initialized")
	}
	wantPrefix := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	if !bytes.Contains(game.outQueue, wantPrefix) {
		t.Fatalf("game client did not receive npc-server broadcast: % X", game.outQueue)
	}
}

func TestNPCServerQuerySendsNCAddressToAuthorizedRC(t *testing.T) {
	server := newLoginTestServer(t)
	server.settings.Set("serverip", "orion.moreno.land")
	server.settings.Set("serverport", "14802")
	server.initNPCServer()

	rc := NewPlayer(nil, server)
	rc.playerType = PLTYPE_RC2
	rc.accountName = "Admin"
	rc.adminRights = PLPERM_NPCCONTROL
	rc.loaded = true
	rc.queueOutgoing = true
	rc.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_NPCSERVERQUERY).WriteGShort(1).Write([]byte("location"))
	rc.msgPLI_NPCSERVERQUERY(packet.Bytes())

	want := append([]byte{PLO_NPCSERVERADDR + 32}, NewBuffer().WriteGShort(1).Bytes()...)
	want = append(want, []byte("orion.moreno.land,14802")...)
	if !bytes.Contains(rc.outQueue, want) {
		t.Fatalf("npc-server address packet = % X, want to contain % X", rc.outQueue, want)
	}
}

func TestGetPropUnknown81IsMarkerWithoutPayload(t *testing.T) {
	p := &Player{
		id:            1,
		server:        &Server{logger: NewLogger("", false), settings: NewSettings()},
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "Z"
	p.character.nickName = "Z"
	p.communityName = "Z"
	p.levelName = "onlinestartlocal.nw"

	prop := p.getProp(PLPROP_UNKNOWN81)
	if len(prop) != 0 {
		t.Fatalf("UNKNOWN81 prop payload = % X, want empty marker payload", prop)
	}
}

func containsTerminatedPacket(stream, prefix []byte) bool {
	idx := bytes.Index(stream, prefix)
	if idx < 0 {
		return false
	}
	end := bytes.IndexByte(stream[idx:], '\n')
	return end >= 0
}

func TestDeletePlayerBroadcastsDisconnectedPropToGameClients(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 1, server: server, playerType: PLTYPE_CLIENT3}
	other := &Player{id: 2, server: server, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	other.conn = nil
	server.players[p.id] = p
	server.players[other.id] = other

	// A queued player may not have a live socket in tests; give it a dummy
	// non-nil connection so DeletePlayer treats it like an active client.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	other.conn = serverConn

	server.DeletePlayer(p)

	want := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)
	want = append(want, PLPROP_PCONNECTED+32)
	want = append(want, '\n')
	if !bytes.Equal(other.outQueue, want) {
		t.Fatalf("delete player client broadcast = % X, want % X", other.outQueue, want)
	}
}

func TestDeletePlayerBroadcastsDelPlayerToRC(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 1, server: server, playerType: PLTYPE_CLIENT3}
	rc := &Player{id: 2, server: server, playerType: PLTYPE_RC2, queueOutgoing: true}
	server.players[p.id] = p
	server.players[rc.id] = rc

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	rc.conn = serverConn

	server.DeletePlayer(p)

	want := append([]byte{PLO_DELPLAYER + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)
	want = append(want, '\n')
	if !bytes.Equal(rc.outQueue, want) {
		t.Fatalf("delete player rc broadcast = % X, want % X", rc.outQueue, want)
	}
}

func TestPlayerWriteFailureDisconnectsAndBroadcastsDelPlayer(t *testing.T) {
	deadServer, deadClient := net.Pipe()
	deadClient.Close()
	defer deadServer.Close()

	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	level := NewLevel()
	dead := &Player{
		conn:       deadServer,
		server:     server,
		id:         2,
		playerType: PLTYPE_ANYCLIENT,
		encryption: *NewEncryption(),
	}
	dead.levelName = "onlinestartlocal.nw"
	dead.encryption.SetGen(ENCRYPT_GEN_1)
	dead.currentLevel = level
	observer := &Player{
		server:        server,
		id:            3,
		playerType:    PLTYPE_ANYCLIENT,
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	observerServer, observerClient := net.Pipe()
	defer observerServer.Close()
	defer observerClient.Close()
	observer.conn = observerServer
	observer.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[dead.id] = dead
	server.players[observer.id] = observer
	level.addPlayer(dead)

	dead.sendPacket([]byte{PLO_SIGNATURE, 73})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, exists := server.players[dead.id]; !exists {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, exists := server.players[dead.id]; exists {
		t.Fatalf("dead player was not removed after write failure")
	}
	if got := level.getPlayers(); len(got) != 0 {
		t.Fatalf("dead player still in level players: %v", got)
	}
	want := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(dead.id).Bytes()...)
	want = append(want, PLPROP_PCONNECTED+32)
	want = append(want, '\n')
	if !bytes.Equal(observer.outQueue, want) {
		t.Fatalf("observer delplayer = % X, want % X", observer.outQueue, want)
	}
}

func TestOnRecvReturnsFalseAfterDisconnectNilsConnection(t *testing.T) {
	p := &Player{
		server:       &Server{logger: NewLogger("", false)},
		id:           2,
		playerType:   PLTYPE_ANYCLIENT,
		disconnected: true,
	}

	if p.OnRecv() {
		t.Fatalf("OnRecv returned true for disconnected player with nil conn")
	}
}

func TestLoadAllowedVersionsStripsCommentsAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"\\config", 0755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	data := "" +
		"// List of version strings.\r\n" +
		" GNW13110\t// 1.41r1\r\n" +
		"GNW22122:GNW28015 // range\r\n" +
		"\r\n" +
		"G3D0311C  // 6.0.3.7\r\n"
	if err := os.WriteFile(dir+"\\config\\allowedversions.txt", []byte(data), 0644); err != nil {
		t.Fatalf("write allowedversions: %v", err)
	}
	server := &Server{logger: NewLogger("", false), config: NewFileSystem(dir)}

	server.loadAllowedVersions()

	want := "GNW13110,GNW22122:GNW28015,G3D0311C"
	if got := server.allowedVersionsListserverText(); got != want {
		t.Fatalf("allowedVersionsListserverText = %q, want %q", got, want)
	}
}

func TestDefaultAllowedVersionsIncludesKnownClientSet(t *testing.T) {
	server := &Server{logger: NewLogger("", false), config: NewFileSystem("servers/default")}

	server.loadAllowedVersions()

	got := map[string]int{}
	for _, version := range server.allowedVersions {
		got[version]++
	}
	want := []string{
		"GNW13110",
		"GNW31101",
		"GNW01012",
		"GNW23012",
		"GNW30042",
		"GNW19052",
		"GNW20052",
		"GNW12102",
		"GNW22122",
		"GNW21033",
		"GNW15053",
		"GNW28063",
		"GNW01113",
		"GNW03014",
		"GNW14015",
		"GNW28015",
		"G3D16053",
		"G3D27063",
		"G3D03014",
		"G3D28095",
		"G3D09125",
		"G3D17026",
		"G3D26076",
		"G3D20126",
		"G3D22067",
		"G3D14097",
		"G3D26090",
		"G3D3007A",
		"G3D2505C",
		"G3D0311C",
		"G3D0511C",
		"G3D04048",
		"G3D18010",
		"G3D29090",
		"G3D2504D",
	}
	for _, version := range want {
		if got[version] == 0 {
			t.Fatalf("default allowedversions missing %s", version)
		}
	}
	if got["G3D3007A"] != 2 {
		t.Fatalf("default allowedversions has %d G3D3007A entries, want 2", got["G3D3007A"])
	}
	if len(server.allowedVersions) != 36 {
		t.Fatalf("default allowedversions count = %d, want 36", len(server.allowedVersions))
	}
}

func TestServerListSendsAllowedVersionsText(t *testing.T) {
	server := &Server{
		logger:          NewLogger("", false),
		allowedVersions: []string{"GNW13110", "GNW22122:GNW28015", "G3D0311C"},
	}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_1,
	}

	sl.sendVersionConfig()

	got := <-sl.sendQueue
	wantText := "Listserver,settings,allowedversions,GNW13110,GNW22122:GNW28015,G3D0311C"
	want := append([]byte{SVO_SENDTEXT + 32}, []byte(wantText)...)
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("allowed versions packet = % X, want % X", got, want)
	}
}

func TestGuestLoginSendsVerifyAccount2WithIdentity(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/defaultaccount.txt", ""+
		"GRACC001\n"+
		"NICK guest\n"+
		"LEVEL onlinestartlocal.nw\n"+
		"X 4\n"+
		"Y 4\n")
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverList = sl
	p := NewPlayer(nil, server)

	packet := buildLoginPacket(t, 5, 0, "G3D03014", "guest", "", "device-token")
	if !p.handleLogin(packet) {
		t.Fatal("handleLogin returned false")
	}

	var got []byte
	select {
	case got = <-sl.sendQueue:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SVO_VERIACC2")
	}
	got = bytes.TrimSuffix(got, []byte{'\n'})
	buf := NewBufferFromBytes(got)
	if packetID := buf.ReadGChar(); packetID != SVO_VERIACC2 {
		t.Fatalf("listserver packet id = %d, want SVO_VERIACC2", packetID)
	}
	if account := buf.ReadGCharString(); account != "guest" {
		t.Fatalf("account = %q, want guest", account)
	}
	if password := buf.ReadGCharString(); password != "" {
		t.Fatalf("password = %q, want empty", password)
	}
	if id := buf.ReadGShort(); id != p.id {
		t.Fatalf("player id = %d, want %d", id, p.id)
	}
	if playerType := buf.ReadGChar(); playerType != PLTYPE_CLIENT3 {
		t.Fatalf("player type = %d, want %d", playerType, PLTYPE_CLIENT3)
	}
	identityLen := int(buf.ReadGShort())
	if identity := string(buf.ReadBytes(identityLen)); identity != "device-token" {
		t.Fatalf("identity = %q, want device-token", identity)
	}
}

func TestGuestLoadUsesPCAccountName(t *testing.T) {
	server := newLoginTestServer(t)
	writeTestFile(t, server.config.GetBasePath(), "accounts/defaultaccount.txt", ""+
		"GRACC001\n"+
		"NICK guest\n"+
		"LEVEL onlinestartlocal.nw\n"+
		"X 4\n"+
		"Y 4\n")
	p := NewPlayer(nil, server)
	p.setServer(server)
	if !p.LoadAccount("guest", true) {
		t.Fatal("LoadAccount guest returned false")
	}
	if !p.isGuest || !p.isLoadOnly {
		t.Fatalf("guest flags = isGuest:%v isLoadOnly:%v, want true/true", p.isGuest, p.isLoadOnly)
	}
	if !strings.HasPrefix(p.accountName, "pc:") {
		t.Fatalf("guest accountName = %q, want pc:*", p.accountName)
	}
	if p.communityName != "guest" {
		t.Fatalf("communityName = %q, want guest", p.communityName)
	}
}

func TestServerListAssignPCIDUpdatesGuest(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 44, server: server, playerType: PLTYPE_CLIENT3}
	p.accountName = "guest"
	p.isGuest = true
	server.players[p.id] = p
	sl := &ServerList{server: server}

	data := NewBuffer().
		WriteGShort(p.id).
		WriteGChar(byte(p.playerType)).
		WriteString8Encoded("6259711").
		Bytes()
	sl.handleListPacket(SVI_ASSIGNPCID, data)

	if p.deviceId != 6259711 {
		t.Fatalf("deviceId = %d, want 6259711", p.deviceId)
	}
	if p.accountName != "pc:6259711" || p.communityName != "guest" || !p.isLoadOnly {
		t.Fatalf("guest after PCID = account:%q community:%q loadOnly:%v", p.accountName, p.communityName, p.isLoadOnly)
	}
}

func TestServerListSendsRCPlayersForPlayerList(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_1,
	}
	rc := &Player{id: 4, playerType: PLTYPE_RC2}
	rc.accountName = "moondeath"
	rc.character.nickName = "Not Denveous"

	sl.AddPlayer(rc)

	select {
	case packet := <-sl.sendQueue:
		rcAdd := append([]byte{SVO_PLYRADD + 32}, NewBuffer().WriteGShort(rc.id).Bytes()...)
		if !bytes.Contains(packet, rcAdd) {
			t.Fatalf("RC player list packet = % X, want % X", packet, rcAdd)
		}
	default:
		t.Fatalf("RC player was not sent to listserver player list")
	}
}

func TestServerListSendPlayersIncludesNPCServerAndRC(t *testing.T) {
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	server.settings.Set("serverside", "true")
	npc := &Player{id: 1, playerType: PLTYPE_NPCSERVER}
	npc.accountName = "(npcserver)"
	npc.character.nickName = "NPC-Server (Server)"
	rc := &Player{id: 4, playerType: PLTYPE_RC2}
	rc.accountName = "moondeath"
	rc.character.nickName = "Not Denveous"
	server.players[npc.id] = npc
	server.players[rc.id] = rc
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 4),
		codec:     ENCRYPT_GEN_1,
	}
	server.serverList = sl

	sl.sendPlayers()

	var packets [][]byte
	for len(sl.sendQueue) > 0 {
		packets = append(packets, <-sl.sendQueue)
	}
	npcAdd := append([]byte{SVO_PLYRADD + 32}, NewBuffer().WriteGShort(npc.id).Bytes()...)
	rcAdd := append([]byte{SVO_PLYRADD + 32}, NewBuffer().WriteGShort(rc.id).Bytes()...)
	if !packetListContains(packets, npcAdd) {
		t.Fatalf("sendPlayers did not include NPC server, packets=% X", packets)
	}
	if !packetListContains(packets, rcAdd) {
		t.Fatalf("sendPlayers did not include RC player, packets=% X", packets)
	}
}

func packetListContains(packets [][]byte, needle []byte) bool {
	for _, packet := range packets {
		if bytes.Contains(packet, needle) {
			return true
		}
	}
	return false
}

func TestServerListSendPacketUsesActiveCodec(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_2,
	}

	buf := NewBuffer()
	buf.WriteGChar(SVO_PING)
	sl.SendPacket(buf.Bytes())

	got := <-sl.sendQueue
	if len(got) < 3 {
		t.Fatalf("encoded packet too short: % X", got)
	}
	frameLen := int(got[0])<<8 | int(got[1])
	if frameLen != len(got)-2 {
		t.Fatalf("listserver frame len = %d, want %d", frameLen, len(got)-2)
	}
	plain, err := ZlibDecompress(got[2:])
	if err != nil {
		t.Fatalf("decompress ping packet: %v", err)
	}
	want := []byte{SVO_PING + 32, '\n'}
	if !bytes.Equal(plain, want) {
		t.Fatalf("decoded ping packet = % X, want % X", plain, want)
	}
}

func TestServerListSendTextPacketEncodesIDAndUsesActiveCodec(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_2,
	}

	sl.SendTextPacket(SVO_REQUESTLIST, "lister\x01pmservers\x01all")

	got := <-sl.sendQueue
	plain, err := ZlibDecompress(got[2:])
	if err != nil {
		t.Fatalf("decompress request list packet: %v", err)
	}
	want := append([]byte{SVO_REQUESTLIST + 32}, []byte("lister\x01pmservers\x01all")...)
	want = append(want, '\n')
	if !bytes.Equal(plain, want) {
		t.Fatalf("decoded request list packet = % X, want % X", plain, want)
	}
}

func TestServerListTimedEventsSendsSetIPKeepalive(t *testing.T) {
	settings := NewSettings()
	settings.Set("serverip", "AUTO")
	server := &Server{logger: NewLogger("", false), settings: settings}
	sl := &ServerList{
		server:        server,
		enabled:       true,
		connected:     true,
		sendQueue:     make(chan []byte, 1),
		codec:         ENCRYPT_GEN_1,
		lastKeepalive: time.Now().Add(-time.Minute),
	}

	sl.doTimedEvents()

	got := <-sl.sendQueue
	want := NewBuffer().WriteGChar(SVO_SETIP).Write([]byte("AUTO")).Bytes()
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("keepalive packet = % X, want % X", got, want)
	}
}

func TestServerListSetIpUsesRawPayload(t *testing.T) {
	server := &Server{logger: NewLogger("", false), settings: NewSettings()}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_1,
	}

	sl.SetIp("orion.moreno.land")

	got := <-sl.sendQueue
	want := NewBuffer().WriteGChar(SVO_SETIP).Write([]byte("orion.moreno.land")).Bytes()
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("SETIP packet = % X, want % X", got, want)
	}
	if bytes.Contains(got, []byte("1orion.moreno.land")) {
		t.Fatalf("SETIP leaked length byte into ip payload: %q", got)
	}
}

func TestOneSecondEventsDisconnectsStalePlayersWithoutDeadlock(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	level := NewLevel()
	p := &Player{
		id:           2,
		server:       server,
		playerType:   PLTYPE_CLIENT3,
		currentLevel: level,
		lastData:     time.Now().Add(-6 * time.Minute),
	}
	level.addPlayer(p)
	server.players[p.id] = p

	done := make(chan struct{})
	go func() {
		server.oneSecondEvents()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("oneSecondEvents deadlocked while disconnecting stale player")
	}

	if got := server.GetPlayer(p.id); got != nil {
		t.Fatalf("stale player still registered after timeout: %#v", got)
	}
	for _, pid := range level.getPlayers() {
		if pid == p.id {
			t.Fatalf("stale player id %d still present in level players", p.id)
		}
	}
}

func TestOneSecondEventsDoesNotDisconnectNPCServer(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	npc := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_NPCSERVER,
		loaded:     true,
		lastData:   time.Now().Add(-6 * time.Minute),
	}
	server.players[npc.id] = npc

	server.oneSecondEvents()

	if got := server.GetPlayer(npc.id); got == nil {
		t.Fatal("npc-server pseudo-player was disconnected as stale")
	}
}

func TestOtherPropsPacketUsesOtherPlayerPropsHeader(t *testing.T) {
	p := &Player{id: 0x1234}
	props := []byte{byte(PLPROP_NICKNAME + 32), byte(len("moondeath") + 32)}
	props = append(props, []byte("moondeath")...)

	got := p.otherPropsPacket(props)

	want := []byte{PLO_OTHERPLPROPS, 0x44, 0x54}
	want = append(want, props...)
	if !bytes.Equal(got, want) {
		t.Fatalf("other props packet = % X, want % X", got, want)
	}
}

func TestSendPacketGen5UsesBz2ForLargeFrames(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	packet := append([]byte{PLO_RAWDATA}, make([]byte, 0x2001)...)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPacket(packet)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	header := make([]byte, 3)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[2] != COMPRESS_BZ2 {
		t.Fatalf("compression type = 0x%02X, want BZ2 0x%02X", header[2], COMPRESS_BZ2)
	}
	encrypted := make([]byte, (int(header[0])<<8|int(header[1]))-1)
	if _, err := io.ReadFull(clientConn, encrypted); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	<-done

	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(header[2])
	in.Decrypt(encrypted)
	decompressed, err := Bz2Decompress(encrypted)
	if err != nil {
		t.Fatalf("decompress bz2 payload: %v", err)
	}
	if decompressed[0] != PLO_RAWDATA+32 || len(decompressed) != len(packet) {
		t.Fatalf("decompressed large frame len/id = %d/0x%02X, want %d/0x%02X", len(decompressed), decompressed[0], len(packet), PLO_RAWDATA+32)
	}
}

func TestHandlePacketDecodesGraalEncodedPacketID(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	if !p.handlePacket([]byte{byte(PLI_LANGUAGE + 32), 'E', 'n', 'g', 'l', 'i', 's', 'h'}) {
		t.Fatal("handlePacket returned false")
	}
	if p.language != "English" {
		t.Fatalf("language = %q, want English", p.language)
	}
}

func TestControlOnlyPacketClassification(t *testing.T) {
	rcOnly := []int{
		PLI_RC_SERVEROPTIONSGET,
		PLI_RC_PLAYERBANSET,
		PLI_RC_FILEBROWSER_START,
		PLI_NPCSERVERQUERY,
		PLI_RC_FOLDERDELETE,
	}
	for _, packetId := range rcOnly {
		if !isRCOnlyPacket(packetId) {
			t.Fatalf("packet %d should be RC-only", packetId)
		}
	}

	ncOnly := []int{
		PLI_NC_LISTNPCS,
		PLI_NC_NPCGET,
		PLI_NC_CLASSDELETE,
		PLI_NC_LEVELLISTGET,
		PLI_NC_LEVELLISTSET,
	}
	for _, packetId := range ncOnly {
		if !isNCOnlyPacket(packetId) {
			t.Fatalf("packet %d should be NC-only", packetId)
		}
	}

	sharedOrClient := []int{
		PLI_PROFILEGET,
		PLI_PROFILESET,
		PLI_UPDATECLASS,
		PLI_RC_UNKNOWN162,
		PLI_REQUESTUPDATEBOARD,
		PLI_LANGUAGE,
	}
	for _, packetId := range sharedOrClient {
		if isRCOnlyPacket(packetId) || isNCOnlyPacket(packetId) {
			t.Fatalf("packet %d should not be control-only", packetId)
		}
	}
}

func TestHandlePacketIgnoresControlPacketsFromGameClients(t *testing.T) {
	p := &Player{
		server:     &Server{logger: NewLogger("", false)},
		playerType: PLTYPE_CLIENT3,
	}
	p.accountName = "moondeath"

	if !p.handlePacket([]byte{byte(PLI_RC_PLAYERBANSET + 32), 0x01, 0x02}) {
		t.Fatal("handlePacket returned false for RC-only packet")
	}
	if p.invalidPackets != 0 {
		t.Fatalf("invalidPackets = %d, want 0", p.invalidPackets)
	}
	if p.packetCount != 1 {
		t.Fatalf("packetCount = %d, want 1", p.packetCount)
	}
}

func TestHandleDecompressedPacketsSplitsNewlineDelimitedClientPackets(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	p.handleDecompressedPackets([]byte{
		byte(PLI_LANGUAGE + 32), 'E', 'n', 'g', 'l', 'i', 's', 'h', '\n',
		byte(PLI_LANGUAGE + 32), 'F', 'r', 'e', 'n', 'c', 'h', '\n',
	})

	if p.language != "French" {
		t.Fatalf("language = %q, want French", p.language)
	}
}

func TestHandleRawDataDecompressesGen2NCFrames(t *testing.T) {
	server := newLoginTestServer(t)
	server.weapons = map[string]*Weapon{
		"ControlWeapon": {name: "ControlWeapon", image: "control.png", script: "function onCreated() {}"},
	}
	p := NewPlayer(nil, server)
	p.playerType = PLTYPE_NC
	p.queueOutgoing = true
	p.encryption.SetGen(ENCRYPT_GEN_2)

	frame, err := ZlibCompress([]byte{byte(PLI_NC_WEAPONLISTGET + 32), '\n'})
	if err != nil {
		t.Fatalf("compress nc weapon list request: %v", err)
	}
	p.handleRawData(frame)

	want := NewBuffer()
	want.WriteByte(PLO_NC_WEAPONLISTGET + 32)
	want.WriteString8Encoded("ControlWeapon")
	want.WriteByte('\n')
	if !bytes.Contains(p.outQueue, want.Bytes()) {
		t.Fatalf("NC weapon list response = % X, want % X", p.outQueue, want.Bytes())
	}
	if p.invalidPackets != 0 {
		t.Fatalf("invalidPackets = %d, want 0", p.invalidPackets)
	}
}

func TestSendPropsWithArrayEncodesPropertyIDs(t *testing.T) {
	p := &Player{}
	p.character.nickName = "moondeath"

	var props [PROPCOUNT]bool
	props[PLPROP_NICKNAME] = true

	got := p.sendPropsWithArray(props)
	want := []byte{byte(PLPROP_NICKNAME + 32), byte(len("moondeath") + 32)}
	want = append(want, []byte("moondeath")...)

	if string(got) != string(want) {
		t.Fatalf("props bytes = % X, want % X", got, want)
	}
}

func TestLoginPropsMatchReferenceByOmittingNickname(t *testing.T) {
	if sendLoginProps[PLPROP_NICKNAME] {
		t.Fatalf("sendLoginProps includes PLPROP_NICKNAME, but C++ __sendLogin omits it")
	}
	if !sendLoginProps[PLPROP_MAXPOWER] || !sendLoginProps[PLPROP_CURPOWER] {
		t.Fatalf("sendLoginProps lost core health props")
	}
}

func TestNewPlayerDefaultsCarrySpriteToNone(t *testing.T) {
	p := NewPlayer(nil, &Server{logger: NewLogger("", false)})

	if p.carrySprite != 0xff {
		t.Fatalf("carrySprite = 0x%02X, want 0xFF", p.carrySprite)
	}
	if got := p.getProp(PLPROP_CARRYSPRITE); !bytes.Equal(got, []byte{0xff}) {
		t.Fatalf("carry sprite prop = % X, want FF", got)
	}
}

func TestLoadAccountParsesDecimalAndRepairsInvalidHealth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"\\accounts", 0755); err != nil {
		t.Fatalf("create accounts dir: %v", err)
	}
	account := "GRACC001\r\n" +
		"NAME moondeath\r\n" +
		"NICK moondeath\r\n" +
		"LEVEL onlinestartlocal.nw\r\n" +
		"X 32.00\r\n" +
		"Y 32.00\r\n" +
		"MAXHP 3.00\r\n" +
		"HP 3.00\r\n"
	if err := os.WriteFile(dir+"\\accounts\\moondeath.txt", []byte(account), 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	server := &Server{logger: NewLogger("", false), config: NewFileSystem(dir)}
	p := &Player{server: server}
	p.setServer(server)

	if !p.LoadAccount("moondeath", false) {
		t.Fatalf("LoadAccount returned false")
	}
	if p.maxHitpoints != 3 || p.character.hitpoints != 3 {
		t.Fatalf("health = max %d current %d, want 3/3", p.maxHitpoints, p.character.hitpoints)
	}

	broken := "GRACC001\r\n" +
		"NAME Z\r\n" +
		"NICK Z\r\n" +
		"LEVEL onlinestartlocal.nw\r\n" +
		"MAXHP 0\r\n" +
		"HP 0\r\n"
	if err := os.WriteFile(dir+"\\accounts\\Z.txt", []byte(broken), 0644); err != nil {
		t.Fatalf("write broken account: %v", err)
	}
	q := &Player{server: server}
	q.setServer(server)
	if !q.LoadAccount("Z", false) {
		t.Fatalf("LoadAccount broken returned false")
	}
	if q.maxHitpoints != 3 || q.character.hitpoints != 3 {
		t.Fatalf("repaired health = max %d current %d, want 3/3", q.maxHitpoints, q.character.hitpoints)
	}
}

func TestLoginWarpTargetUsesSavedAccountLocation(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false), settings: NewSettings()}}
	p.levelName = "inside house.nw"
	p.setX(44)
	p.setY(45.5)

	levelName, x, y := p.loginWarpTarget()

	if levelName != "inside house.nw" || x != 44 || y != 45.5 {
		t.Fatalf("login target = %q %.2f %.2f, want inside house.nw 44.00 45.50", levelName, x, y)
	}
}

func TestOneMinuteEventsSavesMovedPlayerAccount(t *testing.T) {
	dir := t.TempDir()
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_CLIENT3,
	}
	p.setServer(server)
	p.accountName = "moondeath"
	p.communityName = "moondeath"
	p.character.nickName = "moondeath"
	p.character.gani = "idle.gif"
	p.maxHitpoints = 3
	p.character.hitpoints = 3
	p.flagList = make(map[string]string)
	server.players[p.id] = p

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_X2).WriteGShort(encodeSignedGShortCoord(44 * 16))
	packet.WriteGChar(PLPROP_Y2).WriteGShort(encodeSignedGShortCoord(45 * 16))
	packet.WriteGChar(PLPROP_CURLEVEL).WriteGChar(byte(len("inside house.nw"))).Write([]byte("inside house.nw"))
	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}

	server.oneMinuteEvents()

	data, err := os.ReadFile(dir + "\\accounts\\moondeath.txt")
	if err != nil {
		t.Fatalf("read saved account: %v", err)
	}
	for _, want := range []string{"LEVEL inside house.nw\r\n", "X 44.00\r\n", "Y 45.00\r\n"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("saved account missing %q:\n%s", want, data)
		}
	}
}

func TestOneMinuteEventsDoesNotSaveNpcServerAccount(t *testing.T) {
	dir := t.TempDir()
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_NPCSERVER,
	}
	p.setServer(server)
	p.accountName = "(npcserver)"
	p.communityName = "(npcserver)"
	server.players[p.id] = p

	server.oneMinuteEvents()

	if _, err := os.Stat(dir + "\\accounts\\(npcserver).txt"); !os.IsNotExist(err) {
		t.Fatalf("npcserver account save err = %v, want file to not exist", err)
	}
}

func TestDisconnectSavesPlayerAccount(t *testing.T) {
	dir := t.TempDir()
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		id:         1,
		server:     server,
		playerType: PLTYPE_CLIENT3,
	}
	p.setServer(server)
	p.accountName = "moondeath"
	p.communityName = "moondeath"
	p.character.nickName = "moondeath"
	p.character.gani = "idle.gif"
	p.maxHitpoints = 3
	p.character.hitpoints = 3
	p.levelName = "onlinestartlocal.nw"
	p.setX(50)
	p.setY(51)
	p.flagList = make(map[string]string)
	server.players[p.id] = p

	p.disconnect()

	data, err := os.ReadFile(dir + "\\accounts\\moondeath.txt")
	if err != nil {
		t.Fatalf("read saved account: %v", err)
	}
	for _, want := range []string{"LEVEL onlinestartlocal.nw\r\n", "X 50.00\r\n", "Y 51.00\r\n"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("saved account missing %q:\n%s", want, data)
		}
	}
	if _, exists := server.players[p.id]; exists {
		t.Fatalf("disconnect did not remove player from server")
	}
}

func TestLevelBoardPacketUsesEncodedPacketID(t *testing.T) {
	level := NewLevel()
	level.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}
	level.tiles[0].tiles[0] = 0x1234
	got := level.getBoardPacket()

	if len(got) != 8194 {
		t.Fatalf("board packet length = %d, want 8194", len(got))
	}
	if got[0] != PLO_BOARDPACKET+32 {
		t.Fatalf("board packet ID = 0x%02X, want encoded 0x%02X", got[0], PLO_BOARDPACKET+32)
	}
	if got[1] != 0x34 || got[2] != 0x12 {
		t.Fatalf("first board tile bytes = %02X %02X, want little-endian 34 12", got[1], got[2])
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("board packet missing newline terminator")
	}
}

func TestPlayerWarpUsesClientWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), settings: NewSettings()},
		encryption: *NewEncryption(),
	}
	p.server.settings.Set("bigmap", "worldmap.txt, worldmap.png, 30, 40")
	p.server.settings.Set("minimap", "mini.txt, mini.png, 5, 6")
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.setX(32)
		p.setY(32)
		p.sendPlayerWarp(p.x, p.y, p.z, "onlinestartlocal.nw")
		done <- struct{}{}
	}()

	want := append([]byte{PLO_PLAYERWARP + 32, 64 + 32, 64 + 32}, []byte("onlinestartlocal.nw\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read warp packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("warp packet = % X, want % X", got, want)
	}
}

func TestLevelWarpParsesRestOfPacketAsLevelName(t *testing.T) {
	level := NewLevel()
	level.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}
	server := &Server{
		logger:   NewLogger("", false),
		config:   NewFileSystem("."),
		settings: NewSettings(),
		levels:   map[string]*Level{"inside house": level},
	}
	p := &Player{
		id:            2,
		server:        server,
		encryption:    *NewEncryption(),
		queueOutgoing: true,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_LEVELWARP)
	packet.WriteGChar(64)
	packet.WriteGChar(65)
	packet.Write([]byte("inside house.nw"))

	want := append([]byte{PLO_PLAYERWARP + 32, 64 + 32, 65 + 32}, []byte("inside house.nw\n")...)
	if !p.msgPLI_LEVELWARP(packet.Bytes()) {
		t.Fatalf("msgPLI_LEVELWARP returned false")
	}

	if !bytes.HasPrefix(p.outQueue, want) {
		t.Fatalf("level warp response prefix = % X, want % X", p.outQueue[:minInt(len(p.outQueue), len(want))], want)
	}
	levelNamePacket := append([]byte{PLO_LEVELNAME + 32}, []byte("inside house.nw\n")...)
	if !bytes.Contains(p.outQueue, levelNamePacket) {
		t.Fatalf("level warp did not send level data after warp: % X", p.outQueue[:minInt(len(p.outQueue), 64)])
	}
	if !bytes.Contains(p.outQueue, []byte{PLO_RAWDATA + 32}) {
		t.Fatalf("level warp did not send raw board data")
	}
	if p.levelName != "inside house.nw" {
		t.Fatalf("levelName = %q, want inside house.nw", p.levelName)
	}
	if !p.loaded {
		t.Fatalf("player was not marked loaded after level warp")
	}
}

func TestBoardModifyUsesGCharRegionHeader(t *testing.T) {
	level := NewLevel()
	p := &Player{
		server: &Server{logger: NewLogger("", false), settings: NewSettings(), levels: map[string]*Level{"onlinestartlocal.nw": level}},
	}
	p.levelName = "onlinestartlocal.nw"

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2)
	packet.WriteGChar(3)
	packet.WriteGChar(1)
	packet.WriteGChar(1)
	packet.WriteGShort(0x1234)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}

	if len(level.boardChanges) != 1 {
		t.Fatalf("boardChanges = %d, want 1", len(level.boardChanges))
	}
	change := level.boardChanges[0]
	if change.x != 2 || change.y != 3 || change.width != 1 || change.height != 1 {
		t.Fatalf("change region = %d,%d %dx%d, want 2,3 1x1", change.x, change.y, change.width, change.height)
	}
	if len(change.newTiles) != 2 || change.newTiles[0] != 0x12 || change.newTiles[1] != 0x34 {
		t.Fatalf("change tiles = % X, want 12 34", change.newTiles)
	}
}

func TestBoardModifyBroadcastsAndDropsBushItem(t *testing.T) {
	settings := NewSettings()
	settings.Set("bushitems", "true")
	settings.Set("tiledroprate", "100")
	settings.Set("respawntime", "15")
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{id: 1, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, versionId: 222}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{id: 2, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	other.conn, _ = net.Pipe()
	defer other.conn.Close()
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2).WriteGChar(3).WriteGChar(1).WriteGChar(1)
	packet.WriteGShort(0)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}
	if level.getTileAt(2, 3) != 0 {
		t.Fatalf("tile did not change to 0")
	}
	boardModify := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 32, 32, '\n'}
	if !bytes.Contains(other.outQueue, boardModify) {
		t.Fatalf("observer did not receive board modify % X in % X", boardModify, other.outQueue)
	}
	if !bytes.Contains(other.outQueue, []byte{PLO_ITEMADD + 32}) {
		t.Fatalf("observer did not receive item drop: % X", other.outQueue)
	}
	if len(level.items) != 1 {
		t.Fatalf("level items = %d, want 1", len(level.items))
	}
}

func TestItemTakeAwardsPlayerAndRemovesLevelItem(t *testing.T) {
	level := NewLevel()
	level.addItem(10, 12, ItemBlueRupee)
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(t.TempDir()),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{
		id:            1,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		loaded:        true,
		queueOutgoing: true,
	}
	p.setServer(server)
	p.levelName = "onlinestartlocal.nw"
	p.accountName = "moondeath"
	p.character.nickName = "moondeath"
	p.character.gralats = 1
	p.flagList = make(map[string]string)
	other := &Player{id: 2, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	other.conn, _ = net.Pipe()
	defer other.conn.Close()
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_ITEMTAKE).WriteGChar(10).WriteGChar(12)

	if !p.msgPLI_ITEMDEL(packet.Bytes()) {
		t.Fatalf("msgPLI_ITEMDEL returned false")
	}
	if len(level.items) != 0 {
		t.Fatalf("level items = %d, want 0", len(level.items))
	}
	if p.character.gralats != 6 || p.rupees != 6 {
		t.Fatalf("player rupees = character %d prop %d, want 6/6", p.character.gralats, p.rupees)
	}
	wantSelf := append([]byte{PLO_PLAYERPROPS + 32, PLPROP_RUPEESCOUNT + 32}, NewBuffer().WriteGInt(6).Bytes()...)
	wantSelf = append(wantSelf, '\n')
	if !bytes.Contains(p.outQueue, wantSelf) {
		t.Fatalf("player did not receive rupee prop % X in % X", wantSelf, p.outQueue)
	}
	wantDel := []byte{PLO_ITEMDEL + 32, 10 + 32, 12 + 32, '\n'}
	if !bytes.Contains(other.outQueue, wantDel) {
		t.Fatalf("observer did not receive item delete % X in % X", wantDel, other.outQueue)
	}
}

func TestItemAddDoesNotEchoToSenderWhenAlone(t *testing.T) {
	level := NewLevel()
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:            1,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		loaded:        true,
		queueOutgoing: true,
	}
	p.levelName = "onlinestartlocal"
	server.players[p.id] = p
	level.addPlayer(p)

	packet := NewBuffer()
	packet.WriteByte(PLI_ITEMADD).WriteGChar(10).WriteGChar(12).WriteGChar(byte(ItemGreenRupee))
	if !p.msgPLI_ITEMADD(packet.Bytes()) {
		t.Fatalf("msgPLI_ITEMADD returned false")
	}

	if len(level.items) != 1 {
		t.Fatalf("level items = %d, want 1", len(level.items))
	}
	if len(p.outQueue) != 0 {
		t.Fatalf("sender received echoed item add: % X", p.outQueue)
	}
}

func TestOpenChestAwardsOnceAndPersistsChest(t *testing.T) {
	dir := t.TempDir()
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	chest := &LevelChest{x: 20, y: 24, itemType: ItemGreenRupee, signIndex: 0}
	level.chests = []*LevelChest{chest}
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{
		id:            1,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		loaded:        true,
		queueOutgoing: true,
	}
	p.setServer(server)
	p.accountName = "moondeath"
	p.communityName = "moondeath"
	p.levelName = "onlinestartlocal.nw"
	p.character.nickName = "moondeath"
	p.character.gani = "idle.gif"
	p.maxHitpoints = 3
	p.character.hitpoints = 3
	p.character.gralats = 1
	p.flagList = make(map[string]string)
	server.players[p.id] = p
	level.players = []uint16{p.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_OPENCHEST).WriteGChar(20).WriteGChar(24)

	if !p.msgPLI_OPENCHEST(packet.Bytes()) {
		t.Fatalf("msgPLI_OPENCHEST returned false")
	}
	if p.character.gralats != 2 {
		t.Fatalf("rupees after chest = %d, want 2", p.character.gralats)
	}
	chestKey := "20:24:onlinestartlocal.nw"
	if len(p.chestList) != 1 || p.chestList[0] != chestKey {
		t.Fatalf("chestList = %#v, want %q", p.chestList, chestKey)
	}
	wantChest := []byte{PLO_LEVELCHEST + 32, 1 + 32, 20 + 32, 24 + 32, '\n'}
	if !bytes.Contains(p.outQueue, wantChest) {
		t.Fatalf("player did not receive open chest packet % X in % X", wantChest, p.outQueue)
	}
	data, err := os.ReadFile(dir + "\\accounts\\moondeath.txt")
	if err != nil {
		t.Fatalf("read saved account: %v", err)
	}
	if !bytes.Contains(data, []byte("CHEST "+chestKey+"\r\n")) {
		t.Fatalf("saved account missing chest key %q:\n%s", chestKey, data)
	}

	p.outQueue = nil
	if !p.msgPLI_OPENCHEST(packet.Bytes()) {
		t.Fatalf("second msgPLI_OPENCHEST returned false")
	}
	if p.character.gralats != 2 {
		t.Fatalf("rupees after second open = %d, want 2", p.character.gralats)
	}
	if len(p.outQueue) != 0 {
		t.Fatalf("second open sent packets: % X", p.outQueue)
	}
}

func TestOpenChestPersistsOnlyLevelBasename(t *testing.T) {
	dir := t.TempDir()
	level := NewLevel()
	level.levelName = "levels/onlinestartlocal.nw"
	chest := &LevelChest{x: 20, y: 24, itemType: ItemGreenRupee, signIndex: 0}
	level.chests = []*LevelChest{chest}
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"levels/onlinestartlocal.nw": level},
	}
	p := &Player{
		id:            1,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		loaded:        true,
		queueOutgoing: true,
	}
	p.setServer(server)
	p.accountName = "moondeath"
	p.levelName = "levels/onlinestartlocal.nw"
	p.character.nickName = "moondeath"
	p.maxHitpoints = 3
	p.character.hitpoints = 3
	p.flagList = make(map[string]string)
	server.players[p.id] = p
	level.players = []uint16{p.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_OPENCHEST).WriteGChar(20).WriteGChar(24)
	if !p.msgPLI_OPENCHEST(packet.Bytes()) {
		t.Fatalf("msgPLI_OPENCHEST returned false")
	}

	want := "20:24:onlinestartlocal.nw"
	if len(p.chestList) != 1 || p.chestList[0] != want {
		t.Fatalf("chestList = %#v, want %q", p.chestList, want)
	}
	if strings.Contains(p.chestList[0], "/") || strings.Contains(p.chestList[0], "\\") {
		t.Fatalf("chest key contains path separator: %q", p.chestList[0])
	}
}

func TestOpenChestSpinAttackSetsStatusFlag(t *testing.T) {
	dir := t.TempDir()
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	chest := &LevelChest{x: 20, y: 24, itemType: ItemSpinattack, signIndex: 0}
	level.chests = []*LevelChest{chest}
	server := &Server{
		logger:  NewLogger("", false),
		config:  NewFileSystem(dir),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{
		id:            1,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		loaded:        true,
		queueOutgoing: true,
	}
	p.setServer(server)
	p.accountName = "moondeath"
	p.communityName = "moondeath"
	p.levelName = "onlinestartlocal.nw"
	p.character.nickName = "moondeath"
	p.character.gani = "idle.gif"
	p.flagList = make(map[string]string)
	server.players[p.id] = p
	level.players = []uint16{p.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_OPENCHEST).WriteGChar(20).WriteGChar(24)

	if !p.msgPLI_OPENCHEST(packet.Bytes()) {
		t.Fatalf("msgPLI_OPENCHEST returned false")
	}
	if p.status&PLSTATUS_HASSPIN == 0 {
		t.Fatalf("status = %#x, missing HASSPIN", p.status)
	}
	wantProps := []byte{PLO_PLAYERPROPS + 32, PLPROP_STATUS + 32, byte(PLSTATUS_HASSPIN) + 32, '\n'}
	if !bytes.Contains(p.outQueue, wantProps) {
		t.Fatalf("player did not receive spin status props % X in % X", wantProps, p.outQueue)
	}
	data, err := os.ReadFile(dir + "\\accounts\\moondeath.txt")
	if err != nil {
		t.Fatalf("read saved account: %v", err)
	}
	if !bytes.Contains(data, []byte("STATUS 64\r\n")) {
		t.Fatalf("saved account missing spin status:\n%s", data)
	}
}

func TestBoardModifyUsesCurrentLevelWhenNameLookupMisses(t *testing.T) {
	settings := NewSettings()
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{},
	}
	p := &Player{id: 1, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, versionId: 222}
	p.levelName = "onlinestartlocal.nw"
	otherConn, otherPeer := net.Pipe()
	defer otherConn.Close()
	defer otherPeer.Close()
	other := &Player{id: 2, conn: otherConn, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2).WriteGChar(3).WriteGChar(1).WriteGChar(1)
	packet.WriteGShort(0)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}
	if level.getTileAt(2, 3) != 0 {
		t.Fatalf("tile did not change through currentLevel fallback")
	}
	boardModify := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 32, 32, '\n'}
	if !bytes.Contains(other.outQueue, boardModify) {
		t.Fatalf("observer did not receive board modify via currentLevel % X in % X", boardModify, other.outQueue)
	}
}

func TestBoardModifyRespawnsOldTile(t *testing.T) {
	settings := NewSettings()
	settings.Set("respawntime", "0")
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal.nw": level},
	}
	observer := &Player{id: 2, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	observer.conn, _ = net.Pipe()
	defer observer.conn.Close()
	server.players[observer.id] = observer
	level.players = []uint16{observer.id}

	if !level.alterBoard(server, 2, 3, 1, 1, []int16{0}) {
		t.Fatalf("alterBoard returned false")
	}
	level.processBoardRespawns(server)

	if level.getTileAt(2, 3) != 2 {
		t.Fatalf("tile did not respawn to 2")
	}
	respawnPacket := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 32, 34, '\n'}
	if !bytes.Contains(observer.outQueue, respawnPacket) {
		t.Fatalf("observer did not receive respawn packet % X in % X", respawnPacket, observer.outQueue)
	}
}

func TestRequestUpdateBoardParsesLevelAndModTimeHeader(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	levelName := "onlinestartlocal.nw"
	changeTime := time.Unix(1712345680, 0)
	level := NewLevel()
	level.boardChanges = append(level.boardChanges, LevelBoardChange{
		x:        2,
		y:        3,
		width:    1,
		height:   1,
		newTiles: []byte{0x12, 0x34},
		time:     changeTime,
	})
	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), levels: map[string]*Level{levelName: level}},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_REQUESTUPDATEBOARD)
	packet.WriteGChar(byte(len(levelName))).Write([]byte(levelName))
	packet.WriteGInt5(uint64(changeTime.Unix() - 1))
	packet.WriteGShort(2)
	packet.WriteGShort(3)
	packet.WriteGShort(1)
	packet.WriteGShort(1)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTUPDATEBOARD(packet.Bytes())
		done <- struct{}{}
	}()

	want := NewBuffer()
	want.WriteByte(PLO_BOARDPACKET + 32)
	want.WriteShort(2).WriteShort(3).WriteShort(1).WriteShort(1)
	want.WriteShort(0x1234)
	want.WriteByte('\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, want.Len())
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read board update packet: %v", err)
	}
	<-done

	if string(got) != string(want.Bytes()) {
		t.Fatalf("board update packet = % X, want % X", got, want.Bytes())
	}
}

func TestAdjacentLevelSendsRequestedLevelThenRestoresCurrentLevel(t *testing.T) {
	current := NewLevel()
	current.levelName = "onlinestartlocal"
	adjacent := NewLevel()
	adjacent.levelName = "side"
	adjacent.modTime = time.Unix(1712345680, 0)
	adjacent.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}

	server := &Server{
		logger:     NewLogger("", false),
		levels:     map[string]*Level{"side": adjacent, "onlinestartlocal": current},
		settings:   NewSettings(),
		serverTime: 12345,
	}
	p := &Player{
		server:        server,
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
		currentLevel:  current,
	}
	p.levelName = "onlinestartlocal.nw"
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_ADJACENTLEVEL)
	packet.WriteGInt5(0)
	packet.Write([]byte("side.nw"))

	if !p.msgPLI_ADJACENTLEVEL(packet.Bytes()) {
		t.Fatalf("msgPLI_ADJACENTLEVEL returned false")
	}

	got := p.outQueue
	if !bytes.HasPrefix(got, append([]byte{PLO_LEVELNAME + 32}, []byte("side.nw\n")...)) {
		t.Fatalf("adjacent response starts with % X, want adjacent level name", got[:min(len(got), 16)])
	}
	if !bytes.Contains(got, []byte{PLO_RAWDATA + 32}) {
		t.Fatalf("adjacent response did not include requested board rawdata: % X", got[:min(len(got), 32)])
	}
	if !bytes.HasSuffix(got, append([]byte{PLO_LEVELNAME + 32}, []byte("onlinestartlocal.nw\n")...)) {
		t.Fatalf("adjacent response did not restore current level name, tail=% X", got[max(0, len(got)-32):])
	}
}

func TestHitObjectsForwardsSourcePlayerAndHitLocation(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
	}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := NewBuffer()
	packet.WriteByte(PLI_HITOBJECTS)
	packet.WriteGChar(6)
	packet.WriteGChar(8)
	packet.WriteGChar(10)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_HITOBJECTS(packet.Bytes())
		done <- struct{}{}
	}()

	want := []byte{PLO_HITOBJECTS + 32, 32, 33, 38, 40, 42, '\n'}
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read hitobjects packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("hitobjects packet = % X, want % X", got, want)
	}
}

func TestNpcPropsForwardRawPropertiesToOtherLevelPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	npc := NewNPC(LEVELNPC)
	npc.id = 123
	level := NewLevel()
	level.players = []uint16{1, 2}
	level.npcs[123] = npc
	server := &Server{
		logger:   NewLogger("", false),
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal": level},
		settings: NewSettings(),
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
	}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	props := NewBuffer()
	props.WriteGChar(NPCPROP_X).WriteGChar(20)
	props.WriteGChar(NPCPROP_Y).WriteGChar(24)
	packet := NewBuffer()
	packet.WriteByte(PLI_NPCPROPS)
	packet.WriteGInt(123)
	packet.Write(props.Bytes())

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_NPCPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	want := NewBuffer()
	want.WriteByte(PLO_NPCPROPS + 32)
	want.WriteGInt(123)
	want.Write(props.Bytes())
	want.WriteByte('\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, want.Len())
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read npcprops packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("npcprops packet = % X, want % X", got, want.Bytes())
	}
}

func TestBombAddForwardsPlayerIdAndRawBombPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 1, server: server, currentLevel: level}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := []byte{PLI_BOMBADD, 64, 65, 3, 55}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_BOMBADD(packet)
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(1)
	want := append([]byte{PLO_BOMBADD + 32}, idBuf.Bytes()...)
	want = append(want, packet[1:]...)
	want = append(want, '\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read bomb add packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("bomb add packet = % X, want % X", got, want)
	}
}

func TestPositionPropsUsePixelBackedCoordinates(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false)}}
	p.setX(32)
	p.setY(32)

	x := p.getProp(PLPROP_X)
	y := p.getProp(PLPROP_Y)

	if len(x) != 1 || x[0] != 64+32 {
		t.Fatalf("PLPROP_X = % X, want encoded 64", x)
	}
	if len(y) != 1 || y[0] != 64+32 {
		t.Fatalf("PLPROP_Y = % X, want encoded 64", y)
	}
}

func TestPrecisePositionPropsUseSignedShiftedCoordinates(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false)}}
	p.x = 512
	p.y = -384
	p.z = 1600

	wantX := NewBuffer().WriteGShort(1024).Bytes()
	wantY := NewBuffer().WriteGShort(769).Bytes()
	wantZ := NewBuffer().WriteGShort(2720).Bytes()

	if got := p.getProp(PLPROP_X2); !bytes.Equal(got, wantX) {
		t.Fatalf("PLPROP_X2 = % X, want % X", got, wantX)
	}
	if got := p.getProp(PLPROP_Y2); !bytes.Equal(got, wantY) {
		t.Fatalf("PLPROP_Y2 = % X, want % X", got, wantY)
	}
	if got := p.getProp(PLPROP_Z2); !bytes.Equal(got, wantZ) {
		t.Fatalf("PLPROP_Z2 = % X, want clamped % X", got, wantZ)
	}
}

func TestPlayerPropsParsesSignedPreciseCoordinates(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_X2).WriteGShort(1024)
	packet.WriteGChar(PLPROP_Y2).WriteGShort(769)
	packet.WriteGChar(PLPROP_Z2).WriteGShort(33)

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}
	if p.x != 512 {
		t.Fatalf("x = %d, want 512", p.x)
	}
	if p.y != -384 {
		t.Fatalf("y = %d, want -384", p.y)
	}
	if p.z != -16 {
		t.Fatalf("z = %d, want -16", p.z)
	}
}

func TestPlayerPropsConsumesFullKnownPropertyStream(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_RUPEESCOUNT).WriteGInt(1234)
	packet.WriteGChar(PLPROP_APCOUNTER).WriteGShort(77)
	packet.WriteGChar(PLPROP_GATTRIB10).WriteGChar(3).Write([]byte("hat"))
	packet.WriteGChar(PLPROP_TEXTCODEPAGE).WriteGInt(1252)
	packet.WriteGChar(PLPROP_UNKNOWN81).WriteGChar(3)
	packet.WriteGChar(PLPROP_CURCHAT).WriteGChar(5).Write([]byte("hello"))
	packet.WriteGChar(PLPROP_PSTATUSMSG).WriteGChar(7)

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}
	if p.rupees != 1234 {
		t.Fatalf("rupees = %d, want 1234", p.rupees)
	}
	if p.apCounter != 77 {
		t.Fatalf("apCounter = %d, want 77", p.apCounter)
	}
	if p.gAttribs[9] != "hat" {
		t.Fatalf("gAttribs[9] = %q, want hat", p.gAttribs[9])
	}
	if p.envCodePage != 1252 {
		t.Fatalf("envCodePage = %d, want 1252", p.envCodePage)
	}
	if p.character.chatMessage != "hello" {
		t.Fatalf("chatMessage = %q, want hello", p.character.chatMessage)
	}
	if p.statusMsg != 7 {
		t.Fatalf("statusMsg = %d, want 7", p.statusMsg)
	}
	if got := p.getProp(PLPROP_PSTATUSMSG); !bytes.Equal(got, []byte{7 + 32}) {
		t.Fatalf("PSTATUSMSG prop = % X, want encoded status byte % X", got, []byte{7 + 32})
	}
}

func TestPlayerPropsParsesTypedClientPropertyStream(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_NICKNAME).WriteGChar(byte(len("moondeath"))).Write([]byte("moondeath"))
	packet.WriteGChar(PLPROP_X).WriteGChar(64)
	packet.WriteGChar(PLPROP_Y).WriteGChar(65)
	packet.WriteGChar(PLPROP_COLORS)
	for _, color := range []byte{1, 2, 3, 4, 5} {
		packet.WriteGChar(color)
	}
	packet.WriteGChar(PLPROP_SPRITE).WriteGChar(12)
	packet.WriteGChar(PLPROP_CURLEVEL).WriteGChar(byte(len("onlinestartlocal.nw"))).Write([]byte("onlinestartlocal.nw"))

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}

	if p.character.nickName != "moondeath" {
		t.Fatalf("nickname = %q, want moondeath", p.character.nickName)
	}
	if p.x != 64*8 || p.y != 65*8 {
		t.Fatalf("position = %d,%d, want %d,%d", p.x, p.y, 64*8, 65*8)
	}
	if p.character.colors != [5]uint8{1, 2, 3, 4, 5} {
		t.Fatalf("colors = %v, want [1 2 3 4 5]", p.character.colors)
	}
	if p.character.sprite != 12 {
		t.Fatalf("sprite = %d, want 12", p.character.sprite)
	}
	if p.levelName != "onlinestartlocal.nw" {
		t.Fatalf("levelName = %q, want onlinestartlocal.nw", p.levelName)
	}
}

func TestPlayerPropsForwardsChangedPropsToOtherPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		loaded:       true,
	}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:           2,
		conn:         serverConn,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    222,
		encryption:   *NewEncryption(),
	}
	other.levelName = "onlinestartlocal.nw"
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_GANI).WriteGChar(4).Write([]byte("walk"))
	packet.WriteGChar(PLPROP_CURCHAT).WriteGChar(2).Write([]byte("hi"))
	packet.WriteGChar(PLPROP_PSTATUSMSG).WriteGChar(7)
	packet.WriteGChar(PLPROP_X).WriteGChar(64)
	packet.WriteGChar(PLPROP_Y).WriteGChar(65)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PLAYERPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	id := NewBuffer()
	id.WriteGShort(p.id)
	want := append([]byte{PLO_OTHERPLPROPS + 32}, id.Bytes()...)
	want = append(want, PLPROP_GANI+32, 4+32)
	want = append(want, []byte("walk")...)
	want = append(want, PLPROP_CURCHAT+32, 2+32)
	want = append(want, []byte("hi")...)
	want = append(want, PLPROP_PSTATUSMSG+32, 7+32)
	want = append(want, PLPROP_X+32, 64+32, PLPROP_Y+32, 65+32)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read other player prop delta: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("other player prop delta = % X, want % X", got, want)
	}
}

func TestPlayerPropsForwardsCarrySpriteAsRawByte(t *testing.T) {
	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		loaded:       true,
	}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:            2,
		server:        server,
		currentLevel:  level,
		playerType:    PLTYPE_CLIENT3,
		queueOutgoing: true,
	}
	other.conn, _ = net.Pipe()
	defer other.conn.Close()
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_CARRYSPRITE)
	packet.WriteByte(0x02)

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}
	if p.carrySprite != 0x02 {
		t.Fatalf("carrySprite = 0x%02X, want 02", p.carrySprite)
	}

	id := NewBuffer()
	id.WriteGShort(p.id)
	want := append([]byte{PLO_OTHERPLPROPS + 32}, id.Bytes()...)
	want = append(want, PLPROP_CARRYSPRITE+32, 0x02, '\n')
	if !bytes.Contains(other.outQueue, want) {
		t.Fatalf("observer carry sprite packet missing % X in % X", want, other.outQueue)
	}
	if bytes.Contains(other.outQueue, []byte{PLPROP_CARRYSPRITE + 32, 0x22}) {
		t.Fatalf("carry sprite was GChar encoded in % X", other.outQueue)
	}
}

func TestPlayerPropsForwardPreciseMovementToModernPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    300,
		loaded:       true,
	}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:           2,
		conn:         serverConn,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    300,
		encryption:   *NewEncryption(),
	}
	other.levelName = "onlinestartlocal.nw"
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_GANI).WriteGChar(4).Write([]byte("walk"))
	packet.WriteGChar(PLPROP_X2).WriteGShort(64 * 8 * 2)
	packet.WriteGChar(PLPROP_Y2).WriteGShort(65 * 8 * 2)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PLAYERPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	id := NewBuffer()
	id.WriteGShort(p.id)
	want := append([]byte{PLO_OTHERPLPROPS + 32}, id.Bytes()...)
	want = append(want, PLPROP_GANI+32, 4+32)
	want = append(want, []byte("walk")...)
	wantX2 := NewBuffer()
	wantX2.WriteGShort(64 * 8 * 2)
	wantY2 := NewBuffer()
	wantY2.WriteGShort(65 * 8 * 2)
	want = append(want, PLPROP_X2+32)
	want = append(want, wantX2.Bytes()...)
	want = append(want, PLPROP_Y2+32)
	want = append(want, wantY2.Bytes()...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read modern player prop delta: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("modern player prop delta = % X, want % X", got, want)
	}
}

func TestTriggerActionParsesNpcPositionAndRawAction(t *testing.T) {
	called := false
	server := &Server{logger: NewLogger("", false)}
	server.triggerCommands = map[string]func(*Player, []string) bool{
		"clientweapon": func(p *Player, args []string) bool {
			called = true
			command := args[0]
			if command != "clientweapon" {
				t.Fatalf("command = %q, want clientweapon", command)
			}
			if len(args) != 3 || args[1] != "-System" || args[2] != "loaded" {
				t.Fatalf("args = %#v, want clientweapon,-System,loaded", args)
			}
			return true
		},
	}
	p := &Player{server: server}

	packet := NewBuffer()
	packet.WriteByte(PLI_TRIGGERACTION)
	packet.WriteGInt(0)
	packet.WriteGChar(31)
	packet.WriteGChar(32)
	packet.Write([]byte("clientweapon,-System,loaded"))

	if !p.msgPLI_TRIGGERACTION(packet.Bytes()) {
		t.Fatalf("msgPLI_TRIGGERACTION returned false")
	}
	if !called {
		t.Fatalf("trigger handler was not called")
	}
}

func TestTriggerActionForwardsUnhandledActionToLevel(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:          NewLogger("", false),
		players:         make(map[uint16]*Player),
		levels:          map[string]*Level{"onlinestartlocal": level},
		triggerCommands: make(map[string]func(*Player, []string) bool),
	}
	p := &Player{id: 1, server: server, currentLevel: level}
	p.levelName = "onlinestartlocal"
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := NewBuffer()
	packet.WriteByte(PLI_TRIGGERACTION)
	packet.WriteGInt(0)
	packet.WriteGChar(31)
	packet.WriteGChar(32)
	packet.Write([]byte("npcaction,param"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_TRIGGERACTION(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(1)
	want := append([]byte{PLO_TRIGGERACTION + 32}, idBuf.Bytes()...)
	want = append(want, packet.Bytes()[1:]...)
	want = append(want, '\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read triggeraction packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("triggeraction packet = % X, want % X", got, want)
	}
}

func TestLevelBoardChangesSendsEmptyMarker(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELBOARDCHANGES(NewLevel(), time.Time{})
		done <- struct{}{}
	}()

	want := []byte{PLO_LEVELBOARD + 32, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level board changes packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level board changes packet = % X, want % X", got, want)
	}
}

func TestLevelLinkUsesRawTextLineFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	link := &LevelLink{destLevel: "inside house.nw", x: 1, y: 2, width: 3, height: 4, destX: 30, destY: 31}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELLINK_FULL(link)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_LEVELLINK + 32}, []byte("inside house.nw 1 2 3 4 30 31\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level link packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level link packet = % X, want % X", got, want)
	}
}

func TestLevelSignUsesGCharCoordinatesAndEncodedText(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	sign := NewLevelSign(7, 8, "Hi", false)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_SIGN(sign)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_LEVELSIGN + 32}, sign.GetSignStr(p)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level sign packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level sign packet = % X, want % X", got, want)
	}
}

func TestFireSpyAndThrowCarriedForwardRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	server := &Server{logger: NewLogger("", false), players: make(map[uint16]*Player)}
	p := &Player{
		server:       server,
		currentLevel: level,
		id:           12,
	}
	other := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         13,
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.addPlayer(p)
	level.addPlayer(other)

	fireBody := []byte{0x21, 0x22, 0x23}
	throwBody := []byte{0x24, 0x25}

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_FIRESPY(append([]byte{PLI_FIRESPY}, fireBody...))
		p.msgPLI_THROWCARRIED(append([]byte{PLI_THROWCARRIED}, throwBody...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(12)
	want := append([]byte{PLO_FIRESPY + 32}, idBuf.Bytes()...)
	want = append(want, fireBody...)
	want = append(want, '\n', PLO_THROWCARRIED+32)
	want = append(want, idBuf.Bytes()...)
	want = append(want, throwBody...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read raw forward packets: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("raw forward packets = % X, want % X", got, want)
	}
}

func TestSetActiveLevelUsesRawLevelName(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_SETACTIVELEVEL("onlinestartlocal.nw")
		done <- struct{}{}
	}()

	want := append([]byte{PLO_SETACTIVELEVEL + 32}, []byte("onlinestartlocal.nw\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read set active level packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("set active level packet = % X, want % X", got, want)
	}
}

func TestLevelHorseAddUsesLevelWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	horse := LevelHorse{image: "horse.png", x: 12.5, y: 7, dir: 2, bushes: 1}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELHORSEADD(horse)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_HORSEADD + 32, 25, 14, 6}, []byte("horse.png\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level horse packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level horse packet = % X, want % X", got, want)
	}
}

func TestLevelBaddyPropsUseLevelWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		versionId:  222,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	level := NewLevel()
	baddy := NewLevelBaddy(4, 5, 1, level, nil)
	baddy.id = 7
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELBADDYPROPS(baddy)
		done <- struct{}{}
	}()

	want := []byte{PLO_BADDYPROPS + 32, 7 + 32}
	want = append(want, baddy.getProps(222)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level baddy packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level baddy packet = % X, want % X", got, want)
	}
}

func TestLevelChestUsesGCharFields(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	chest := &LevelChest{x: 20, y: 24, itemType: ItemGreenRupee, signIndex: 0}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELCHEST(chest, false)
		done <- struct{}{}
	}()

	want := []byte{PLO_LEVELCHEST + 32, 0 + 32, 20 + 32, 24 + 32, byte(ItemGreenRupee) + 32, 0 + 32, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level chest packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level chest packet = % X, want % X", got, want)
	}
}

func TestNpcPropsUseTypedPropertyStream(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	npc := NewNPC(PUTNPC)
	npc.id = 3
	npc.x = 16 * 16
	npc.y = 17 * 16
	npc.image = "block.png"
	npc.script = "message hi;"
	npc.npcName = "guide"
	npc.character.gani = "idle"

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NPCPROPS(npc)
		done <- struct{}{}
	}()

	npcID := NewBuffer()
	npcID.WriteGInt(3)
	scriptLen := NewBuffer()
	scriptLen.WriteGShort(uint16(len(npc.script)))
	want := append([]byte{PLO_NPCPROPS + 32}, npcID.Bytes()...)
	want = append(want, NPCPROP_X+32, byte(npc.x/8)+32, NPCPROP_Y+32, byte(npc.y/8)+32)
	want = append(want, NPCPROP_IMAGE+32, byte(len(npc.image))+32)
	want = append(want, []byte(npc.image)...)
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLen.Bytes()...)
	want = append(want, []byte(npc.script)...)
	want = append(want, NPCPROP_NICKNAME+32, byte(len(npc.npcName))+32)
	want = append(want, []byte(npc.npcName)...)
	want = append(want, NPCPROP_GANI+32, byte(len(npc.character.gani))+32)
	want = append(want, []byte(npc.character.gani)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read npc props packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("npc props packet = % X, want % X", got, want)
	}
}

func TestShowImgForwardsRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		server:       server,
		currentLevel: level,
		encryption:   *NewEncryption(),
		id:           7,
	}
	other := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         8,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.addPlayer(p)
	level.addPlayer(other)

	body := []byte{0x21, 0x22, 'i', 'm', 'g'}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOWIMG(append([]byte{PLI_SHOWIMG}, body...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(7)
	want := append([]byte{PLO_SHOWIMG + 32}, idBuf.Bytes()...)
	want = append(want, body...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read showimg packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("showimg packet = % X, want % X", got, want)
	}
}

func TestToAllUsesPlayerIdAndGCharLengthMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), players: map[uint16]*Player{}},
		encryption: *NewEncryption(),
		id:         5,
	}
	p.server.players[5] = p
	p.encryption.SetGen(ENCRYPT_GEN_1)
	p.levelName = "onlinestartlocal.nw"

	packet := NewBuffer()
	packet.WriteByte(PLI_TOALL)
	packet.WriteGChar(byte(len("hello")))
	packet.Write([]byte("hello"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_TOALL(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(5)
	want := append([]byte{PLO_TOALL + 32}, idBuf.Bytes()...)
	want = append(want, byte(len("hello"))+32)
	want = append(want, []byte("hello\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read toall packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("toall packet = % X, want % X", got, want)
	}
}

func TestPrivateMessageUsesTargetIdsAndRawMessage(t *testing.T) {
	senderConn, targetConn := net.Pipe()
	defer senderConn.Close()
	defer targetConn.Close()

	p := &Player{
		conn:       senderConn,
		server:     &Server{logger: NewLogger("", false), players: map[uint16]*Player{}},
		encryption: *NewEncryption(),
		id:         5,
	}
	target := &Player{
		conn:       targetConn,
		server:     p.server,
		encryption: *NewEncryption(),
		id:         6,
	}
	p.server.players[5] = p
	p.server.players[6] = target
	target.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_PRIVATEMESSAGE)
	packet.WriteGShort(1)
	packet.WriteGShort(6)
	packet.Write([]byte("\"hi\""))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PRIVATEMESSAGE(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(5)
	want := append([]byte{PLO_PRIVATEMESSAGE + 32}, idBuf.Bytes()...)
	want = append(want, []byte("\"\",\"Private message:\",\"hi\"\n")...)

	senderConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(senderConn, got); err != nil {
		t.Fatalf("read private message packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("private message packet = % X, want % X", got, want)
	}
}

func TestWeaponAddAndDeleteMutateWeaponList(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false), npcs: map[uint32]*NPC{}}}

	addPacket := NewBuffer()
	addPacket.WriteByte(PLI_WEAPONADD)
	addPacket.WriteGChar(0)
	addPacket.WriteGChar(byte(ItemSpinattack))
	if !p.msgPLI_WEAPONADD(addPacket.Bytes()) {
		t.Fatalf("msgPLI_WEAPONADD returned false")
	}
	if len(p.weaponList) != 1 || p.weaponList[0] != getItemName(ItemSpinattack) {
		t.Fatalf("weaponList after add = %#v", p.weaponList)
	}

	if !p.msgPLI_NPCWEAPONDEL(append([]byte{PLI_NPCWEAPONDEL}, []byte(getItemName(ItemSpinattack))...)) {
		t.Fatalf("msgPLI_NPCWEAPONDEL returned false")
	}
	if len(p.weaponList) != 0 {
		t.Fatalf("weaponList after delete = %#v, want empty", p.weaponList)
	}
}

func TestPrivateMessageToNPCServerGetsDefaultReply(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         5,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)
	npc := &Player{
		server:     server,
		playerType: PLTYPE_NPCSERVER,
		id:         1,
		loaded:     true,
	}
	npc.accountName = "(npcserver)"
	npc.character.nickName = "NPC-Server (Server)"
	server.players[p.id] = p
	server.players[npc.id] = npc

	packet := NewBuffer()
	packet.WriteByte(PLI_PRIVATEMESSAGE)
	packet.WriteGShort(1)
	packet.WriteGShort(1)
	packet.Write([]byte("\"hello\""))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PRIVATEMESSAGE(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(1)
	want := append([]byte{PLO_PRIVATEMESSAGE + 32}, idBuf.Bytes()...)
	want = append(want, []byte("\"\",")...)
	want = append(want, []byte(gtokenizeText("I am the npcserver for\nthis game server. Almost\nall npc actions are controlled\nby me."))...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read npcserver pm reply packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("npcserver pm reply packet = % X, want % X", got, want)
	}
}

func TestExplosionAddsPlayerIdAndTypedPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		id:         9,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_EXPLOSION)
	packet.WriteGChar(3)
	packet.WriteGChar(40)
	packet.WriteGChar(41)
	packet.WriteGChar(2)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_EXPLOSION(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(9)
	want := append([]byte{PLO_EXPLOSION + 32}, idBuf.Bytes()...)
	want = append(want, 3+32, 40+32, 41+32, 2+32, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read explosion packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("explosion packet = % X, want % X", got, want)
	}
}

func TestClientVersionIDUsesSemanticVersion(t *testing.T) {
	if got := clientVersionID("GNW03014"); got != 222 {
		t.Fatalf("clientVersionID(GNW03014) = %d, want 222", got)
	}
	if got := clientVersionID("G3D03014"); got != 300 {
		t.Fatalf("clientVersionID(G3D03014) = %d, want 300", got)
	}
}

func TestNPCWeaponDelUsesRawNameWithoutNul(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NPCWEAPONDEL("Bomb")
		done <- struct{}{}
	}()

	want := []byte{PLO_NPCWEAPONDEL + 32, 'B', 'o', 'm', 'b', '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read weapon delete packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("weapon delete packet = % X, want % X", got, want)
	}
}

func TestZlibFixWeaponMatchesLegacyClientPacketShape(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_ZLIBFIXWEAPON()
		done <- struct{}{}
	}()

	want := []byte{PLO_NPCWEAPONADD + 32, 12 + 32}
	want = append(want, []byte("-gr_zlib_fix")...)
	want = append(want, NPCPROP_IMAGE+32, 1+32, '-')
	scriptLen := len(zlibFixScript)
	scriptLenBuf := NewBuffer()
	scriptLenBuf.WriteGShort(uint16(scriptLen))
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLenBuf.Bytes()...)
	want = append(want, []byte(zlibFixScript)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read zlib fix weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("zlib fix weapon packet = % X, want % X", got, want)
	}
}

func TestSendWeaponUsesNpcWeaponPropertyStream(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	weapon := &Weapon{name: "sword", image: "sword.png", script: "function onCreated() {}"}
	done := make(chan struct{}, 1)
	go func() {
		p.sendWeapon(weapon)
		done <- struct{}{}
	}()

	scriptLen := NewBuffer()
	scriptLen.WriteGShort(uint16(len(weapon.script)))
	want := []byte{PLO_NPCWEAPONADD + 32, byte(len(weapon.name)) + 32}
	want = append(want, []byte(weapon.name)...)
	want = append(want, NPCPROP_IMAGE+32, byte(len(weapon.image))+32)
	want = append(want, []byte(weapon.image)...)
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLen.Bytes()...)
	want = append(want, []byte(weapon.script)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("weapon packet = % X, want % X", got, want)
	}
}

func TestSendAccountWeaponUsesDefaultWeaponPacketForBombAndBow(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), weapons: make(map[string]*Weapon)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendAccountWeapon("bomb")
		p.sendAccountWeapon("bow")
		done <- struct{}{}
	}()

	want := []byte{PLO_DEFAULTWEAPON + 32, byte(ItemBomb) + 32, '\n'}
	want = append(want, PLO_DEFAULTWEAPON+32, byte(ItemBow)+32, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read default weapon packets: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("default weapon packets = % X, want % X", got, want)
	}
}

func TestSendAccountWeaponRespectsDefaultWeaponsServerOption(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings(), weapons: make(map[string]*Weapon)},
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.server.settings.Set("defaultweapons", "false")
	p.encryption.SetGen(ENCRYPT_GEN_1)

	if p.sendAccountWeapon("bomb") {
		t.Fatalf("sendAccountWeapon returned true with defaultweapons=false")
	}
	if len(p.outQueue) != 0 {
		t.Fatalf("default weapon packet was sent with defaultweapons=false: % X", p.outQueue)
	}
}

func TestMissingDefaultWeaponDeletesAlwaysRemoveClientAutoDefaults(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings()},
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.weaponList = []string{"bomb", "bow"}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	p.sendMissingDefaultWeaponDeletes()

	bombDel := []byte{PLO_NPCWEAPONDEL + 32, 'B', 'o', 'm', 'b', '\n'}
	bowDel := []byte{PLO_NPCWEAPONDEL + 32, 'B', 'o', 'w', '\n'}
	if !bytes.Contains(p.outQueue, bombDel) || !bytes.Contains(p.outQueue, bowDel) {
		t.Fatalf("missing client auto-default deletes: % X", p.outQueue)
	}
}

func TestMissingDefaultWeaponDeletesRemoveAbsentBombAndBow(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings()},
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	p.sendMissingDefaultWeaponDeletes()

	want := []byte{PLO_NPCWEAPONDEL + 32, 'B', 'o', 'm', 'b', '\n'}
	want = append(want, PLO_NPCWEAPONDEL+32, 'B', 'o', 'w', '\n')
	if !bytes.Equal(p.outQueue, want) {
		t.Fatalf("default weapon delete packets = % X, want % X", p.outQueue, want)
	}
}

func TestSendAccountWeaponFallsBackToScriptWeapon(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	weapon := &Weapon{name: "custom", image: "custom.png"}
	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), weapons: map[string]*Weapon{"custom": weapon}},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendAccountWeapon("custom")
		done <- struct{}{}
	}()

	want := []byte{PLO_NPCWEAPONADD + 32, byte(len(weapon.name)) + 32}
	want = append(want, []byte(weapon.name)...)
	want = append(want, NPCPROP_IMAGE+32, byte(len(weapon.image))+32)
	want = append(want, []byte(weapon.image)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read script weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("script weapon packet = % X, want % X", got, want)
	}
}

func TestLevelModTimeUsesGInt5WireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	modTime := int64(1712345678)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELMODTIME(modTime)
		done <- struct{}{}
	}()

	expectedTime := NewBuffer()
	expectedTime.WriteGInt5(uint64(modTime))
	want := append([]byte{PLO_LEVELMODTIME + 32}, expectedTime.Bytes()...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level modtime packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level modtime packet = % X, want % X", got, want)
	}
}

func TestShootParsesCompactProjectilePacket(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	server := &Server{logger: NewLogger("", false), players: make(map[uint16]*Player)}
	p := &Player{
		server:       server,
		currentLevel: level,
		id:           14,
	}
	other := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         15,
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.addPlayer(p)
	level.addPlayer(other)

	packet := NewBuffer()
	packet.WriteByte(PLI_SHOOT)
	packet.WriteGInt(0)
	packet.WriteGChar(20)
	packet.WriteGChar(21)
	packet.WriteGChar(50)
	packet.WriteGChar(10)
	packet.WriteGChar(11)
	packet.WriteGChar(12)
	packet.WriteGChar(4)
	packet.Write([]byte("fire"))
	packet.WriteGChar(3)
	packet.Write([]byte("abc"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOOT(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(14)
	zero := NewBuffer()
	zero.WriteGInt(0)
	want := append([]byte{PLO_SHOOT + 32}, idBuf.Bytes()...)
	want = append(want, zero.Bytes()...)
	want = append(want, 20+32, 21+32, 50+32, 10+32, 11+32, 12+32, 4+32)
	want = append(want, []byte("fire")...)
	want = append(want, 3+32)
	want = append(want, []byte("abc\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read shoot packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("shoot packet = % X, want % X", got, want)
	}
}

func TestShoot2ForwardsRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	server := &Server{logger: NewLogger("", false), players: make(map[uint16]*Player)}
	p := &Player{
		server:       server,
		currentLevel: level,
		id:           15,
	}
	other := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         16,
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.addPlayer(p)
	level.addPlayer(other)

	body := []byte{0x20, 0x21, 0x22, 0x23}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOOT2(append([]byte{PLI_SHOOT2}, body...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(15)
	want := append([]byte{PLO_SHOOT2 + 32}, idBuf.Bytes()...)
	want = append(want, body...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read shoot2 packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("shoot2 packet = % X, want % X", got, want)
	}
}

func TestFileStatusPacketsUseRawFilenamePayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	fileName := "sprites/player.png"
	want := append([]byte{PLO_FILEUPTODATE + 32}, []byte(fileName)...)
	want = append(want, '\n', PLO_FILESENDFAILED+32)
	want = append(want, []byte(fileName)...)
	want = append(want, '\n')

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_FILEUPTODATE(fileName)
		p.sendPLO_FILESENDFAILED(fileName)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read file status packets: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("file status packets = % X, want % X", got, want)
	}
}

func TestSendFileWrapsLegacyFilePacketInRawData(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	fileName := "sprites/player.png"
	fileData := []byte("png-ish")
	if err := os.MkdirAll(dir+"\\sprites", 0755); err != nil {
		t.Fatalf("create sprites dir: %v", err)
	}
	if err := os.WriteFile(dir+"\\"+fileName, fileData, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	modTime := time.Unix(1712345678, 0)
	if err := os.Chtimes(dir+"\\"+fileName, modTime, modTime); err != nil {
		t.Fatalf("set test file modtime: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	embedded := NewBuffer()
	embedded.WriteGChar(PLO_FILE)
	embedded.WriteGInt5(uint64(modTime.Unix()))
	embedded.WriteGChar(byte(len(fileName)))
	embedded.Write([]byte(fileName))
	embedded.Write(fileData)
	embedded.WriteByte('\n')

	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(embedded.Len()))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, embedded.Bytes()...)

	done := make(chan struct{}, 1)
	go func() {
		p.sendFile(fileName)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read file packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("file packet = % X, want % X", got, want)
	}
}

func TestUpdateFileRequestSendsFileWhenModTimeDiffers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	fileName := "sprites/player.png"
	fileData := []byte("newer")
	if err := os.MkdirAll(dir+"\\sprites", 0755); err != nil {
		t.Fatalf("create sprites dir: %v", err)
	}
	if err := os.WriteFile(dir+"\\"+fileName, fileData, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	modTime := time.Unix(1712345678, 0)
	if err := os.Chtimes(dir+"\\"+fileName, modTime, modTime); err != nil {
		t.Fatalf("set test file modtime: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := NewBuffer()
	request.WriteByte(PLI_UPDATEFILE)
	request.WriteGInt5(uint64(modTime.Unix() - 1))
	request.Write([]byte(fileName))

	embedded := NewBuffer()
	embedded.WriteGChar(PLO_FILE)
	embedded.WriteGInt5(uint64(modTime.Unix()))
	embedded.WriteGChar(byte(len(fileName)))
	embedded.Write([]byte(fileName))
	embedded.Write(fileData)
	embedded.WriteByte('\n')
	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(embedded.Len()))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, embedded.Bytes()...)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATEFILE(request.Bytes())
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update file response: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update file response = % X, want % X", got, want)
	}
}

func TestUpdateFileDefaultClientAssetReturnsUpToDate(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	fileName := "walk.gani"
	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(t.TempDir())},
		encryption: *NewEncryption(),
		versionId:  222,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := NewBuffer()
	request.WriteByte(PLI_UPDATEFILE)
	request.WriteGInt5(0)
	request.Write([]byte(fileName))

	want := append([]byte{PLO_FILEUPTODATE + 32}, []byte(fileName)...)
	want = append(want, '\n')

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATEFILE(request.Bytes())
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read default asset update response: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("default asset update response = % X, want % X", got, want)
	}
}

func TestUpdateGaniReadsGInt5AndSendsRawSetbackPacket(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	ganiData := []byte("GANI0001\nSETBACKTO walk\n")
	if err := os.WriteFile(dir+"\\walk.gani", ganiData, 0644); err != nil {
		t.Fatalf("write gani: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_UPDATEGANI)
	packet.WriteGInt5(uint64(calculateCrc32Checksum(ganiData)))
	packet.Write([]byte("walk"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATEGANI(packet.Bytes())
		done <- struct{}{}
	}()

	want := []byte{PLO_UNKNOWN195 + 32, byte(len("walk")) + 32}
	want = append(want, []byte("walk\"SETBACKTO walk\"")...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update gani packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update gani packet = % X, want % X", got, want)
	}
}

func TestUpdateScriptWrapsBytecodeInRawData(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	bytecode := []byte{0x01, 0x02, 0x03}
	p := &Player{
		conn: serverConn,
		server: &Server{
			logger:  NewLogger("", false),
			weapons: map[string]*Weapon{"-test": {bytecode: bytecode}},
		},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_UPDATESCRIPT}, []byte("-test")...)
	payload := append([]byte{PLO_NPCWEAPONSCRIPT + 32}, bytecode...)
	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(len(payload)))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, payload...)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATESCRIPT(packet)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update script packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update script packet = % X, want % X", got, want)
	}
}

func TestRequestTextParsesAfterPacketIDAndSendsRawServerText(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := "GraalEngine\x01lister\x01subscriptions\x01"
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...))
		done <- struct{}{}
	}()

	response := "GraalEngine\x01lister\x01subscriptions\x01unlimited\x01Unlimited Subscription\x01\"\"\x01"
	want := append([]byte{PLO_SERVERTEXT + 32}, []byte(response)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read request text packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("request text packet = % X, want % X", got, want)
	}
}

func TestRequestTextRespondsLocallyWhenListserverUnavailable(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), name: "Orion-Go"},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := "GraalEngine\x01pmservers\x01all\x01"
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...))
		done <- struct{}{}
	}()

	response := "GraalEngine\x01pmservers\x01all\x01"
	want := append([]byte{PLO_SERVERTEXT + 32}, []byte(response)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read request text fallback packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("request text fallback = % X, want % X", got, want)
	}
}

func TestRequestTextForwardsListRequestWithPlayerId(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverList = sl
	p := &Player{id: 321, server: server}

	request := "GraalEngine\x01pmservers\x01all\x01"
	if !p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...)) {
		t.Fatal("msgPLI_REQUESTTEXT returned false")
	}

	var got []byte
	select {
	case got = <-sl.sendQueue:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded list request")
	}
	want := NewBuffer().
		WriteGChar(SVO_REQUESTLIST).
		WriteGShort(321).
		Write([]byte(request)).
		WriteByte('\n').
		Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("forwarded list request = % X, want % X", got, want)
	}
}

func TestServerWarpRequestsServerInfoFromListserver(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverList = sl
	p := &Player{id: 321, server: server}

	if !p.msgPLI_SERVERWARP(append([]byte{PLI_SERVERWARP}, []byte("Testbed")...)) {
		t.Fatal("msgPLI_SERVERWARP returned false")
	}

	var got []byte
	select {
	case got = <-sl.sendQueue:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for serverwarp listserver request")
	}
	want := NewBuffer().
		WriteGChar(SVO_SERVERINFO).
		WriteGShort(321).
		Write([]byte("Testbed")).
		WriteByte('\n').
		Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("serverwarp request = % X, want % X", got, want)
	}
}

func TestServerListServerInfoWarpsTargetPlayer(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		id:            321,
		server:        server,
		versionId:     222,
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	sl := &ServerList{server: server}

	serverPacket := []byte("P Testbed 127.0.0.1:14802")
	data := NewBuffer().WriteGShort(p.id).Write(serverPacket).Bytes()
	sl.handleListPacket(SVI_SERVERINFO, data)

	want := append([]byte{PLO_SERVERWARP + 32}, serverPacket...)
	want = append(want, '\n')
	if !bytes.Equal(p.outQueue, want) {
		t.Fatalf("serverwarp response = % X, want % X", p.outQueue, want)
	}
}

func TestRequestTextForwardsListRequestToAllConnectedListservers(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	first := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	second := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverLists = []*ServerList{first, second}
	server.serverList = first
	p := &Player{id: 321, server: server}

	request := "GraalEngine\x01pmservers\x01all\x01"
	if !p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...)) {
		t.Fatal("msgPLI_REQUESTTEXT returned false")
	}

	want := NewBuffer().
		WriteGChar(SVO_REQUESTLIST).
		WriteGShort(321).
		Write([]byte(request)).
		WriteByte('\n').
		Bytes()
	for name, queue := range map[string]chan []byte{"first": first.sendQueue, "second": second.sendQueue} {
		var got []byte
		select {
		case got = <-queue:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s listserver request", name)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s forwarded list request = % X, want % X", name, got, want)
		}
	}
}

func TestRequestTextForwardsTwoFieldSimpleListToListserver(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverList = sl
	p := &Player{id: 321, server: server}

	request := "lister\x01simplelist\x01"
	if !p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...)) {
		t.Fatal("msgPLI_REQUESTTEXT returned false")
	}

	forwardedText := "GraalEngine\x01lister\x01simpleserverlist\x01"
	var got []byte
	select {
	case got = <-sl.sendQueue:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded simplelist request")
	}
	want := NewBuffer().
		WriteGChar(SVO_REQUESTLIST).
		WriteGShort(321).
		Write([]byte(forwardedText)).
		WriteByte('\n').
		Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("forwarded simplelist request = % X, want % X", got, want)
	}
}

func TestRequestTextForwardsCommaStylePMServersToListserver(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 1),
	}
	server.serverList = sl
	p := &Player{id: 321, server: server}

	request := "-ServerListScreen,pmservers,\"\""
	if !p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...)) {
		t.Fatal("msgPLI_REQUESTTEXT returned false")
	}

	var got []byte
	select {
	case got = <-sl.sendQueue:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded comma pmservers request")
	}
	want := NewBuffer().
		WriteGChar(SVO_REQUESTLIST).
		WriteGShort(321).
		Write([]byte(request)).
		WriteByte('\n').
		Bytes()
	if !bytes.Equal(got, want) {
		t.Fatalf("forwarded comma pmservers request = % X, want % X", got, want)
	}
}

func TestHandlePacketAcceptsCommaRequestTextAndUpdateGani(t *testing.T) {
	server := &Server{logger: NewLogger("", false), config: NewFileSystem(t.TempDir())}
	sl := &ServerList{
		server:    server,
		connected: true,
		enabled:   true,
		codec:     ENCRYPT_GEN_1,
		sendQueue: make(chan []byte, 2),
	}
	server.serverList = sl
	p := &Player{id: 321, server: server}
	p.Account.accountName = "moondeath"

	if !p.handlePacket(append([]byte{byte(PLI_REQUESTTEXT + 32)}, []byte("-ServerListScreen,pmservers,\"\"")...)) {
		t.Fatal("handlePacket returned false for comma requestText")
	}
	updateGani := append([]byte{byte(PLI_UPDATEGANI + 32), 0x20, 0x20, 0x20, 0x20, 0x20}, []byte("walk")...)
	if !p.handlePacket(updateGani) {
		t.Fatal("handlePacket returned false for updateGANI")
	}
	if p.invalidPackets != 0 {
		t.Fatalf("invalidPackets = %d, want 0", p.invalidPackets)
	}
}

func TestSparsePLIPacketConstantsMatchProtocolIDs(t *testing.T) {
	tests := map[string]int{
		"PLI_RC_FILEBROWSER_MOVE":      PLI_RC_FILEBROWSER_MOVE,
		"PLI_RC_FILEBROWSER_DELETE":    PLI_RC_FILEBROWSER_DELETE,
		"PLI_RC_FILEBROWSER_RENAME":    PLI_RC_FILEBROWSER_RENAME,
		"PLI_NC_LISTNPCS":              PLI_NC_LISTNPCS,
		"PLI_NC_NPCGET":                PLI_NC_NPCGET,
		"PLI_REQUESTUPDATEBOARD":       PLI_REQUESTUPDATEBOARD,
		"PLI_NC_LEVELLISTGET":          PLI_NC_LEVELLISTGET,
		"PLI_NC_LEVELLISTSET":          PLI_NC_LEVELLISTSET,
		"PLI_REQUESTTEXT":              PLI_REQUESTTEXT,
		"PLI_SENDTEXT":                 PLI_SENDTEXT,
		"PLI_RC_LARGEFILESTART":        PLI_RC_LARGEFILESTART,
		"PLI_RC_LARGEFILEEND":          PLI_RC_LARGEFILEEND,
		"PLI_UPDATEGANI":               PLI_UPDATEGANI,
		"PLI_UPDATESCRIPT":             PLI_UPDATESCRIPT,
		"PLI_UPDATEPACKAGEREQUESTFILE": PLI_UPDATEPACKAGEREQUESTFILE,
		"PLI_RC_FOLDERDELETE":          PLI_RC_FOLDERDELETE,
		"PLI_UPDATECLASS":              PLI_UPDATECLASS,
		"PLI_RC_UNKNOWN162":            PLI_RC_UNKNOWN162,
	}
	want := map[string]int{
		"PLI_RC_FILEBROWSER_MOVE":      96,
		"PLI_RC_FILEBROWSER_DELETE":    97,
		"PLI_RC_FILEBROWSER_RENAME":    98,
		"PLI_NC_LISTNPCS":              100,
		"PLI_NC_NPCGET":                103,
		"PLI_REQUESTUPDATEBOARD":       130,
		"PLI_NC_LEVELLISTGET":          150,
		"PLI_NC_LEVELLISTSET":          151,
		"PLI_REQUESTTEXT":              152,
		"PLI_SENDTEXT":                 154,
		"PLI_RC_LARGEFILESTART":        155,
		"PLI_RC_LARGEFILEEND":          156,
		"PLI_UPDATEGANI":               157,
		"PLI_UPDATESCRIPT":             158,
		"PLI_UPDATEPACKAGEREQUESTFILE": 159,
		"PLI_RC_FOLDERDELETE":          160,
		"PLI_UPDATECLASS":              161,
		"PLI_RC_UNKNOWN162":            162,
	}
	for name, got := range tests {
		if got != want[name] {
			t.Fatalf("%s = %d, want %d", name, got, want[name])
		}
	}
}

func TestUpdatePackageRequestSkipsPacketId(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages"), 0755); err != nil {
		t.Fatalf("mkdir packages: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "packages", "basepackage.gupd"), []byte("body.png 8\n"), 0644); err != nil {
		t.Fatalf("write package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "body.png"), []byte("bodydata"), 0644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	p := &Player{
		server:        &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption:    *NewEncryption(),
		queueOutgoing: true,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_UPDATEPACKAGEREQUESTFILE)
	packet.WriteGByte(byte(len("basepackage")))
	packet.Write([]byte("basepackage"))
	packet.WriteGByte(1)
	packet.WriteString("")

	p.msgPLI_UPDATEPACKAGEREQUESTFILE(packet.Bytes())

	want := NewBuffer()
	want.WriteByte(PLO_UPDATEPACKAGESIZE + 32)
	want.WriteGByte(byte(len("basepackage")))
	want.Write([]byte("basepackage"))
	want.WriteInt64(int64(len("bodydata")))
	want.WriteByte('\n')

	got := p.outQueue[:want.Len()]
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("update package size = % X, want % X", got, want.Bytes())
	}
}

func TestServerListRequestTextRelaysToPlayer(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		id:         321,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	sl := &ServerList{server: server}

	message := "GraalEngine\x01pmservers\x01all\x01server-data\x01"
	data := NewBuffer().WriteGShort(p.id).Write([]byte(message)).Bytes()
	done := make(chan struct{}, 1)
	go func() {
		sl.handleListPacket(SVI_REQUESTTEXT, data)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_SERVERTEXT + 32}, []byte(message)...)
	want = append(want, '\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read relayed server text: %v", err)
	}
	<-done
	if !bytes.Equal(got, want) {
		t.Fatalf("relayed server text = % X, want % X", got, want)
	}
}

func TestNewWorldTimeUsesGInt4WireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	worldTime := uint(0x123456)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NEWWORLDTIME(worldTime)
		done <- struct{}{}
	}()

	want := []byte{PLO_NEWWORLDTIME + 32, 0x20, 0x68, 0x88, 0x76, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read new world time packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("new world time packet = % X, want % X", got, want)
	}
}

func TestGInt4MatchesCStringWireFormat(t *testing.T) {
	buf := NewBuffer()
	buf.WriteGInt4(0xAB4)

	want := []byte{0x20, 0x20, 0x35, 0x54}
	if string(buf.Bytes()) != string(want) {
		t.Fatalf("GInt4 bytes = % X, want % X", buf.Bytes(), want)
	}

	got := NewBufferFromBytes(want).ReadGInt4()
	if got != 0xAB4 {
		t.Fatalf("ReadGInt4 = %X, want AB4", got)
	}
}

func TestGIntReadersRoundTripWriterEncoding(t *testing.T) {
	shortBuf := NewBuffer()
	shortBuf.WriteGShort(0x1234)
	if got := NewBufferFromBytes(shortBuf.Bytes()).ReadGShort(); got != 0x1234 {
		t.Fatalf("ReadGShort = %X, want 1234", got)
	}

	intBuf := NewBuffer()
	intBuf.WriteGInt(0x34567)
	if got := NewBufferFromBytes(intBuf.Bytes()).ReadGInt(); got != 0x34567 {
		t.Fatalf("ReadGInt = %X, want 34567", got)
	}

	int5Buf := NewBuffer()
	int5Buf.WriteGInt5(0x12345678)
	if got := NewBufferFromBytes(int5Buf.Bytes()).ReadGInt5(); got != 0x12345678 {
		t.Fatalf("ReadGInt5 = %X, want 12345678", got)
	}
}

func TestGhostIconUsesSingleBytePayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_GHOSTICON(false)
		done <- struct{}{}
	}()

	want := []byte{PLO_GHOSTICON + 32, 0, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read ghost icon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("ghost icon packet = % X, want % X", got, want)
	}
}

func TestDefaultWeaponUsesSingleGCharPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_DEFAULTWEAPON(7)
		done <- struct{}{}
	}()

	want := []byte{PLO_DEFAULTWEAPON + 32, 7 + 32, '\n'}
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read default weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("default weapon packet = % X, want % X", got, want)
	}
}

func TestMapPacketsUseRawCommaTextPayload(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings()},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
	}
	p.server.settings.Set("bigmap", "worldmap.txt, worldmap.png, 30, 40")
	p.server.settings.Set("minimap", "mini.txt, mini.png, 5, 6")
	p.sendPLO_BIGMAP()
	p.sendPLO_MINIMAP()

	want := append([]byte{PLO_BIGMAP + 32}, []byte("worldmap.txt,worldmap.png,30,40\n")...)
	want = append(want, PLO_MINIMAP+32)
	want = append(want, []byte("mini.txt,mini.png,5,6\n")...)

	if string(p.outQueue) != string(want) {
		t.Fatalf("map packets = % X, want % X", p.outQueue, want)
	}
}

func TestRpgWindowUsesCStringCompatibleTextPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	message := "\"Welcome to Orion.\",\"Go Code GServer.\""
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_RPGWINDOW(message)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_RPGWINDOW + 32}, []byte(message)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read rpg window packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("rpg window packet = % X, want % X", got, want)
	}
}

func TestStartMessageUsesRawConfiguredMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	message := "<html><body>Welcome</body></html>"
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_STARTMESSAGE(message)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_STARTMESSAGE + 32}, []byte(message)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read start message packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("start message packet = % X, want % X", got, want)
	}
}
