package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var DEBUG_MODE bool = false
var PACKET_DEBUG_MODE bool = false

const zlibFixScript = "//#CLIENTSIDE\xa7" +
	"if(playerchats) {\xa7" +
	"  this.chr = {ascii(#e(0,1,#c)),0,0,0,0};\xa7" +
	"  for(this.c=0;this.c<strlen(#c)*(strlen(#c)>=11);this.c++) {\xa7" +
	"    this.chr[2] = ascii(#e(this.c,1,#c));\xa7" +
	"    this.chr[3] += 1*(this.chr[2]==this.chr[0]);\xa7" +
	"    if(!(this.chr[2] in {this.chr[0],this.chr[1]})) {\xa7" +
	"      if(this.chr[1]==0) {\xa7" +
	"        if(this.chr[2]!=this.chr[0]) this.chr[1]=this.chr[2];\xa7" +
	"      } else break; //[A][B][C]\xa7" +
	"    }\xa7" +
	"    this.chr[4] += 1*(this.chr[2]==this.chr[1]);\xa7" +
	"    if(this.chr[1]>0 && this.chr[3] in |2,10|) break; //[1<A<11][B]\xa7" +
	"    if(this.chr[3]>=11 && this.chr[4]>1) break; //[A>=11][B>1]\xa7" +
	"  }\xa7" +
	"  if(this.c>0 && this.c == strlen(#c)) setplayerprop #c,\xa0#c\xa0; //Pad\xa7" +
	"}\xa7"

// ============ SERVER ============
type Server struct {
	name            string
	running         bool
	config          *FileSystem
	settings        *Settings
	adminSettings   *Settings
	logger          *Logger
	socketMgr       *SocketManager
	listener        net.Listener
	players         map[uint16]*Player
	playerMu        sync.RWMutex
	playerIdGen     uint16
	allowedVersions []string
	levels          map[string]*Level
	levelMu         sync.RWMutex
	npcs            map[uint32]*NPC
	npcMu           sync.RWMutex
	npcIdGen        uint32
	weapons         map[string]*Weapon
	classes         map[string]*ScriptClass
	weaponMu        sync.RWMutex
	flags           map[string]string
	flagMu          sync.RWMutex
	serverList      *ServerList
	triggerCommands map[string]func(*Player, []string) bool
	serverMessage   string
	serverTime      uint
	startTime       time.Time
	lastTimer       time.Time
	last1mTimer     time.Time
	last5mTimer     time.Time
	shutdown        chan struct{}
	wordFilter      *WordFilter
}

func NewServer(name string) *Server {
	s := &Server{
		name: name, running: false, config: NewFileSystem("."), settings: NewSettings(), adminSettings: NewSettings(),
		logger: NewLogger("[SERVER] ", true), socketMgr: NewSocketManager(),
		players: make(map[uint16]*Player), playerIdGen: PLAYERID_INIT,
		levels: make(map[string]*Level), npcs: make(map[uint32]*NPC),
		npcIdGen: NPCID_INIT, weapons: make(map[string]*Weapon),
		classes: make(map[string]*ScriptClass), flags: make(map[string]string),
		serverMessage: "Welcome to " + name, serverTime: 0, startTime: time.Now(),
		shutdown: make(chan struct{}),
	}
	s.serverList = NewServerList(s)
	s.triggerCommands = make(map[string]func(*Player, []string) bool)
	s.initTriggerCommands()
	s.wordFilter = &WordFilter{server: s}
	return s
}

func (s *Server) Init(serverIP, serverPort, localIP, serverInterface string) error {
	s.log(":: Initializing player listen socket.\n")
	if serverIP != "" {
		s.settings.Set("serverip", serverIP)
	}
	if serverPort != "" {
		s.settings.Set("serverport", serverPort)
	}
	if localIP != "" {
		s.settings.Set("localip", localIP)
	}
	if serverInterface != "" {
		s.settings.Set("serverinterface", serverInterface)
	}
	s.loadConfigFiles()
	addr := ":14802"
	if port := s.settings.Get("serverport"); port != "" {
		addr = ":" + port
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener
	return nil
}

func (s *Server) initNPCServer() {
	if s.players == nil {
		s.players = make(map[uint16]*Player)
	}
	p := &Player{conn: nil, server: s, recvBuffer: make([]byte, 0, 8192), encryption: *NewEncryption(), playerType: PLTYPE_NPCSERVER, cachedLevels: make([]*CachedLevel, 0), rcLargeFiles: make(map[string]string), singleplayerLevels: make(map[string]*Level), channelList: make(map[string]bool), knownFiles: make(map[string]bool), externalPlayers: make(map[uint16]*Player), externalPlayerIdGen: EXTERNALPLAYERID_INIT, firstLevel: true, loaded: true, packetCount: 0, invalidPackets: 0}
	p.flagList = make(map[string]string)
	p.folderRights = *NewFilePermissions()
	p.setServer(s)
	p.accountName = "(npcserver)"
	p.id = 1
	if p.LoadAccount("(npcserver)", false) {
		p.accountName = "(npcserver)"
		p.id = 1
		p.playerType = PLTYPE_NPCSERVER
		p.loaded = true
		p.accountIp = 0
		p.accountIpStr = "0"
	}
	p.character.headImage = s.settings.Get("staffhead")
	if p.character.headImage == "" {
		p.character.headImage = "head25.png"
	}
	if p.character.nickName == "" {
		nickName := s.settings.Get("nickname")
		if nickName == "" {
			nickName = "NPC-Server"
		}
		p.character.nickName = nickName + " (Server)"
	}
	p.lastData = time.Now()
	p.lastMovement = time.Now()
	p.lastSave = time.Now()
	p.last1m = time.Now()
	p.alignment = 50
	s.playerMu.Lock()
	s.players[p.id] = p
	s.playerMu.Unlock()
	if s.serverList != nil {
		s.serverList.AddPlayer(p)
	}
	s.broadcastPlayerListEntryToClients(p)
	s.logger.Info("NPC-Server initialized (id=%d account=%s nickname=%s type=%d x=%d y=%d)", p.id, p.accountName, p.character.nickName, p.playerType, int(p.x), int(p.y))
}

func (s *Server) syncNPCServer() {
	wantNPCServer := s.settings.GetBool("serverside", false)
	var npcServer *Player
	s.playerMu.RLock()
	for _, player := range s.players {
		if player != nil && player.playerType == PLTYPE_NPCSERVER {
			npcServer = player
			break
		}
	}
	s.playerMu.RUnlock()

	if wantNPCServer {
		if npcServer == nil {
			s.initNPCServer()
		}
		return
	}
	if npcServer != nil {
		s.DeletePlayer(npcServer)
	}
}

func (s *Server) Run() error {
	s.running = true
	s.logger.Info("Server started")
	go s.acceptConnections()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.shutdown:
			s.running = false
			return nil
		case <-ticker.C:
			s.socketMgr.Update(100 * time.Millisecond)
			s.doTimedEvents()
		}
	}
}

func (s *Server) Stop() {
	close(s.shutdown)
	s.running = false
	if s.listener != nil {
		s.listener.Close()
	}
	s.socketMgr.Cleanup()
	s.logger.Info("Server stopped")
}

func (s *Server) nextPlayerId() uint16 {
	s.playerIdGen++
	if s.playerIdGen < 2 {
		s.playerIdGen = 2
	}
	return s.playerIdGen
}

func (s *Server) acceptConnections() {
	for s.running {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running {
				s.logger.Error("Accept error: %v", err)
			}
			continue
		}
		s.logger.Debug("New connection from %s", conn.RemoteAddr())
		player := NewPlayer(conn, s)
		s.socketMgr.Register(conn, player)
	}
}

func (s *Server) doTimedEvents() bool {
	now := time.Now()
	s.updateServerTime()
	if now.Sub(s.lastTimer) >= time.Second {
		s.lastTimer = now
		s.oneSecondEvents()
	}
	if now.Sub(s.last1mTimer) >= time.Minute {
		s.last1mTimer = now
		s.oneMinuteEvents()
	}
	if now.Sub(s.last5mTimer) >= 5*time.Minute {
		s.last5mTimer = now
		s.fiveMinuteEvents()
	}
	return true
}

func (s *Server) updateServerTime() { s.serverTime++ }

func (s *Server) oneSecondEvents() {
	if s.serverList != nil {
		s.serverList.doTimedEvents()
	}
	s.processLevelBoardRespawns()
	s.npcMu.Lock()
	for _, npc := range s.npcs {
		if npc.timeout > 0 {
			npc.timeout--
			if npc.timeout == 0 {
				go npc.runTimeout()
			}
		}
	}
	s.npcMu.Unlock()
	players := s.GetAllPlayers()
	for _, player := range players {
		player.processTimeout()
	}
}

func (s *Server) processLevelBoardRespawns() {
	s.levelMu.RLock()
	levels := make([]*Level, 0, len(s.levels))
	for _, level := range s.levels {
		levels = append(levels, level)
	}
	s.levelMu.RUnlock()
	for _, level := range levels {
		if level != nil {
			level.processBoardRespawns(s)
		}
	}
}

func (s *Server) oneMinuteEvents() {
	s.logger.Debug("One minute timer")
	s.savePlayerAccounts()
}

func (s *Server) fiveMinuteEvents() {
	s.logger.Info("Five minute timer - saving data")
	s.saveData()
}

func (s *Server) savePlayerAccounts() {
	for _, player := range s.GetAllPlayers() {
		if player != nil && player.shouldSavePlayerAccount() {
			player.SaveAccount()
			player.lastSave = time.Now()
		}
	}
}

func (s *Server) log(msg string) { s.logger.Write(msg) }
func (s *Server) loadSettings() {
	if err := s.settings.Load(s.config.ResolvePath("config/serveroptions.txt")); err != nil {
		s.logger.Error("Could not open config/serveroptions.txt. Will use default config.")
	}
	DEBUG_MODE = s.settings.GetBool("debugmode", false)
	PACKET_DEBUG_MODE = s.settings.GetBool("packetdebugmode", false)
	s.syncNPCServer()
}
func (s *Server) loadAdminSettings() {
	if err := s.adminSettings.Load(s.config.ResolvePath("config/adminconfig.txt")); err != nil {
		s.logger.Error("Could not open config/adminconfig.txt. Will use default config.\n")
	}
}
func (s *Server) loadAllowedVersions() {
	s.allowedVersions = s.allowedVersions[:0]
	lines, err := s.config.LoadFileAsLines("config/allowedversions.txt")
	if err != nil {
		s.logger.Error("Could not open config/allowedversions.txt. No client version list will be sent to the listserver.")
		return
	}
	for _, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.ReplaceAll(line, "\r", "")
		line = strings.ReplaceAll(line, "\t", "")
		line = strings.ReplaceAll(line, " ", "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s.allowedVersions = append(s.allowedVersions, line)
	}
}

func (s *Server) allowedVersionsListserverText() string {
	return strings.Join(s.allowedVersions, ",")
}
func (s *Server) loadFileSystem() {
	if s.settings.GetBool("nofoldersconfig", false) {
		return
	}
	lines, err := s.config.LoadFileAsLines("config/foldersconfig.txt")
	if err != nil {
		return
	}
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '/' {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		folderType := parts[0]
		config := strings.Join(parts[1:], " ")
		lastSlash := strings.LastIndex(config, "\\")
		if lastSlash == -1 {
			lastSlash = strings.LastIndex(config, "/")
		}
		if lastSlash != -1 {
			folder := "world/" + config[:lastSlash+1]
			wildcard := config[lastSlash+1:]
			s.log(fmt.Sprintf("        adding %s [%s] to %s\n", folder, wildcard, folderType))
		}
	}
}
func (s *Server) loadServerMessage() {
	if data, err := s.config.LoadFile("config/servermessage.html"); err == nil {
		s.serverMessage = string(data)
	}
}
func (s *Server) loadIPBans() {}
func (s *Server) loadWeapons(print bool) {
	files, err := s.config.ListFiles("weapons/")
	if err != nil {
		return
	}
	for _, file := range files {
		if !strings.HasPrefix(file, "weapon") || !strings.HasSuffix(file, ".txt") {
			continue
		}
		if data, err := s.config.LoadFile("weapons/" + file); err == nil {
			if weapon := parseWeapon(string(data)); weapon != nil {
				s.weapons[strings.ToLower(weapon.name)] = weapon
				if print {
					s.log("       " + weapon.name + "\n")
				}
			}
		}
	}
}

func parseWeapon(data string) *Weapon {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 || lines[0] != "GRAWP001" {
		return nil
	}
	weapon := &Weapon{}
	inScript := false
	var scriptLines []string
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if inScript {
			if line == "SCRIPTEND" {
				inScript = false
				weapon.script = strings.Join(scriptLines, "\n")
				scriptLines = nil
			} else {
				scriptLines = append(scriptLines, line)
			}
			continue
		}
		if strings.HasPrefix(line, "REALNAME ") {
			weapon.name = strings.TrimSpace(line[8:])
		} else if strings.HasPrefix(line, "IMAGE ") {
			weapon.image = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "BYTECODE ") {
			weapon.bytecodeFile = strings.TrimSpace(line[9:])
		} else if line == "SCRIPT" {
			inScript = true
		}
	}
	if weapon.name == "" {
		return nil
	}
	return weapon
}

func parseDatabaseNPC(data string) *NPC {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "GRNPC001" {
		return nil
	}
	npc := NewNPC(DBNPC)
	inScript := false
	var scriptLines []string
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if inScript {
			if trimmed == "NPCSCRIPTEND" {
				inScript = false
				npc.script = strings.Join(scriptLines, "\n")
				scriptLines = nil
			} else {
				scriptLines = append(scriptLines, line)
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if trimmed == "NPCSCRIPT" {
			inScript = true
			continue
		}
		parts := strings.SplitN(trimmed, " ", 2)
		key := strings.ToUpper(parts[0])
		value := ""
		if len(parts) == 2 {
			value = strings.TrimSpace(parts[1])
		}
		switch key {
		case "NAME":
			npc.npcName = value
		case "ID":
			if id, err := strconv.ParseUint(value, 10, 32); err == nil {
				npc.id = uint32(id)
			}
		case "TYPE":
			npc.scriptType = value
		case "SCRIPTER":
			npc.scripter = value
		case "IMAGE":
			npc.image = value
		case "STARTLEVEL":
			if value != "" {
				npc.level = &Level{levelName: value}
			}
		case "STARTX":
			if x, err := strconv.ParseFloat(value, 32); err == nil {
				npc.x = int16(x * 16)
			}
		case "STARTY":
			if y, err := strconv.ParseFloat(value, 32); err == nil {
				npc.y = int16(y * 16)
			}
		case "NICK":
			npc.character.nickName = value
		case "ANI":
			npc.character.gani = value
		case "HP":
			if hp, err := strconv.Atoi(value); err == nil {
				npc.character.hitpoints = hp
			}
		case "GRALATS":
			if gralats, err := strconv.Atoi(value); err == nil {
				npc.character.gralats = gralats
			}
		case "ARROWS":
			if arrows, err := strconv.Atoi(value); err == nil {
				npc.character.arrows = arrows
			}
		case "BOMBS":
			if bombs, err := strconv.Atoi(value); err == nil {
				npc.character.bombs = bombs
			}
		case "GLOVEP":
			if glove, err := strconv.Atoi(value); err == nil {
				npc.character.glovePower = glove
			}
		case "SWORDP":
			if sword, err := strconv.Atoi(value); err == nil {
				npc.character.swordPower = sword
			}
		case "SHIELDP":
			if shield, err := strconv.Atoi(value); err == nil {
				npc.character.shieldPower = shield
			}
		case "HEAD":
			npc.character.headImage = value
		case "BODY":
			npc.character.bodyImage = value
		case "SWORD":
			npc.character.swordImage = value
		case "SHIELD":
			npc.character.shieldImage = value
		case "HORSE":
			npc.character.horseImage = value
		case "COLORS":
			colorParts := strings.Split(value, ",")
			for i := 0; i < len(colorParts) && i < len(npc.character.colors); i++ {
				if color, err := strconv.Atoi(strings.TrimSpace(colorParts[i])); err == nil {
					npc.character.colors[i] = byte(color)
				}
			}
		case "SPRITE":
			if sprite, err := strconv.Atoi(value); err == nil {
				npc.character.sprite = byte(sprite)
			}
		case "AP":
			if ap, err := strconv.Atoi(value); err == nil {
				npc.character.ap = ap
			}
		case "TIMEOUT":
			if timeout, err := strconv.Atoi(value); err == nil {
				npc.timeout = timeout
			}
		case "SAVEARR":
			saveParts := strings.Split(value, ",")
			for i := 0; i < len(saveParts) && i < len(npc.saves); i++ {
				if save, err := strconv.Atoi(strings.TrimSpace(saveParts[i])); err == nil {
					npc.saves[i] = byte(save)
				}
			}
		}
	}
	if inScript && len(scriptLines) > 0 {
		npc.script = strings.Join(scriptLines, "\n")
	}
	if npc.npcName == "" || npc.id == 0 {
		return nil
	}
	return npc
}

func (s *Server) loadClasses(print bool) {}
func (s *Server) loadMaps(print bool)    {}
func (s *Server) loadNpcs(print bool) {
	files, err := s.config.ListFiles("npcs/")
	if err != nil {
		return
	}
	for _, file := range files {
		if !strings.HasPrefix(file, "npc") || !strings.HasSuffix(file, ".txt") {
			continue
		}
		data, err := s.config.LoadFile("npcs/" + file)
		if err != nil {
			continue
		}
		npc := parseDatabaseNPC(string(data))
		if npc == nil {
			continue
		}
		if !s.AddNPC(npc) {
			s.logger.Warning("Could not add database NPC %s (id=%d)", npc.npcName, npc.id)
			continue
		}
		if print {
			s.log("       " + npc.npcName + "\n")
		}
	}
}
func (s *Server) loadTranslations() {}
func (s *Server) loadWordFilter() {
	s.wordFilter.load("config/wordfilter.txt")
}
func (s *Server) loadConfigFiles() error {
	s.log(":: Loading server configuration...\n")
	s.log("     Loading settings...\n")
	s.loadSettings()
	s.log("     Loading admin settings...\n")
	s.loadAdminSettings()
	s.log("     Loading allowed client versions...\n")
	s.loadAllowedVersions()
	s.log("     Folder config: ")
	if s.settings.GetBool("nofoldersconfig", false) {
		s.log("disabled\n")
	} else {
		s.log("ENABLED\n")
	}
	s.log("     Loading file system...\n")
	s.loadFileSystem()
	s.log("     Loading serverflags.txt...\n")
	s.loadFlags()
	s.log("     Loading config/servermessage.html...\n")
	s.loadServerMessage()
	s.log("     Loading config/ipbans.txt...\n")
	s.loadIPBans()
	s.log("     Loading weapons...\n")
	s.loadWeapons(true)
	s.log("     Loading classes...\n")
	s.loadClasses(true)
	s.log("     Loading maps...\n")
	s.loadMaps(true)
	s.log("     Loading npcs...\n")
	s.loadNpcs(true)
	s.log("     Loading translations...\n")
	s.loadTranslations()
	s.log("     Loading word filter...\n")
	s.loadWordFilter()
	if name := s.settings.Get("name"); name != "" {
		s.name = name
	}
	s.serverList.enabled = true
	return nil
}

func (s *Server) loadFlags() {
	s.flagMu.Lock()
	defer s.flagMu.Unlock()
	lines, err := s.config.LoadFileAsLines("config/serverflags.txt")
	if err != nil {
		s.logger.Warning("Could not load serverflags.txt: %v", err)
		return
	}
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '/' {
			continue
		}
		parts := splitN(line, '=', 2)
		if len(parts) == 2 {
			name := trimSpace(parts[0])
			value := parts[1]
			if !isValidServerFlag(name, value) {
				s.logger.Warning("Skipping malformed server flag %q", line)
				continue
			}
			s.flags[name] = value
		}
	}
}

func (s *Server) saveFlags() {
	s.flagMu.RLock()
	defer s.flagMu.RUnlock()
	var lines []string
	for key, value := range s.flags {
		if !isValidServerFlag(key, value) {
			s.logger.Warning("Skipping malformed server flag while saving: %q", key)
			continue
		}
		lines = append(lines, fmt.Sprintf("%s=%s", key, value))
	}
	if err := s.config.SaveLinesAsFile("config/serverflags.txt", lines); err != nil {
		s.logger.Error("Could not save serverflags.txt: %v", err)
	}
}

func isValidServerFlag(name, value string) bool {
	if name == "" || strings.ContainsAny(name, "\x00\r\n=") || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	return true
}

func (s *Server) saveData() { s.saveFlags() }
func (s *Server) loadLevels() {
	levelsFolder := "levels/"
	entries, err := os.ReadDir(s.config.ResolvePath(levelsFolder))
	if err != nil {
		s.logger.Warning("Could not read levels folder: %v", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(strings.ToLower(entry.Name()), ".nw") || strings.HasSuffix(strings.ToLower(entry.Name()), ".zelda")) {
			levelName := levelsFolder + entry.Name()
			level := NewLevel()
			if level.loadLevel(s, levelName) {
				s.levelMu.Lock()
				s.levels[strings.TrimSuffix(entry.Name(), ".nw")] = level
				s.levelMu.Unlock()
				s.logger.Info("Loaded level: %s", entry.Name())
			}
		}
	}
}
func (s *Server) GetFlag(name string) string {
	s.flagMu.RLock()
	defer s.flagMu.RUnlock()
	return s.flags[name]
}
func (s *Server) SetFlag(name, value string) {
	s.flagMu.Lock()
	s.flags[name] = value
	s.flagMu.Unlock()
}
func (s *Server) DeleteFlag(name string) { s.flagMu.Lock(); delete(s.flags, name); s.flagMu.Unlock() }

func (s *Server) AddPlayer(player *Player, id uint16) bool {
	s.playerMu.Lock()
	defer s.playerMu.Unlock()
	if id == 0 || id == 1 {
		return false
	}
	if _, exists := s.players[id]; exists {
		return false
	}
	player.setId(id)
	s.players[id] = player
	s.logger.Info("Player %d added (account: %s)", id, player.getAccountName())
	if s.serverList != nil && isListserverPlayer(player) {
		s.serverList.AddPlayer(player)
	}
	return true
}

func (s *Server) DeletePlayer(player *Player) {
	s.playerMu.Lock()
	id := player.getId()
	_, existed := s.players[id]
	if _, exists := s.players[id]; exists {
		delete(s.players, id)
		s.logger.Info("Player %d removed", id)
	}
	remaining := make([]*Player, 0, len(s.players))
	for _, other := range s.players {
		remaining = append(remaining, other)
	}
	s.playerMu.Unlock()
	if !existed {
		return
	}
	if s.serverList != nil {
		s.serverList.DeletePlayer(player)
	}
	for _, other := range remaining {
		if other != nil && other.conn != nil && other.isLoggedIn() && other.playerType&PLTYPE_ANYCLIENT != 0 {
			other.sendPLO_DELPLAYER(id)
		}
	}
}
func (s *Server) removePlayer(player *Player) {
	s.DeletePlayer(player)
}

func (s *Server) GetPlayer(id uint16) *Player {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	return s.players[id]
}
func (s *Server) GetAllPlayers() []*Player {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	players := make([]*Player, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	return players
}
func (s *Server) GetPlayerCount() int {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	return len(s.players)
}
func (s *Server) getPlayerById(id uint16) *Player {
	return s.GetPlayer(id)
}
func (s *Server) getPlayerByAccount(accountName string, playerType int) *Player {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, p := range s.players {
		if p.accountName == accountName && (playerType == PLTYPE_ANYCLIENT || p.playerType == playerType || p.playerType == PLTYPE_ANYCLIENT) {
			return p
		}
	}
	return nil
}
func (s *Server) accountExists(accountName string) bool {
	accountPath := "accounts/" + accountName + ".txt"
	if _, err := os.Stat(accountPath); err == nil {
		return true
	}
	return false
}

func (s *Server) initTriggerCommands() {
	s.triggerCommands["gr.addweapon"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_weapons", false) {
			return true
		}
		for i := 1; i < len(args); i++ {
			p.addWeapon(strings.TrimSpace(args[i]))
		}
		return true
	}
	s.triggerCommands["gr.deleteweapon"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_weapons", false) {
			return true
		}
		for i := 1; i < len(args); i++ {
			p.deleteWeapon(strings.TrimSpace(args[i]))
		}
		return true
	}
	s.triggerCommands["gr.setgroup"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_groups", true) || len(args) != 2 {
			return true
		}
		p.setGroup(args[1])
		return true
	}
	s.triggerCommands["gr.setlevelgroup"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_groups", true) || len(args) != 2 {
			return true
		}
		if level := p.server.GetLevel(p.levelName); level != nil {
			for _, pid := range level.getPlayers() {
				if pl := p.server.GetPlayer(pid); pl != nil {
					pl.setGroup(args[1])
				}
			}
		}
		return true
	}
	s.triggerCommands["gr.setplayergroup"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_groups", true) || len(args) != 3 {
			return true
		}
		if target := p.server.getPlayerByAccount(args[1], PLTYPE_ANYCLIENT); target != nil {
			target.setGroup(args[2])
		}
		return true
	}
	s.triggerCommands["gr.rcchat"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_rc", false) {
			return true
		}
		msg := strings.Join(args[1:], ",")
		buf := NewBuffer()
		buf.WriteByte(PLO_PRIVATEMESSAGE)
		buf.WriteString8("[RC] " + p.Account.accountName + ": " + msg)
		s.sendPacketToType(PLTYPE_RC, buf.Bytes())
		return true
	}
	s.triggerCommands["gr.addguildmember"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_guilds", false) || len(args) < 3 {
			return true
		}
		guild, account, nick := args[1], args[2], ""
		if len(args) > 3 {
			nick = args[3]
		}
		if guild != "" && account != "" {
			guildPath := fmt.Sprintf("guilds/guild%s.txt", guild)
			if data, err := s.config.LoadFile(guildPath); err == nil {
				if !strings.Contains(string(data), account) {
					line := account
					if nick != "" {
						line += ":" + nick
					}
					s.config.SaveFile(guildPath, append(data, []byte("\n"+line)...))
				}
			} else {
				s.config.SaveFile(guildPath, []byte(account))
			}
		}
		return true
	}
	s.triggerCommands["gr.removeguildmember"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_guilds", false) || len(args) < 3 {
			return true
		}
		guild, account := args[1], args[2]
		if guild != "" && account != "" {
			guildPath := fmt.Sprintf("guilds/guild%s.txt", guild)
			if data, err := s.config.LoadFile(guildPath); err == nil {
				if idx := strings.Index(string(data), account); idx != -1 {
					endIdx := strings.Index(string(data[idx:]), "\n")
					if endIdx != -1 {
						endIdx += idx + 1
					}
					newData := append(data[:idx], data[endIdx+1:]...)
					s.config.SaveFile(guildPath, newData)
				}
			}
		}
		return true
	}
	s.triggerCommands["gr.removeguild"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_guilds", false) || len(args) < 2 {
			return true
		}
		guild := args[1]
		if guild != "" {
			guildPath := fmt.Sprintf("guilds/guild%s.txt", guild)
			s.config.DeleteFile(guildPath)
			for _, pl := range s.players {
				if pl.guild == guild {
					pl.guild = ""
					pl.setNickname(strings.Split(pl.character.nickName, "(")[0])
					buf := NewBuffer()
					buf.WriteByte(PLO_PLAYERPROPS)
					buf.WriteGChar(PLPROP_NICKNAME)
					buf.Write(pl.getProp(PLPROP_NICKNAME))
					pl.SendPacket(buf.Bytes())
				}
			}
		}
		return true
	}
	s.triggerCommands["gr.setguild"] = func(p *Player, args []string) bool {
		if !s.settings.GetBool("triggerhack_guilds", false) || len(args) < 2 {
			return true
		}
		guild := args[1]
		account := ""
		if len(args) > 2 {
			account = args[2]
		}
		if guild != "" {
			target := p
			if account != "" {
				target = s.getPlayerByAccount(account, PLTYPE_ANYCLIENT)
			}
			if target != nil {
				target.guild = guild
				baseNick := strings.Split(target.character.nickName, "(")[0]
				target.setNickname(baseNick + " (" + guild + ")")
				buf := NewBuffer()
				buf.WriteByte(PLO_PLAYERPROPS)
				buf.WriteGChar(PLPROP_NICKNAME)
				buf.Write(target.getProp(PLPROP_NICKNAME))
				target.SendPacket(buf.Bytes())
			}
		}
		return true
	}
}

func (s *Server) handleTriggerCommand(player *Player, command string, args []string) bool {
	if handler, exists := s.triggerCommands[command]; exists {
		return handler(player, args)
	}
	return false
}
func (s *Server) sendPacketToType(playerType int, data []byte) {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, p := range s.players {
		if p != nil && p.playerType&playerType != 0 {
			p.sendPacket(data)
		}
	}
}

func (s *Server) sendRCChat(message string) {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, p := range s.players {
		if p != nil && p.playerType&PLTYPE_ANYRC != 0 {
			p.send(NewBufferFromBytes(rcChatPacket(message)))
		}
	}
}
func (s *Server) sendPacketToAll(data []byte, excludeId uint16) {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, p := range s.players {
		if p.id != excludeId {
			p.sendPacket(data)
		}
	}
}

func (s *Server) AddNPC(npc *NPC) bool {
	s.npcMu.Lock()
	defer s.npcMu.Unlock()
	if npc.id == 0 {
		npc.setId(s.npcIdGen)
	}
	if _, exists := s.npcs[npc.id]; exists {
		return false
	}
	s.npcs[npc.id] = npc
	if npc.id >= s.npcIdGen {
		s.npcIdGen = npc.id + 1
	}
	return true
}
func (s *Server) DeleteNPC(id uint32) bool {
	s.npcMu.Lock()
	defer s.npcMu.Unlock()
	if _, exists := s.npcs[id]; exists {
		delete(s.npcs, id)
		return true
	}
	return false
}
func (s *Server) GetNPC(id uint32) *NPC { s.npcMu.RLock(); defer s.npcMu.RUnlock(); return s.npcs[id] }

func (s *Server) GetLevel(name string) *Level {
	s.levelMu.RLock()
	defer s.levelMu.RUnlock()
	return s.levels[name]
}
func (s *Server) AddLevel(level *Level) {
	s.levelMu.Lock()
	defer s.levelMu.Unlock()
	s.levels[level.getName()] = level
}
func (s *Server) DeleteLevel(name string) {
	s.levelMu.Lock()
	defer s.levelMu.Unlock()
	delete(s.levels, name)
}

func (s *Server) loadLevel(name string) *Level {
	s.levelMu.RLock()
	level, exists := s.levels[name]
	s.levelMu.RUnlock()
	if exists {
		return level
	}
	level = NewLevel()
	level.levelName = name
	level.fileName = name
	level.actualLevelName = name
	s.AddLevel(level)
	s.logger.Debug("loadLevel: Created new level '%s'", name)
	return level
}

func (s *Server) AddWeapon(weapon *Weapon) {
	s.weaponMu.Lock()
	defer s.weaponMu.Unlock()
	s.weapons[weapon.name] = weapon
}
func (s *Server) GetWeapon(name string) *Weapon {
	s.weaponMu.RLock()
	defer s.weaponMu.RUnlock()
	return s.weapons[name]
}
func (s *Server) DeleteWeapon(name string) {
	s.weaponMu.Lock()
	defer s.weaponMu.Unlock()
	delete(s.weapons, name)
}

func (s *Server) GetServerTime() uint           { return s.serverTime }
func (s *Server) GetServerStartTime() time.Time { return s.startTime }
func (s *Server) GetLogger() *Logger            { return s.logger }
func (s *Server) GetConfig() *FileSystem        { return s.config }
func (s *Server) GetSettings() *Settings        { return s.settings }

func (s *Server) SendPacketToAll(packet []byte, exclude map[uint16]bool) {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for id, player := range s.players {
		if !exclude[id] {
			player.sendPacket(packet)
		}
	}
}

func (s *Server) SendPacketToType(playerType int, packet []byte, exclude *Player) {
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, player := range s.players {
		if player.getType() == playerType && player != exclude {
			player.sendPacket(packet)
		}
	}
}

func (s *Server) broadcastBoardModify(level *Level, x, y, width, height int16, tiles []int16) {
	if level == nil {
		return
	}
	for _, plId := range level.getPlayers() {
		if pl := s.GetPlayer(plId); pl != nil && pl.conn != nil {
			pl.sendPLO_BOARDMODIFY(x, y, width, height, tiles)
		}
	}
}

func (s *Server) broadcastItemAdd(level *Level, x, y int16, itemIdx int) {
	if level == nil {
		return
	}
	for _, plId := range level.getPlayers() {
		if pl := s.GetPlayer(plId); pl != nil && pl.conn != nil {
			pl.sendPLO_ITEMADD(x, y, itemIdx, "")
		}
	}
}

// ============ PLAYER & ACCOUNT ============
type Character struct {
	nickName, bodyImage, headImage, swordImage, shieldImage, horseImage, gani, chatMessage string
	sprite                                                                                 uint8
	colors                                                                                 [5]uint8
	hitpoints, gralats, arrows, bombs, glovePower, swordPower, shieldPower                 int
	ganiAttributes                                                                         [30]string
	ap, bowPower                                                                           int
	bowImage                                                                               string
}

type Account struct {
	mu                                                                                           sync.RWMutex
	accountName, communityName, email, adminIp, banReason, banLength, accountComments, levelName string
	accountIpStr                                                                                 string
	accountIp                                                                                    uint
	isBanned, isGuest, isExternal, isLoadOnly, isStaff, loadedFromDefault                        bool
	adminRights                                                                                  int
	deviceId                                                                                     int64
	character                                                                                    Character
	language                                                                                     string
	x, y, z                                                                                      int16
	alignment                                                                                    int
	eloRating, eloDeviation                                                                      float32
	maxHitpoints, mp, apCounter, horseBombCount                                                  uint8
	kills, deaths, additionalFlags, rupees                                                       uint32
	carrySprite                                                                                  byte
	onlineTime, status, udpport                                                                  int
	lastSparTime                                                                                 time.Time
	attachNPC                                                                                    uint32
	statusMsg                                                                                    uint8
	gAttribs                                                                                     [30]string
	os                                                                                           string
	envCodePage                                                                                  int
	flagList                                                                                     map[string]string
	chestList, folderList, weaponList, privateMessageServerList                                  []string
	folderRights                                                                                 FilePermissions
	server                                                                                       *Server
	lastFolder                                                                                   string
}

func (a *Account) SetFlag(name, value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flagList == nil {
		a.flagList = make(map[string]string)
	}
	a.flagList[name] = value
}
func (a *Account) GetFlag(name string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.flagList == nil {
		return ""
	}
	return a.flagList[name]
}
func (a *Account) DeleteFlag(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flagList != nil {
		delete(a.flagList, name)
	}
}
func (a *Account) setServer(s *Server) { a.server = s }
func (a *Account) getX() float32       { return float32(a.x) / 16 }
func (a *Account) getY() float32       { return float32(a.y) / 16 }
func (a *Account) getZ() float32       { return float32(a.z) }
func (a *Account) setX(v float32)      { a.x = int16(v * 16) }
func (a *Account) setY(v float32)      { a.y = int16(v * 16) }
func (a *Account) setZ(v float32)      { a.z = int16(v) }
func (a *Account) LoadAccount(accountName string, ignoreNick bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accountName = accountName
	var filePath string
	loadedFromDefault := false
	if data, err := a.server.config.LoadFile("accounts/" + accountName + ".txt"); err == nil && len(data) > 0 {
		filePath = "accounts/" + accountName + ".txt"
	} else {
		filePath = "accounts/defaultaccount.txt"
		loadedFromDefault = true
	}
	lines, _ := a.server.config.LoadFileAsLines(filePath)
	if len(lines) == 0 || lines[0] != "GRACC001" {
		return false
	}
	a.loadedFromDefault = loadedFromDefault
	a.flagList = make(map[string]string)
	a.weaponList = []string{}
	a.chestList = []string{}
	a.folderList = []string{}
	for i := 1; i < len(lines); i++ {
		line := trimSpace(lines[i])
		if line == "" {
			continue
		}
		parts := splitN(line, ' ', 2)
		if len(parts) < 2 {
			continue
		}
		section, val := parts[0], parts[1]
		switch section {
		case "NICK":
			if !ignoreNick {
				a.character.nickName = val
			}
			if len(a.character.nickName) > 223 {
				a.character.nickName = a.character.nickName[:223]
			}
		case "COMMUNITYNAME":
			a.communityName = val
		case "LEVEL":
			a.levelName = val
		case "X":
			a.setX(parseFloat(val))
		case "Y":
			a.setY(parseFloat(val))
		case "Z":
			a.setZ(parseFloat(val))
		case "MAXHP":
			a.maxHitpoints = uint8(int(parseFloat(val)) & 0xFF)
		case "HP":
			a.character.hitpoints = int(parseFloat(val))
		case "RUPEES":
			a.character.gralats = atoi(val)
		case "ANI":
			a.character.gani = val
		case "ARROWS":
			a.character.arrows = atoi(val)
		case "BOMBS":
			a.character.bombs = atoi(val)
		case "GLOVEP":
			a.character.glovePower = atoi(val)
		case "SHIELDP":
			a.character.shieldPower = atoi(val)
		case "SWORDP":
			a.character.swordPower = atoi(val)
		case "BOWP":
			a.character.bowPower = atoi(val)
		case "BOW":
			a.character.bowImage = val
		case "HEAD":
			a.character.headImage = val
		case "BODY":
			a.character.bodyImage = val
		case "SWORD":
			a.character.swordImage = val
		case "SHIELD":
			a.character.shieldImage = val
		case "COLORS":
			colors := splitN(val, ',', 5)
			for i := 0; i < 5 && i < len(colors); i++ {
				a.character.colors[i] = uint8(atoi(colors[i]))
			}
		case "SPRITE":
			a.character.sprite = uint8(atoi(val))
		case "STATUS":
			a.status = atoi(val)
		case "MP":
			a.mp = uint8(atoi(val) & 0xFF)
		case "AP":
			a.character.ap = atoi(val)
		case "APCOUNTER":
			a.apCounter = uint8(atoi(val) & 0xFF)
		case "ONSECS":
			a.onlineTime = atoi(val)
		case "IP":
			a.accountIp = uint(atoi(val))
			a.accountIpStr = val
		case "LANGUAGE":
			a.language = val
			if a.language == "" {
				a.language = "English"
			}
		case "KILLS":
			a.kills = uint32(atoi(val))
		case "DEATHS":
			a.deaths = uint32(atoi(val))
		case "RATING":
			a.eloRating = parseFloat(val)
		case "DEVIATION":
			a.eloDeviation = parseFloat(val)
		case "FLAG":
			flagParts := splitN(val, '=', 2)
			if len(flagParts) == 2 {
				a.flagList[flagParts[0]] = flagParts[1]
			} else {
				a.flagList[val] = ""
			}
		case "WEAPON":
			a.weaponList = append(a.weaponList, val)
		case "CHEST":
			a.chestList = append(a.chestList, val)
		case "BANNED":
			a.isBanned = atoi(val) != 0
		case "BANREASON":
			a.banReason = val
		case "BANLENGTH":
			a.banLength = val
		case "COMMENTS":
			a.accountComments = val
		case "EMAIL":
			a.email = val
		case "LOCALRIGHTS":
			a.adminRights = atoi(val)
		case "IPRANGE":
			a.adminIp = val
		case "LOADONLY":
			a.isLoadOnly = atoi(val) != 0
		case "FOLDERRIGHT":
			a.folderList = append(a.folderList, val)
		case "LASTFOLDER":
			a.lastFolder = val
		default:
			if len(section) > 4 && section[:4] == "ATTR" {
				if attrNum := atoi(section[4:]); attrNum >= 1 && attrNum <= 30 {
					a.character.ganiAttributes[attrNum-1] = val
				}
			}
		}
	}
	a.normalizeHealth()
	a.isStaff = a.adminRights > 0
	if toLower(accountName) == "guest" {
		a.isLoadOnly = true
		a.isGuest = true
		a.communityName = "guest"
		a.accountName = accountName
	} else {
		a.communityName = accountName
	}
	return true
}

func (a *Account) normalizeHealth() {
	if a.maxHitpoints == 0 {
		a.maxHitpoints = 3
	}
	if a.maxHitpoints > 20 {
		a.maxHitpoints = 20
	}
	if a.character.hitpoints <= 0 {
		a.character.hitpoints = int(a.maxHitpoints)
	}
	if a.character.hitpoints > int(a.maxHitpoints) {
		a.character.hitpoints = int(a.maxHitpoints)
	}
}

func (a *Account) SaveAccount() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isLoadOnly || a.server == nil || a.server.config == nil || a.accountName == "" {
		return false
	}
	var buf strings.Builder
	buf.WriteString("GRACC001\r\n")
	fmt.Fprintf(&buf, "NAME %s\r\n", a.accountName)
	fmt.Fprintf(&buf, "NICK %s\r\n", a.character.nickName)
	fmt.Fprintf(&buf, "COMMUNITYNAME %s\r\n", a.communityName)
	fmt.Fprintf(&buf, "LEVEL %s\r\n", a.levelName)
	fmt.Fprintf(&buf, "X %.2f\r\n", a.getX())
	fmt.Fprintf(&buf, "Y %.2f\r\n", a.getY())
	fmt.Fprintf(&buf, "Z %d\r\n", a.z)
	fmt.Fprintf(&buf, "MAXHP %d\r\n", a.maxHitpoints)
	fmt.Fprintf(&buf, "HP %d\r\n", a.character.hitpoints)
	fmt.Fprintf(&buf, "RUPEES %d\r\n", a.character.gralats)
	fmt.Fprintf(&buf, "ANI %s\r\n", a.character.gani)
	fmt.Fprintf(&buf, "ARROWS %d\r\n", a.character.arrows)
	fmt.Fprintf(&buf, "BOMBS %d\r\n", a.character.bombs)
	fmt.Fprintf(&buf, "GLOVEP %d\r\n", a.character.glovePower)
	fmt.Fprintf(&buf, "SHIELDP %d\r\n", a.character.shieldPower)
	fmt.Fprintf(&buf, "SWORDP %d\r\n", a.character.swordPower)
	fmt.Fprintf(&buf, "BOWP %d\r\n", a.character.bowPower)
	fmt.Fprintf(&buf, "BOW %s\r\n", a.character.bowImage)
	fmt.Fprintf(&buf, "HEAD %s\r\n", a.character.headImage)
	fmt.Fprintf(&buf, "BODY %s\r\n", a.character.bodyImage)
	fmt.Fprintf(&buf, "SWORD %s\r\n", a.character.swordImage)
	fmt.Fprintf(&buf, "SHIELD %s\r\n", a.character.shieldImage)
	fmt.Fprintf(&buf, "COLORS %d,%d,%d,%d,%d\r\n", a.character.colors[0], a.character.colors[1], a.character.colors[2], a.character.colors[3], a.character.colors[4])
	fmt.Fprintf(&buf, "SPRITE %d\r\n", a.character.sprite)
	fmt.Fprintf(&buf, "STATUS %d\r\n", a.status)
	fmt.Fprintf(&buf, "MP %d\r\n", a.mp)
	fmt.Fprintf(&buf, "AP %d\r\n", a.character.ap)
	fmt.Fprintf(&buf, "APCOUNTER %d\r\n", a.apCounter)
	fmt.Fprintf(&buf, "ONSECS %d\r\n", a.onlineTime)
	fmt.Fprintf(&buf, "IP %d\r\n", a.accountIp)
	fmt.Fprintf(&buf, "LANGUAGE %s\r\n", a.language)
	fmt.Fprintf(&buf, "KILLS %d\r\n", a.kills)
	fmt.Fprintf(&buf, "DEATHS %d\r\n", a.deaths)
	fmt.Fprintf(&buf, "RATING %f\r\n", a.eloRating)
	fmt.Fprintf(&buf, "DEVIATION %f\r\n", a.eloDeviation)
	for i := 0; i < 30; i++ {
		if a.character.ganiAttributes[i] != "" {
			fmt.Fprintf(&buf, "ATTR%d %s\r\n", i+1, a.character.ganiAttributes[i])
		}
	}
	for _, chest := range a.chestList {
		fmt.Fprintf(&buf, "CHEST %s\r\n", chest)
	}
	for _, weapon := range a.weaponList {
		fmt.Fprintf(&buf, "WEAPON %s\r\n", weapon)
	}
	for flag, val := range a.flagList {
		if val != "" {
			fmt.Fprintf(&buf, "FLAG %s=%s\r\n", flag, val)
		} else {
			fmt.Fprintf(&buf, "FLAG %s\r\n", flag)
		}
	}
	buf.WriteString("\r\n")
	banned := 0
	if a.isBanned {
		banned = 1
	}
	fmt.Fprintf(&buf, "BANNED %d\r\n", banned)
	fmt.Fprintf(&buf, "BANREASON %s\r\n", a.banReason)
	fmt.Fprintf(&buf, "BANLENGTH %s\r\n", a.banLength)
	fmt.Fprintf(&buf, "COMMENTS %s\r\n", a.accountComments)
	fmt.Fprintf(&buf, "EMAIL %s\r\n", a.email)
	fmt.Fprintf(&buf, "LOCALRIGHTS %d\r\n", a.adminRights)
	fmt.Fprintf(&buf, "IPRANGE %s\r\n", a.adminIp)
	loadOnly := 0
	if a.isLoadOnly {
		loadOnly = 1
	}
	fmt.Fprintf(&buf, "LOADONLY %d\r\n", loadOnly)
	for _, folder := range a.folderList {
		fmt.Fprintf(&buf, "FOLDERRIGHT %s\r\n", folder)
	}
	fmt.Fprintf(&buf, "LASTFOLDER %s\r\n", a.lastFolder)
	filePath := fmt.Sprintf("accounts/%s.txt", a.accountName)
	if err := a.server.config.SaveFile(filePath, []byte(buf.String())); err != nil {
		return false
	}
	return true
}

type Player struct {
	Account
	mu                                                                        sync.Mutex
	conn                                                                      net.Conn
	server                                                                    *Server
	recvBuffer                                                                []byte
	encryptionKey                                                             byte
	encryption                                                                Encryption
	outEncryption                                                             Encryption
	queueOutgoing                                                             bool
	outQueue                                                                  []byte
	version, os, serverName                                                   string
	id                                                                        uint16
	envCodePage                                                               int
	playerType                                                                int
	versionId                                                                 int
	lastData, lastMovement, lastChat, lastNick, lastMessage, lastSave, last1m time.Time
	cachedLevels                                                              []*CachedLevel
	rcLargeFiles                                                              map[string]string
	singleplayerLevels                                                        map[string]*Level
	channelList                                                               map[string]bool
	knownFiles                                                                map[string]bool
	mapRef                                                                    *Map
	currentLevel                                                              *Level
	externalPlayers                                                           map[uint16]*Player
	externalPlayerIdGen                                                       uint16
	carryNpcId                                                                uint
	carryNpcThrown                                                            bool
	loaded                                                                    bool
	nextIsRaw                                                                 bool
	rawPacketSize                                                             int
	isFtp                                                                     bool
	grMovementUpdated                                                         bool
	disconnected                                                              bool
	firstLevel                                                                bool
	grMovementPackets                                                         string
	npcserverPort                                                             string
	packetCount, invalidPackets                                               int
	guild, levelGroup                                                         string
	grExecParameterList                                                       string
}

type CachedLevel struct {
	level   *Level
	modTime time.Time
}

func (p *Player) SendPacket(data []byte) { p.sendPacket(append(data, '\n')) }
func NewPlayer(conn net.Conn, s *Server) *Player {
	p := &Player{conn: conn, server: s, recvBuffer: make([]byte, 0, 8192), encryption: *NewEncryption(), playerType: PLTYPE_AWAIT, cachedLevels: make([]*CachedLevel, 0), rcLargeFiles: make(map[string]string), singleplayerLevels: make(map[string]*Level), channelList: make(map[string]bool), knownFiles: make(map[string]bool), externalPlayers: make(map[uint16]*Player), externalPlayerIdGen: EXTERNALPLAYERID_INIT, firstLevel: true, packetCount: 0, invalidPackets: 0}
	p.flagList = make(map[string]string)
	p.folderRights = *NewFilePermissions()
	p.setServer(s)
	p.lastData = time.Now()
	p.lastMovement = time.Now()
	p.lastSave = time.Now()
	p.last1m = time.Now()
	p.lastChat = time.Time{}
	p.lastNick = time.Time{}
	p.lastMessage = time.Time{}
	p.x = 60
	p.y = 61
	p.carrySprite = 0xff
	p.alignment = 50
	return p
}

func accountIPFromAddr(addr net.Addr) uint {
	if addr == nil {
		return 0
	}
	var ip net.IP
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		ip = tcpAddr.IP
	} else {
		host, _, err := net.SplitHostPort(addr.String())
		if err == nil {
			ip = net.ParseIP(host)
		} else {
			ip = net.ParseIP(addr.String())
		}
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return uint(ip4[0])<<24 | uint(ip4[1])<<16 | uint(ip4[2])<<8 | uint(ip4[3])
}

func (p *Player) setAccountIPFromRemoteAddr(addr net.Addr) {
	ip := accountIPFromAddr(addr)
	if ip == 0 {
		return
	}
	p.accountIp = ip
	p.accountIpStr = fmt.Sprintf("%d", ip)
}

func (p *Player) OnRegister() bool { return true }
func (p *Player) OnUnregister()    { p.disconnect() }
func (p *Player) CanRecv() bool    { return true }
func (p *Player) CanSend() bool    { return len(p.recvBuffer) > 0 }

func (p *Player) OnRecv() bool {
	p.mu.Lock()
	conn := p.conn
	disconnected := p.disconnected
	p.mu.Unlock()
	if disconnected || conn == nil {
		return false
	}
	if !p.isLoggedIn() {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	} else {
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return true
		}
		p.server.logger.Debug("OnRecv: read error from %s before loggedIn=%v: %v", conn.RemoteAddr(), p.isLoggedIn(), err)
		p.disconnect()
		return false
	}
	if n > 0 {
		p.lastData = time.Now()
		p.server.logger.PacketDebug("OnRecv: received %d bytes, buffer now %d bytes", n, len(p.recvBuffer)+n)
	}
	p.recvBuffer = append(p.recvBuffer, buf[:n]...)
	if len(p.recvBuffer) > 0 {
		p.server.logger.PacketDebug("OnRecv: buffer[0]=%02X buffer[1]=%02X", p.recvBuffer[0], p.recvBuffer[1])
	}
	p.processPackets()
	return true
}

func (p *Player) OnSend() bool { return true }

func (p *Player) processPackets() {
	for len(p.recvBuffer) >= 2 {
		length := int(p.recvBuffer[0])<<8 | int(p.recvBuffer[1])
		if len(p.recvBuffer) < length+2 {
			return
		}
		packet := p.recvBuffer[2 : length+2]
		p.recvBuffer = p.recvBuffer[length+2:]
		if p.playerType == PLTYPE_AWAIT {
			p.server.logger.Debug("Login packet: %d bytes", len(packet))
			if !p.handleLogin(packet) {
				p.server.logger.Error("handleLogin returned false")
				p.disconnect()
				return
			}
			p.server.AddPlayer(p, p.id)
			if p.playerType&PLTYPE_ANYCLIENT != 0 {
				p.sendPostLoginTail()
			} else if p.playerType&PLTYPE_ANYRC != 0 {
				p.sendRCPostLoginTail()
			} else if p.playerType&PLTYPE_ANYNC != 0 {
				p.sendNCPostLoginTail()
			}
			continue
		}
		p.handleRawData(packet)
	}
}

func loginTypeUsesEncryptionKey(clientType int) bool {
	return clientType == PLTYPE_CLIENT2 || clientType == PLTYPE_CLIENT3 || clientType == PLTYPE_RC2
}

func (p *Player) sendRCLoginPayload() {
	maxUpload := p.server.settings.GetInt("maxuploadfilesize", 20*1024*1024)
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_MAXUPLOADFILESIZE).WriteGInt5(uint64(maxUpload))
	p.send(buf)

	lines, err := p.server.config.LoadFileAsLines("config/rcmessage.txt")
	if err == nil && len(lines) > 0 {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				p.sendPLO_RC_CHAT(line)
			}
		}
		return
	}
	p.sendPLO_RC_CHAT("Welcome to " + p.server.name + " RC.")
}

func (p *Player) sendPLO_RC_CHAT(message string) bool {
	p.send(NewBufferFromBytes(rcChatPacket(message)))
	return true
}

func rcChatPacket(message string) []byte {
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_CHAT).Write([]byte(message))
	return buf.Bytes()
}

func gtokenizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return ""
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	lines := strings.Split(text, "\n")
	tokens := make([]string, 0, len(lines))
	for _, line := range lines[:len(lines)-1] {
		line = strings.ReplaceAll(line, "\r", "")
		complex := strings.TrimSpace(line) == ""
		for i := 0; i < len(line) && !complex; i++ {
			if line[i] < 33 || line[i] > 126 || line[i] == ',' || line[i] == '/' {
				complex = true
			}
		}
		if complex {
			line = strings.ReplaceAll(line, "\\", "\\\\")
			line = strings.ReplaceAll(line, "\"", "\"\"")
			tokens = append(tokens, "\""+line+"\"")
		} else {
			tokens = append(tokens, line)
		}
	}
	return strings.Join(tokens, ",")
}

func guntokenizeText(text string) string {
	var out strings.Builder
	inQuote := false
	start := 0
	if strings.HasPrefix(text, "\"") {
		inQuote = true
		start = 1
	}
	for i := start; i < len(text); i++ {
		ch := text[i]
		switch ch {
		case ',':
			if inQuote {
				out.WriteByte(ch)
			} else {
				out.WriteByte('\n')
				for i+1 < len(text) && text[i+1] == ' ' {
					i++
				}
				if i+1 < len(text) && text[i+1] == '"' {
					inQuote = true
					i++
				}
			}
		case '"':
			if inQuote {
				if i+1 < len(text) && text[i+1] == '"' {
					out.WriteByte('"')
					i++
				} else if i+1 < len(text) && text[i+1] == ',' {
					inQuote = false
				}
			} else {
				out.WriteByte(ch)
			}
		case '\\':
			if i+1 < len(text) && text[i+1] == '\\' {
				out.WriteByte('\\')
				i++
			}
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func (p *Player) sendRCPostLoginTail() {
	p.server.broadcastPlayerListEntryToClients(p)
	p.server.playerMu.RLock()
	defer p.server.playerMu.RUnlock()
	for _, other := range p.server.players {
		if other == nil || other.id == p.id || !other.isLoggedIn() {
			continue
		}
		if other.playerType&PLTYPE_ANYNC != 0 {
			continue
		}
		p.sendPLO_ADDPLAYER(other)
	}
}

func (p *Player) sendNCPostLoginTail() {
	p.sendNCNPCList()
	p.sendNCClassList()

	p.server.playerMu.RLock()
	for _, other := range p.server.players {
		if other != nil && other != p && other.playerType&PLTYPE_ANYNC != 0 && other.isLoggedIn() {
			p.sendPLO_RC_CHAT("New NC: " + other.accountName)
		}
	}
	p.server.playerMu.RUnlock()
	p.server.sendPacketToType(PLTYPE_ANYNC, rcChatPacket("New NC: "+p.accountName))
}

func (p *Player) sendNCNPCList() {
	p.server.npcMu.RLock()
	npcs := make([]*NPC, 0, len(p.server.npcs))
	for _, npc := range p.server.npcs {
		if npc != nil && npc.npcType == DBNPC {
			npcs = append(npcs, npc)
		}
	}
	p.server.npcMu.RUnlock()
	for _, npc := range npcs {
		p.sendNCNPCAdd(npc)
	}
}

func (p *Player) sendNCNPCAdd(npc *NPC) {
	levelName := ""
	if npc.level != nil {
		levelName = npc.level.levelName
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_NPCADD)
	buf.WriteGInt(npc.id)
	buf.WriteByte(byte(NPCPROP_NAME))
	buf.WriteGString(npc.npcName)
	buf.WriteByte(byte(NPCPROP_TYPE))
	buf.WriteGString(npc.scriptType)
	buf.WriteByte(byte(NPCPROP_CURLEVEL))
	buf.WriteGString(levelName)
	p.send(buf)
}

func (p *Player) sendNCClassList() {
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_CLASSADD)
	p.server.weaponMu.RLock()
	for className := range p.server.classes {
		buf.Write([]byte(className))
		buf.WriteByte('\n')
	}
	p.server.weaponMu.RUnlock()
	if buf.Len() > 1 {
		p.send(buf)
	}
}

func (p *Player) sendPostLoginTail() {
	p.server.logger.Info("Sending PLO_STAFFGUILDS...")
	staffGuilds := p.server.settings.Get("staffguilds")
	if staffGuilds != "" {
		guilds := strings.Split(staffGuilds, ",")
		buf := NewBuffer()
		buf.WriteByte(PLO_STAFFGUILDS)
		for _, guild := range guilds {
			guild = strings.TrimSpace(guild)
			if guild != "" {
				buf.Write([]byte("\"" + guild + "\","))
			}
		}
		if buf.Len() > 1 {
			packet := NewBufferFromBytes(buf.Bytes()[:len(buf.Bytes())-1])
			p.sendPacket(packet.Bytes())
		} else {
			p.sendPacket(buf.Bytes())
		}
	}
	p.server.logger.Info("Sending PLO_STATUSLIST...")
	statusList := p.server.settings.Get("playerlisticons")
	if statusList == "" {
		statusList = p.server.settings.Get("statuslist")
	}
	if statusList == "" {
		statusList = "Online,Away,DND,Eating,Hiding,No PMs,RPing,Sparring,PKing"
	}
	if statusList != "" {
		statuses := strings.Split(statusList, ",")
		buf := NewBuffer()
		buf.WriteByte(PLO_STATUSLIST)
		for _, status := range statuses {
			status = strings.TrimSpace(status)
			if status != "" {
				buf.Write([]byte(status + ","))
			}
		}
		if buf.Len() > 1 {
			packet := NewBufferFromBytes(buf.Bytes()[:len(buf.Bytes())-1])
			p.sendPacket(packet.Bytes())
		} else {
			p.sendPacket(buf.Bytes())
		}
	}
	p.server.logger.Info("Exchanging player props with existing players...")
	myClientProps := p.sendPropsWithArray(getRCLoginProps)
	if p.playerType&PLTYPE_ANYCLIENT != 0 {
		myClientProps = p.sendPropsWithArray(getLoginProps)
	}
	p.server.playerMu.RLock()
	for _, other := range p.server.players {
		if other == nil {
			continue
		}
		if other.id == p.id {
			continue
		}
		if !isPlayerListPlayer(other) {
			continue
		}
		if !other.isLoggedIn() {
			continue
		}
		if other.conn != nil && isPlayerListPlayer(p) {
			if other.playerType&PLTYPE_ANYCLIENT != 0 {
				if shouldSendClientPlayerListEntry(p) && len(myClientProps) > 0 {
					other.sendPacket(append(p.otherPropsPacket(myClientProps), '\n'))
				}
			} else {
				other.sendPLO_ADDPLAYER(p)
			}
		}
		if p.playerType&PLTYPE_ANYCLIENT != 0 {
			if !shouldSendClientPlayerListEntry(other) {
				continue
			}
			otherPropsSet := getRCLoginProps
			if other.playerType&PLTYPE_ANYCLIENT != 0 {
				otherPropsSet = getLoginProps
			}
			otherProps := other.sendPropsWithArray(otherPropsSet)
			if len(otherProps) > 0 {
				p.sendPacket(append(other.otherPropsPacket(otherProps), '\n'))
			}
		}
	}
	p.server.playerMu.RUnlock()
	if p.playerType&PLTYPE_ANYCLIENT != 0 && p.versionId > 0 && p.versionId < 300 {
		p.sendPLO_LISTPROCESSES()
	}
}

func (p *Player) otherPropsPacket(props []byte) []byte {
	buf := NewBuffer()
	buf.WriteByte(PLO_OTHERPLPROPS).WriteGShort(p.id).Write(props)
	return buf.Bytes()
}

func (p *Player) sendToCurrentLevelExceptSelf(packet []byte) {
	level := p.currentLevel
	if level == nil && p.server != nil && p.levelName != "" {
		level = p.server.GetLevel(cleanLevelName(p.levelName))
	}
	if level == nil || p.server == nil {
		return
	}
	for _, plId := range level.getPlayers() {
		if plId == p.id {
			continue
		}
		if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
			pl.sendPacket(append(packet, '\n'))
		}
	}
}

func (p *Player) handlePacket(packet []byte) bool {
	if len(packet) == 0 {
		return true
	}
	packetId := int(NewBufferFromBytes(packet).ReadGChar())
	packet = append([]byte{byte(packetId)}, packet[1:]...)
	p.packetCount++
	packetName := pliNames[byte(packetId)]
	if packetName == "" {
		packetName = fmt.Sprintf("UNKNOWN_%d", packetId)
	}
	p.server.logger.Debug("[PACKET] Received %s (ID %d, %d bytes) from %s", packetName, packetId, len(packet), p.accountName)
	switch packetId {
	case PLI_LEVELWARP, PLI_LEVELWARPMOD:
		return p.msgPLI_LEVELWARP(packet)
	case PLI_BOARDMODIFY:
		return p.msgPLI_BOARDMODIFY(packet)
	case PLI_REQUESTUPDATEBOARD:
		return p.msgPLI_REQUESTUPDATEBOARD(packet)
	case PLI_PLAYERPROPS:
		return p.msgPLI_PLAYERPROPS(packet)
	case PLI_NPCPROPS:
		return p.msgPLI_NPCPROPS(packet)
	case PLI_BOMBADD:
		return p.msgPLI_BOMBADD(packet)
	case PLI_BOMBDEL:
		return p.msgPLI_BOMBDEL(packet)
	case PLI_TOALL:
		return p.msgPLI_TOALL(packet)
	case PLI_HORSEADD:
		return p.msgPLI_HORSEADD(packet)
	case PLI_HORSEDEL:
		return p.msgPLI_HORSEDEL(packet)
	case PLI_ARROWADD:
		return p.msgPLI_ARROWADD(packet)
	case PLI_FIRESPY:
		return p.msgPLI_FIRESPY(packet)
	case PLI_THROWCARRIED:
		return p.msgPLI_THROWCARRIED(packet)
	case PLI_ITEMADD:
		return p.msgPLI_ITEMADD(packet)
	case PLI_ITEMDEL, PLI_ITEMTAKE:
		return p.msgPLI_ITEMDEL(packet)
	case PLI_CLAIMPKER:
		return p.msgPLI_CLAIMPKER(packet)
	case PLI_BADDYPROPS:
		return p.msgPLI_BADDYPROPS(packet)
	case PLI_BADDYHURT:
		return p.msgPLI_BADDYHURT(packet)
	case PLI_BADDYADD:
		return p.msgPLI_BADDYADD(packet)
	case PLI_FLAGSET:
		return p.msgPLI_FLAGSET(packet)
	case PLI_FLAGDEL:
		return p.msgPLI_FLAGDEL(packet)
	case PLI_OPENCHEST:
		return p.msgPLI_OPENCHEST(packet)
	case PLI_PUTNPC:
		return p.msgPLI_PUTNPC(packet)
	case PLI_NPCDEL:
		return p.msgPLI_NPCDEL(packet)
	case PLI_WANTFILE:
		return p.msgPLI_WANTFILE(packet)
	case PLI_SHOWIMG:
		return p.msgPLI_SHOWIMG(packet)
	case PLI_HURTPLAYER:
		return p.msgPLI_HURTPLAYER(packet)
	case PLI_EXPLOSION:
		return p.msgPLI_EXPLOSION(packet)
	case PLI_PRIVATEMESSAGE:
		return p.msgPLI_PRIVATEMESSAGE(packet)
	case PLI_NPCWEAPONDEL:
		return p.msgPLI_NPCWEAPONDEL(packet)
	case PLI_PACKETCOUNT:
		return p.msgPLI_PACKETCOUNT(packet)
	case PLI_WEAPONADD:
		return p.msgPLI_WEAPONADD(packet)
	case PLI_UPDATEFILE:
		return p.msgPLI_UPDATEFILE(packet)
	case PLI_ADJACENTLEVEL:
		return p.msgPLI_ADJACENTLEVEL(packet)
	case PLI_HITOBJECTS:
		return p.msgPLI_HITOBJECTS(packet)
	case PLI_LANGUAGE:
		return p.msgPLI_LANGUAGE(packet)
	case PLI_TRIGGERACTION:
		return p.msgPLI_TRIGGERACTION(packet)
	case PLI_MAPINFO:
		return p.msgPLI_MAPINFO(packet)
	case PLI_SHOOT:
		return p.msgPLI_SHOOT(packet)
	case PLI_SHOOT2:
		return p.msgPLI_SHOOT2(packet)
	case PLI_SERVERWARP:
		return p.msgPLI_SERVERWARP(packet)
	case PLI_MUTEPLAYER:
		return true
	case PLI_PROCESSLIST:
		return p.msgPLI_PROCESSLIST(packet)
	case PLI_UNKNOWN46:
		return p.msgPLI_UNKNOWN46(packet)
	case PLI_VERIFYWANTSEND:
		return p.msgPLI_VERIFYWANTSEND(packet)
	case PLI_UPDATECLASS:
		return p.msgPLI_UPDATECLASS(packet)
	case PLI_RAWDATA:
		return p.msgPLI_RAWDATA(packet)
	case PLI_RC_SERVEROPTIONSGET:
		return p.msgPLI_RC_SERVEROPTIONSGET(packet)
	case PLI_RC_SERVEROPTIONSSET:
		return p.msgPLI_RC_SERVEROPTIONSSET(packet)
	case PLI_RC_FOLDERCONFIGGET:
		return p.msgPLI_RC_FOLDERCONFIGGET(packet)
	case PLI_RC_FOLDERCONFIGSET:
		return p.msgPLI_RC_FOLDERCONFIGSET(packet)
	case PLI_RC_RESPAWNSET:
		return p.msgPLI_RC_RESPAWNSET(packet)
	case PLI_RC_HORSELIFESET:
		return p.msgPLI_RC_HORSELIFESET(packet)
	case PLI_RC_APINCREMENTSET:
		return p.msgPLI_RC_APINCREMENTSET(packet)
	case PLI_RC_BADDYRESPAWNSET:
		return p.msgPLI_RC_BADDYRESPAWNSET(packet)
	case PLI_RC_PLAYERPROPSGET:
		return p.msgPLI_RC_PLAYERPROPSGET(packet)
	case PLI_RC_PLAYERPROPSSET:
		return p.msgPLI_RC_PLAYERPROPSSET(packet)
	case PLI_RC_DISCONNECTPLAYER:
		return p.msgPLI_RC_DISCONNECTPLAYER(packet)
	case PLI_RC_UPDATELEVELS:
		return p.msgPLI_RC_UPDATELEVELS(packet)
	case PLI_RC_ADMINMESSAGE:
		return p.msgPLI_RC_ADMINMESSAGE(packet)
	case PLI_RC_PRIVADMINMESSAGE:
		return p.msgPLI_RC_PRIVADMINMESSAGE(packet)
	case PLI_RC_LISTRCS:
		return p.msgPLI_RC_LISTRCS(packet)
	case PLI_RC_DISCONNECTRC:
		return p.msgPLI_RC_DISCONNECTRC(packet)
	case PLI_RC_APPLYREASON:
		return p.msgPLI_RC_APPLYREASON(packet)
	case PLI_RC_SERVERFLAGSGET:
		return p.msgPLI_RC_SERVERFLAGSGET(packet)
	case PLI_RC_SERVERFLAGSSET:
		return p.msgPLI_RC_SERVERFLAGSSET(packet)
	case PLI_RC_ACCOUNTADD:
		return p.msgPLI_RC_ACCOUNTADD(packet)
	case PLI_RC_ACCOUNTDEL:
		return p.msgPLI_RC_ACCOUNTDEL(packet)
	case PLI_RC_ACCOUNTLISTGET:
		return p.msgPLI_RC_ACCOUNTLISTGET(packet)
	case PLI_RC_PLAYERPROPSGET2:
		return p.msgPLI_RC_PLAYERPROPSGET2(packet)
	case PLI_RC_PLAYERPROPSGET3:
		return p.msgPLI_RC_PLAYERPROPSGET3(packet)
	case PLI_RC_PLAYERPROPSRESET:
		return p.msgPLI_RC_PLAYERPROPSRESET(packet)
	case PLI_RC_PLAYERPROPSSET2:
		return p.msgPLI_RC_PLAYERPROPSSET2(packet)
	case PLI_RC_ACCOUNTGET:
		return p.msgPLI_RC_ACCOUNTGET(packet)
	case PLI_RC_ACCOUNTSET:
		return p.msgPLI_RC_ACCOUNTSET(packet)
	case PLI_RC_CHAT:
		return p.msgPLI_RC_CHAT(packet)
	case PLI_PROFILEGET:
		return p.msgPLI_PROFILEGET(packet)
	case PLI_PROFILESET:
		return p.msgPLI_PROFILESET(packet)
	case PLI_RC_WARPPLAYER:
		return p.msgPLI_RC_WARPPLAYER(packet)
	case PLI_RC_PLAYERRIGHTSGET:
		return p.msgPLI_RC_PLAYERRIGHTSGET(packet)
	case PLI_RC_PLAYERRIGHTSSET:
		return p.msgPLI_RC_PLAYERRIGHTSSET(packet)
	case PLI_RC_PLAYERCOMMENTSGET:
		return p.msgPLI_RC_PLAYERCOMMENTSGET(packet)
	case PLI_RC_PLAYERCOMMENTSSET:
		return p.msgPLI_RC_PLAYERCOMMENTSSET(packet)
	case PLI_RC_PLAYERBANGET:
		return p.msgPLI_RC_PLAYERBANGET(packet)
	case PLI_RC_PLAYERBANSET:
		return p.msgPLI_RC_PLAYERBANSET(packet)
	case PLI_RC_FILEBROWSER_START:
		return p.msgPLI_RC_FILEBROWSER_START(packet)
	case PLI_RC_FILEBROWSER_CD:
		return p.msgPLI_RC_FILEBROWSER_CD(packet)
	case PLI_RC_FILEBROWSER_END:
		return p.msgPLI_RC_FILEBROWSER_END(packet)
	case PLI_RC_FILEBROWSER_DOWN:
		return p.msgPLI_RC_FILEBROWSER_DOWN(packet)
	case PLI_RC_FILEBROWSER_UP:
		return p.msgPLI_RC_FILEBROWSER_UP(packet)
	case PLI_NPCSERVERQUERY:
		return p.msgPLI_NPCSERVERQUERY(packet)
	case PLI_RC_FILEBROWSER_MOVE:
		return p.msgPLI_RC_FILEBROWSER_MOVE(packet)
	case PLI_RC_FILEBROWSER_DELETE:
		return p.msgPLI_RC_FILEBROWSER_DELETE(packet)
	case PLI_RC_FILEBROWSER_RENAME:
		return p.msgPLI_RC_FILEBROWSER_RENAME(packet)
	case PLI_RC_LARGEFILESTART:
		return p.msgPLI_RC_LARGEFILESTART(packet)
	case PLI_RC_LARGEFILEEND:
		return p.msgPLI_RC_LARGEFILEEND(packet)
	case PLI_RC_FOLDERDELETE:
		return p.msgPLI_RC_FOLDERDELETE(packet)
	case PLI_NC_LISTNPCS:
		return p.msgPLI_NC_LISTNPCS(packet)
	case PLI_NC_NPCGET:
		return p.msgPLI_NC_NPCGET(packet)
	case PLI_NC_NPCDELETE:
		return p.msgPLI_NC_NPCDELETE(packet)
	case PLI_NC_NPCRESET:
		return p.msgPLI_NC_NPCRESET(packet)
	case PLI_NC_NPCSCRIPTGET:
		return p.msgPLI_NC_NPCSCRIPTGET(packet)
	case PLI_NC_NPCWARP:
		return p.msgPLI_NC_NPCWARP(packet)
	case PLI_NC_NPCFLAGSGET:
		return p.msgPLI_NC_NPCFLAGSGET(packet)
	case PLI_NC_NPCSCRIPTSET:
		return p.msgPLI_NC_NPCSCRIPTSET(packet)
	case PLI_NC_NPCFLAGSSET:
		return p.msgPLI_NC_NPCFLAGSSET(packet)
	case PLI_NC_NPCADD:
		return p.msgPLI_NC_NPCADD(packet)
	case PLI_NC_CLASSEDIT:
		return p.msgPLI_NC_CLASSEDIT(packet)
	case PLI_NC_CLASSADD:
		return p.msgPLI_NC_CLASSADD(packet)
	case PLI_NC_LOCALNPCSGET:
		return p.msgPLI_NC_LOCALNPCSGET(packet)
	case PLI_NC_WEAPONLISTGET:
		return p.msgPLI_NC_WEAPONLISTGET(packet)
	case PLI_NC_WEAPONGET:
		return p.msgPLI_NC_WEAPONGET(packet)
	case PLI_NC_WEAPONADD:
		return p.msgPLI_NC_WEAPONADD(packet)
	case PLI_NC_WEAPONDELETE:
		return p.msgPLI_NC_WEAPONDELETE(packet)
	case PLI_NC_CLASSDELETE:
		return p.msgPLI_NC_CLASSDELETE(packet)
	case PLI_NC_LEVELLISTGET:
		return p.msgPLI_NC_LEVELLISTGET(packet)
	case PLI_REQUESTTEXT:
		return p.msgPLI_REQUESTTEXT(packet)
	case PLI_SENDTEXT:
		return p.msgPLI_SENDTEXT(packet)
	case PLI_UPDATEGANI:
		return p.msgPLI_UPDATEGANI(packet)
	case PLI_UPDATESCRIPT:
		return p.msgPLI_UPDATESCRIPT(packet)
	case PLI_UPDATEPACKAGEREQUESTFILE:
		return p.msgPLI_UPDATEPACKAGEREQUESTFILE(packet)
	case PLI_RC_UNKNOWN162:
		return p.msgPLI_RC_UNKNOWN162(packet)
	default:
		p.invalidPackets++
		if p.invalidPackets > 5 {
			p.server.logger.Warning("Player %s sending invalid packets", p.accountName)
			return false
		}
	}
	return true
}

func (p *Player) handleLogin(packet []byte) bool {
	p.server.logger.Debug("handleLogin: packet size = %d", len(packet))
	decompressed, err := ZlibDecompress(packet)
	if err != nil {
		p.server.logger.Error("handleLogin: decompress failed: %v", err)
		return false
	}
	p.server.logger.Debug("handleLogin: decompressed to %d bytes", len(decompressed))
	if len(decompressed) < 10 {
		return false
	}
	p.server.logger.Debug("handleLogin: raw bytes = %q", string(decompressed))
	p.server.logger.Debug("handleLogin: raw hex = % X", decompressed)
	// C++ format: [GChar client type][version: 8 bytes][account data...]
	buf := NewBufferFromBytes(decompressed)
	// Read client type (GChar)
	clientTypeByte := buf.ReadGChar()
	clientType := 1 << clientTypeByte
	p.server.logger.Debug("handleLogin: clientTypeByte=%d (raw byte=%d) clientType=%d", clientTypeByte, decompressed[0], clientType)
	// Read encryption key for GEN_4+ clients (key is G-encoded)
	var encryptionKey byte
	if loginTypeUsesEncryptionKey(clientType) {
		p.server.logger.Debug("handleLogin: Reading encryption key for GEN_4+ client")
		if buf.Remaining() < 1 {
			return false
		}
		encryptionKey = buf.ReadGChar() // ReadGChar decodes G-encoded value
		p.server.logger.Debug("handleLogin: encryptionKey=%d", encryptionKey)
	}
	// Check client type - all clients send 8-char version after client type byte
	if buf.Remaining() < 8 {
		return false
	}
	version := string(buf.data[buf.read : buf.read+8])
	buf.read += 8
	p.server.logger.Debug("handleLogin: version=%q", version)
	p.version = version
	p.versionId = clientVersionID(version)
	// Read account (GChar length + string)
	if buf.Remaining() < 1 {
		return false
	}
	accountLen := buf.ReadGChar()
	if buf.Remaining() < int(accountLen) {
		return false
	}
	accountBytes := make([]byte, accountLen)
	for i := 0; i < int(accountLen); i++ {
		accountBytes[i] = buf.ReadByte()
	}
	account := string(accountBytes)
	// Read password (GChar length + string)
	if buf.Remaining() < 1 {
		return false
	}
	passwordLen := buf.ReadGChar()
	if buf.Remaining() < int(passwordLen) {
		return false
	}
	passwordBytes := make([]byte, passwordLen)
	for i := 0; i < int(passwordLen); i++ {
		passwordBytes[i] = buf.ReadByte()
	}
	password := string(passwordBytes)
	// Read client identity (raw string until newline)
	identity := buf.ReadString()
	p.server.logger.Debug("handleLogin: account=%s password=%s identity=%s", account, password, identity)
	p.playerType = clientType
	p.setAccountName(account)
	p.setNickname(account)
	p.setId(p.server.nextPlayerId())
	p.setX(32)
	p.setY(32)
	p.levelName = "empty"
	p.character.nickName = account
	p.character.gani = "idle.gif"
	p.communityName = "default"
	p.language = "english"
	p.accountIp = 0
	p.isBanned = false
	p.isGuest = false
	p.isLoadOnly = false
	p.adminRights = 0
	p.maxHitpoints = 3
	p.character.hitpoints = 3
	p.character.gralats = 0
	p.character.arrows = 0
	p.character.bombs = 0
	p.character.glovePower = 0
	p.character.shieldPower = 0
	p.character.swordPower = 0
	p.character.bowPower = 0
	p.character.sprite = 0
	p.status = 0
	p.mp = 0
	p.apCounter = 0
	p.kills = 0
	p.deaths = 0
	p.eloRating = 1500.0
	p.eloDeviation = 350.0
	p.onlineTime = 0
	p.rupees = 50
	p.statusMsg = 0
	p.os = "wind"
	p.envCodePage = 1252
	for i := range p.gAttribs {
		p.gAttribs[i] = ""
	}
	p.flagList = make(map[string]string)
	p.weaponList = []string{}
	p.chestList = []string{}
	p.folderList = []string{}
	// Set encryption based on client type
	p.server.logger.Debug("Setting encryption: clientType=%d PLTYPE_CLIENT=%d PLTYPE_CLIENT2=%d PLTYPE_CLIENT3=%d", clientType, PLTYPE_CLIENT, PLTYPE_CLIENT2, PLTYPE_CLIENT3)
	switch clientType {
	case PLTYPE_CLIENT:
		p.server.logger.Debug("Matched PLTYPE_CLIENT")
		p.encryption.gen = ENCRYPT_GEN_2
	case PLTYPE_CLIENT2:
		p.server.logger.Debug("Matched PLTYPE_CLIENT2")
		p.encryption.gen = ENCRYPT_GEN_4
	case PLTYPE_CLIENT3:
		p.server.logger.Debug("Matched PLTYPE_CLIENT3")
		p.encryption.gen = ENCRYPT_GEN_5
	case PLTYPE_RC2:
		p.server.logger.Debug("Matched PLTYPE_RC2")
		p.encryption.gen = ENCRYPT_GEN_5
	default:
		p.server.logger.Debug("Matched default case")
		p.encryption.gen = ENCRYPT_GEN_3
	}
	// Initialize encryption with key for GEN_4+ clients
	if p.encryption.gen > ENCRYPT_GEN_3 && encryptionKey != 0 {
		p.server.logger.Debug("Initializing encryption with key=%d", encryptionKey)
		p.encryption.Reset(encryptionKey)
		// Initialize output codec with same key and generation
		p.outEncryption.SetGen(p.encryption.GetGen())
		p.outEncryption.Reset(encryptionKey)
		p.server.logger.Debug("Initialized inEncryption gen=%d key=%d iterator=%08X", p.encryption.gen, encryptionKey, p.encryption.iterator)
		p.server.logger.Debug("Initialized outEncryption gen=%d key=%d iterator=%08X", p.outEncryption.gen, encryptionKey, p.outEncryption.iterator)
	}
	p.server.logger.Info("Setting encryption gen to %d (ENCRYPT_GEN_3=%d) for client type %d", p.encryption.gen, ENCRYPT_GEN_3, clientType)
	p.queueOutgoing = true
	p.outQueue = p.outQueue[:0]
	sigBuf := NewBuffer()
	sigBuf.WriteByte(PLO_SIGNATURE).WriteByte(73)
	p.send(sigBuf)
	if strings.Contains(strings.ToLower(p.server.name), "login") {
		p.sendPLO_FULLSTOP()
		ghostBuf := NewBuffer()
		ghostBuf.WriteByte(PLO_GHOSTICON).WriteByte(1)
		p.send(ghostBuf)
	}
	if clientType&PLTYPE_ANYCLIENT != 0 {
		if p.server.settings.GetBool("serverside", false) {
			p.sendPLO_HASNPCSERVER()
		}
		unkBuf := NewBuffer()
		unkBuf.WriteByte(PLO_UNKNOWN168)
		p.send(unkBuf)
	}
	if !p.LoadAccount(account, true) {
		p.server.logger.Error("Failed to load account for: %s", account)
		return false
	}
	p.applyServerOptionsStaffRights()
	if p.conn != nil {
		p.setAccountIPFromRemoteAddr(p.conn.RemoteAddr())
	}
	if p.loadedFromDefault && !p.isLoadOnly {
		p.server.logger.Info("Creating new account from default: %s", account)
		p.SaveAccount()
	}
	if clientType&PLTYPE_ANYRC != 0 {
		p.levelName = ""
		p.currentLevel = nil
		p.x = 0
		p.y = 0
		p.z = 0
		p.server.logger.Info("Sending RC login payload...")
		p.sendRCLoginPayload()
		p.sendCompress(true)
		p.loaded = true
		p.server.logger.Info("[%s] RC logged in (type=%d)", account, clientType)
		return true
	}
	if clientType&PLTYPE_ANYNC != 0 {
		p.sendCompress(true)
		p.loaded = true
		p.server.logger.Info("[%s] NC logged in (type=%d)", account, clientType)
		return true
	}
	p.server.logger.Info("Sending login props (PLO_PLAYERPROPS)...")
	p.sendProps(sendLoginProps)
	p.server.logger.Info("Sending PLO_CLEARWEAPONS...")
	p.sendPLO_CLEARWEAPONS()
	p.server.logger.Info("Sending player flags...")
	p.sendPLO_FLAGSET("head", p.character.headImage)
	p.sendPLO_FLAGSET("body", p.character.bodyImage)
	p.sendPLO_FLAGSET("sword", p.character.swordImage)
	p.sendPLO_FLAGSET("shield", p.character.shieldImage)
	p.sendPLO_FLAGSET("color1", fmt.Sprintf("%d", p.character.colors[0]))
	p.sendPLO_FLAGSET("color2", fmt.Sprintf("%d", p.character.colors[1]))
	p.sendPLO_FLAGSET("color3", fmt.Sprintf("%d", p.character.colors[2]))
	p.sendPLO_FLAGSET("color4", fmt.Sprintf("%d", p.character.colors[3]))
	p.sendPLO_FLAGSET("color5", fmt.Sprintf("%d", p.character.colors[4]))
	p.sendPLO_FLAGSET("sprite", fmt.Sprintf("%d", p.character.sprite))
	p.server.logger.Info("Sending server flags...")
	for flag, value := range p.server.flags {
		if !isValidServerFlag(flag, value) {
			p.server.logger.Warning("Skipping malformed server flag for %s: %q", p.accountName, flag)
			continue
		}
		p.sendPLO_FLAGSET(flag, value)
	}
	p.server.logger.Info("Deleting missing default weapons...")
	p.sendMissingDefaultWeaponDeletes()
	p.server.logger.Info("Sending weapons...")
	for _, weaponName := range p.weaponList {
		if strings.HasPrefix(weaponName, "-") {
			continue
		}
		p.sendAccountWeapon(weaponName)
	}
	if p.versionId >= 221 && p.versionId <= 231 {
		p.server.logger.Info("Sending zlib fix weapon...")
		p.sendPLO_ZLIBFIXWEAPON()
	}
	p.server.logger.Info("Sending PLO_UNKNOWN190...")
	p.sendPLO_UNKNOWN190()
	startLevel, startX, startY := p.loginWarpTarget()
	p.server.logger.Info("Warping player to '%s' at (%.2f, %.2f)...", startLevel, startX, startY)
	p.warp(startLevel, startX, startY)
	p.server.logger.Info("Sending PLO_BIGMAP...")
	bigmap := p.server.settings.Get("bigmap")
	if bigmap != "" {
		p.sendPLO_BIGMAP()
	}
	p.server.logger.Info("Sending PLO_MINIMAP...")
	minimap := p.server.settings.Get("minimap")
	if minimap != "" {
		p.sendPLO_MINIMAP()
	}
	p.server.logger.Info("Sending PLO_RPGWINDOW...")
	p.sendPLO_RPGWINDOW(fmt.Sprintf("\"Welcome to %s.\",\"Go Code GServer.\"", p.server.name))
	p.server.logger.Info("Sending PLO_STARTMESSAGE...")
	p.sendPLO_STARTMESSAGE(p.server.serverMessage)
	p.server.logger.Info("Sending PLO_SERVERTEXT (no message)...")
	buf = NewBuffer()
	buf.WriteByte(PLO_SERVERTEXT)
	p.send(buf)
	p.sendCompress(true)
	p.server.logger.Info("[%s] Player logged in (type=%d)", account, clientType)
	return true
}

func (p *Player) handleRawData(data []byte) {
	if len(data) == 0 {
		return
	}
	p.server.logger.PacketDebug("handleRawData: RAW data: % X (gen=%d)", data, p.encryption.gen)
	var decompressed []byte
	var err error
	if p.encryption.gen == ENCRYPT_GEN_4 {
		p.server.logger.PacketDebug("handleRawData: GEN_4 - decrypting and decompressing with BZ2")
		p.encryption.limit = 4 // COMPRESS_BZ2 limit
		p.encryption.Decrypt(data)
		decompressed, err = Bz2Decompress(data)
		if err != nil {
			p.server.logger.Debug("handleRawData: BZ2 decompress failed: %v", err)
			return
		}
	} else if p.encryption.gen >= ENCRYPT_GEN_5 {
		if len(data) < 1 {
			return
		}
		compressType := data[0]
		encryptedData := data[1:]
		p.server.logger.PacketDebug("handleRawData: GEN_5+ - compressType=%d, encrypted data: % X", compressType, encryptedData)
		// Set encryption limit based on compression type
		limits := map[uint8]int32{COMPRESS_UNCOMPRESSED: 12, COMPRESS_ZLIB: 4, COMPRESS_BZ2: 4}
		if limit, ok := limits[compressType]; ok {
			p.encryption.limit = limit
		}
		p.server.logger.PacketDebug("handleRawData: BEFORE decrypt - iterator=%08X limit=%d", p.encryption.iterator, p.encryption.limit)
		p.encryption.Decrypt(encryptedData)
		p.server.logger.PacketDebug("handleRawData: AFTER decrypt - iterator=%08X data: % X", p.encryption.iterator, encryptedData)
		if compressType == COMPRESS_ZLIB {
			decompressed, err = ZlibDecompress(encryptedData)
			if err != nil {
				p.server.logger.Debug("handleRawData: ZLIB decompress failed: %v", err)
				return
			}
		} else if compressType == COMPRESS_BZ2 {
			decompressed, err = Bz2Decompress(encryptedData)
			if err != nil {
				p.server.logger.Debug("handleRawData: BZ2 decompress failed: %v", err)
				return
			}
		} else if compressType == COMPRESS_UNCOMPRESSED {
			decompressed = encryptedData
		} else {
			p.server.logger.Debug("handleRawData: Unknown compression type %d", compressType)
			return
		}
	} else {
		p.server.logger.PacketDebug("handleRawData: GEN_1-3 - using newline delimiter")
		p.handleDecompressedPackets(data)
		return
	}
	p.server.logger.PacketDebug("handleRawData: DECOMPRESSED data: % X", decompressed)
	p.handleDecompressedPackets(decompressed)
}

func (p *Player) handleDecompressedPackets(data []byte) {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline == -1 {
			if len(data) > 0 {
				p.handlePacket(data)
			}
			return
		}
		packet := data[:newline]
		data = data[newline+1:]
		if len(packet) > 0 {
			p.handlePacket(packet)
		}
	}
}

func (p *Player) sendPacket(packet []byte) {
	if len(packet) == 0 {
		return
	}
	rawPacketId := packet[0]
	packet = encodeOutgoingPacket(packet)
	packetId := rawPacketId
	packetName := ploNames[packetId]
	if packetName == "" {
		packetName = "UNKNOWN"
	}
	p.server.logger.PacketDebug("sendPacket: RAW %s (ID %d): % X", packetName, packetId, packet)
	if p.queueOutgoing {
		p.server.logger.PacketDebug("sendPacket: queued %s (ID %d), %d bytes", packetName, packetId, len(packet))
		p.outQueue = append(p.outQueue, packet...)
		return
	}
	p.writeEncodedPacket(packetName, packetId, packet)
}

func (p *Player) writeEncodedPacket(packetName string, packetId byte, packet []byte) {
	var data []byte
	switch p.encryption.gen {
	case ENCRYPT_GEN_1:
		p.server.logger.PacketDebug("sendPacket: GEN_1, sending %s (ID %d), raw %d bytes", packetName, packetId, len(packet))
		data = packet
	case ENCRYPT_GEN_2, ENCRYPT_GEN_3:
		compressed, err := ZlibCompress(packet)
		if err != nil {
			p.server.logger.Error("sendPacket: compression failed: %v", err)
			return
		}
		if len(compressed) > 0xFFFD {
			p.server.logger.Error("sendPacket: compressed packet too large (%d bytes)", len(compressed))
			return
		}
		p.server.logger.PacketDebug("sendPacket: GEN_%d, sending %s (ID %d), compressed %d -> %d bytes", p.encryption.gen, packetName, packetId, len(packet), len(compressed))
		data = make([]byte, 2+len(compressed))
		data[0] = byte(len(compressed) >> 8)
		data[1] = byte(len(compressed))
		copy(data[2:], compressed)
	case ENCRYPT_GEN_5:
		// Choose compression based on packet size
		var compressionType uint8 = COMPRESS_UNCOMPRESSED
		var compressed []byte
		var err error
		if len(packet) > 0x2000 {
			compressionType = COMPRESS_BZ2
			compressed, err = Bz2Compress(packet)
			if err != nil {
				p.server.logger.Error("sendPacket: BZ2 compression failed: %v", err)
				return
			}
		} else if len(packet) > 55 {
			compressionType = COMPRESS_ZLIB
			compressed, err = ZlibCompress(packet)
			if err != nil {
				p.server.logger.Error("sendPacket: Zlib compression failed: %v", err)
				return
			}
		} else {
			compressed = packet
		}
		// Set encryption limit based on compression type
		limits := map[uint8]int32{COMPRESS_UNCOMPRESSED: 12, COMPRESS_ZLIB: 4, COMPRESS_BZ2: 4}
		if limit, ok := limits[compressionType]; ok {
			p.outEncryption.limit = limit
		}
		// Encrypt the compressed data with OUTPUT codec
		p.server.logger.PacketDebug("sendPacket: BEFORE encrypt - outIterator=%08X limit=%d", p.outEncryption.iterator, p.outEncryption.limit)
		encrypted := p.outEncryption.Encrypt(compressed)
		p.server.logger.PacketDebug("sendPacket: AFTER encrypt - outIterator=%08X", p.outEncryption.iterator)
		// Build packet: [length_hi][length_lo][compression_type][encrypted...]
		frameLen := 1 + len(encrypted)
		if frameLen > 0xFFFC {
			p.server.logger.Error("sendPacket: GEN_5 packet too large (%s ID %d, %d bytes)", packetName, packetId, frameLen)
			return
		}
		data = make([]byte, 2+frameLen)
		data[0] = byte(frameLen >> 8)
		data[1] = byte(frameLen)
		data[2] = compressionType
		copy(data[3:], encrypted)
		p.server.logger.PacketDebug("sendPacket: GEN_5, sending %s (ID %d), original %d bytes, compressed %d bytes, compression_type=%d", packetName, packetId, len(packet), len(compressed), compressionType)
	default:
		p.server.logger.PacketDebug("sendPacket: Unknown GEN_%d, sending %s (ID %d), raw %d bytes", p.encryption.gen, packetName, packetId, len(packet))
		data = packet
	}
	p.server.logger.PacketDebug("sendPacket: Writing %d bytes: % X", len(data), data)
	p.mu.Lock()
	conn := p.conn
	disconnected := p.disconnected
	p.mu.Unlock()
	if disconnected || conn == nil {
		return
	}
	if _, err := conn.Write(data); err != nil {
		p.server.logger.Debug("sendPacket: write error to player %d (%s): %v", p.id, p.accountName, err)
		go p.disconnect()
	}
}

func encodeOutgoingPacket(packet []byte) []byte {
	encoded := append([]byte(nil), packet...)
	if encoded[0] > 223 {
		encoded[0] = 223
	}
	encoded[0] += 32
	return encoded
}

func clientVersionID(version string) int {
	switch version {
	case "GNW31101":
		return 210
	case "GNW01012":
		return 212
	case "GNW23012":
		return 213
	case "GNW30042":
		return 214
	case "GNW19052":
		return 215
	case "GNW12102":
		return 216
	case "GNW22122":
		return 217
	case "GNW21033":
		return 218
	case "GNW15053":
		return 219
	case "GNW28063":
		return 220
	case "GNW01113":
		return 221
	case "GNW03014":
		return 222
	case "GNW14015":
		return 230
	case "GNW28015":
		return 231
	}
	if strings.HasPrefix(version, "G3D") {
		return 300
	}
	return 0
}

func (p *Player) sendCompress(forceSend bool) {
	if !p.queueOutgoing {
		return
	}
	p.queueOutgoing = false
	if len(p.outQueue) == 0 {
		return
	}
	queued := append([]byte(nil), p.outQueue...)
	p.outQueue = p.outQueue[:0]
	p.server.logger.PacketDebug("sendCompress: flushing queued login stream, %d bytes", len(queued))
	p.writeEncodedPacket("QUEUED", 0, queued)
}

func (p *Player) send(buf *Buffer) {
	data := append(buf.Bytes(), '\n')
	p.sendPacket(data)
}
func (p *Player) disconnect() {
	p.mu.Lock()
	if p.disconnected {
		p.mu.Unlock()
		return
	}
	p.disconnected = true
	conn := p.conn
	p.conn = nil
	level := p.currentLevel
	p.currentLevel = nil
	server := p.server
	shouldSave := p.shouldSavePlayerAccount()
	p.mu.Unlock()

	if shouldSave {
		p.SaveAccount()
	}
	if conn != nil {
		conn.Close()
	}
	if level != nil {
		level.removePlayer(p)
	}
	if server != nil {
		server.DeletePlayer(p)
	}
}
func (p *Player) hasRight(perm int) bool { return p.adminRights&perm != 0 }

func (p *Player) applyServerOptionsStaffRights() {
	if p.server == nil || p.server.settings == nil || !serverOptionsStaffContains(p.server.settings.Get("staff"), p.accountName) {
		return
	}
	p.adminRights |= allLocalRights()
	p.isStaff = true
	if strings.TrimSpace(p.adminIp) == "" || p.adminIp == "0.0.0.0" {
		p.adminIp = "*.*.*.*"
	}
	if len(p.folderList) == 0 {
		p.folderList = p.server.defaultRCFolderRights()
	}
}

func (s *Server) defaultRCFolderRights() []string {
	lines, err := s.config.LoadFileAsLines("config/foldersconfig.txt")
	if err != nil {
		return nil
	}
	folders := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '/' {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		path := strings.Join(parts[1:], " ")
		if strings.Contains(path, "..") || strings.Contains(path, ":") {
			continue
		}
		entry := "rw " + strings.ReplaceAll(path, "\\", "/")
		if !seen[entry] {
			folders = append(folders, entry)
			seen[entry] = true
		}
	}
	return folders
}

func serverOptionsStaffContains(staffList, accountName string) bool {
	accountName = strings.TrimSpace(accountName)
	if accountName == "" {
		return false
	}
	for _, entry := range strings.Split(staffList, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" || (strings.HasPrefix(entry, "(") && strings.HasSuffix(entry, ")")) {
			continue
		}
		if strings.EqualFold(entry, accountName) {
			return true
		}
	}
	return false
}

func allLocalRights() int {
	return (PLPERM_NPCCONTROL << 1) - 1
}
func (p *Player) sendPLO_LEVELBOARDCHANGES(level *Level, since time.Time) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELBOARD)
	if level != nil {
		for _, change := range level.boardChanges {
			if !change.GetModTime().Before(since) {
				buf.Write(change.GetBoardStr())
			}
		}
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELLINK(x, y int16, levelName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELLINK)
	buf.Write([]byte(fmt.Sprintf("%s %d %d 1 1 %d %d", levelName, x, y, x, y)))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELLINK_FULL(link *LevelLink) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELLINK)
	buf.Write([]byte(link.GetLinkStr()))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SIGN(sign *LevelSign) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELSIGN)
	buf.WriteGChar(byte(sign.x))
	buf.WriteGChar(byte(sign.y))
	buf.Write([]byte(sign.text))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_OTHERPLPROPS(other *Player) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_OTHERPLPROPS).WriteGShort(other.id)
	propIds := []int{PLPROP_NICKNAME, PLPROP_GANI, PLPROP_BODYIMG, PLPROP_HEADGIF, PLPROP_SWORDPOWER, PLPROP_SHIELDPOWER, PLPROP_HORSEGIF, PLPROP_SPRITE, PLPROP_COLORS, PLPROP_X, PLPROP_Y, PLPROP_Z, PLPROP_CURLEVEL}
	for _, propId := range propIds {
		buf.WriteGChar(byte(propId))
		buf.Write(other.getProp(propId))
	}
	p.send(buf)
	return true
}
func (p *Player) writeString8(s string) {
	p.sendPacket(append([]byte{byte(len(s))}, s...))
}
func (p *Player) resetAccount() {
	p.Account = Account{}
	p.character = Character{}
	p.setX(32)
	p.setY(32)
	p.levelName = ""
	p.flagList = make(map[string]string)
	p.folderRights = *NewFilePermissions()
}
func (p *Player) parseProps(props []byte) {
	buf := NewBufferFromBytes(props)
	for buf.Remaining() > 0 {
		propType := buf.ReadByte()
		value := buf.ReadGString()
		switch propType {
		case PLPROP_ACCOUNTNAME:
			p.accountName = value
		case PLPROP_NICKNAME:
			p.character.nickName = value
		case PLPROP_X:
			p.x = int16(atoi(value))
		case PLPROP_Y:
			p.y = int16(atoi(value))
		}
	}
}

func (p *Player) sendPLO_PLAYERPROPS() bool {
	p.sendProps(getLoginProps)
	return true
}
func (p *Player) sendPLO_PLAYERWARP(x, y, z int16, levelName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_PLAYERWARP)
	buf.WriteGChar(byte(x / 8))
	buf.WriteGChar(byte(y / 8))
	buf.Write([]byte(levelName))
	p.send(buf)
	return true
}
func (p *Player) sendPTO_ALL_CHAT(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_TOALL).WriteGShort(p.id).WriteGChar(byte(len(message))).Write([]byte(message))
	for _, pl := range p.server.players {
		if pl.isLoggedIn() && pl.levelName == p.levelName && pl.conn != nil {
			pl.send(buf)
		}
	}
	return true
}
func (p *Player) sendPLO_PRIVATEMESSAGE(from, message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(p.id).Write([]byte("\"Private message:\",\""))
	buf.Write([]byte(message))
	buf.WriteByte('"')
	p.send(buf)
	return true
}
func (p *Player) sendPLO_DISCMESSAGE(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_DISCMESSAGE).Write([]byte(message))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_WARPFAILED(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_WARPFAILED).Write([]byte(message))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELNAME(levelName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELNAME).Write([]byte(levelName))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELCHEST(chest *LevelChest, open bool) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELCHEST)
	if open {
		buf.WriteGChar(1)
		buf.WriteGChar(byte(chest.x))
		buf.WriteGChar(byte(chest.y))
	} else {
		buf.WriteGChar(0)
		buf.WriteGChar(byte(chest.x))
		buf.WriteGChar(byte(chest.y))
		buf.WriteGChar(byte(chest.itemType))
		buf.WriteGChar(byte(chest.signIndex))
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELSIGN(x, y int16, text string) bool {
	buf := NewBuffer()
	sign := NewLevelSign(int(x), int(y), text, false)
	buf.WriteByte(PLO_LEVELSIGN).Write(sign.GetSignStr(p))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCPROPS(npc *NPC) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCPROPS).WriteGInt(uint32(npc.id))
	buf.WriteGChar(NPCPROP_X).WriteGChar(byte(npc.x / 8))
	buf.WriteGChar(NPCPROP_Y).WriteGChar(byte(npc.y / 8))
	if npc.image != "" {
		buf.WriteGChar(NPCPROP_IMAGE).WriteGChar(byte(len(npc.image))).Write([]byte(npc.image))
	}
	if npc.script != "" {
		scriptLen := len(npc.script)
		if scriptLen > 0x3fff {
			scriptLen = 0x3fff
		}
		buf.WriteGChar(NPCPROP_SCRIPT).WriteGShort(uint16(scriptLen)).Write([]byte(npc.script[:scriptLen]))
	}
	if npc.npcName != "" {
		buf.WriteGChar(NPCPROP_NICKNAME).WriteGChar(byte(len(npc.npcName))).Write([]byte(npc.npcName))
	}
	if npc.character.gani != "" {
		buf.WriteGChar(NPCPROP_GANI).WriteGChar(byte(len(npc.character.gani))).Write([]byte(npc.character.gani))
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCDEL(npcId uint32) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCDEL).WriteGInt(npcId)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCMOVED(npcId uint32, x, y int16) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCMOVED).WriteGInt(npcId).WriteGInt(uint32(x)).WriteGInt(uint32(y))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCACTION(npcId uint32, action string, params ...string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCACTION).WriteGInt(npcId).WriteGString(action)
	for _, param := range params {
		buf.WriteGString(param)
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BOMBADD(x, y int16, power int, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BOMBADD).WriteGChar(byte(x)).WriteGChar(byte(y)).WriteGChar(byte(power))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BOMBDEL(bombIdx int) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BOMBDEL).WriteGChar(byte(bombIdx))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_HORSEADD(horseId uint32, x, y int16, image string, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_HORSEADD).WriteByte(byte(x)).WriteByte(byte(y)).WriteByte(0).Write([]byte(image))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELHORSEADD(horse LevelHorse) bool {
	buf := NewBuffer()
	dirBush := (horse.bushes << 2) | (horse.dir & 0x03)
	buf.WriteByte(PLO_HORSEADD)
	buf.WriteByte(byte(horse.x * 2))
	buf.WriteByte(byte(horse.y * 2))
	buf.WriteByte(dirBush)
	buf.Write([]byte(horse.image))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_HORSEDEL(horseId uint32) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_HORSEDEL).WriteGChar(byte(horseId))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_ARROWADD(x, y int16, angle float32, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_ARROWADD).WriteGChar(byte(x)).WriteGChar(byte(y)).WriteGChar(byte(angle))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_ITEMADD(x, y int16, itemIdx int, image string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_ITEMADD).WriteGChar(byte(x)).WriteGChar(byte(y)).WriteGChar(byte(itemIdx))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_ITEMDEL(itemIdx int) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_ITEMDEL).WriteGChar(byte(itemIdx))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FLAGSET(flag, value string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FLAGSET).Write([]byte(flag))
	if value != "" {
		buf.WriteByte('=').Write([]byte(value))
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FLAGDEL(flag string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FLAGDEL).Write([]byte(flag))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SHOWIMG(idx int, image, x, y string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SHOWIMG).WriteGInt(uint32(idx)).WriteGString(image).WriteGString(x).WriteGString(y)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_HURTPLAYER(hurter, damage int) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_HURTPLAYER).WriteGInt(uint32(hurter)).WriteGInt(uint32(damage))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_EXPLOSION(x, y int16, power int) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_EXPLOSION).WriteShort(x).WriteShort(y).WriteGInt(uint32(power))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_ADDPLAYER(other *Player) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_ADDPLAYER).WriteGShort(other.id)
	buf.WriteGChar(byte(len(other.accountName)))
	buf.Write([]byte(other.accountName))
	levelName := other.levelName
	if levelName == "" {
		levelName = " "
	}
	buf.WriteGChar(PLPROP_CURLEVEL).WriteGChar(byte(len(levelName))).Write([]byte(levelName))
	buf.WriteGChar(PLPROP_PSTATUSMSG).Write(other.getProp(PLPROP_PSTATUSMSG))
	buf.WriteGChar(PLPROP_NICKNAME).Write(other.getProp(PLPROP_NICKNAME))
	buf.WriteGChar(PLPROP_COMMUNITYNAME).Write(other.getProp(PLPROP_COMMUNITYNAME))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_DELPLAYER(id uint16) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_DELPLAYER).WriteGShort(id)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_STARTMESSAGE(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_STARTMESSAGE).Write([]byte(message))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SERVERTEXT(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SERVERTEXT).Write([]byte(message))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SHOOT(x, y int16, angle float32, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SHOOT).WriteGShort(p.id).WriteGInt(0).WriteGChar(byte(x)).WriteGChar(byte(y)).WriteGChar(50)
	buf.WriteGChar(byte(angle)).WriteGChar(0).WriteGChar(0).WriteGChar(0).WriteGChar(0)
	p.send(buf)
	return true
}
func (p *Player) sendPBoardPacket(x, y, width, height int16, tiles []int16) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BOARDPACKET).WriteShort(x).WriteShort(y).WriteShort(width).WriteShort(height)
	for _, tile := range tiles {
		buf.WriteShort(tile)
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_TOALL(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_TOALL).WriteGString(message)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BOARDMODIFY(x, y, width, height int16, tiles []int16) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BOARDMODIFY).WriteGChar(byte(x)).WriteGChar(byte(y)).WriteGChar(byte(width)).WriteGChar(byte(height))
	for _, tile := range tiles {
		buf.WriteGShort(uint16(tile))
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BADDYPROPS(id uint32, x, y int16, image string, props []string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BADDYPROPS).WriteGChar(byte(id))
	if image != "" {
		buf.WriteGChar(byte(len(image))).Write([]byte(image))
	}
	for _, prop := range props {
		buf.Write([]byte(prop))
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELBADDYPROPS(baddy *LevelBaddy) bool {
	if baddy == nil {
		return true
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_BADDYPROPS)
	buf.WriteGChar(baddy.id)
	buf.Write(baddy.getProps(p.versionId))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FIRESPY(x, y int16, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FIRESPY).WriteGInt(uint32(x)).WriteGInt(uint32(y)).WriteGString(owner)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_THROWCARRIED(x, y int16, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_THROWCARRIED).WriteGInt(uint32(x)).WriteGInt(uint32(y)).WriteGString(owner)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SIGNATURE(nickName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SIGNATURE).WriteGString(nickName)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FULLSTOP() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FULLSTOP)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BADDYHURT(baddyId uint32, hurtPower int) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_BADDYHURT).WriteGChar(byte(baddyId)).WriteGChar(byte(hurtPower))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCWEAPONADD(weaponId uint32, image string, owner string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCWEAPONADD).WriteGInt(weaponId).WriteGString(image).WriteGString(owner)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_ZLIBFIXWEAPON() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCWEAPONADD)
	buf.WriteGChar(byte(len("-gr_zlib_fix")))
	buf.Write([]byte("-gr_zlib_fix"))
	buf.WriteGChar(NPCPROP_IMAGE)
	buf.WriteGChar(byte(len("-")))
	buf.WriteByte('-')
	buf.WriteGChar(NPCPROP_SCRIPT)
	buf.WriteGShort(uint16(len(zlibFixScript)))
	buf.Write([]byte(zlibFixScript))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NPCWEAPONDEL(weaponName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCWEAPONDEL).Write([]byte(weaponName))
	p.send(buf)
	return true
}

func (p *Player) sendMissingDefaultWeaponDeletes() {
	p.sendPLO_NPCWEAPONDEL("Bomb")
	p.sendPLO_NPCWEAPONDEL("Bow")
}

func (p *Player) sendWeapon(weapon *Weapon) bool {
	if weapon == nil {
		return false
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCWEAPONADD)
	buf.WriteGChar(byte(len(weapon.name))).Write([]byte(weapon.name))
	if weapon.image != "" {
		buf.WriteGChar(NPCPROP_IMAGE).WriteGChar(byte(len(weapon.image))).Write([]byte(weapon.image))
	}
	if weapon.script != "" {
		buf.WriteGChar(NPCPROP_SCRIPT).WriteGShort(uint16(len(weapon.script))).Write([]byte(weapon.script))
	}
	p.send(buf)
	if len(weapon.bytecode) > 0 {
		p.server.logger.Debug("sendWeapon: sending bytecode for %s (%d bytes)", weapon.name, len(weapon.bytecode))
		p.sendRawNpcWeaponScript(weapon.bytecode)
	}
	return true
}

func (p *Player) sendPLO_RC_ADMINMESSAGE(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_ADMINMESSAGE).Write([]byte(message))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_PUSHAWAY(idx uint16, x, y float32) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_PUSHAWAY).WriteShort(int16(idx)).WriteGString(fmt.Sprintf("%.0f,%.0f", x, y))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LEVELMODTIME(modTime int64) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LEVELMODTIME).WriteGInt5(uint64(modTime))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_NEWWORLDTIME(worldTime uint) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_NEWWORLDTIME).WriteGInt4(uint32(worldTime))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_GHOSTICON(enabled bool) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_GHOSTICON)
	if enabled {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_DEFAULTWEAPON(weaponId byte) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_DEFAULTWEAPON).WriteGChar(weaponId)
	p.send(buf)
	return true
}

func (p *Player) sendAccountWeapon(weaponName string) bool {
	if p == nil || p.server == nil || weaponName == "" {
		return false
	}
	if itemType := getItemId(strings.ToLower(weaponName)); itemType != LevelItemType(-1) {
		if p.server.settings != nil && !p.server.settings.GetBool("defaultweapons", true) {
			return false
		}
		p.server.logger.Debug("Sending default weapon: %s", weaponName)
		return p.sendPLO_DEFAULTWEAPON(byte(itemType))
	}
	weapon := p.server.weapons[weaponName]
	if weapon == nil {
		weapon = p.server.weapons[strings.ToLower(weaponName)]
	}
	if weapon == nil {
		return false
	}
	p.server.logger.Debug("Sending weapon: %s", weaponName)
	return p.sendWeapon(weapon)
}

func (p *Player) sendPLO_HASNPCSERVER() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_HASNPCSERVER)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FILEUPTODATE(filename string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FILEUPTODATE).Write([]byte(filename))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_FILESENDFAILED(filename string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_FILESENDFAILED).Write([]byte(filename))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_HITOBJECTS(objects []string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_HITOBJECTS).WriteGInt(uint32(len(objects)))
	for _, obj := range objects {
		buf.WriteGString(obj)
	}
	p.send(buf)
	return true
}
func (p *Player) sendPLO_CLEARWEAPONS() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_CLEARWEAPONS)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_UNKNOWN190() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_UNKNOWN190)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_BIGMAP() bool {
	buf := NewBuffer()
	value := ""
	if p.server != nil && p.server.settings != nil {
		value = p.server.settings.Get("bigmap")
	}
	buf.WriteByte(PLO_BIGMAP).Write([]byte(normalizedMapSetting(value)))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_MINIMAP() bool {
	buf := NewBuffer()
	value := ""
	if p.server != nil && p.server.settings != nil {
		value = p.server.settings.Get("minimap")
	}
	buf.WriteByte(PLO_MINIMAP).Write([]byte(normalizedMapSetting(value)))
	p.send(buf)
	return true
}

func normalizedMapSetting(value string) string {
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return strings.TrimSpace(value)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}
func (p *Player) sendPLO_ISLEADER() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_ISLEADER)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_LISTPROCESSES() bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_LISTPROCESSES)
	p.send(buf)
	return true
}
func (p *Player) sendPLO_SETACTIVELEVEL(levelName string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SETACTIVELEVEL).Write([]byte(levelName))
	p.send(buf)
	return true
}
func (p *Player) sendPLO_RPGWINDOW(message string) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_RPGWINDOW).Write([]byte(message))
	p.send(buf)
	return true
}

func (p *Player) warp(levelName string, x float64, y float64, clientModTime ...int64) {
	modTime := int64(0)
	if len(clientModTime) > 0 {
		modTime = clientModTime[0]
	}
	cleanLevelName := cleanLevelName(levelName)
	level := p.server.loadLevel(cleanLevelName)
	if level == nil {
		p.server.logger.Error("warp: Failed to load level: %s", cleanLevelName)
		return
	}
	if level.fileVersion == "" {
		paths := []string{
			"world/" + cleanLevelName + ".nw",
			"world/levels/" + cleanLevelName + ".nw",
			"world/" + cleanLevelName + ".zelda",
			"world/levels/" + cleanLevelName + ".zelda",
			"world/" + cleanLevelName + ".graal",
			"world/levels/" + cleanLevelName + ".graal",
		}
		levelLoaded := false
		for _, levelPath := range paths {
			if level.loadLevel(p.server, levelPath) {
				levelLoaded = true
				p.server.logger.Debug("warp: Loaded level from %s", levelPath)
				break
			}
		}
		if !levelLoaded {
			p.server.logger.Warning("warp: Could not load level file for %s, using empty level", cleanLevelName)
		}
	}
	if p.currentLevel != nil {
		p.currentLevel.removePlayer(p)
	}
	p.currentLevel = level
	p.setAccountName(p.accountName)
	p.setX(float32(x))
	p.setY(float32(y))
	p.levelName = levelName
	level.addPlayer(p)
	p.sendPLO_PLAYERWARP(p.x, p.y, p.z, levelName)
	p.sendLevelData(level, levelName, modTime, false, modTime == 0)
	p.loaded = true
	p.server.logger.Debug("warp: Player %s warped to %s at (%.0f, %.0f)", p.accountName, levelName, x, y)
}

func cleanLevelName(levelName string) string {
	levelName = strings.TrimSuffix(levelName, ".nw")
	levelName = strings.TrimSuffix(levelName, ".zelda")
	levelName = strings.TrimSuffix(levelName, ".graal")
	return levelName
}

func (p *Player) sendLevelData(level *Level, levelName string, clientModTime int64, fromAdjacent bool, forceBoard bool) {
	p.sendPLO_LEVELNAME(levelName)
	if forceBoard || clientModTime != level.modTime.Unix() {
		boardData := level.getBoardPacket()
		buf := NewBuffer()
		buf.WriteByte(PLO_RAWDATA)
		buf.WriteGInt(uint32(len(boardData)))
		buf.WriteByte('\n')
		buf.Write(boardData)
		p.sendPacket(buf.Bytes())
	}
	p.sendPLO_LEVELMODTIME(level.modTime.Unix())
	for _, link := range level.links {
		p.sendPLO_LEVELLINK_FULL(link)
	}
	for _, sign := range level.signs {
		p.sendPLO_SIGN(sign)
	}
	if !fromAdjacent {
		p.sendPLO_LEVELBOARDCHANGES(level, time.Time{})
		for _, chest := range level.chests {
			p.sendPLO_LEVELCHEST(chest, p.hasChest(level.getChestKey(chest)))
		}
		for _, horse := range level.horses {
			p.sendPLO_LEVELHORSEADD(horse)
		}
		for _, baddy := range level.baddies {
			p.sendPLO_LEVELBADDYPROPS(baddy)
		}
	}
	p.sendPLO_GHOSTICON(false)
	if !fromAdjacent {
		p.sendPLO_ISLEADER()
	}
	p.sendPLO_NEWWORLDTIME(p.server.serverTime)
	p.sendPLO_SETACTIVELEVEL(levelName)
	for _, npc := range level.npcs {
		p.sendPLO_NPCPROPS(npc)
	}
}
func (p *Player) processTimeout() {
	if p.playerType&PLTYPE_NPCSERVER != 0 {
		return
	}
	if time.Since(p.lastData) > 5*time.Minute {
		p.disconnect()
	}
}

func (p *Player) loginWarpTarget() (string, float64, float64) {
	levelName := p.levelName
	x := float64(p.getX())
	y := float64(p.getY())
	if levelName != "" {
		return levelName, x, y
	}
	if p.server != nil && p.server.settings != nil {
		levelName = p.server.settings.Get("startlevel")
		if levelName == "" {
			levelName = p.server.settings.Get("unstickmelevel")
		}
	}
	if levelName == "" {
		levelName = "onlinestartlocal.nw"
	}
	return levelName, 32, 32
}

func (p *Player) getId() uint16           { return p.id }
func (p *Player) setId(id uint16)         { p.id = id }
func (p *Player) setX(v float32)          { p.Account.x = int16(v * 16) }
func (p *Player) setY(v float32)          { p.Account.y = int16(v * 16) }
func (p *Player) setSprite(v string)      { p.character.bodyImage = v }
func (p *Player) setNickname(v string)    { p.character.nickName = v }
func (p *Player) setAccountName(v string) { p.accountName = v }
func (p *Player) getAccountName() string  { return p.accountName }
func (p *Player) getType() int            { return p.playerType }
func (p *Player) isLoggedIn() bool        { return p.playerType != PLTYPE_AWAIT && p.id > 0 }
func (p *Player) shouldSavePlayerAccount() bool {
	return p.isLoggedIn() && p.playerType&PLTYPE_ANYCLIENT != 0
}
func (p *Player) addWeapon(weaponName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.weaponList {
		if w == weaponName {
			return
		}
	}
	p.weaponList = append(p.weaponList, weaponName)
}
func (p *Player) hasAccountWeapon(weaponName string) bool {
	weaponName = strings.ToLower(strings.TrimSpace(weaponName))
	if weaponName == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.weaponList {
		if strings.ToLower(strings.TrimSpace(w)) == weaponName {
			return true
		}
	}
	return false
}
func (p *Player) hasChest(chestKey string) bool {
	for _, chest := range p.chestList {
		if chest == chestKey {
			return true
		}
	}
	return false
}
func (p *Player) addChest(chestKey string) {
	if chestKey == "" || p.hasChest(chestKey) {
		return
	}
	p.chestList = append(p.chestList, chestKey)
}
func (p *Player) deleteWeapon(weaponName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.weaponList {
		if w == weaponName {
			p.weaponList = append(p.weaponList[:i], p.weaponList[i+1:]...)
			return
		}
	}
}
func (p *Player) setGroup(group string) { p.levelGroup = group }

func (p *Player) applyLevelItem(itemType LevelItemType) bool {
	if getItemName(itemType) == "" {
		return false
	}
	props := getItemPlayerProp(itemType, p)
	if len(props) > 0 {
		p.msgPLI_PLAYERPROPS(append([]byte{PLI_PLAYERPROPS}, props...))
		packet := append([]byte{PLO_PLAYERPROPS}, props...)
		packet = append(packet, '\n')
		p.sendPacket(packet)
	}
	p.SaveAccount()
	return true
}

func (p *Player) msgPLI_LEVELWARP(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	modTime := int64(0)
	if packet[0] == PLI_LEVELWARPMOD {
		modTime = int64(buf.ReadGInt5())
	}
	x, y := float32(buf.ReadGChar())/2, float32(buf.ReadGChar())/2
	levelName := string(buf.ReadBytes(buf.BytesLeft()))
	p.warp(levelName, float64(x), float64(y), modTime)
	return true
}
func (p *Player) msgPLI_BOARDMODIFY(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	x := buf.ReadGChar()
	y := buf.ReadGChar()
	width := buf.ReadGChar()
	height := buf.ReadGChar()
	tileCount := width * height
	tiles := make([]int16, tileCount)
	for i := 0; i < int(tileCount); i++ {
		tiles[i] = int16(buf.ReadGShort())
	}
	if level := p.getCurrentLevel(); level != nil {
		oldTile := level.getTileAt(int(x), int(y))
		if level.alterBoard(p.server, int(x), int(y), int(width), int(height), tiles) {
			p.server.broadcastBoardModify(level, int16(x), int16(y), int16(width), int16(height), tiles)
			p.maybeDropTileItem(level, int(x), int(y), oldTile)
		}
	}
	return true
}

func (p *Player) getCurrentLevel() *Level {
	if p.currentLevel != nil {
		return p.currentLevel
	}
	if p.server == nil || p.levelName == "" {
		return nil
	}
	if level := p.server.GetLevel(p.levelName); level != nil {
		return level
	}
	return p.server.GetLevel(cleanLevelName(p.levelName))
}

func (p *Player) maybeDropTileItem(level *Level, x, y int, oldTile int16) {
	if level == nil || p.server == nil || x < 0 || x > 63 || y < 0 || y > 63 {
		return
	}
	if p.versionId > 0 && p.versionId < 210 {
		return
	}
	itemType := LevelItemType(-1)
	switch oldTile {
	case 2, 0x1a4, 0x1ff, 0x3ff:
		if !p.server.settings.GetBool("bushitems", true) {
			return
		}
		dropRate := p.server.settings.GetInt("tiledroprate", 50)
		if dropRate <= 0 || rand.Intn(100) >= dropRate {
			return
		}
		itemType = LevelItemType(rand.Intn(6))
	case 0x2ac:
		if !p.server.settings.GetBool("vasesdrop", true) {
			return
		}
		itemType = ItemHeart
	default:
		return
	}
	level.addItem(float32(x), float32(y), itemType)
	p.server.broadcastItemAdd(level, int16(x*2), int16(y*2), int(itemType))
}

func (p *Player) msgPLI_REQUESTUPDATEBOARD(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	levelName := buf.ReadGCharString()
	modTime := int64(buf.ReadGInt5())
	x := buf.ReadGShort()
	y := buf.ReadGShort()
	w := buf.ReadGShort()
	h := buf.ReadGShort()
	p.server.logger.Debug("REQUESTUPDATEBOARD level=%s modtime=%d region=%d,%d %dx%d", levelName, modTime, x, y, w, h)
	if level, ok := p.server.levels[levelName]; ok {
		since := time.Unix(modTime, 0)
		for _, change := range level.boardChanges {
			if !change.GetModTime().Before(since) {
				p.sendPBoardPacket(int16(change.x), int16(change.y), int16(change.width), int16(change.height), bytesToShorts(change.newTiles))
			}
		}
	}
	return true
}

// getProp returns property data in the format expected by the client
func (p *Player) getProp(propId int) []byte {
	buf := NewBuffer()
	switch propId {
	case PLPROP_NICKNAME:
		buf.WriteGChar(byte(len(p.character.nickName)))
		buf.data = append(buf.data, p.character.nickName...)
	case PLPROP_MAXPOWER:
		buf.WriteGChar(byte(p.maxHitpoints))
	case PLPROP_CURPOWER:
		buf.WriteGChar(byte(p.character.hitpoints * 2))
	case PLPROP_RUPEESCOUNT:
		buf.WriteGInt(uint32(p.character.gralats))
	case PLPROP_ARROWSCOUNT:
		buf.WriteGChar(byte(p.character.arrows))
	case PLPROP_BOMBSCOUNT:
		buf.WriteGChar(byte(p.character.bombs))
	case PLPROP_GLOVEPOWER:
		buf.WriteGChar(byte(p.character.glovePower))
	case PLPROP_SWORDPOWER:
		buf.WriteGChar(byte(p.character.swordPower + 30))
		buf.WriteGChar(byte(len(p.character.swordImage)))
		buf.data = append(buf.data, p.character.swordImage...)
	case PLPROP_SHIELDPOWER:
		buf.WriteGChar(byte(p.character.shieldPower + 10))
		buf.WriteGChar(byte(len(p.character.shieldImage)))
		buf.data = append(buf.data, p.character.shieldImage...)
	case PLPROP_GANI:
		buf.WriteGChar(byte(len(p.character.gani)))
		buf.data = append(buf.data, p.character.gani...)
	case PLPROP_HEADGIF:
		buf.WriteGChar(byte(len(p.character.headImage) + 100))
		buf.data = append(buf.data, p.character.headImage...)
	case PLPROP_CURCHAT:
		buf.WriteGChar(byte(len(p.character.chatMessage)))
		buf.data = append(buf.data, p.character.chatMessage...)
	case PLPROP_COLORS:
		for i := 0; i < 5; i++ {
			buf.WriteGChar(p.character.colors[i])
		}
	case PLPROP_ID:
		buf.WriteGShort(p.id)
	case PLPROP_X:
		buf.WriteGChar(byte(p.x / 8))
	case PLPROP_Y:
		buf.WriteGChar(byte(p.y / 8))
	case PLPROP_SPRITE:
		buf.WriteGChar(p.character.sprite)
	case PLPROP_STATUS:
		buf.WriteGChar(byte(p.status))
	case PLPROP_CARRYSPRITE:
		buf.WriteByte(p.carrySprite)
	case PLPROP_CURLEVEL:
		buf.WriteGChar(byte(len(p.levelName)))
		buf.data = append(buf.data, p.levelName...)
	case PLPROP_HORSEGIF:
		buf.WriteGChar(byte(len(p.character.horseImage)))
		buf.data = append(buf.data, p.character.horseImage...)
	case PLPROP_HORSEBUSHES:
		buf.WriteGChar(0)
	case PLPROP_EFFECTCOLORS:
		buf.WriteGChar(0)
	case PLPROP_CARRYNPC:
		buf.WriteGInt(0)
	case PLPROP_APCOUNTER:
		buf.WriteGShort(uint16(p.apCounter + 1))
	case PLPROP_MAGICPOINTS:
		buf.WriteGChar(byte(p.mp))
	case PLPROP_KILLSCOUNT:
		buf.WriteGInt(p.kills)
	case PLPROP_DEATHSCOUNT:
		buf.WriteGInt(p.deaths)
	case PLPROP_ONLINESECS:
		buf.WriteGInt(uint32(p.onlineTime))
	case PLPROP_IPADDR:
		buf.WriteGInt5(uint64(p.accountIp))
	case PLPROP_UDPPORT:
		buf.WriteGInt(uint32(p.udpport))
	case PLPROP_ALIGNMENT:
		buf.WriteGChar(byte(p.alignment))
	case PLPROP_ADDITFLAGS:
		buf.WriteGChar(byte(p.additionalFlags))
	case PLPROP_ACCOUNTNAME:
		buf.WriteGChar(byte(len(p.accountName)))
		buf.data = append(buf.data, p.accountName...)
	case PLPROP_BODYIMG:
		buf.WriteGChar(byte(len(p.character.bodyImage)))
		buf.data = append(buf.data, p.character.bodyImage...)
	case PLPROP_RATING:
		rating := ((uint32(p.eloRating) & 0xFFF) << 9) | (uint32(p.eloDeviation) & 0x1FF)
		buf.WriteGInt(rating)
	case PLPROP_GATTRIB1, PLPROP_GATTRIB2, PLPROP_GATTRIB3, PLPROP_GATTRIB4, PLPROP_GATTRIB5:
		idx := propId - PLPROP_GATTRIB1
		if idx < len(p.gAttribs) {
			buf.WriteGChar(byte(len(p.gAttribs[idx])))
			buf.data = append(buf.data, p.gAttribs[idx]...)
		} else {
			buf.WriteGChar(0)
		}
	case PLPROP_JOINLEAVELVL:
		buf.WriteGChar(1)
	case PLPROP_PCONNECTED:
		return buf.Bytes()
	case PLPROP_PLANGUAGE:
		buf.WriteGChar(byte(len(p.language)))
		buf.data = append(buf.data, p.language...)
	case PLPROP_PSTATUSMSG:
		buf.WriteGChar(p.statusMsg)
	case PLPROP_Z:
		z := int(p.z) / 8
		if z < -50 {
			z = -50
		}
		if z > 170 {
			z = 170
		}
		buf.WriteGChar(byte(z + 50))
	case PLPROP_COMMUNITYNAME:
		buf.WriteGChar(byte(len(p.communityName)))
		buf.data = append(buf.data, p.communityName...)
	case PLPROP_OSTYPE:
		buf.WriteGChar(byte(len(p.os)))
		buf.data = append(buf.data, p.os...)
	case PLPROP_TEXTCODEPAGE:
		buf.WriteGInt(uint32(p.envCodePage))
	case PLPROP_X2:
		buf.WriteGShort(encodeSignedGShortCoord(p.x))
	case PLPROP_Y2:
		buf.WriteGShort(encodeSignedGShortCoord(p.y))
	case PLPROP_Z2:
		buf.WriteGShort(encodeSignedGShortCoord(clampInt16(p.z, -25*16, 85*16)))
	case PLPROP_UNKNOWN81:
		return buf.Bytes()
	default:
		// For unimplemented properties, return empty
		buf.WriteGChar(0)
	}
	return buf.Bytes()
}

func encodeSignedGShortCoord(v int16) uint16 {
	val := int32(v)
	if val < 0 {
		return uint16((-val << 1) | 1)
	}
	return uint16(val << 1)
}

func decodeSignedGShortCoord(v uint16) int16 {
	val := int16(v >> 1)
	if v&1 != 0 {
		return -val
	}
	return val
}

func clampInt16(v, min, max int16) int16 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// sendProps sends properties marked as true in the props array
func (p *Player) sendProps(props [PROPCOUNT]bool) {
	buf := NewBuffer()
	for propId := 0; propId < PROPCOUNT; propId++ {
		if props[propId] {
			buf.WriteGChar(byte(propId))
			propData := p.getProp(propId)
			buf.data = append(buf.data, propData...)
		}
	}
	if buf.Len() > 0 {
		p.sendPacket(append([]byte{PLO_PLAYERPROPS}, buf.Bytes()...))
	}
}

// sendPropsWithArray returns properties as byte array without sending
func (p *Player) sendPropsWithArray(props [PROPCOUNT]bool) []byte {
	buf := NewBuffer()
	for propId := 0; propId < PROPCOUNT; propId++ {
		if props[propId] {
			buf.WriteGChar(byte(propId))
			propData := p.getProp(propId)
			buf.data = append(buf.data, propData...)
		}
	}
	return buf.Bytes()
}

func (p *Player) msgPLI_PLAYERPROPS(packet []byte) bool {
	p.server.logger.Debug("msgPLI_PLAYERPROPS: Processing %d bytes from %s", len(packet), p.accountName)
	p.server.logger.Debug("msgPLI_PLAYERPROPS: Raw packet bytes: % X", packet)
	buf := NewBufferFromBytes(packet[1:])
	commonBuff := NewBuffer()
	legacyMoveBuff := NewBuffer()
	preciseMoveBuff := NewBuffer()
	for buf.BytesLeft() > 0 {
		propId := buf.ReadGChar()
		p.server.logger.Debug("msgPLI_PLAYERPROPS: propId=%d", propId)
		switch propId {
		case PLPROP_NICKNAME:
			val := buf.ReadGCharString()
			if val != "" && val != "unknown" {
				p.character.nickName = val
			}
		case PLPROP_MAXPOWER:
			p.maxHitpoints = buf.ReadGChar()
			p.character.hitpoints = int(p.maxHitpoints)
		case PLPROP_CURPOWER:
			p.character.hitpoints = int(buf.ReadGChar()) / 2
		case PLPROP_RUPEESCOUNT:
			p.rupees = buf.ReadGInt()
			p.character.gralats = int(p.rupees)
		case PLPROP_ARROWSCOUNT:
			p.character.arrows = int(buf.ReadGChar())
		case PLPROP_BOMBSCOUNT:
			p.character.bombs = int(buf.ReadGChar())
		case PLPROP_GLOVEPOWER:
			p.character.glovePower = minInt(int(buf.ReadGChar()), 3)
		case PLPROP_BOMBPOWER:
			_ = buf.ReadGChar()
		case PLPROP_SWORDPOWER:
			p.readPlayerPowerImageProp(true, buf)
		case PLPROP_SHIELDPOWER:
			p.readPlayerPowerImageProp(false, buf)
		case PLPROP_GANI:
			p.character.gani = buf.ReadGCharString()
		case PLPROP_BODYIMG:
			p.character.bodyImage = buf.ReadGCharString()
		case PLPROP_HEADGIF:
			length := int(buf.ReadGChar())
			switch {
			case length < 100:
				ext := ".png"
				if p.versionId > 0 && p.versionId < 210 {
					ext = ".gif"
				}
				p.character.headImage = fmt.Sprintf("head%d%s", length, ext)
			case length > 100:
				imageLen := length - 100
				if imageLen > buf.BytesLeft() {
					imageLen = buf.BytesLeft()
				}
				p.character.headImage = string(buf.ReadBytes(imageLen))
			}
		case PLPROP_COLORS:
			for i := 0; i < 5; i++ {
				p.character.colors[i] = buf.ReadGChar()
			}
		case PLPROP_ID:
			_ = buf.ReadGShort()
		case PLPROP_X:
			p.x = int16(buf.ReadGChar()) * 8
		case PLPROP_Y:
			p.y = int16(buf.ReadGChar()) * 8
		case PLPROP_Z:
			p.z = (int16(buf.ReadGChar()) - 50) * 8
		case PLPROP_CURLEVEL:
			p.levelName = buf.ReadGCharString()
		case PLPROP_SPRITE:
			p.character.sprite = buf.ReadGChar()
		case PLPROP_STATUS:
			p.status = int(buf.ReadGChar())
		case PLPROP_CARRYSPRITE:
			p.carrySprite = buf.ReadByte()
		case PLPROP_HORSEGIF:
			p.character.horseImage = buf.ReadGCharString()
		case PLPROP_HORSEBUSHES:
			p.horseBombCount = buf.ReadGChar()
		case PLPROP_EFFECTCOLORS:
			if buf.ReadGChar() > 0 {
				_ = buf.ReadGInt4()
			}
		case PLPROP_CARRYNPC:
			p.carryNpcId = uint(buf.ReadGInt())
		case PLPROP_APCOUNTER:
			p.apCounter = uint8(buf.ReadGShort() & 0xFF)
		case PLPROP_MAGICPOINTS:
			p.mp = minUint8(buf.ReadGChar(), 100)
		case PLPROP_KILLSCOUNT:
			_ = buf.ReadGInt()
		case PLPROP_DEATHSCOUNT:
			_ = buf.ReadGInt()
		case PLPROP_ONLINESECS:
			_ = buf.ReadGInt()
		case PLPROP_IPADDR:
			_ = buf.ReadGInt5()
		case PLPROP_UDPPORT:
			p.udpport = int(buf.ReadGInt())
		case PLPROP_ALIGNMENT:
			p.alignment = minInt(int(buf.ReadGChar()), 100)
		case PLPROP_ADDITFLAGS:
			p.additionalFlags = uint32(buf.ReadGChar())
		case PLPROP_ACCOUNTNAME:
			_ = buf.ReadGCharString()
		case PLPROP_RATING:
			_ = buf.ReadGInt()
		case PLPROP_ATTACHNPC:
			_ = buf.ReadGChar()
			p.attachNPC = buf.ReadGInt()
		case PLPROP_GMAPLEVELX:
			_ = buf.ReadGChar()
		case PLPROP_GMAPLEVELY:
			_ = buf.ReadGChar()
		case PLPROP_JOINLEAVELVL:
			_ = buf.ReadGChar()
		case PLPROP_PCONNECTED:
		case PLPROP_CURCHAT:
			p.character.chatMessage = buf.ReadGCharString()
		case PLPROP_PLANGUAGE:
			p.language = buf.ReadGCharString()
		case PLPROP_PSTATUSMSG:
			p.statusMsg = buf.ReadGChar()
		case PLPROP_GATTRIB1, PLPROP_GATTRIB2, PLPROP_GATTRIB3, PLPROP_GATTRIB4, PLPROP_GATTRIB5:
			p.gAttribs[propId-PLPROP_GATTRIB1] = buf.ReadGCharString()
		case PLPROP_GATTRIB6, PLPROP_GATTRIB7, PLPROP_GATTRIB8, PLPROP_GATTRIB9:
			p.gAttribs[propId-PLPROP_GATTRIB6+5] = buf.ReadGCharString()
		case PLPROP_GATTRIB10, PLPROP_GATTRIB11, PLPROP_GATTRIB12, PLPROP_GATTRIB13, PLPROP_GATTRIB14,
			PLPROP_GATTRIB15, PLPROP_GATTRIB16, PLPROP_GATTRIB17, PLPROP_GATTRIB18, PLPROP_GATTRIB19,
			PLPROP_GATTRIB20, PLPROP_GATTRIB21, PLPROP_GATTRIB22, PLPROP_GATTRIB23, PLPROP_GATTRIB24,
			PLPROP_GATTRIB25, PLPROP_GATTRIB26, PLPROP_GATTRIB27, PLPROP_GATTRIB28, PLPROP_GATTRIB29,
			PLPROP_GATTRIB30:
			p.gAttribs[propId-PLPROP_GATTRIB10+9] = buf.ReadGCharString()
		case PLPROP_OSTYPE:
			p.os = buf.ReadGCharString()
		case PLPROP_TEXTCODEPAGE:
			p.envCodePage = int(buf.ReadGInt())
		case PLPROP_UNKNOWN77:
			_ = buf.ReadGChar()
		case PLPROP_X2:
			p.x = decodeSignedGShortCoord(buf.ReadGShort())
		case PLPROP_Y2:
			p.y = decodeSignedGShortCoord(buf.ReadGShort())
		case PLPROP_Z2:
			p.z = decodeSignedGShortCoord(buf.ReadGShort())
		case PLPROP_UNKNOWN81:
			_ = buf.ReadGChar()
		case PLPROP_COMMUNITYNAME:
			p.communityName = buf.ReadGCharString()
		default:
			p.server.logger.Debug("msgPLI_PLAYERPROPS: unhandled propId=%d, stopping to avoid stream desync", propId)
			return true
		}
		p.appendPlayerPropDelta(int(propId), commonBuff, legacyMoveBuff, preciseMoveBuff)
	}
	if p.isLoggedIn() && p.loaded && (commonBuff.Len() > 0 || legacyMoveBuff.Len() > 0 || preciseMoveBuff.Len() > 0) {
		p.server.logger.Debug("msgPLI_PLAYERPROPS: Forwarding changed props common=%d legacyMove=%d preciseMove=%d to level %s", commonBuff.Len(), legacyMoveBuff.Len(), preciseMoveBuff.Len(), p.levelName)
		p.sendPlayerPropDeltasToCurrentLevel(commonBuff.Bytes(), legacyMoveBuff.Bytes(), preciseMoveBuff.Bytes())
	}
	p.server.logger.Debug("msgPLI_PLAYERPROPS: Done processing")
	return true
}

func (p *Player) appendPlayerPropDelta(propId int, commonBuff, legacyMoveBuff, preciseMoveBuff *Buffer) {
	if propId < 0 || propId >= len(sendLocalProps) || !sendLocalProps[propId] {
		return
	}
	switch propId {
	case PLPROP_X:
		legacyMoveBuff.WriteGChar(PLPROP_X)
		legacyMoveBuff.Write(p.getProp(PLPROP_X))
		preciseMoveBuff.WriteGChar(PLPROP_X2)
		preciseMoveBuff.Write(p.getProp(PLPROP_X2))
	case PLPROP_Y:
		legacyMoveBuff.WriteGChar(PLPROP_Y)
		legacyMoveBuff.Write(p.getProp(PLPROP_Y))
		preciseMoveBuff.WriteGChar(PLPROP_Y2)
		preciseMoveBuff.Write(p.getProp(PLPROP_Y2))
	case PLPROP_Z:
		legacyMoveBuff.WriteGChar(PLPROP_Z)
		legacyMoveBuff.Write(p.getProp(PLPROP_Z))
		preciseMoveBuff.WriteGChar(PLPROP_Z2)
		preciseMoveBuff.Write(p.getProp(PLPROP_Z2))
	case PLPROP_X2:
		preciseMoveBuff.WriteGChar(PLPROP_X2)
		preciseMoveBuff.Write(p.getProp(PLPROP_X2))
		legacyMoveBuff.WriteGChar(PLPROP_X)
		legacyMoveBuff.Write(p.getProp(PLPROP_X))
	case PLPROP_Y2:
		preciseMoveBuff.WriteGChar(PLPROP_Y2)
		preciseMoveBuff.Write(p.getProp(PLPROP_Y2))
		legacyMoveBuff.WriteGChar(PLPROP_Y)
		legacyMoveBuff.Write(p.getProp(PLPROP_Y))
	case PLPROP_Z2:
		preciseMoveBuff.WriteGChar(PLPROP_Z2)
		preciseMoveBuff.Write(p.getProp(PLPROP_Z2))
		legacyMoveBuff.WriteGChar(PLPROP_Z)
		legacyMoveBuff.Write(p.getProp(PLPROP_Z))
	default:
		commonBuff.WriteGChar(byte(propId))
		commonBuff.Write(p.getProp(propId))
	}
}

func (p *Player) sendPlayerPropDeltasToCurrentLevel(common, legacyMove, preciseMove []byte) {
	level := p.currentLevel
	if level == nil && p.server != nil && p.levelName != "" {
		level = p.server.GetLevel(cleanLevelName(p.levelName))
	}
	if level == nil || p.server == nil {
		return
	}
	for _, plId := range level.getPlayers() {
		if plId == p.id {
			continue
		}
		pl, ok := p.server.players[plId]
		if !ok || pl == nil || pl.conn == nil {
			continue
		}
		moveProps := legacyMove
		if playerSupportsPreciseMovement(pl) {
			moveProps = preciseMove
		}
		if len(common) == 0 && len(moveProps) == 0 {
			continue
		}
		out := NewBuffer()
		out.WriteByte(PLO_OTHERPLPROPS).WriteGShort(p.id)
		out.Write(common)
		out.Write(moveProps)
		pl.sendPacket(append(out.Bytes(), '\n'))
	}
}

func playerSupportsPreciseMovement(p *Player) bool {
	return p != nil && p.versionId >= 230
}

func (p *Player) readPlayerPowerImageProp(sword bool, buf *Buffer) {
	power := int(buf.ReadGChar())
	if sword {
		if power <= 4 {
			p.character.swordPower = power
			p.character.swordImage = fmt.Sprintf("sword%d.png", power)
			return
		}
		p.character.swordPower = power - 30
		p.character.swordImage = buf.ReadGCharString()
		return
	}

	if power <= 3 {
		p.character.shieldPower = power
		p.character.shieldImage = fmt.Sprintf("shield%d.png", power)
		return
	}
	p.character.shieldPower = power - 10
	p.character.shieldImage = buf.ReadGCharString()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minUint8(a, b uint8) uint8 {
	if a < b {
		return a
	}
	return b
}
func (p *Player) msgPLI_NPCPROPS(packet []byte) bool {
	if p.server != nil && p.server.settings != nil && p.server.settings.GetBool("serverside", false) {
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	if buf.BytesLeft() < 3 {
		return true
	}
	npcId := buf.ReadGInt()
	props := buf.ReadBytes(buf.BytesLeft())
	level := p.currentLevel
	if level == nil && p.server != nil {
		level = p.server.GetLevel(cleanLevelName(p.levelName))
	}
	if level == nil {
		return true
	}
	npc, ok := level.npcs[npcId]
	if !ok || npc == nil {
		return true
	}
	out := NewBuffer()
	out.WriteByte(PLO_NPCPROPS).WriteGInt(npcId).Write(props)
	for _, plId := range level.getPlayers() {
		if plId == p.id {
			continue
		}
		if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
			pl.send(out)
		}
	}
	return true
}
func (p *Player) msgPLI_BOMBADD(packet []byte) bool {
	out := NewBuffer()
	out.WriteByte(PLO_BOMBADD).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_BOMBDEL(packet []byte) bool {
	if len(packet) > 1 {
		out := NewBuffer()
		out.WriteByte(PLO_BOMBDEL).Write(packet[1:])
		p.sendToCurrentLevelExceptSelf(out.Bytes())
	}
	return true
}
func (p *Player) msgPLI_TOALL(packet []byte) bool {
	if len(packet) > 1 {
		buf := NewBufferFromBytes(packet[1:])
		msg := buf.ReadGCharString()
		p.lastChat = time.Now()
		p.sendPTO_ALL_CHAT(msg)
	}
	return true
}
func (p *Player) msgPLI_HORSEADD(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	x := buf.ReadByte()
	y := buf.ReadByte()
	dirBush := buf.ReadByte()
	image := string(buf.ReadBytes(buf.BytesLeft()))
	if level, ok := p.server.levels[p.levelName]; ok {
		level.horses = append(level.horses, LevelHorse{
			x:      float32(x) / 2,
			y:      float32(y) / 2,
			dir:    dirBush & 0x03,
			bushes: dirBush >> 2,
			image:  image,
		})
	}
	out := NewBuffer()
	out.WriteByte(PLO_HORSEADD).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_HORSEDEL(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	x := float32(buf.ReadByte()) / 2
	y := float32(buf.ReadByte()) / 2
	if level, ok := p.server.levels[p.levelName]; ok {
		for i, horse := range level.horses {
			if horse.x == x && horse.y == y {
				level.horses = append(level.horses[:i], level.horses[i+1:]...)
				break
			}
		}
	}
	out := NewBuffer()
	out.WriteByte(PLO_HORSEDEL).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_ARROWADD(packet []byte) bool {
	out := NewBuffer()
	out.WriteByte(PLO_ARROWADD).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_FIRESPY(packet []byte) bool {
	out := NewBuffer()
	out.WriteByte(PLO_FIRESPY).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_THROWCARRIED(packet []byte) bool {
	out := NewBuffer()
	out.WriteByte(PLO_THROWCARRIED).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_ITEMADD(packet []byte) bool {
	if len(packet) >= 4 {
		buf := NewBufferFromBytes(packet[1:])
		x := float32(buf.ReadGChar())
		y := float32(buf.ReadGChar())
		itemType := LevelItemType(buf.ReadGChar())
		if level := p.getCurrentLevel(); level != nil {
			level.addItem(x, y, itemType)
		}
	}
	out := NewBuffer()
	out.WriteByte(PLO_ITEMADD).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_ITEMDEL(packet []byte) bool {
	var itemType LevelItemType = -1
	if len(packet) >= 3 {
		buf := NewBufferFromBytes(packet[1:])
		x := float32(buf.ReadGChar())
		y := float32(buf.ReadGChar())
		if level := p.getCurrentLevel(); level != nil {
			itemType = level.removeItem(x, y)
			if itemType == LevelItemType(-1) {
				itemType = level.removeItem(x/2, y/2)
			}
		}
	}
	out := NewBuffer()
	out.WriteByte(PLO_ITEMDEL).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	if packet[0] == PLI_ITEMTAKE && itemType != LevelItemType(-1) {
		p.applyLevelItem(itemType)
	}
	return true
}
func (p *Player) msgPLI_CLAIMPKER(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	pkerId := buf.ReadGShort()
	if pl, ok := p.server.players[uint16(pkerId)]; ok {
		pl.SetFlag("killer", p.accountName)
	}
	return true
}
func (p *Player) msgPLI_BADDYPROPS(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	baddyId := buf.ReadGChar()
	props := string(buf.ReadBytes(buf.BytesLeft()))
	if level, ok := p.server.levels[p.levelName]; ok {
		if baddy, ok := level.baddies[baddyId]; ok {
			baddy.setProps([]byte(props))
		}
		for _, plId := range level.players {
			if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
				pl.sendPLO_BADDYPROPS(uint32(baddyId), 0, 0, "", []string{props})
			}
		}
	}
	return true
}
func (p *Player) msgPLI_BADDYHURT(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	baddyId := buf.ReadGChar()
	hurtPower := buf.ReadGChar()
	if level, ok := p.server.levels[p.levelName]; ok {
		for _, plId := range level.players {
			if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
				pl.sendPLO_BADDYHURT(uint32(baddyId), int(hurtPower))
			}
		}
	}
	return true
}
func (p *Player) msgPLI_BADDYADD(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	x := float32(buf.ReadGChar())
	y := float32(buf.ReadGChar())
	baddyType := buf.ReadGChar()
	baddyPower := buf.ReadGChar()
	baddyImage := string(buf.ReadBytes(buf.BytesLeft()))
	if level, ok := p.server.levels[p.levelName]; ok {
		baddy := NewLevelBaddy(x/2, y/2, uint8(baddyType), level, p.server)
		baddy.id = uint8(len(level.baddies))
		if baddyPower > 0 || baddyImage != "" {
			props := NewBuffer()
			props.WriteGChar(BDPROP_POWERIMAGE).WriteGChar(baddyPower).WriteGChar(byte(len(baddyImage))).Write([]byte(baddyImage))
			baddy.setProps(props.Bytes())
		}
		level.baddies[baddy.id] = baddy
		for _, plId := range level.players {
			if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
				pl.sendPLO_LEVELBADDYPROPS(baddy)
			}
		}
	}
	return true
}
func (p *Player) msgPLI_FLAGSET(packet []byte) bool {
	if len(packet) > 1 {
		parts := strings.SplitN(string(packet[1:]), "=", 2)
		if len(parts) == 2 {
			p.SetFlag(parts[0], parts[1])
		} else {
			p.SetFlag(parts[0], "")
		}
	}
	return true
}
func (p *Player) msgPLI_FLAGDEL(packet []byte) bool {
	if len(packet) > 1 {
		p.DeleteFlag(string(packet[1:]))
	}
	return true
}
func (p *Player) msgPLI_OPENCHEST(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	x := int(buf.ReadGChar())
	y := int(buf.ReadGChar())
	if level := p.getCurrentLevel(); level != nil {
		for _, chest := range level.chests {
			if chest.x == x && chest.y == y {
				chestKey := level.getChestKey(chest)
				if !p.hasChest(chestKey) {
					p.applyLevelItem(chest.itemType)
					p.sendPLO_LEVELCHEST(chest, true)
					p.addChest(chestKey)
					p.SaveAccount()
				}
				break
			}
		}
	}
	return true
}
func (p *Player) msgPLI_PUTNPC(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	image := buf.ReadGCharString()
	npcCode := buf.ReadGCharString()
	x := float32(buf.ReadGChar()) / 2
	y := float32(buf.ReadGChar()) / 2
	if level, ok := p.server.levels[p.levelName]; ok {
		npc := NewNPC(PUTNPC)
		npc.image = image
		npc.x, npc.y = int16(x*16), int16(y*16)
		npc.script = npcCode
		npc.level = level
		level.npcs[npc.id] = npc
		p.sendPLO_NPCPROPS(npc)
	}
	return true
}
func (p *Player) msgPLI_NPCDEL(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	if level, ok := p.server.levels[p.levelName]; ok {
		if _, npcOk := level.npcs[npcId]; npcOk {
			delete(level.npcs, npcId)
			p.sendPLO_NPCDEL(npcId)
		}
	}
	return true
}
func (p *Player) msgPLI_WANTFILE(packet []byte) bool {
	if len(packet) > 1 {
		fileName := string(packet[1:])
		p.server.logger.Debug("WANTFILE: %s", fileName)
		p.sendFile(fileName)
	}
	return true
}
func (p *Player) msgPLI_SHOWIMG(packet []byte) bool {
	buf := NewBuffer()
	buf.WriteByte(PLO_SHOWIMG).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(buf.Bytes())
	return true
}
func (p *Player) msgPLI_HURTPLAYER(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	victimId := buf.ReadGShort()
	hurtDx := buf.ReadGChar()
	hurtDy := buf.ReadGChar()
	power := buf.ReadGChar()
	npcId := buf.ReadGInt()
	if victim, ok := p.server.players[victimId]; ok && victim.conn != nil {
		out := NewBuffer()
		out.WriteByte(PLO_HURTPLAYER)
		out.WriteGShort(p.id)
		out.WriteGChar(hurtDx)
		out.WriteGChar(hurtDy)
		out.WriteGChar(power)
		out.WriteGInt(npcId)
		victim.send(out)
	}
	return true
}
func (p *Player) msgPLI_EXPLOSION(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	radius := buf.ReadGChar()
	x := buf.ReadGChar()
	y := buf.ReadGChar()
	power := buf.ReadGChar()
	out := NewBuffer()
	out.WriteByte(PLO_EXPLOSION).WriteGShort(p.id).WriteGChar(radius).WriteGChar(x).WriteGChar(y).WriteGChar(power)
	p.send(out)
	return true
}
func (p *Player) msgPLI_PRIVATEMESSAGE(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	targetCount := int(buf.ReadGShort())
	targets := make([]uint16, 0, targetCount)
	for i := 0; i < targetCount; i++ {
		targets = append(targets, buf.ReadGShort())
	}
	msg := string(buf.ReadBytes(buf.BytesLeft()))
	msgType := "\"Private message:\","
	if targetCount > 1 {
		msgType = "\"Mass message:\","
	}
	for _, targetId := range targets {
		if pl, ok := p.server.players[targetId]; ok && pl != p {
			if pl.playerType&PLTYPE_NPCSERVER != 0 {
				p.sendNPCServerPMFallback(pl)
				continue
			}
			if pl.conn == nil {
				continue
			}
			out := NewBuffer()
			out.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(p.id).Write([]byte("\"\",")).Write([]byte(msgType)).Write([]byte(msg))
			pl.send(out)
		}
	}
	return true
}

func (p *Player) sendNPCServerPMFallback(npcServer *Player) bool {
	if p == nil || npcServer == nil {
		return false
	}
	msg := "I am the npcserver for\nthis game server. Almost\nall npc actions are controlled\nby me."
	buf := NewBuffer()
	buf.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(npcServer.id).Write([]byte("\"\","))
	buf.Write([]byte(gtokenizeText(msg)))
	p.send(buf)
	return true
}

func (p *Player) msgPLI_NPCWEAPONDEL(packet []byte) bool {
	if len(packet) > 1 {
		p.deleteWeapon(string(packet[1:]))
	}
	return true
}
func (p *Player) msgPLI_PACKETCOUNT(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	_ = buf.ReadGShort()
	p.packetCount = 0
	return true
}
func (p *Player) msgPLI_WEAPONADD(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	weaponType := buf.ReadGChar()
	if weaponType == 0 {
		itemType := LevelItemType(buf.ReadGChar())
		if name := getItemName(itemType); name != "" {
			p.addWeapon(name)
		}
		return true
	}
	npcId := buf.ReadGInt()
	if npc, ok := p.server.npcs[npcId]; ok && npc.weaponName != "" {
		p.addWeapon(npc.weaponName)
	}
	return true
}
func (p *Player) msgPLI_UPDATEFILE(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	modTime := int64(buf.ReadGInt5())
	fileName := string(buf.ReadBytes(buf.BytesLeft()))
	p.server.logger.Debug("UPDATEFILE request: %s (client modtime %d)", fileName, modTime)
	if fileName == "" {
		return true
	}
	serverModTime := time.Time{}
	if p.server != nil && p.server.config != nil {
		serverModTime, _ = p.server.config.FileModTime(fileName)
	}
	if serverModTime.IsZero() || serverModTime.Unix() != modTime {
		p.sendFile(fileName)
	} else {
		p.sendPLO_FILEUPTODATE(fileName)
	}
	return true
}
func (p *Player) msgPLI_HITOBJECTS(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	if buf.BytesLeft() < 3 {
		return true
	}
	power := buf.ReadGChar()
	x := buf.ReadGChar()
	y := buf.ReadGChar()
	var npcId uint32
	fromNpc := false
	if buf.BytesLeft() > 0 {
		npcId = buf.ReadGInt()
		fromNpc = true
	}
	out := NewBuffer()
	out.WriteByte(PLO_HITOBJECTS)
	if fromNpc {
		out.WriteGShort(0)
	} else {
		out.WriteGShort(p.id)
	}
	out.WriteGChar(power).WriteGChar(x).WriteGChar(y)
	if fromNpc {
		out.WriteGInt(npcId)
	}
	level := p.currentLevel
	if level == nil && p.server != nil {
		level = p.server.GetLevel(cleanLevelName(p.levelName))
	}
	if level == nil {
		return true
	}
	for _, plId := range level.getPlayers() {
		if plId == p.id {
			continue
		}
		if pl, ok := p.server.players[plId]; ok && pl.conn != nil {
			pl.send(out)
		}
	}
	return true
}
func (p *Player) msgPLI_LANGUAGE(packet []byte) bool {
	if len(packet) > 1 {
		p.language = string(packet[1:])
	}
	return true
}
func (p *Player) msgPLI_TRIGGERACTION(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	x := buf.ReadGChar()
	y := buf.ReadGChar()
	action := strings.TrimSpace(string(buf.ReadBytes(buf.BytesLeft())))
	p.server.logger.Debug("TRIGGERACTION npc=%d at %d,%d: %s", npcId, x, y, action)
	parts := strings.Split(action, ",")
	if len(parts) == 0 {
		return true
	}
	command := parts[0]
	if p.server.handleTriggerCommand(p, command, parts) {
		return true
	}
	if level, ok := p.server.levels[p.levelName]; ok {
		for _, npc := range level.npcs {
			if npc.script == action {
				npc.timeout = 0
				break
			}
		}
	}
	out := NewBuffer()
	out.WriteByte(PLO_TRIGGERACTION).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_MAPINFO(packet []byte) bool {
	p.server.logger.Debug("MAPINFO request payload: % X", packet[1:])
	return true
}
func (p *Player) msgPLI_ADJACENTLEVEL(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	modTime := int64(buf.ReadGInt5())
	levelName := string(buf.ReadBytes(buf.BytesLeft()))
	p.server.logger.Debug("ADJACENTLEVEL request: %s (client modtime %d)", levelName, modTime)
	level := p.server.loadLevel(cleanLevelName(levelName))
	if level == nil {
		return true
	}
	p.sendLevelData(level, levelName, modTime, true, false)
	if p.levelName != "" {
		p.sendPLO_LEVELNAME(p.levelName)
	}
	return true
}
func (p *Player) msgPLI_SHOOT(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	_ = buf.ReadGInt()
	x := buf.ReadGChar()
	y := buf.ReadGChar()
	z := buf.ReadGChar()
	angle := buf.ReadGChar()
	zAngle := buf.ReadGChar()
	speed := buf.ReadGChar()
	gani := buf.ReadGCharString()
	shootParamsLen := buf.ReadGChar()
	shootParams := string(buf.ReadBytes(buf.BytesLeft()))
	if int(shootParamsLen) < len(shootParams) {
		shootParams = shootParams[:shootParamsLen]
	}
	out := NewBuffer()
	out.WriteByte(PLO_SHOOT).WriteGShort(p.id).WriteGInt(0)
	out.WriteGChar(x).WriteGChar(y).WriteGChar(z).WriteGChar(angle).WriteGChar(zAngle).WriteGChar(speed)
	out.WriteGChar(byte(len(gani))).Write([]byte(gani))
	out.WriteGChar(byte(len(shootParams))).Write([]byte(shootParams))
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_SHOOT2(packet []byte) bool {
	out := NewBuffer()
	out.WriteByte(PLO_SHOOT2).WriteGShort(p.id).Write(packet[1:])
	p.sendToCurrentLevelExceptSelf(out.Bytes())
	return true
}
func (p *Player) msgPLI_SERVERWARP(packet []byte) bool {
	serverName := string(packet[1:])
	p.server.logger.Debug("SERVERWARP to %s", serverName)
	return true
}
func (p *Player) msgPLI_PROCESSLIST(packet []byte) bool {
	p.server.logger.Debug("PROCESSLIST packet")
	return true
}
func (p *Player) msgPLI_UNKNOWN46(packet []byte) bool { return true }
func (p *Player) msgPLI_VERIFYWANTSEND(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	fileChecksum := buf.ReadGInt5()
	fileName := buf.ReadGString()
	if fileName == "" {
		return true
	}
	ignoreChecksum := strings.HasSuffix(strings.ToLower(fileName), ".gupd")
	if !ignoreChecksum {
		fileData, err := p.server.config.LoadFile(fileName)
		if err == nil && len(fileData) > 0 {
			checksum := calculateCrc32Checksum(fileData)
			if checksum == uint32(fileChecksum) {
				p.sendPLO_FILEUPTODATE(fileName)
				return true
			}
		}
	}
	p.sendFile(fileName)
	return true
}
func (p *Player) msgPLI_UPDATECLASS(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	_ = buf.ReadGInt5()
	className := string(buf.ReadBytes(buf.BytesLeft()))
	p.server.logger.Debug("UPDATECLASS: %s", className)
	p.server.weaponMu.RLock()
	classObj, exists := p.server.classes[className]
	p.server.weaponMu.RUnlock()
	if exists {
		classCode := classObj.script
		p.sendRawNpcWeaponScript([]byte(classCode))
	} else {
		headerData := []string{"class", className, "1", "0", "0", "0"}
		classCode := strings.Join(headerData, " ")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NPCWEAPONSCRIPT)
		buf2.WriteShort(int16(len(classCode)))
		buf2.Write([]byte(classCode))
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_RAWDATA(packet []byte) bool {
	p.server.logger.Debug("RAWDATA packet")
	return true
}
func (p *Player) msgPLI_RC_SERVEROPTIONSGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted SERVEROPTIONSGET (non-RC)", p.accountName)
		return true
	}
	settingsData, err := p.server.config.LoadFile("config/serveroptions.txt")
	settingsStr := string(settingsData)
	if err != nil {
		settings := p.server.settings.GetAll()
		for key, value := range settings {
			settingsStr += key + "=" + value + "\n"
		}
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_SERVEROPTIONSGET)
	buf.Write([]byte(gtokenizeText(settingsStr)))
	p.send(buf)
	return true
}
func (p *Player) msgPLI_RC_SERVEROPTIONSSET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted SERVEROPTIONSSET (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_SETSERVEROPTIONS) {
		p.server.logger.Warning("%s attempted SERVEROPTIONSSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to change the server options.")))
		return true
	}
	options := ""
	if len(packet) > 1 {
		options = guntokenizeText(string(packet[1:]))
	}
	adminOptions := []string{"name", "description", "url", "serverip", "serverport", "localip", "listip", "listport", "maxplayers", "onlystaff", "nofoldersconfig", "oldcreated", "serverside", "triggerhack_weapons", "triggerhack_guilds", "triggerhack_groups", "triggerhack_files", "triggerhack_rc", "flaghack_movement", "flaghack_ip", "sharefolder", "language"}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		var filteredOptions []string
		for _, line := range strings.Split(options, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			optionName := strings.TrimSpace(parts[0])
			isAdmin := false
			for _, admin := range adminOptions {
				if optionName == admin {
					isAdmin = true
					break
				}
			}
			if isAdmin {
				if currentVal := p.server.settings.Get(optionName); currentVal != "" {
					filteredOptions = append(filteredOptions, optionName+" = "+currentVal)
				}
			} else {
				filteredOptions = append(filteredOptions, line)
			}
		}
		options = strings.Join(filteredOptions, "\n")
	}
	p.server.settings.LoadFromString(options)
	if !strings.HasSuffix(options, "\n") {
		options += "\n"
	}
	if err := p.server.config.SaveFile("config/serveroptions.txt", []byte(options)); err != nil {
		p.server.logger.Error("Failed to save serveroptions.txt: %v", err)
		return true
	}
	p.server.loadSettings()
	p.server.logger.Info("%s has updated the server options.", p.accountName)
	p.server.sendRCChat(p.accountName + " has updated the server options.")
	return true
}
func (p *Player) msgPLI_RC_FOLDERCONFIGGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FOLDERCONFIGGET (non-RC)", p.accountName)
		return true
	}
	foldersConfigData, err := p.server.config.LoadFile("config/foldersconfig.txt")
	if err != nil {
		foldersConfigData = []byte{}
	}
	foldersConfig := string(foldersConfigData)
	foldersConfig = strings.ReplaceAll(foldersConfig, "\r\n", "\n")
	foldersConfig = strings.ReplaceAll(foldersConfig, "\r", "\n")
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FOLDERCONFIGGET)
	buf.Write([]byte(gtokenizeText(foldersConfig)))
	p.send(buf)
	return true
}
func (p *Player) msgPLI_RC_FOLDERCONFIGSET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FOLDERCONFIGSET (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_SETFOLDEROPTIONS) {
		p.server.logger.Warning("%s attempted FOLDERCONFIGSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to change the folder config.")))
		return true
	}
	folders := ""
	if len(packet) > 1 {
		folders = guntokenizeText(string(packet[1:]))
	}
	folders = strings.ReplaceAll(folders, "\\", "")
	folders = strings.ReplaceAll(folders, "\n", "\r\n")
	if err := p.server.config.SaveFile("config/foldersconfig.txt", []byte(folders)); err != nil {
		p.server.logger.Error("Failed to save foldersconfig.txt: %v", err)
		return true
	}
	p.server.loadFileSystem()
	p.server.logger.Info("%s updated folder config", p.accountName)
	p.server.sendRCChat(p.accountName + " updated the folder config.")
	return true
}
func (p *Player) msgPLI_RC_RESPAWNSET(packet []byte) bool {
	p.server.logger.Debug("RC_RESPAWNSET")
	return true
}
func (p *Player) msgPLI_RC_HORSELIFESET(packet []byte) bool {
	p.server.logger.Debug("RC_HORSELIFESET")
	return true
}
func (p *Player) msgPLI_RC_APINCREMENTSET(packet []byte) bool {
	p.server.logger.Debug("RC_APINCREMENTSET")
	return true
}
func (p *Player) msgPLI_RC_BADDYRESPAWNSET(packet []byte) bool {
	p.server.logger.Debug("RC_BADDYRESPAWNSET")
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSGET(packet []byte) bool {
	p.server.logger.Debug("RC_PLAYERPROPSGET")
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSSET(packet []byte) bool {
	p.server.logger.Debug("RC_PLAYERPROPSSET")
	return true
}
func (p *Player) msgPLI_RC_DISCONNECTPLAYER(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	playerId := buf.ReadGShort()
	targetPlayer := p.server.getPlayerById(uint16(playerId))
	if targetPlayer == nil {
		return true
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted DISCONNECTPLAYER (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_DISCONNECT) {
		p.server.logger.Warning("%s attempted DISCONNECTPLAYER without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to disconnect players.")))
		return true
	}
	reason := buf.ReadGString()
	disconnectMessage := "One of the server administrators, " + p.accountName + ", has disconnected you"
	if reason != "" {
		disconnectMessage += " for the following reason: " + reason
		p.server.logger.Info("%s disconnected %s: %s", p.accountName, targetPlayer.accountName, reason)
	} else {
		disconnectMessage += "."
		p.server.logger.Info("%s disconnected %s", p.accountName, targetPlayer.accountName)
	}
	p.server.sendRCChat(p.accountName + " disconnected " + targetPlayer.accountName)
	targetPlayer.sendPacket([]byte{PLO_DISCMESSAGE, 0})
	targetPlayer.writeString8(disconnectMessage)
	p.server.removePlayer(targetPlayer)
	return true
}
func (p *Player) msgPLI_RC_UPDATELEVELS(packet []byte) bool {
	p.server.logger.Debug("RC_UPDATELEVELS")
	return true
}
func (p *Player) msgPLI_RC_ADMINMESSAGE(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ADMINMESSAGE (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_ADMINMSG) {
		p.server.logger.Warning("%s attempted ADMINMESSAGE without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to send an admin message.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	message := buf.ReadString()
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_ADMINMESSAGE)
	buf2.WriteString8("Admin " + p.accountName + ":\xa7" + message)
	p.server.sendPacketToAll(buf2.Bytes(), p.id)
	p.server.logger.Info("[ADMINMSG] %s: %s", p.accountName, message)
	return true
}
func (p *Player) msgPLI_RC_PRIVADMINMESSAGE(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PRIVADMINMESSAGE (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_ADMINMSG) {
		p.server.logger.Warning("%s attempted PRIVADMINMESSAGE without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to send an admin message.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	playerId := buf.ReadGShort()
	targetPlayer := p.server.getPlayerById(uint16(playerId))
	if targetPlayer == nil {
		return true
	}
	message := buf.ReadString()
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_ADMINMESSAGE)
	buf2.WriteString8("Admin " + p.accountName + ":\xa7" + message)
	targetPlayer.send(buf2)
	p.server.logger.Info("[PRIVADMINMSG] %s -> %s: %s", p.accountName, targetPlayer.accountName, message)
	return true
}
func (p *Player) msgPLI_RC_LISTRCS(packet []byte) bool {
	p.server.logger.Debug("RC_LISTRCS")
	return true
}
func (p *Player) msgPLI_RC_DISCONNECTRC(packet []byte) bool {
	p.server.logger.Debug("RC_DISCONNECTRC")
	return true
}
func (p *Player) msgPLI_RC_APPLYREASON(packet []byte) bool {
	p.server.logger.Debug("RC_APPLYREASON")
	return true
}
func (p *Player) msgPLI_RC_SERVERFLAGSGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted SERVERFLAGSGET (non-RC)", p.accountName)
		return true
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_SERVERFLAGSGET)
	p.server.flagMu.RLock()
	validFlags := make(map[string]string)
	for flag, value := range p.server.flags {
		if !isValidServerFlag(flag, value) {
			continue
		}
		validFlags[flag] = value
	}
	buf.WriteShort(int16(len(validFlags)))
	for flag, value := range validFlags {
		flagStr := flag + "=" + value
		buf.WriteString8(flagStr)
	}
	p.server.flagMu.RUnlock()
	p.send(buf)
	return true
}
func (p *Player) msgPLI_RC_SERVERFLAGSSET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted SERVERFLAGSSET (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_SETSERVERFLAGS) {
		p.server.logger.Warning("%s attempted SERVERFLAGSSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to set the server flags.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	count := buf.ReadGShort()
	p.server.flagMu.Lock()
	oldFlags := make(map[string]string)
	for k, v := range p.server.flags {
		oldFlags[k] = v
	}
	p.server.flags = make(map[string]string)
	for i := 0; i < int(count); i++ {
		flagLen := buf.ReadByte()
		flagBytes := make([]byte, flagLen)
		for j := range flagBytes {
			flagBytes[j] = buf.ReadByte()
		}
		flagStr := string(flagBytes)
		if idx := strings.Index(flagStr, "="); idx != -1 {
			name := trimSpace(flagStr[:idx])
			value := flagStr[idx+1:]
			if isValidServerFlag(name, value) {
				p.server.flags[name] = value
			} else {
				p.server.logger.Warning("Ignoring malformed server flag from RC: %q", flagStr)
			}
		} else if isValidServerFlag(flagStr, "") {
			p.server.flags[flagStr] = ""
		} else {
			p.server.logger.Warning("Ignoring malformed server flag from RC: %q", flagStr)
		}
	}
	p.server.flagMu.Unlock()
	for flag, value := range p.server.flags {
		if oldValue, exists := oldFlags[flag]; !exists || oldValue != value {
			buf2 := NewBuffer()
			buf2.WriteByte(PLO_FLAGSET)
			buf2.WriteString8(flag)
			if value != "" {
				buf2.WriteByte('=')
				buf2.WriteString8(value)
			}
			p.server.sendPacketToType(PLTYPE_ANYCLIENT, buf2.Bytes())
		}
	}
	for flag := range oldFlags {
		if _, exists := p.server.flags[flag]; !exists {
			buf2 := NewBuffer()
			buf2.WriteByte(PLO_FLAGDEL)
			buf2.WriteString8(flag)
			p.server.sendPacketToType(PLTYPE_ANYCLIENT, buf2.Bytes())
		}
	}
	p.server.saveFlags()
	p.server.logger.Info("%s has updated the server flags.", p.accountName)
	p.server.sendRCChat(p.accountName + " has updated the server flags.")
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTADD(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTADD (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		p.server.logger.Warning("%s attempted ACCOUNTADD without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to create new accounts.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	_ = buf.ReadGString()
	email := buf.ReadGString()
	banned := buf.ReadByte() != 0
	loadOnly := buf.ReadByte() != 0
	_ = buf.ReadByte()
	account := &Account{server: p.server}
	account.accountName = accountName
	account.email = email
	account.isBanned = banned
	account.isLoadOnly = loadOnly
	account.SaveAccount()
	p.server.logger.Info("%s has created a new account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has created a new account: " + accountName)
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTDEL(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTDEL (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		p.server.logger.Warning("%s attempted ACCOUNTDEL without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to delete accounts.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[idx+1:]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[idx+1:]
	}
	if accountName == "" || !p.server.accountExists(accountName) {
		return true
	}
	if accountName == "defaultaccount" {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not allowed to delete the default account.")))
		return true
	}
	accountPath := "accounts/" + accountName + ".txt"
	if err := p.server.config.DeleteFile(accountPath); err != nil {
		p.server.logger.Error("Failed to delete account file: %v", err)
		return true
	}
	p.server.logger.Info("%s has deleted the account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has deleted the account: " + accountName)
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTLISTGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTLISTGET (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	name := buf.ReadGString()
	_ = buf.ReadGString()
	name = strings.ReplaceAll(name, "%", "*")
	if name == "" {
		name = "*"
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_ACCOUNTLISTGET)
	accounts, err := p.server.config.ListFiles("accounts")
	if err != nil {
		p.server.logger.Error("Failed to list accounts: %v", err)
		return true
	}
	for _, accountFile := range accounts {
		if strings.HasSuffix(accountFile, ".txt") {
			accountName := strings.TrimSuffix(accountFile, ".txt")
			matched, err := filepath.Match(name, accountName)
			if err == nil && matched {
				buf2.WriteByte(byte(len(accountName)))
				buf2.WriteString8(accountName)
			}
		}
	}
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSGET2(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	playerId := buf.ReadGShort()
	targetPlayer := p.server.getPlayerById(uint16(playerId))
	if targetPlayer == nil {
		return true
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERPROPSGET2 (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_VIEWATTRIBUTES) {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to view player props.")))
		return true
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERPROPSGET)
	buf2.WriteShort(int16(targetPlayer.id))
	buf2.WriteString8(targetPlayer.accountName)
	buf2.WriteString8("main")
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSGET3(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERPROPSGET3 (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_VIEWATTRIBUTES) {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to view player props.")))
		return true
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERPROPSGET)
	buf2.WriteShort(int16(targetPlayer.id))
	buf2.WriteString8(targetPlayer.accountName)
	buf2.WriteString8("main")
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSRESET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERPROPSRESET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if !p.hasRight(PLPERM_RESETATTRIBUTES) {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to reset accounts.")))
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		accountPath := "accounts/" + accountName + ".txt"
		os.Remove(accountPath)
		p.server.logger.Info("%s reset account: %s", p.accountName, accountName)
		p.server.sendRCChat(p.accountName + " has reset the attributes of account: " + accountName)
		return true
	}
	targetPlayer.resetAccount()
	if targetPlayer.id != 0 {
		targetPlayer.sendPacket([]byte{PLO_DISCMESSAGE, 0})
		targetPlayer.writeString8("Your account was reset by " + p.accountName)
		targetPlayer.loaded = false
		p.server.removePlayer(targetPlayer)
	}
	p.server.logger.Info("%s reset account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has reset the attributes of account: " + accountName)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSSET2(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountNameLen := buf.ReadByte()
	accountName := string(buf.ReadBytes(int(accountNameLen)))
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERPROPSSET2 (non-RC): %s", p.accountName, accountName)
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		return true
	}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) && targetPlayer.isStaff {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to modify staff accounts.")))
		return true
	}
	props := buf.ReadBytes(buf.Remaining())
	targetPlayer.parseProps(props)
	p.server.logger.Info("%s modified props for account: %s", p.accountName, accountName)
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTGET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTGET (non-RC): %s", p.accountName, accountName)
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_ACCOUNTGET)
	buf2.WriteString8(accountName)
	buf2.WriteByte(0)
	buf2.WriteString8(targetPlayer.email)
	banned := byte(0)
	if targetPlayer.isBanned {
		banned = 1
	}
	buf2.WriteByte(banned)
	loadOnly := byte(0)
	if targetPlayer.isLoadOnly {
		loadOnly = 1
	}
	buf2.WriteByte(loadOnly)
	buf2.WriteByte(0)
	buf2.WriteString8("main")
	buf2.WriteString8(targetPlayer.banLength)
	buf2.WriteString8(targetPlayer.banReason)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTSET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountNameLen := buf.ReadByte()
	accountName := string(buf.ReadBytes(int(accountNameLen)))
	if len(accountName) == 0 {
		return true
	}
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTSET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		p.server.logger.Warning("%s attempted ACCOUNTSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to edit accounts.")))
		return true
	}
	passwordLen := buf.ReadByte()
	_ = string(buf.ReadBytes(int(passwordLen)))
	emailLen := buf.ReadByte()
	email := string(buf.ReadBytes(int(emailLen)))
	banned := buf.ReadByte() != 0
	loadOnly := buf.ReadByte() != 0
	buf.ReadByte()
	worldLen := buf.ReadByte()
	_ = string(buf.ReadBytes(int(worldLen)))
	banReasonLen := buf.ReadByte()
	banReason := string(buf.ReadBytes(int(banReasonLen)))
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	targetPlayer.email = email
	targetPlayer.isLoadOnly = loadOnly
	if p.hasRight(PLPERM_BAN) {
		targetPlayer.isBanned = banned
		targetPlayer.banReason = banReason
	}
	targetPlayer.SaveAccount()
	p.server.logger.Info("%s modified account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " modified the account " + accountName)
	return true
}
func (p *Player) msgPLI_RC_CHAT(packet []byte) bool {
	if len(packet) <= 1 {
		return true
	}
	message := string(packet[1:])
	p.server.logger.Info("RC CHAT: %s", message)
	p.server.sendRCChat(p.accountName + ": " + message)
	return true
}
func (p *Player) msgPLI_PROFILEGET(packet []byte) bool {
	p.server.logger.Debug("PROFILEGET")
	return true
}
func (p *Player) msgPLI_PROFILESET(packet []byte) bool {
	p.server.logger.Debug("PROFILESET")
	return true
}
func (p *Player) msgPLI_RC_WARPPLAYER(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted WARPPLAYER (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_WARPTOPLAYER) {
		p.server.logger.Warning("%s attempted WARPPLAYER without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to warp players.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	playerId := buf.ReadGShort()
	x := float64(buf.ReadGChar()) / 2.0
	y := float64(buf.ReadGChar()) / 2.0
	levelName := buf.ReadGString()
	targetPlayer := p.server.getPlayerById(uint16(playerId))
	if targetPlayer == nil {
		return true
	}
	targetPlayer.warp(levelName, x, y)
	p.server.logger.Info("%s warped %s to %s (%.0f, %.0f)", p.accountName, targetPlayer.accountName, levelName, x, y)
	return true
}
func (p *Player) msgPLI_RC_PLAYERRIGHTSGET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERRIGHTSGET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if accountName != p.accountName && !p.hasRight(PLPERM_SETRIGHTS) {
		p.server.logger.Warning("%s attempted PLAYERRIGHTSGET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to view that player's rights.")))
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	folders := strings.Join(targetPlayer.folderList, "\n")
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERRIGHTSGET)
	buf2.WriteString8(accountName)
	buf2.WriteByte(byte(targetPlayer.adminRights >> 24))
	buf2.WriteByte(byte(targetPlayer.adminRights >> 16))
	buf2.WriteByte(byte(targetPlayer.adminRights >> 8))
	buf2.WriteByte(byte(targetPlayer.adminRights))
	buf2.WriteByte(byte(targetPlayer.adminRights >> 32))
	buf2.WriteString8(targetPlayer.adminIp)
	buf2.WriteShort(int16(len(folders)))
	buf2.WriteString8(folders)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERRIGHTSSET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountNameLen := buf.ReadByte()
	accountName := string(buf.ReadBytes(int(accountNameLen)))
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERRIGHTSSET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if !p.hasRight(PLPERM_SETRIGHTS) {
		p.server.logger.Warning("%s attempted PLAYERRIGHTSSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to set player rights.")))
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	newAdminRights := buf.ReadInt()
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		for i := 0; i < 20; i++ {
			if (p.adminRights & (1 << i)) == 0 {
				newAdminRights &= ^(1 << i)
			}
		}
	}
	if targetPlayer == p {
		if (newAdminRights & PLPERM_MODIFYSTAFFACCOUNT) == 0 {
			newAdminRights |= PLPERM_MODIFYSTAFFACCOUNT
		}
		if (newAdminRights & PLPERM_SETRIGHTS) == 0 {
			newAdminRights |= PLPERM_SETRIGHTS
		}
	}
	targetPlayer.adminRights = int(newAdminRights)
	adminIpLen := buf.ReadByte()
	adminIp := string(buf.ReadBytes(int(adminIpLen)))
	targetPlayer.adminIp = adminIp
	foldersLen := buf.ReadShort()
	folders := string(buf.ReadBytes(int(foldersLen)))
	folderList := strings.Split(folders, "\n")
	validFolders := []string{}
	for _, folder := range folderList {
		folder = strings.TrimSpace(folder)
		if strings.Contains(folder, ":") || strings.Contains(folder, "..") || strings.Contains(folder, " /*") {
			continue
		}
		if folder != "" {
			validFolders = append(validFolders, folder)
		}
	}
	targetPlayer.folderList = validFolders
	targetPlayer.SaveAccount()
	p.server.logger.Info("%s set rights for account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has set the rights of " + accountName)
	return true
}
func (p *Player) msgPLI_RC_PLAYERCOMMENTSGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERCOMMENTSGET (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERCOMMENTSGET)
	buf2.WriteString8(accountName)
	buf2.WriteString8(targetPlayer.accountComments)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERCOMMENTSSET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERCOMMENTSSET (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_SETCOMMENTS) {
		p.server.logger.Warning("%s attempted PLAYERCOMMENTSSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to set player comments.")))
		return true
	}
	buf := NewBufferFromBytes(packet)
	accountNameLen := buf.ReadByte()
	accountName := string(buf.ReadBytes(int(accountNameLen)))
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	comment := buf.ReadGString()
	targetPlayer.accountComments = comment
	targetPlayer.SaveAccount()
	if rcPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYRC); rcPlayer != nil {
		rcPlayer.LoadAccount(accountName, false)
	}
	p.server.logger.Info("%s has set the comments of %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has set the comments of " + accountName)
	return true
}
func (p *Player) msgPLI_RC_PLAYERBANGET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountName := buf.ReadGString()
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERBANGET (non-RC): %s", p.accountName, accountName)
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERBANGET)
	buf2.WriteString8(accountName)
	banned := byte(0)
	if targetPlayer.isBanned {
		banned = 1
	}
	buf2.WriteByte(banned)
	buf2.WriteString8(targetPlayer.banReason)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERBANSET(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	accountNameLen := buf.ReadByte()
	accountName := string(buf.ReadBytes(int(accountNameLen)))
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERBANSET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if !p.hasRight(PLPERM_BAN) {
		p.server.logger.Warning("%s attempted PLAYERBANSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to set player bans.")))
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		if !p.server.accountExists(accountName) {
			return true
		}
		tempPlayer := NewPlayer(nil, p.server)
		if !tempPlayer.LoadAccount(accountName, false) {
			return true
		}
		targetPlayer = tempPlayer
	}
	banned := buf.ReadByte() != 0
	reason := buf.ReadGString()
	targetPlayer.isBanned = banned
	targetPlayer.banReason = reason
	targetPlayer.SaveAccount()
	p.server.logger.Info("%s set ban for account: %s (banned=%v)", p.accountName, accountName, banned)
	p.server.sendRCChat(p.accountName + " has set the ban of " + accountName)
	if banned && targetPlayer.id != 0 {
		targetPlayer.sendPacket([]byte{PLO_DISCMESSAGE, 0})
		targetPlayer.writeString8(p.accountName + " has banned you.  Reason: " + reason)
		p.server.removePlayer(targetPlayer)
	}
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_START(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_START (non-RC)", p.accountName)
		return true
	}
	if len(p.folderList) == 0 {
		if p.hasRight(PLPERM_SETFOLDERRIGHTS) || p.hasRight(PLPERM_SETFOLDEROPTIONS) {
			p.folderList = p.server.defaultRCFolderRights()
		}
		if len(p.folderList) == 0 {
			return true
		}
	}
	var folders string
	for _, folder := range p.folderList {
		folders += folder + "\n"
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FILEBROWSER_DIRLIST)
	buf.Write([]byte(gtokenizeText(folders)))
	p.send(buf)
	if !p.isFtp {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_RC_FILEBROWSER_MESSAGE)
		buf2.Write([]byte("Welcome to the File Browser."))
		p.send(buf2)
	}
	folderMap := p.rcFolderMap()
	if _, exists := folderMap[p.lastFolder]; !exists {
		for folder := range folderMap {
			p.lastFolder = folder
			break
		}
	}
	p.sendRCFileBrowserDir(folderMap)
	p.isFtp = true
	return true
}

func fileBrowserMessagePacket(message string) *Buffer {
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FILEBROWSER_MESSAGE)
	buf.Write([]byte(message))
	return buf
}

func (p *Player) rcFolderMap() map[string]string {
	folderMap := make(map[string]string)
	for _, folder := range p.folderList {
		rights := "r"
		folderPath := folder
		if parts := strings.SplitN(folder, " ", 2); len(parts) == 2 {
			rights = strings.ToLower(strings.TrimSpace(parts[0]))
			folderPath = strings.TrimSpace(parts[1])
		}
		folderPath = strings.ReplaceAll(folderPath, "\\", "/")
		folderPath = strings.TrimSpace(folderPath)
		wild := "*"
		if !strings.HasSuffix(folderPath, "/") {
			if idx := strings.LastIndex(folderPath, "/"); idx != -1 {
				wild = folderPath[idx+1:]
				folderPath = folderPath[:idx+1]
			} else if strings.ContainsAny(folderPath, "*?") {
				wild = folderPath
				folderPath = ""
			}
		}
		folderMap[folderPath] += rights + ":" + wild + "\n"
	}
	return folderMap
}

func (p *Player) sendRCFileBrowserDir(folderMap map[string]string) {
	files, err := p.server.config.ListFiles(p.lastFolder)
	if err != nil {
		p.server.logger.Error("Failed to list files in %s: %v", p.lastFolder, err)
		return
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FILEBROWSER_DIR)
	buf.WriteByte(byte(len(p.lastFolder)))
	buf.Write([]byte(p.lastFolder))
	wildcards := strings.Split(folderMap[p.lastFolder], "\n")
	for _, wildcardEntry := range wildcards {
		if wildcardEntry == "" {
			continue
		}
		parts := strings.SplitN(wildcardEntry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		rights := parts[0]
		wildcard := parts[1]
		for _, file := range files {
			matched, err := filepath.Match(wildcard, file)
			if err != nil || !matched {
				continue
			}
			filePath := p.lastFolder + file
			fileInfo, err := p.server.config.FileInfo(filePath)
			if err != nil {
				continue
			}
			entry := NewBuffer()
			entry.WriteByte(byte(len(file)))
			entry.Write([]byte(file))
			entry.WriteByte(byte(len(rights)))
			entry.Write([]byte(rights))
			entry.WriteGInt5(uint64(fileInfo.Size()))
			entry.WriteGInt5(uint64(fileInfo.ModTime().Unix()))
			entryData := entry.Bytes()
			buf.WriteByte(' ')
			buf.WriteByte(byte(len(entryData)))
			buf.Write(entryData)
		}
	}
	p.send(buf)
}

func (p *Player) msgPLI_RC_FILEBROWSER_CD(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_CD (non-RC)", p.accountName)
		return true
	}
	newFolder := ""
	if len(packet) > 1 {
		newFolder = string(packet[1:])
	}
	folderMap := p.rcFolderMap()
	if _, exists := folderMap[newFolder]; !exists {
		return true
	}
	p.lastFolder = newFolder
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_FILEBROWSER_MESSAGE)
	buf2.Write([]byte("Folder changed to " + p.lastFolder))
	p.send(buf2)
	p.sendRCFileBrowserDir(folderMap)
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_END(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_END (non-RC)", p.accountName)
		return true
	}
	p.isFtp = false
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_DOWN(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_DOWN (non-RC)", p.accountName)
		return true
	}
	fileName := ""
	if len(packet) > 1 {
		fileName = string(packet[1:])
	}
	filePath := p.lastFolder + fileName
	protectedFiles := []string{"accounts/defaultaccount.txt", "config/adminconfig.txt", "config/allowedversions.txt", "config/rchelp.txt"}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) {
		for _, protected := range protectedFiles {
			if filePath == protected {
				p.send(fileBrowserMessagePacket("Insufficient rights to download/view " + filePath))
				return true
			}
		}
	}
	p.sendRCFileBrowserFile(fileName, filePath)
	p.server.logger.Info("%s downloaded file %s", p.accountName, fileName)
	p.send(fileBrowserMessagePacket("Downloaded file " + fileName))
	return true
}

func (p *Player) sendRCFileBrowserFile(fileName, filePath string) {
	data, err := p.server.config.LoadFile(filePath)
	if err != nil {
		p.server.logger.Error("Failed to load file %s: %v", filePath, err)
		p.sendPLO_FILESENDFAILED(fileName)
		return
	}
	modTime := time.Time{}
	if p.server != nil && p.server.config != nil {
		modTime, _ = p.server.config.FileModTime(filePath)
	}
	filePacket := NewBuffer()
	filePacket.WriteGChar(PLO_FILE)
	filePacket.WriteGInt5(uint64(modTime.Unix()))
	filePacket.WriteGChar(byte(len(fileName)))
	filePacket.Write([]byte(fileName))
	filePacket.Write(data)
	filePacket.WriteByte('\n')

	buf := NewBuffer()
	buf.WriteByte(PLO_RAWDATA)
	buf.WriteGInt(uint32(filePacket.Len()))
	buf.WriteByte('\n')
	buf.Write(filePacket.Bytes())
	p.sendPacket(buf.Bytes())
}

func (p *Player) msgPLI_RC_FILEBROWSER_UP(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_UP (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	nameLen := int(buf.ReadByte())
	fileName := string(buf.ReadBytes(nameLen))
	filePath := p.lastFolder + fileName
	importantFiles := []string{"accounts/defaultaccount.txt", "config/adminconfig.txt", "config/allowedversions.txt", "config/foldersconfig.txt", "config/ipbans.txt", "config/rchelp.txt", "config/rcmessage.txt", "config/rules.txt", "config/servermessage.html", "config/serveroptions.txt"}
	importantFileRights := []int{PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_SETFOLDEROPTIONS, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_MODIFYSTAFFACCOUNT, PLPERM_SETSERVEROPTIONS}
	isProtected := false
	fileID := -1
	for i, important := range importantFiles {
		if filePath == important {
			isProtected = true
			fileID = i
			break
		}
	}
	hasPermission := true
	if isProtected {
		hasPermission = p.hasRight(PLPERM_MODIFYSTAFFACCOUNT)
		if !hasPermission && fileID >= 0 && fileID < len(importantFileRights) {
			hasPermission = p.hasRight(importantFileRights[fileID])
		}
	}
	if isProtected && !hasPermission {
		p.send(fileBrowserMessagePacket("Insufficient rights to upload " + filePath))
		return true
	}
	fileData := buf.ReadBytes(buf.Remaining())
	if _, exists := p.rcLargeFiles[fileName]; !exists {
		if err := p.server.config.SaveFile(filePath, fileData); err != nil {
			p.server.logger.Error("Failed to save file %s: %v", filePath, err)
			return true
		}
		p.server.logger.Info("%s uploaded file %s", p.accountName, fileName)
		p.send(fileBrowserMessagePacket("Uploaded file " + fileName))
	} else {
		p.rcLargeFiles[fileName] += string(fileData)
	}
	return true
}
func (p *Player) msgPLI_NPCSERVERQUERY(packet []byte) bool {
	p.server.logger.Debug("NPCSERVERQUERY")
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_MOVE(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_MOVE (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	dirLen := int(buf.ReadByte())
	dir := string(buf.ReadBytes(dirLen))
	fileName := string(buf.ReadBytes(buf.Remaining()))
	fileName = strings.ReplaceAll(fileName, "\"", "")
	if !strings.HasSuffix(dir, "/") && !strings.HasSuffix(dir, "\\") {
		dir += "/"
	}
	source := p.lastFolder + fileName
	destination := dir + fileName
	importantFiles := []string{"accounts/defaultaccount.txt", "config/adminconfig.txt", "config/allowedversions.txt", "config/foldersconfig.txt", "config/ipbans.txt", "config/rchelp.txt", "config/rcmessage.txt", "config/rules.txt", "config/servermessage.html", "config/serveroptions.txt"}
	for _, important := range importantFiles {
		if source == important {
			p.send(fileBrowserMessagePacket("Not allowed to move file " + source))
			return true
		}
	}
	data, err := p.server.config.LoadFile(source)
	if err != nil {
		p.server.logger.Error("Failed to load file for move: %v", err)
		return true
	}
	if err := p.server.config.SaveFile(destination, data); err != nil {
		p.server.logger.Error("Failed to save file for move: %v", err)
		return true
	}
	if err := p.server.config.DeleteFile(source); err != nil {
		p.server.logger.Error("Failed to delete source file after move: %v", err)
		return true
	}
	p.server.logger.Info("%s moved file %s to %s", p.accountName, source, destination)
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_DELETE(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_DELETE (non-RC)", p.accountName)
		return true
	}
	fileName := ""
	if len(packet) > 1 {
		fileName = string(packet[1:])
	}
	filePath := p.lastFolder + fileName
	importantFiles := []string{"accounts/defaultaccount.txt", "config/adminconfig.txt", "config/allowedversions.txt", "config/foldersconfig.txt", "config/ipbans.txt", "config/rchelp.txt", "config/rcmessage.txt", "config/rules.txt", "config/servermessage.html", "config/serveroptions.txt"}
	for _, important := range importantFiles {
		if filePath == important {
			p.send(fileBrowserMessagePacket("Not allowed to delete file " + filePath))
			return true
		}
	}
	if err := p.server.config.DeleteFile(filePath); err != nil {
		p.server.logger.Error("Failed to delete file %s: %v", filePath, err)
		p.send(fileBrowserMessagePacket("Error removing " + fileName + ". File may not exist or may not be empty."))
		return true
	}
	p.server.logger.Info("%s deleted file %s", p.accountName, fileName)
	p.send(fileBrowserMessagePacket("Deleted file " + fileName))
	return true
}
func (p *Player) msgPLI_RC_FILEBROWSER_RENAME(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FILEBROWSER_RENAME (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	oldLen := int(buf.ReadByte())
	oldName := string(buf.ReadBytes(oldLen))
	newLen := int(buf.ReadByte())
	newName := string(buf.ReadBytes(newLen))
	oldPath := p.lastFolder + oldName
	newPath := p.lastFolder + newName
	importantFiles := []string{"accounts/defaultaccount.txt", "config/adminconfig.txt", "config/allowedversions.txt", "config/foldersconfig.txt", "config/ipbans.txt", "config/rchelp.txt", "config/rcmessage.txt", "config/rules.txt", "config/servermessage.html", "config/serveroptions.txt"}
	for _, important := range importantFiles {
		if oldPath == important || newPath == important {
			p.send(fileBrowserMessagePacket("Not allowed to rename/overwrite file " + oldPath + " or " + newPath))
			return true
		}
	}
	data, err := p.server.config.LoadFile(oldPath)
	if err != nil {
		p.server.logger.Error("Failed to load file for rename: %v", err)
		return true
	}
	if err := p.server.config.SaveFile(newPath, data); err != nil {
		p.server.logger.Error("Failed to save renamed file: %v", err)
		return true
	}
	if err := p.server.config.DeleteFile(oldPath); err != nil {
		p.server.logger.Error("Failed to delete old file after rename: %v", err)
		return true
	}
	p.server.logger.Info("%s renamed file %s to %s", p.accountName, oldName, newName)
	p.send(fileBrowserMessagePacket("Renamed file " + oldName + " to " + newName))
	return true
}
func (p *Player) msgPLI_RC_LARGEFILESTART(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted LARGEFILESTART (non-RC)", p.accountName)
		return true
	}
	fileName := ""
	if len(packet) > 1 {
		fileName = string(packet[1:])
	}
	p.rcLargeFiles[fileName] = ""
	return true
}
func (p *Player) msgPLI_RC_LARGEFILEEND(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted LARGEFILEEND (non-RC)", p.accountName)
		return true
	}
	fileName := ""
	if len(packet) > 1 {
		fileName = string(packet[1:])
	}
	filePath := p.lastFolder + fileName
	fileData, exists := p.rcLargeFiles[fileName]
	if !exists {
		return true
	}
	if err := p.server.config.SaveFile(filePath, []byte(fileData)); err != nil {
		p.server.logger.Error("Failed to save large file %s: %v", filePath, err)
		return true
	}
	delete(p.rcLargeFiles, fileName)
	p.server.logger.Info("%s uploaded large file %s", p.accountName, fileName)
	p.send(fileBrowserMessagePacket("Uploaded large file " + fileName))
	return true
}
func (p *Player) msgPLI_RC_FOLDERDELETE(packet []byte) bool {
	folder := ""
	if len(packet) > 1 {
		folder = string(packet[1:])
	}
	folderPath := p.server.config.GetBasePath() + "/" + folder
	folderPath = strings.ReplaceAll(folderPath, "/", "\\")
	if len(folderPath) > 0 && folderPath[len(folderPath)-1] == '\\' {
		folderPath = folderPath[:len(folderPath)-1]
	}
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted FOLDERDELETE (non-RC): %s", p.accountName, folder)
		return true
	}
	if err := os.RemoveAll(folderPath); err != nil {
		p.server.logger.Error("Failed to remove folder %s: %v", folderPath, err)
		p.send(fileBrowserMessagePacket("Error removing " + folder + ". Folder may not exist or may not be empty."))
		return true
	}
	p.server.logger.Info("%s deleted folder %s", p.accountName, folder)
	return true
}

func (p *Player) msgPLI_NC_LISTNPCS(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LISTNPCS (non-NC)", p.accountName)
		return true
	}
	p.sendNCNPCList()
	return true
}

func (p *Player) msgPLI_NC_NPCGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	if buf.Remaining() == 0 {
		return true
	}
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		var flagsStr string
		npc.mu.Lock()
		for k, v := range npc.saves {
			if v != 0 {
				flagsStr += fmt.Sprintf("save%d=%d\n", k, v)
			}
		}
		npc.mu.Unlock()
		flagsStr = strings.ReplaceAll(flagsStr, "\n", "\x01")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCATTRIBUTES)
		buf2.WriteString8(flagsStr)
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil && npc.npcType == DBNPC {
		npcName := npc.npcName
		p.server.DeleteNPC(npcId)
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCDELETE)
		buf2.WriteGInt(uint32(npcId))
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
		p.server.logger.Info("NPC %s deleted by %s", npcName, p.accountName)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCRESET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCRESET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil && npc.npcType == DBNPC {
		npc.script = ""
		p.server.logger.Info("NPC script of %s reset by %s", npc.npcName, p.accountName)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCSCRIPTGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCSCRIPTGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		code := npc.script
		code = strings.ReplaceAll(code, "\n", "\x01")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCSCRIPT)
		buf2.WriteGInt(uint32(npcId))
		buf2.WriteString8(code)
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCWARP(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCWARP (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npcX := float32(buf.ReadGByte()) / 2.0
	npcY := float32(buf.ReadGByte()) / 2.0
	npcLevelName := buf.ReadGString()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		level := p.server.GetLevel(npcLevelName)
		if level != nil {
			npc.x = int16(npcX * 16)
			npc.y = int16(npcY * 16)
			npc.level = level
		}
	}
	return true
}
func (p *Player) msgPLI_NC_NPCFLAGSGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCFLAGSGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCFLAGS)
		buf2.WriteGInt(uint32(npcId))
		buf2.WriteString8("")
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCSCRIPTSET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCSCRIPTSET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcId := buf.ReadGInt()
	npcScript := buf.ReadGString()
	npcScript = strings.ReplaceAll(npcScript, "\x01", "\n")
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		npc.script = npcScript
		p.server.logger.Info("NPC script of %s updated by %s", npc.npcName, p.accountName)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCFLAGSSET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCFLAGSSET (non-NC)", p.accountName)
		return true
	}
	return true
}
func (p *Player) msgPLI_NC_NPCADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	npcData := buf.ReadGString()
	npcData = strings.ReplaceAll(npcData, "\x01", "\n")
	parts := strings.Split(npcData, "\n")
	if len(parts) < 7 {
		return true
	}
	npcName := strings.TrimSpace(parts[0])
	if npcName == "" {
		return true
	}
	npcIdStr := parts[1]
	npcScripter := parts[3]
	npcLevelName := parts[4]
	npcX := parts[5]
	npcY := parts[6]
	level := p.server.GetLevel(npcLevelName)
	if level == nil {
		p.server.logger.Info("Error adding database npc: Level does not exist")
		return true
	}
	x, _ := strconv.ParseFloat(npcX, 32)
	y, _ := strconv.ParseFloat(npcY, 32)
	_, _ = strconv.ParseUint(npcIdStr, 10, 32)
	newNpc := NewNPC(DBNPC)
	newNpc.npcName = npcName
	newNpc.scripter = npcScripter
	newNpc.x = int16(x * 16)
	newNpc.y = int16(y * 16)
	newNpc.level = level
	p.server.AddNPC(newNpc)
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_NC_NPCADD)
	buf2.WriteGInt(newNpc.id)
	p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
	p.server.logger.Info("NPC %s added by %s", npcName, p.accountName)
	return true
}
func (p *Player) msgPLI_NC_CLASSEDIT(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSEDIT (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	className := buf.ReadGString()
	p.server.weaponMu.RLock()
	classObj, exists := p.server.classes[className]
	p.server.weaponMu.RUnlock()
	if exists {
		classCode := classObj.script
		classCode = strings.ReplaceAll(classCode, "\n", "\x01")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSGET)
		buf2.WriteString8(className)
		buf2.WriteString8(classCode)
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_CLASSADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	classNameLen := buf.ReadGByte()
	className := string(buf.ReadBytes(int(classNameLen)))
	classCode := buf.ReadGString()
	classCode = strings.ReplaceAll(classCode, "\x01", "\n")
	p.server.weaponMu.Lock()
	_, hasClass := p.server.classes[className]
	p.server.classes[className] = &ScriptClass{name: className, script: classCode}
	p.server.weaponMu.Unlock()
	if !hasClass {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSADD)
		buf2.WriteString8(className)
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
	}
	p.server.logger.Info("Script %s %s by %s", className, map[bool]string{true: "added", false: "updated"}[!hasClass], p.accountName)
	return true
}
func (p *Player) msgPLI_NC_LOCALNPCSGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LOCALNPCSGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	levelName := buf.ReadGString()
	if levelName == "" {
		return true
	}
	level := p.server.GetLevel(levelName)
	if level != nil {
		var npcDump string
		npcDump += "Variables dump from level " + levelName + "\n"
		for _, npc := range level.npcs {
			if npc != nil {
				npcDump += fmt.Sprintf("\nNPC %d: %s\n", npc.id, npc.npcName)
			}
		}
		npcDump = strings.ReplaceAll(npcDump, "\n", "\x01")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_LEVELDUMP)
		buf2.WriteString8(npcDump)
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_WEAPONLISTGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONLISTGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_WEAPONLISTGET)
	p.server.weaponMu.RLock()
	for weaponName := range p.server.weapons {
		if weaponName != "" {
			buf.WriteString8(weaponName)
		}
	}
	p.server.weaponMu.RUnlock()
	p.send(buf)
	return true
}
func (p *Player) msgPLI_NC_WEAPONGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	weaponName := buf.ReadGString()
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil {
		script := weapon.script
		script = strings.ReplaceAll(script, "\n", "\xa7")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_WEAPONGET)
		buf2.WriteString8(weaponName)
		buf2.WriteString8(weapon.image)
		buf2.WriteString8(script)
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_WEAPONADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	weaponNameLen := buf.ReadGByte()
	weaponName := string(buf.ReadBytes(int(weaponNameLen)))
	weaponImageLen := buf.ReadGByte()
	weaponImage := string(buf.ReadBytes(int(weaponImageLen)))
	weaponCode := buf.ReadGString()
	weaponCode = strings.ReplaceAll(weaponCode, "\xa7", "\n")
	actionTaken := ""
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil {
		weapon.image = weaponImage
		weapon.script = weaponCode
		actionTaken = "updated"
	} else {
		newWeapon := NewWeapon(weaponName)
		newWeapon.image = weaponImage
		newWeapon.script = weaponCode
		p.server.AddWeapon(newWeapon)
		actionTaken = "added"
	}
	if actionTaken != "" {
		p.server.logger.Info("Weapon/GUI-script %s %s by %s", weaponName, actionTaken, p.accountName)
	}
	return true
}
func (p *Player) msgPLI_NC_WEAPONDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	weaponName := buf.ReadGString()
	p.server.weaponMu.RLock()
	_, exists := p.server.weapons[weaponName]
	p.server.weaponMu.RUnlock()
	if exists {
		p.server.DeleteWeapon(weaponName)
		p.server.logger.Info("Weapon %s deleted by %s", weaponName, p.accountName)
	} else {
		p.server.logger.Info("%s prob: weapon %s doesn't exist", p.accountName, weaponName)
	}
	return true
}
func (p *Player) msgPLI_NC_CLASSDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet)
	className := buf.ReadGString()
	p.server.weaponMu.Lock()
	_, exists := p.server.classes[className]
	delete(p.server.classes, className)
	p.server.weaponMu.Unlock()
	if exists {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSDELETE)
		buf2.WriteString8(className)
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
		p.server.logger.Info("%s has deleted class %s", p.accountName, className)
	} else {
		p.server.logger.Info("error: %s does not exist on this server!", className)
	}
	return true
}
func (p *Player) msgPLI_NC_LEVELLISTGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LEVELLISTGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_LEVELLIST)
	p.server.levelMu.RLock()
	for levelName := range p.server.levels {
		buf.WriteString8(levelName)
		buf.WriteByte('\n')
	}
	p.server.levelMu.RUnlock()
	levelList := strings.ReplaceAll(string(buf.Bytes()[1:]), "\n", "\x01")
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_NC_LEVELLIST)
	buf2.WriteString8(levelList)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_REQUESTTEXT(packet []byte) bool {
	if len(packet) <= 1 {
		return true
	}
	rawText := string(packet[1:])
	data := rawText
	data = strings.ReplaceAll(data, "\x01", "\n")
	parts := strings.SplitN(data, "\n", 4)
	if len(parts) < 3 {
		return true
	}
	weapon := parts[0]
	type_ := parts[1]
	option := parts[2]
	p.server.logger.Debug("REQUESTTEXT: weapon=%s type=%s option=%s raw=%q", weapon, type_, option, rawText)
	if type_ == "lister" {
		if option == "simplelist" {
			p.sendServerTextFields(weapon, type_, "simpleserverlist")
		} else if option == "subscriptions" {
			p.sendServerTextFields(weapon, type_, "subscriptions", "unlimited", "Unlimited Subscription", "\"\"")
		} else if option == "bantypes" {
			bans := "\"\"Event Interruption\",259200,\"\"\"Message Code Abuse\"\",259200,\"\"\"General Scamming\"\",604800,\"Advertising,604800,\"\"\"General Harassment\"\",604800,\"\"\"Racism or Severe Vulgarity\"\",1209600,\"\"\"Sexual Harassment\"\",1209600,\"Cheating,2592000,\"\"\"Advertising Money Trade\"\",2592000,\"\"\"Ban Evasion\"\",2592000,\"\"\"Speed Hacking\"\",2592000,\"\"\"Bug Abuse\"\",2592000,\"\"\"Multiple Jailings\"\",2592000,\"\"\"Server Destruction\"\",3888000,\"\"\"Leaking Information\"\",3888000,\"\"\"Account Scam\"\",7776000,\"\"\"Account Sharing\"\",315360000,\"Hacking,315360000,\"\"\"Multiple Bans\"\",315360000,\"\"\"Other Unlimited\"\",315360001"
			p.sendServerTextFields(weapon, type_, "bantypes", bans)
		} else if option == "getglobalitems" {
			p.sendServerTextFields(weapon, type_, "globalitems", p.accountName,
				"autobill=1\x01autobillmine=1\x01bundle=1\x01creationtime=1212768763\x01currenttime=1353248504\x01description=Gives\x01duration=2629800\x01flags=subscription\x01icon=graalicon_big.png\x01itemid=1\x01lifetime=1\x01owner=global\x01ownertype=server\x01price=100\x01quantity=988506\x01status=available\x01title=Gold\x01tradable=1\x01typeid=62\x01world=global")
		} else if option == "serverinfo" {
			if p.server.serverList != nil && p.server.serverList.connected {
				p.server.serverList.SendTextPacket(SVO_REQUESTSVRINFO, rawText)
			} else {
				p.sendServerTextFields(weapon, type_, option, p.server.name)
			}
		}
	} else if type_ == "pmservers" || type_ == "pmguilds" {
		if p.server.serverList != nil && p.server.serverList.connected {
			p.server.serverList.SendTextPacket(SVO_REQUESTLIST, rawText)
		} else {
			p.sendServerTextFields(weapon, type_, option)
		}
	} else if type_ == "pmserverplayers" {
		p.addPMServer(option)
		p.sendServerTextFields(weapon, type_, option)
	} else if type_ == "pmunmapserver" {
		p.remPMServer(option)
		p.sendServerTextFields(weapon, type_, option)
	} else if type_ == "packageinfo" {
		p.sendServerTextFields(weapon, type_, option, "0", "0")
	} else if type_ == "irc" {
		p.sendServerTextFields(weapon, type_, option)
	}
	return true
}

func (p *Player) sendServerTextFields(fields ...string) {
	buf := NewBuffer()
	buf.WriteByte(PLO_SERVERTEXT)
	buf.Write([]byte(strings.Join(fields, "\x01")))
	buf.WriteByte('\x01')
	p.send(buf)
}
func (p *Player) msgPLI_SENDTEXT(packet []byte) bool {
	if len(packet) <= 1 {
		return true
	}
	rawText := string(packet[1:])
	data := rawText
	data = strings.ReplaceAll(data, "\x01", "\n")
	parts := strings.SplitN(data, "\n", 3)
	if len(parts) < 3 {
		return true
	}
	weapon := parts[0]
	type_ := parts[1]
	if type_ == "lister" {
		option := parts[2]
		if option == "verifybuddies" || option == "addbuddy" || option == "deletebuddy" {
			if p.server.serverList != nil && p.server.serverList.connected {
				p.server.serverList.SendTextPacket(SVO_SENDTEXT, rawText)
			}
		}
	}
	p.server.logger.Debug("SENDTEXT: weapon=%s, type=%s", weapon, type_)
	return true
}
func (p *Player) sendRawDataPayload(payload []byte) {
	buf := NewBuffer()
	buf.WriteByte(PLO_RAWDATA)
	buf.WriteGInt(uint32(len(payload)))
	buf.WriteByte('\n')
	buf.Write(payload)
	p.sendPacket(buf.Bytes())
}

func (p *Player) sendRawNpcWeaponScript(bytecode []byte) {
	payload := NewBuffer()
	payload.WriteGChar(PLO_NPCWEAPONSCRIPT)
	payload.Write(bytecode)
	p.sendRawDataPayload(payload.Bytes())
}

func (p *Player) msgPLI_UPDATEGANI(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	checksum := uint32(buf.ReadGInt5())
	ganiName := string(buf.ReadBytes(buf.BytesLeft()))
	ganiFile := ganiName + ".gani"
	p.server.logger.Debug("UPDATEGANI: %s (checksum: %d)", ganiFile, checksum)
	ganiData, err := p.server.config.LoadFile(ganiFile)
	if err != nil {
		p.server.logger.Debug("Gani file not found: %s", ganiFile)
		return true
	}
	ganiStr := string(ganiData)
	setBackTo := "idle"
	if strings.Contains(ganiStr, "SETBACKTO") {
		lines := strings.Split(ganiStr, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "SETBACKTO") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					setBackTo = parts[1]
				}
				break
			}
		}
	}
	hasScript := strings.Contains(ganiStr, "SCRIPT")
	if checksum != crc32.ChecksumIEEE(ganiData) {
		if hasScript {
			scriptStart := strings.Index(ganiStr, "SCRIPT")
			scriptEnd := strings.Index(ganiStr, "SCRIPTEND")
			if scriptStart != -1 && scriptEnd != -1 {
				scriptCode := ganiStr[scriptStart+7 : scriptEnd]
				p.server.logger.Debug("Sending gani script for %s", ganiFile)
				payload := NewBuffer()
				payload.WriteGChar(PLO_GANISCRIPT)
				payload.WriteGChar(byte(len(ganiName)))
				payload.Write([]byte(ganiName))
				payload.Write([]byte(scriptCode))
				p.sendRawDataPayload(payload.Bytes())
			}
		}
	}
	outBuf2 := NewBuffer()
	outBuf2.WriteByte(PLO_UNKNOWN195)
	outBuf2.WriteGChar(byte(len(ganiName)))
	outBuf2.Write([]byte(ganiName))
	outBuf2.Write([]byte("\"SETBACKTO " + setBackTo + "\""))
	p.send(outBuf2)
	return true
}
func (p *Player) msgPLI_UPDATESCRIPT(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
	weaponName := buf.ReadString()
	p.server.logger.Debug("UPDATESCRIPT: %s", weaponName)
	p.server.weaponMu.RLock()
	weaponObj, exists := p.server.weapons[weaponName]
	p.server.weaponMu.RUnlock()
	if exists && len(weaponObj.bytecode) > 0 {
		p.sendRawNpcWeaponScript(weaponObj.bytecode)
	}
	return true
}
func (p *Player) msgPLI_UPDATEPACKAGEREQUESTFILE(packet []byte) bool {
	buf := NewBufferFromBytes(packet)
	packageNameLen := buf.ReadGByte()
	packageName := string(buf.ReadBytes(int(packageNameLen)))
	installType := buf.ReadGByte()
	_ = buf.ReadString()
	if installType == 2 {
		buf.Reset()
		buf.ReadGByte()
		buf.ReadBytes(int(packageNameLen))
		_ = buf.ReadGByte()
		_ = buf.ReadString()
	}
	totalDownloadSize := int64(0)
	var missingFiles []string
	packagePath := fmt.Sprintf("packages/%s.gupd", packageName)
	packageData, err := p.server.config.LoadFile(packagePath)
	if err == nil && len(packageData) > 0 {
		packageLines := strings.Split(string(packageData), "\n")
		for _, line := range packageLines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				fileName := parts[0]
				if fileData, err := p.server.config.LoadFile(fileName); err == nil {
					totalDownloadSize += int64(len(fileData))
					missingFiles = append(missingFiles, fileName)
				}
			}
		}
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_UPDATEPACKAGESIZE)
	buf2.WriteGByte(uint8(len(packageName)))
	buf2.data = append(buf2.data, packageName...)
	buf2.WriteInt64(totalDownloadSize)
	p.send(buf2)
	for _, fileName := range missingFiles {
		p.sendFile(fileName)
	}
	buf3 := NewBuffer()
	buf3.WriteByte(PLO_UPDATEPACKAGEDONE)
	buf3.WriteGByte(uint8(len(packageName)))
	buf3.data = append(buf3.data, packageName...)
	p.send(buf3)
	p.server.logger.Debug("UPDATEPACKAGEREQUESTFILE: %s, %d files, %d bytes", packageName, len(missingFiles), totalDownloadSize)
	return true
}
func (p *Player) msgPLI_RC_UNKNOWN162(packet []byte) bool {
	p.server.logger.Debug("RC_UNKNOWN162")
	return true
}

// ============ LEVEL ============
type Level struct {
	mu                                                sync.RWMutex
	fileName, fileVersion, actualLevelName, levelName string
	modTime                                           time.Time
	isSparringZone, isSingleplayer                    bool
	mapX, mapY                                        int
	mapRef                                            *Map
	tiles                                             map[uint8]*LevelTiles
	baddies                                           map[uint8]*LevelBaddy
	boardChanges                                      []LevelBoardChange
	chests                                            []*LevelChest
	horses                                            []LevelHorse
	items                                             []LevelItem
	links                                             []*LevelLink
	signs                                             []*LevelSign
	npcs                                              map[uint32]*NPC
	players                                           []uint16
}

type LevelTiles struct {
	width, height int
	tiles         []int16
}
type LevelItemType int

const (
	BDPROP_ID          = 0
	BDPROP_X           = 1
	BDPROP_Y           = 2
	BDPROP_TYPE        = 3
	BDPROP_POWERIMAGE  = 4
	BDPROP_MODE        = 5
	BDPROP_ANI         = 6
	BDPROP_DIR         = 7
	BDPROP_VERSESIGHT  = 8
	BDPROP_VERSEHURT   = 9
	BDPROP_VERSEATTACK = 10
	BDPROP_COUNT       = 11
)

const (
	BDMODE_WALK      = 0
	BDMODE_LOOK      = 1
	BDMODE_HUNT      = 2
	BDMODE_HURT      = 3
	BDMODE_BUMPED    = 4
	BDMODE_DIE       = 5
	BDMODE_SWAMPSHOT = 6
	BDMODE_HAREJUMP  = 7
	BDMODE_OCTOSHOT  = 8
	BDMODE_DEAD      = 9
	BDMODE_COUNT     = 10
)

const baddyTypes = 10

var baddyImages = []string{
	"baddygray.png", "baddyblue.png", "baddyred.png", "baddyblue.png", "baddygray.png",
	"baddyhare.png", "baddyoctopus.png", "baddygold.png", "baddylizardon.png", "baddydragon.png",
}

var baddyStartMode = []byte{
	BDMODE_WALK, BDMODE_WALK, BDMODE_WALK, BDMODE_WALK, BDMODE_SWAMPSHOT,
	BDMODE_HAREJUMP, BDMODE_WALK, BDMODE_WALK, BDMODE_WALK, BDMODE_WALK,
}

var baddyPower = []int{
	2, 3, 4, 3, 2,
	1, 1, 6, 12, 8,
}

type LevelBaddy struct {
	server                *Server
	level                 *Level
	mu                    sync.RWMutex
	baddyType             byte
	id                    byte
	power, mode, ani, dir byte
	x, y, startX, startY  float32
	image                 string
	verses                [3]string
	canRespawn            bool
	hasCustomImage        bool
	timeout               time.Time
}

func NewLevelBaddy(x float32, y float32, baddyType byte, level *Level, server *Server) *LevelBaddy {
	if baddyType >= baddyTypes {
		baddyType = 0
	}
	baddy := &LevelBaddy{server: server, level: level, baddyType: baddyType, x: x, y: y, startX: x, startY: y, canRespawn: true}
	baddy.reset()
	return baddy
}

func (lb *LevelBaddy) reset() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.mode = baddyStartMode[lb.baddyType]
	lb.x = lb.startX
	lb.y = lb.startY
	lb.power = byte(baddyPower[lb.baddyType])
	lb.image = baddyImages[lb.baddyType]
	lb.dir = (2 << 2) | 2
	lb.ani = 0
	lb.hasCustomImage = false
}

func (lb *LevelBaddy) dropItem() {
	itemId := rand.Intn(12)
	var itemType LevelItemType
	switch itemId {
	case 0, 1, 2, 3, 4, 5:
		itemType = getItemId(strconv.Itoa(itemId))
	default:
		if itemId > 5 && itemId < 10 {
			itemType = ItemGreenRupee
		}
	}
	if itemType != LevelItemType(-1) {
		if lb.level != nil {
			lb.level.mu.Lock()
			lb.level.items = append(lb.level.items, LevelItem{x: lb.x, y: lb.y, itemType: itemType})
			lb.level.mu.Unlock()
			buf := NewBuffer()
			buf.WriteByte(PLO_ITEMADD)
			buf.WriteByte(byte(lb.x * 2))
			buf.WriteByte(byte(lb.y * 2))
			buf.WriteByte(byte(itemType))
			for _, pid := range lb.level.players {
				if pl, ok := lb.server.players[pid]; ok {
					pl.SendPacket(buf.Bytes())
				}
			}
		}
	}
}

func (lb *LevelBaddy) getProp(propId int, clientVersion int) []byte {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	buf := NewBuffer()
	switch propId {
	case BDPROP_ID:
		buf.WriteByte(lb.id)
	case BDPROP_X:
		buf.WriteByte(byte(lb.x * 2))
	case BDPROP_Y:
		buf.WriteByte(byte(lb.y * 2))
	case BDPROP_TYPE:
		buf.WriteByte(lb.baddyType)
	case BDPROP_POWERIMAGE:
		buf.WriteByte(lb.power)
		image := lb.image
		if clientVersion < 201 && lb.image == baddyImages[lb.baddyType] {
			image = strings.ReplaceAll(lb.image, ".png", ".gif")
		}
		buf.WriteString(image)
	case BDPROP_MODE:
		buf.WriteByte(lb.mode)
	case BDPROP_ANI:
		buf.WriteByte(lb.ani)
	case BDPROP_DIR:
		buf.WriteByte(lb.dir)
	case BDPROP_VERSESIGHT, BDPROP_VERSEHURT, BDPROP_VERSEATTACK:
		verseId := int(propId - BDPROP_VERSESIGHT)
		if verseId < len(lb.verses) {
			buf.WriteString(lb.verses[verseId])
		} else {
			buf.WriteByte(0)
		}
	}
	return buf.Bytes()
}

func (lb *LevelBaddy) getProps(clientVersion int) []byte {
	buf := NewBuffer()
	for i := 1; i < BDPROP_COUNT; i++ {
		buf.WriteByte(byte(i))
		buf.Write(lb.getProp(i, clientVersion))
	}
	return buf.Bytes()
}

func (lb *LevelBaddy) setProps(data []byte) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	buf := NewBufferFromBytes(data)
	for buf.Remaining() > 0 {
		propId := buf.ReadGChar()
		switch propId {
		case BDPROP_ID:
			lb.id = buf.ReadGChar()
		case BDPROP_X:
			val := float32(buf.ReadGChar()) / 2.0
			if val < 0 {
				val = 0
			} else if val > 63.5 {
				val = 63.5
			}
			lb.x = val
		case BDPROP_Y:
			val := float32(buf.ReadGChar()) / 2.0
			if val < 0 {
				val = 0
			} else if val > 63.5 {
				val = 63.5
			}
			lb.y = val
		case BDPROP_TYPE:
			lb.baddyType = byte(buf.ReadGChar())
		case BDPROP_POWERIMAGE:
			lb.power = byte(buf.ReadGChar())
			if buf.Remaining() > 0 {
				strLen := buf.ReadGChar()
				if strLen > 0 && buf.Remaining() >= int(strLen) {
					newImage := string(buf.ReadBytes(int(strLen)))
					if newImage == "" {
						lb.image = baddyImages[lb.baddyType]
					} else if !lb.hasCustomImage {
						lb.hasCustomImage = true
						lb.image = newImage
					}
				}
			}
		case BDPROP_MODE:
			lb.mode = byte(buf.ReadGChar())
			if lb.baddyType == 4 && lb.mode == BDMODE_HURT {
				lb.timeout = time.Now().Add(2 * time.Second)
			} else if lb.mode == BDMODE_DIE {
				lb.timeout = time.Now().Add(2 * time.Second)
				if lb.server.settings.GetBool("baddyitems", false) {
					go lb.dropItem()
				}
			} else if lb.mode == BDMODE_DEAD {
				if lb.canRespawn {
					respawnTime := lb.server.settings.GetInt("baddyrespawntime", 60)
					lb.timeout = time.Now().Add(time.Duration(respawnTime) * time.Second)
				} else if lb.level != nil {
					lb.level.removeBaddy(lb.id)
				}
			}
		case BDPROP_ANI:
			lb.ani = byte(buf.ReadGChar())
		case BDPROP_DIR:
			lb.dir = byte(buf.ReadGChar())
		case BDPROP_VERSESIGHT, BDPROP_VERSEHURT, BDPROP_VERSEATTACK:
			verseId := int(propId - BDPROP_VERSESIGHT)
			if verseId < len(lb.verses) {
				strLen := buf.ReadGChar()
				if strLen > 0 && buf.Remaining() >= int(strLen) {
					lb.verses[verseId] = string(buf.ReadBytes(int(strLen)))
				}
			}
		}
	}
}

func (lb *LevelBaddy) setRespawn(respawn bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.canRespawn = respawn
}
func (lb *LevelBaddy) setId(id byte) { lb.mu.Lock(); defer lb.mu.Unlock(); lb.id = id }

type LevelBoardChange struct {
	x, y, width, height int
	newTiles, oldTiles  []byte
	time                time.Time
	timeout             time.Time
}

func NewLevelBoardChange(x, y, width, height int, newTiles []byte, oldTiles []byte, respawn time.Duration) *LevelBoardChange {
	change := &LevelBoardChange{x: x, y: y, width: width, height: height, newTiles: newTiles, oldTiles: oldTiles, time: time.Now()}
	if respawn >= 0 {
		change.timeout = time.Now().Add(respawn)
	}
	return change
}

func (lbc *LevelBoardChange) GetBoardStr() []byte {
	buf := NewBuffer()
	buf.WriteByte(byte(lbc.x))
	buf.WriteByte(byte(lbc.y))
	buf.WriteByte(byte(lbc.width))
	buf.WriteByte(byte(lbc.height))
	buf.Write(lbc.newTiles)
	return buf.Bytes()
}

func (lbc *LevelBoardChange) SwapTiles() {
	lbc.newTiles, lbc.oldTiles = lbc.oldTiles, lbc.newTiles
}

func (lbc *LevelBoardChange) GetX() int              { return lbc.x }
func (lbc *LevelBoardChange) GetY() int              { return lbc.y }
func (lbc *LevelBoardChange) GetWidth() int          { return lbc.width }
func (lbc *LevelBoardChange) GetHeight() int         { return lbc.height }
func (lbc *LevelBoardChange) GetTiles() []byte       { return lbc.newTiles }
func (lbc *LevelBoardChange) GetModTime() time.Time  { return lbc.time }
func (lbc *LevelBoardChange) SetModTime(t time.Time) { lbc.time = t }
func (lbc *LevelBoardChange) GetTimeout() time.Time  { return lbc.timeout }
func (lbc *LevelBoardChange) IsExpired() bool        { return time.Now().After(lbc.timeout) }

type LevelChest struct {
	x, y      int
	itemType  LevelItemType
	signIndex int
}
type LevelHorse struct {
	image       string
	x, y        float32
	dir, bushes byte
}
type LevelItem struct {
	x, y     float32
	itemType LevelItemType
}
type LevelLink struct {
	x, y          float32
	width, height float32
	destLevel     string
	destX, destY  float32
}

func NewLevelLink() *LevelLink { return &LevelLink{} }

func (ll *LevelLink) GetLinkStr() string {
	return fmt.Sprintf("%s %d %d %d %d %s %s",
		ll.destLevel,
		int(ll.x),
		int(ll.y),
		int(ll.width),
		int(ll.height),
		fmt.Sprintf("%.0f", ll.destX),
		fmt.Sprintf("%.0f", ll.destY),
	)
}

func (ll *LevelLink) ParseLinkStr(parts []string) {
	if len(parts) < 7 {
		return
	}
	offset := 0
	if len(parts) > 7 {
		offset = len(parts) - 7
	}
	ll.destLevel = parts[0]
	for i := 0; i < offset; i++ {
		ll.destLevel += " " + parts[1+i]
	}
	ll.x = parseFloat(parts[1+offset])
	ll.y = parseFloat(parts[2+offset])
	ll.width = parseFloat(parts[3+offset])
	ll.height = parseFloat(parts[4+offset])
	ll.destX = parseFloat(parts[5+offset])
	ll.destY = parseFloat(parts[6+offset])
}

func (ll *LevelLink) GetNewLevel() string { return ll.destLevel }
func (ll *LevelLink) GetNewX() float32    { return ll.destX }
func (ll *LevelLink) GetNewY() float32    { return ll.destY }
func (ll *LevelLink) GetX() float32       { return ll.x }
func (ll *LevelLink) GetY() float32       { return ll.y }
func (ll *LevelLink) GetWidth() float32   { return ll.width }
func (ll *LevelLink) GetHeight() float32  { return ll.height }

func (ll *LevelLink) SetNewLevel(level string) { ll.destLevel = level }
func (ll *LevelLink) SetNewX(x float32)        { ll.destX = x }
func (ll *LevelLink) SetNewY(y float32)        { ll.destY = y }
func (ll *LevelLink) SetX(x float32)           { ll.x = x }
func (ll *LevelLink) SetY(y float32)           { ll.y = y }
func (ll *LevelLink) SetWidth(w float32)       { ll.width = w }
func (ll *LevelLink) SetHeight(h float32)      { ll.height = h }

type LevelSign struct {
	x, y            int
	text            string
	unformattedText string
}

func NewLevelSign(x, y int, sign string, encoded bool) *LevelSign {
	ls := &LevelSign{x: x, y: y}
	if encoded {
		ls.text = sign
		ls.unformattedText = decodeSignCode([]byte(sign))
	} else {
		ls.unformattedText = sign
		ls.text = encodeSign(sign)
	}
	return ls
}

func (ls *LevelSign) GetSignStr(player *Player) []byte {
	buf := NewBuffer()
	buf.WriteGChar(byte(ls.x))
	buf.WriteGChar(byte(ls.y))
	if player != nil {
		buf.Write([]byte(encodeSign(ls.unformattedText)))
	} else {
		buf.Write([]byte(ls.text))
	}
	return buf.Bytes()
}

func (ls *LevelSign) GetX() int        { return ls.x }
func (ls *LevelSign) GetY() int        { return ls.y }
func (ls *LevelSign) GetText() string  { return ls.text }
func (ls *LevelSign) GetUText() string { return ls.unformattedText }
func (ls *LevelSign) SetX(value int)   { ls.x = value }
func (ls *LevelSign) SetY(value int)   { ls.y = value }

func (ls *LevelSign) SetText(value string) {
	ls.text = value
	ls.unformattedText = decodeSignCode([]byte(value))
}

func (ls *LevelSign) SetUText(value string) {
	ls.text = encodeSign(value)
	ls.unformattedText = value
}

const signText = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!?-.,#>()#####\"####':/~&### <####;\n"
const signSymbols = "ABXYudlrhxyz#4."

var ctablen = []int{1, 1, 1, 1, 1, 1, 1, 1, 2, 1, 1, 1, 2, 2, 1}
var ctabindex = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 15, 17}
var ctab = []byte{91, 92, 93, 94, 77, 78, 79, 80, 74, 75, 71, 72, 73, 86, 86, 87, 88, 67}

func encodeSignCode(text string) []byte {
	buf := NewBuffer()
	for i := 0; i < len(text); i++ {
		letter := text[i]
		if letter == '#' && i+1 < len(text) {
			i++
			letter = text[i]
			code := strings.IndexByte(signSymbols, letter)
			if code != -1 {
				for ii := 0; ii < ctablen[code]; ii++ {
					buf.WriteGChar(ctab[ctabindex[code]+ii] - 32)
				}
				continue
			} else {
				i--
				letter = '#'
			}
		}
		code := strings.IndexByte(signText, letter)
		if letter == '#' {
			code = 86
		}
		if code != -1 {
			buf.WriteGChar(byte(code))
		} else if letter != '\r' {
			buf.WriteGChar(86 - 32)
			buf.WriteByte(byte(10))
			buf.WriteGChar(69 - 32)
			scode := fmt.Sprintf("%d", letter)
			for j := 0; j < len(scode); j++ {
				c := strings.IndexByte(signText, scode[j])
				if c != -1 {
					buf.WriteGChar(byte(c))
				}
			}
			buf.WriteGChar(70 - 32)
		}
	}
	return buf.Bytes()
}

func decodeSignCode(data []byte) string {
	buf := NewBufferFromBytes(data)
	var result strings.Builder
	for buf.Remaining() > 0 {
		letter := buf.ReadGChar()
		isCode := false
		codeID := -1
		for j := 0; j < len(ctab); j++ {
			if byte(letter) == ctab[j] {
				codeID = j
				isCode = true
				break
			}
		}
		if isCode {
			codeIndex := -1
			for j := 0; j < len(ctabindex); j++ {
				if ctabindex[j] == codeID {
					codeIndex = j
					break
				}
			}
			if codeIndex != -1 && codeIndex < len(signSymbols) {
				result.WriteByte('#')
				result.WriteByte(signSymbols[codeIndex])
			}
		} else if int(letter) < len(signText) {
			result.WriteByte(signText[letter])
		}
	}
	str := result.String()
	str = strings.ReplaceAll(str, "#K(13)", "")
	return str
}

func encodeSign(signText string) string {
	lines := strings.Split(signText, "\n")
	var result strings.Builder
	for _, line := range lines {
		result.Write(encodeSignCode(line + "\n"))
	}
	return result.String()
}

const (
	ItemGreenRupee LevelItemType = iota
	ItemBlueRupee
	ItemRedRupee
	ItemBombs
	ItemDarts
	ItemHeart
	ItemGlove1
	ItemBow
	ItemBomb
	ItemShield
	ItemSword
	ItemFullHeart
	ItemSuperBomb
	ItemBattleAxe
	ItemGoldenSword
	ItemMirrorShield
	ItemGlove2
	ItemLizardShield
	ItemLizardSword
	ItemGoldRupee
	ItemFireball
	ItemFireblast
	ItemNukeshot
	ItemJoltbomb
	ItemSpinattack
)

var itemList = []string{
	"greenrupee", "bluerupee", "redrupee", "bombs", "darts", "heart", "glove1", "bow", "bomb", "shield", "sword",
	"fullheart", "superbomb", "battleaxe", "goldensword", "mirrorshield", "glove2", "lizardshield", "lizardsword",
	"goldrupee", "fireball", "fireblast", "nukeshot", "joltbomb", "spinattack",
}

func getItemId(itemName string) LevelItemType {
	for i, name := range itemList {
		if name == itemName {
			return LevelItemType(i)
		}
	}
	return LevelItemType(-1)
}
func getItemName(itemType LevelItemType) string {
	if int(itemType) < 0 || int(itemType) >= len(itemList) {
		return ""
	}
	return itemList[itemType]
}
func getItemPlayerProp(itemType LevelItemType, player *Player) []byte {
	buf := NewBuffer()
	switch itemType {
	case ItemGreenRupee, ItemBlueRupee, ItemRedRupee, ItemGoldRupee:
		rupeeCount := int(player.character.gralats)
		if itemType == ItemGoldRupee {
			rupeeCount += 100
		} else if itemType == ItemRedRupee {
			rupeeCount += 30
		} else if itemType == ItemBlueRupee {
			rupeeCount += 5
		} else {
			rupeeCount += 1
		}
		if rupeeCount > 9999999 {
			rupeeCount = 9999999
		}
		buf.WriteGChar(PLPROP_RUPEESCOUNT)
		buf.WriteGInt(uint32(rupeeCount))
	case ItemBombs:
		bombCount := int(player.character.bombs) + 5
		if bombCount > 99 {
			bombCount = 99
		}
		buf.WriteGChar(PLPROP_BOMBSCOUNT)
		buf.WriteGChar(byte(bombCount))
	case ItemDarts:
		arrowCount := int(player.character.arrows) + 5
		if arrowCount > 99 {
			arrowCount = 99
		}
		buf.WriteGChar(PLPROP_ARROWSCOUNT)
		buf.WriteGChar(byte(arrowCount))
	case ItemHeart:
		newPower := float64(player.character.hitpoints) + 1.0
		maxPower := float64(player.maxHitpoints)
		if newPower > maxPower {
			newPower = maxPower
		}
		buf.WriteGChar(PLPROP_CURPOWER)
		buf.WriteGChar(byte(int(newPower * 2)))
	case ItemGlove1, ItemGlove2:
		glovePower := int(player.character.glovePower)
		if itemType == ItemGlove2 {
			glovePower = 3
		} else if glovePower < 2 {
			glovePower = 2
		}
		buf.WriteGChar(PLPROP_GLOVEPOWER)
		buf.WriteGChar(byte(glovePower))
	case ItemBow, ItemBomb, ItemSuperBomb, ItemFireball, ItemFireblast, ItemNukeshot, ItemJoltbomb:
		player.addWeapon(getItemName(itemType))
		return nil
	case ItemShield, ItemMirrorShield, ItemLizardShield:
		newShieldPower := 1
		if itemType == ItemLizardShield {
			newShieldPower = 3
		} else if itemType == ItemMirrorShield {
			newShieldPower = 2
		}
		if int(player.character.shieldPower) > newShieldPower {
			newShieldPower = int(player.character.shieldPower)
		}
		buf.WriteGChar(PLPROP_SHIELDPOWER)
		buf.WriteGChar(byte(newShieldPower + 10))
	case ItemSword, ItemBattleAxe, ItemLizardSword, ItemGoldenSword:
		swordPower := int(player.character.swordPower)
		if itemType == ItemGoldenSword {
			swordPower = 4
		} else if itemType == ItemLizardSword {
			if swordPower < 3 {
				swordPower = 3
			}
		} else if itemType == ItemBattleAxe {
			if swordPower < 2 {
				swordPower = 2
			}
		} else {
			if swordPower < 1 {
				swordPower = 1
			}
		}
		buf.WriteGChar(PLPROP_SWORDPOWER)
		buf.WriteGChar(byte(swordPower + 30))
	case ItemFullHeart:
		heartMax := int(player.maxHitpoints) + 1
		if heartMax > 20 {
			heartMax = 20
		}
		buf.WriteGChar(PLPROP_MAXPOWER)
		buf.WriteGChar(byte(heartMax))
		buf.WriteGChar(PLPROP_CURPOWER)
		buf.WriteGChar(byte(heartMax * 2))
	case ItemSpinattack:
		return nil
	}
	return buf.Bytes()
}

func NewLevel() *Level {
	return &Level{
		tiles: make(map[uint8]*LevelTiles), baddies: make(map[uint8]*LevelBaddy),
		npcs: make(map[uint32]*NPC), players: make([]uint16, 0),
	}
}
func getBase64Position(c byte) int {
	switch {
	case c >= 'a' && c <= 'z':
		return 26 + int(c-'a')
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= '0' && c <= '9':
		return 52 + int(c-'0')
	case c == '+':
		return 62
	case c == '/':
		return 63
	}
	return 0
}
func (l *Level) loadLevel(server *Server, levelName string) bool {
	if strings.HasSuffix(strings.ToLower(levelName), ".nw") {
		return l.loadNW(server, levelName)
	}
	if strings.HasSuffix(strings.ToLower(levelName), ".zelda") {
		return l.loadZelda(server, levelName)
	}
	return false
}
func (l *Level) loadNW(server *Server, levelName string) bool {
	l.levelName = levelName
	lines, err := server.config.LoadFileAsLines(levelName)
	if err != nil {
		return false
	}
	if len(lines) == 0 {
		return false
	}
	if modTime, err := server.config.FileModTime(levelName); err == nil {
		l.modTime = modTime
	}
	l.fileVersion = lines[0]
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "BOARD":
			if len(parts) != 6 {
				continue
			}
			x, _ := strconv.Atoi(parts[1])
			y, _ := strconv.Atoi(parts[2])
			w, _ := strconv.Atoi(parts[3])
			layer, _ := strconv.Atoi(parts[4])
			if x < 0 || x >= 64 || y < 0 || y >= 64 || w <= 0 || x+w > 64 {
				continue
			}
			data := parts[5]
			if len(data) >= w*2 {
				if l.tiles[uint8(layer)] == nil {
					l.tiles[uint8(layer)] = &LevelTiles{tiles: make([]int16, 4096)}
				}
				for ii := 0; ii < w; ii++ {
					left := getBase64Position(data[ii*2])
					right := getBase64Position(data[ii*2+1])
					tile := int16(left<<6 | right)
					l.tiles[uint8(layer)].tiles[x+ii+y*64] = tile
				}
			}
		case "CHEST":
			if len(parts) != 5 {
				continue
			}
			chestx, _ := strconv.Atoi(parts[1])
			chesty, _ := strconv.Atoi(parts[2])
			itemType := getItemId(parts[3])
			signIdx, _ := strconv.Atoi(parts[4])
			l.chests = append(l.chests, &LevelChest{x: chestx, y: chesty, itemType: itemType, signIndex: signIdx})
		case "SIGN":
			if len(parts) != 3 {
				continue
			}
			signx, _ := strconv.Atoi(parts[1])
			signy, _ := strconv.Atoi(parts[2])
			var text strings.Builder
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "SIGNEND" {
				text.WriteString(lines[i] + "\n")
				i++
			}
			l.signs = append(l.signs, NewLevelSign(signx, signy, text.String(), false))
		case "LINK":
			if len(parts) < 8 {
				continue
			}
			linkx, _ := strconv.ParseFloat(parts[1], 32)
			linky, _ := strconv.ParseFloat(parts[2], 32)
			destX, _ := strconv.ParseFloat(parts[4], 32)
			destY, _ := strconv.ParseFloat(parts[5], 32)
			destLevel := strings.Join(parts[6:len(parts)-1], " ")
			l.links = append(l.links, &LevelLink{x: float32(linkx), y: float32(linky), destLevel: destLevel, destX: float32(destX), destY: float32(destY)})
		case "BADDY":
			if len(parts) != 4 {
				continue
			}
			bx, _ := strconv.Atoi(parts[1])
			by, _ := strconv.Atoi(parts[2])
			btype, _ := strconv.Atoi(parts[3])
			baddy := NewLevelBaddy(float32(bx), float32(by), byte(btype), l, server)
			baddy.id = uint8(len(l.baddies))
			l.baddies[baddy.id] = baddy
		case "NPC":
			if len(parts) < 4 {
				continue
			}
			npcx, _ := strconv.ParseFloat(parts[2], 32)
			npcy, _ := strconv.ParseFloat(parts[3], 32)
			image := parts[1]
			if len(parts) > 4 {
				image = strings.Join(parts[1:len(parts)-2], " ")
			}
			var script strings.Builder
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "NPCEND" {
				script.WriteString(lines[i] + "\n")
				i++
			}
			npc := &NPC{npcType: LEVELNPC, x: int16(npcx), y: int16(npcy), z: 0, image: image, script: script.String(), level: l, saves: [10]byte{}}
			if server.AddNPC(npc) {
				l.npcs[npc.id] = npc
			}
		}
	}
	return true
}
func (l *Level) loadZelda(server *Server, levelName string) bool {
	levelPath := "world/levels/" + levelName + ".zelda"
	data, err := server.config.LoadFile(levelPath)
	if err != nil {
		return false
	}
	buf := NewBufferFromBytes(data)
	l.levelName = levelName
	version := string(buf.data[buf.read : buf.read+8])
	buf.read += 8
	var bits int
	if version == "Z3-V1.03" {
		bits = 12
	} else if version == "Z3-V1.04" {
		bits = 13
	} else {
		return false
	}
	var bitBuffer uint32
	var bitRead int
	var code uint16
	tiles := [2]int16{-1, -1}
	boardIndex := 0
	count := 1
	doubleMode := false
	for boardIndex < 64*64 && buf.read < len(buf.data) {
		for bitRead < bits {
			bitBuffer += uint32(buf.ReadByte()) << bitRead
			bitRead += 8
		}
		if bits == 12 {
			code = uint16(bitBuffer & 0xFFF)
		} else {
			code = uint16(bitBuffer & 0x1FFF)
		}
		bitBuffer >>= uint(bits)
		bitRead -= bits
		controlBit := uint16(0x800)
		if bits == 13 {
			controlBit = 0x1000
		}
		if code&controlBit != 0 {
			if code&0x100 != 0 {
				doubleMode = true
			}
			count = int(code & 0xFF)
			continue
		}
		if count == 1 {
			l.tiles[0].tiles[boardIndex] = int16(code)
			boardIndex++
			continue
		}
		if doubleMode {
			if tiles[0] == -1 {
				tiles[0] = int16(code)
				continue
			}
			tiles[1] = int16(code)
			for i := 0; i < count && boardIndex < 64*64-1; i++ {
				l.tiles[0].tiles[boardIndex] = tiles[0]
				boardIndex++
				l.tiles[0].tiles[boardIndex] = tiles[1]
				boardIndex++
			}
			tiles = [2]int16{-1, -1}
			doubleMode = false
			count = 1
		} else {
			for i := 0; i < count && boardIndex < 64*64; i++ {
				l.tiles[0].tiles[boardIndex] = int16(code)
				boardIndex++
			}
			count = 1
		}
	}
	for buf.read < len(buf.data) {
		lineBytes := []byte{}
		for buf.read < len(buf.data) && buf.data[buf.read] != '\n' {
			lineBytes = append(lineBytes, buf.ReadByte())
		}
		if buf.read < len(buf.data) && buf.data[buf.read] == '\n' {
			buf.read++
		}
		if len(lineBytes) == 0 {
			break
		}
		line := string(lineBytes)
		if line == "#" {
			break
		}
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}
		linkx, _ := strconv.ParseFloat(parts[0], 32)
		linky, _ := strconv.ParseFloat(parts[1], 32)
		destX, _ := strconv.ParseFloat(parts[3], 32)
		destY, _ := strconv.ParseFloat(parts[4], 32)
		width, _ := strconv.ParseFloat(parts[5], 32)
		height, _ := strconv.ParseFloat(parts[6], 32)
		destLevel := strings.Join(parts[7:], " ")
		l.links = append(l.links, &LevelLink{x: float32(linkx), y: float32(linky), destLevel: destLevel, destX: float32(destX), destY: float32(destY), width: float32(width), height: float32(height)})
	}
	for buf.read < len(buf.data) {
		x := buf.ReadByte()
		y := buf.ReadByte()
		btype := buf.ReadByte()
		if x == 0xFF && y == 0xFF && btype == 0xFF {
			if buf.read < len(buf.data) && buf.data[buf.read] == '\n' {
				buf.read++
			}
			break
		}
		bx := int8(x)
		by := int8(y)
		baddy := NewLevelBaddy(float32(bx), float32(by), btype, l, server)
		baddy.id = uint8(len(l.baddies))
		l.baddies[baddy.id] = baddy
		if bits == 13 {
			verseBytes := []byte{}
			for buf.read < len(buf.data) && buf.data[buf.read] != '\n' {
				verseBytes = append(verseBytes, buf.ReadByte())
			}
			if buf.read < len(buf.data) && buf.data[buf.read] == '\n' {
				buf.read++
			}
			verses := strings.Split(string(verseBytes), "\\")
			if len(verses) > 3 {
				verses = verses[:3]
			}
			for i := 0; i < len(verses); i++ {
				baddy.verses[i] = verses[i]
			}
		}
	}
	for buf.read < len(buf.data) {
		lineBytes := []byte{}
		for buf.read < len(buf.data) && buf.data[buf.read] != '\n' {
			lineBytes = append(lineBytes, buf.ReadByte())
		}
		if buf.read < len(buf.data) && buf.data[buf.read] == '\n' {
			buf.read++
		}
		if len(lineBytes) == 0 {
			break
		}
		lineBuf := NewBufferFromBytes(lineBytes)
		sx := int8(lineBuf.ReadByte())
		sy := int8(lineBuf.ReadByte())
		text := string(lineBuf.data[lineBuf.read:])
		l.signs = append(l.signs, NewLevelSign(int(sx), int(sy), text, true))
	}
	return true
}

func (l *Level) getName() string        { return l.levelName }
func (l *Level) getModTime() time.Time  { return l.modTime }
func (l *Level) setSparringZone(v bool) { l.isSparringZone = v }
func (l *Level) setSingleplayer(v bool) { l.isSingleplayer = v }
func (l *Level) addPlayer(p *Player) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, pid := range l.players {
		if pid == p.id {
			return
		}
	}
	l.players = append(l.players, p.id)
}
func (l *Level) removePlayer(p *Player) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, pid := range l.players {
		if pid == p.id {
			l.players = append(l.players[:i], l.players[i+1:]...)
			return
		}
	}
}
func (l *Level) getPlayers() []uint16 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.players
}
func (l *Level) getBoardPacket() []byte {
	buf := NewBuffer()
	buf.WriteGChar(PLO_BOARDPACKET)
	mainLayer := l.tiles[0]
	if mainLayer != nil && len(mainLayer.tiles) == 4096 {
		for i := 0; i < 4096; i++ {
			tile := mainLayer.tiles[i]
			buf.WriteByte(byte(tile & 0xFF))
			buf.WriteByte(byte(uint16(tile) >> 8))
		}
	} else {
		for i := 0; i < 8192; i++ {
			buf.WriteByte(0)
		}
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
func (l *Level) getTileAt(x, y int) int16 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if x < 0 || x >= 64 || y < 0 || y >= 64 {
		return 0
	}
	if l.tiles[0] != nil && len(l.tiles[0].tiles) == 4096 {
		return l.tiles[0].tiles[x+y*64]
	}
	return 0
}
func (l *Level) setTileAt(x, y int, tile int16) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setTileAtLocked(x, y, tile)
}

func (l *Level) setTileAtLocked(x, y int, tile int16) {
	if x < 0 || x >= 64 || y < 0 || y >= 64 {
		return
	}
	if l.tiles[0] == nil {
		l.tiles[0] = &LevelTiles{tiles: make([]int16, 4096)}
	}
	if len(l.tiles[0].tiles) == 4096 {
		l.tiles[0].tiles[x+y*64] = tile
	}
}

func (l *Level) alterBoard(server *Server, x, y, width, height int, tiles []int16) bool {
	if x < 0 || y < 0 || width <= 0 || height <= 0 || x+width > 64 || y+height > 64 || len(tiles) < width*height {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tiles[0] == nil {
		l.tiles[0] = &LevelTiles{tiles: make([]int16, 4096)}
	}
	oldTiles := make([]int16, 0, width*height)
	for yy := 0; yy < height; yy++ {
		for xx := 0; xx < width; xx++ {
			oldTiles = append(oldTiles, l.tiles[0].tiles[(x+xx)+(y+yy)*64])
		}
	}
	for i := len(l.boardChanges) - 1; i >= 0; i-- {
		change := l.boardChanges[i]
		if change.x >= x && change.x+change.width <= x+width && change.y >= y && change.y+change.height <= y+height {
			l.boardChanges = append(l.boardChanges[:i], l.boardChanges[i+1:]...)
		}
	}
	idx := 0
	for yy := 0; yy < height; yy++ {
		for xx := 0; xx < width; xx++ {
			l.setTileAtLocked(x+xx, y+yy, tiles[idx])
			idx++
		}
	}
	oldTile := int16(0)
	if len(oldTiles) > 0 {
		oldTile = oldTiles[0]
	}
	respawnDuration := time.Duration(-1)
	if isRespawningTile(oldTile) {
		respawnSeconds := 15
		if server != nil && server.settings != nil {
			respawnSeconds = server.settings.GetInt("respawntime", 15)
		}
		respawnDuration = time.Duration(respawnSeconds) * time.Second
	}
	change := NewLevelBoardChange(x, y, width, height, shortsToBytes(tiles[:width*height]), shortsToBytes(oldTiles), respawnDuration)
	l.boardChanges = append(l.boardChanges, *change)
	return true
}

func (l *Level) processBoardRespawns(server *Server) {
	l.mu.Lock()
	var respawns []LevelBoardChange
	now := time.Now()
	for i := 0; i < len(l.boardChanges); {
		change := l.boardChanges[i]
		if !change.timeout.IsZero() && !now.Before(change.timeout) && len(change.oldTiles) > 0 {
			oldTiles := bytesToShorts(change.oldTiles)
			idx := 0
			for yy := 0; yy < change.height; yy++ {
				for xx := 0; xx < change.width && idx < len(oldTiles); xx++ {
					l.setTileAtLocked(change.x+xx, change.y+yy, oldTiles[idx])
					idx++
				}
			}
			respawns = append(respawns, LevelBoardChange{x: change.x, y: change.y, width: change.width, height: change.height, newTiles: change.oldTiles, time: now})
			l.boardChanges = append(l.boardChanges[:i], l.boardChanges[i+1:]...)
			continue
		}
		i++
	}
	l.mu.Unlock()
	for _, change := range respawns {
		server.broadcastBoardModify(l, int16(change.x), int16(change.y), int16(change.width), int16(change.height), bytesToShorts(change.newTiles))
	}
}

func (l *Level) addItem(x, y float32, itemType LevelItemType) {
	if getItemName(itemType) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, LevelItem{x: x, y: y, itemType: itemType})
}

func (l *Level) removeItem(x, y float32) LevelItemType {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, item := range l.items {
		if item.x == x && item.y == y {
			l.items = append(l.items[:i], l.items[i+1:]...)
			return item.itemType
		}
	}
	return LevelItemType(-1)
}

func (l *Level) getChestKey(chest *LevelChest) string {
	if l == nil || chest == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s", chest.x, chest.y, l.levelName)
}

func isRespawningTile(tile int16) bool {
	switch tile {
	case 2, 0x1a4, 0x1ff, 0x2ac, 0x3ff:
		return true
	default:
		return false
	}
}

func (l *Level) removeBaddy(id uint8) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.baddies, id)
}

// ============ NPC ============
type NPC struct {
	mu                            sync.Mutex
	id                            uint32
	npcType                       NPCType
	x, y, z                       int16
	width, height                 int
	image                         string
	script                        string
	npcName, scripter, scriptType string
	timeout                       int
	sprite                        byte
	visFlags, blockFlags          byte
	hurtX, hurtY                  float32
	saves                         [10]byte
	character                     Character
	weaponName                    string
	scriptData                    string
	level                         *Level
}

func NewNPC(npcType NPCType) *NPC {
	return &NPC{id: 0, npcType: npcType, x: 30 * 16, y: 30 * 16, z: 0, saves: [10]byte{}, level: nil}
}
func (n *NPC) setId(id uint32) { n.id = id }
func (n *NPC) getId() uint32   { return n.id }
func (n *NPC) runTimeout()     { /* TODO: Implement timeout event */ }

// ============ WEAPON ============
type Weapon struct {
	name, image, script string
	bytecode            []byte
	bytecodeFile        string
	defPlayer           bool
	modified            bool
}

func NewWeapon(name string) *Weapon { return &Weapon{name: name} }

// ============ SCRIPT CLASS ============
type ScriptClass struct{ name, script string }

func NewScriptClass(name string) *ScriptClass { return &ScriptClass{name: name} }

// ============ MAP ============
type MapType int

const (
	MapTypeBigMap MapType = iota
	MapTypeGmap
)

type MapLevel struct{ mapx, mapy int }

type Map struct {
	server           *Server
	mapType          MapType
	modTime          time.Time
	width, height    int
	groupMap         bool
	loadFullMap      bool
	mapName          string
	mapImage         string
	miniMapImage     string
	levels           map[string]MapLevel
	levelList        []string
	preloadLevelList []string
	mu               sync.RWMutex
}

func NewMap(mapType MapType, groupMap bool) *Map {
	return &Map{mapType: mapType, groupMap: groupMap, levels: make(map[string]MapLevel), levelList: make([]string, 0), preloadLevelList: make([]string, 0)}
}

func (m *Map) IsLevelOnMap(level string) (bool, int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ml, ok := m.levels[strings.ToLower(level)]; ok {
		return true, ml.mapx, ml.mapy
	}
	return false, -1, -1
}

func (m *Map) GetLevelAt(mx, my int) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if mx >= 0 && mx < m.width && my >= 0 && my < m.height {
		return m.levelList[mx+my*m.width]
	}
	return ""
}

func (m *Map) GetMapName() string { return m.mapName }
func (m *Map) GetType() MapType   { return m.mapType }
func (m *Map) GetWidth() int      { return m.width }
func (m *Map) GetHeight() int     { return m.height }
func (m *Map) IsBigMap() bool     { return m.mapType == MapTypeBigMap }
func (m *Map) IsGmap() bool       { return m.mapType == MapTypeGmap }
func (m *Map) IsGroupMap() bool   { return m.groupMap }

func (m *Map) Load(fileName string) error {
	if m.mapType == MapTypeBigMap {
		return m.loadBigMap(fileName)
	} else if m.mapType == MapTypeGmap {
		return m.loadGMap(fileName)
	}
	return nil
}

func (m *Map) loadBigMap(fileName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := m.server.config.LoadFile(fileName)
	if err != nil {
		return err
	}
	m.modTime, _ = m.server.config.FileModTime(fileName)
	m.mapName = fileName
	lines := strings.Split(string(data), "\n")
	m.levels = make(map[string]MapLevel)
	m.width = 0
	m.height = 0
	var mapData [][]string
	for _, line := range lines {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if line == "" {
			continue
		}
		levelList := strings.Split(guntokenize(line), "\n")
		empty := 0
		for _, lvl := range levelList {
			if lvl == "" {
				empty++
			}
		}
		currentWidth := len(levelList) - empty
		m.height++
		if currentWidth > m.width {
			m.width = currentWidth
		}
		mapData = append(mapData, levelList)
	}
	levelMap := make([]string, m.width*m.height)
	for my, row := range mapData {
		for mx, lvl := range row {
			if mx < m.width {
				lcLevelName := strings.ToLower(lvl)
				if lcLevelName != "" {
					levelMap[mx+my*m.width] = lcLevelName
					m.levels[lcLevelName] = MapLevel{mapx: mx, mapy: my}
				}
			}
		}
	}
	m.levelList = levelMap
	return nil
}

func (m *Map) loadGMap(fileName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := m.server.config.LoadFile(fileName)
	if err != nil {
		return err
	}
	m.modTime, _ = m.server.config.FileModTime(fileName)
	m.mapName = fileName
	m.levels = make(map[string]MapLevel)
	m.width = 0
	m.height = 0
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.ReplaceAll(lines[i], "\r", ""))
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "WIDTH":
			if len(parts) == 2 {
				m.width, _ = strconv.Atoi(parts[1])
			}
		case "HEIGHT":
			if len(parts) == 2 {
				m.height, _ = strconv.Atoi(parts[1])
			}
		case "GENERATED":
		case "LEVELNAMES":
			i++
			gmapy := 0
			levelMap := make([]string, m.width*m.height)
			for i < len(lines) {
				line := strings.TrimSpace(strings.ReplaceAll(lines[i], "\r", ""))
				if line == "" {
					i++
					continue
				}
				if line == "LEVELNAMESEND" {
					break
				}
				if gmapy < m.height {
					line = guntokenize(line)
					names := strings.Split(line, "\n")
					gmapx := 0
					for _, levelName := range names {
						if gmapx < m.width && levelName != "\r" {
							lcLevelName := strings.ToLower(levelName)
							levelMap[gmapx+gmapy*m.width] = lcLevelName
							m.levels[lcLevelName] = MapLevel{mapx: gmapx, mapy: gmapy}
							gmapx++
						}
					}
					gmapy++
				}
				i++
			}
			m.levelList = levelMap
		case "MAPIMG":
			if len(parts) == 2 {
				m.mapImage = parts[1]
			}
		case "MINIMAPIMG":
			if len(parts) == 2 {
				m.miniMapImage = parts[1]
			}
		case "NOAUTOMAPPING":
		case "LOADFULLMAP":
			m.loadFullMap = true
		case "LOADATSTART":
			m.loadFullMap = false
			i++
			for i < len(lines) {
				line := strings.ReplaceAll(lines[i], "\r", "")
				if line == "LOADATSTARTEND" {
					break
				}
				line = guntokenize(line)
				names := strings.Split(line, "\n")
				for _, levelName := range names {
					m.preloadLevelList = append(m.preloadLevelList, strings.ToLower(levelName))
				}
				i++
			}
		}
	}
	return nil
}

func (m *Map) LoadMapLevels() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.loadFullMap {
		for _, levelName := range m.levelList {
			if levelName != "" {
				m.server.GetLevel(levelName)
			}
		}
	} else if len(m.preloadLevelList) > 0 {
		for _, levelName := range m.preloadLevelList {
			m.server.GetLevel(levelName)
		}
	}
}

func guntokenize(s string) string {
	var result []byte
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\\' {
			switch s[i+1] {
			case 'n':
				result = append(result, '\n')
				i += 2
			case 'r':
				result = append(result, '\r')
				i += 2
			case 't':
				result = append(result, '\t')
				i += 2
			case 's':
				result = append(result, ' ')
				i += 2
			case '\\':
				result = append(result, '\\')
				i += 2
			default:
				result = append(result, s[i])
				i++
			}
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

// ============ FILE PERMISSIONS ============
type PermissionType int

const (
	PermissionRead PermissionType = iota
	PermissionWrite
	PermissionCount
)

type Permission struct {
	flags    [PermissionCount]bool
	segments []*regexp.Regexp
}

type FilePermissions struct {
	mu                  sync.RWMutex
	permissions         []Permission
	negativePermissions []Permission
}

func NewFilePermissions() *FilePermissions {
	return &FilePermissions{permissions: make([]Permission, 0), negativePermissions: make([]Permission, 0)}
}

func (fp *FilePermissions) HasPermission(path string, permType PermissionType) bool {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	for _, perm := range fp.negativePermissions {
		if perm.flags[permType] && fp.match(path, perm) {
			return false
		}
	}
	for _, perm := range fp.permissions {
		if perm.flags[permType] && fp.match(path, perm) {
			return true
		}
	}
	return false
}

func (fp *FilePermissions) AddPermission(permissionString string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	perm := Permission{}
	var segments []string
	idx := 0
	negative := false
	if len(permissionString) > 0 && permissionString[0] == '-' {
		negative = true
		idx = 1
	}
	for ; idx < len(permissionString); idx++ {
		ch := permissionString[idx]
		if ch == 'r' {
			perm.flags[PermissionRead] = true
		} else if ch == 'w' {
			perm.flags[PermissionWrite] = true
		} else if ch == ' ' {
			segments = splitInput(permissionString[idx+1:], '/')
			break
		}
	}
	if len(segments) > 0 {
		for _, seg := range segments {
			replaced := strings.ReplaceAll(seg, "*", ".*")
			if re, err := regexp.Compile("^" + replaced + "$"); err == nil {
				perm.segments = append(perm.segments, re)
			}
		}
		if negative {
			fp.negativePermissions = append(fp.negativePermissions, perm)
		} else {
			fp.permissions = append(fp.permissions, perm)
		}
	}
}

func (fp *FilePermissions) LoadPermissions(permissionString string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.permissions = make([]Permission, 0)
	fp.negativePermissions = make([]Permission, 0)
	lines := splitInput(permissionString, '\n')
	for _, line := range lines {
		fp.addPermissionUnsafe(line)
	}
}

func (fp *FilePermissions) addPermissionUnsafe(permissionString string) {
	perm := Permission{}
	var segments []string
	idx := 0
	negative := false
	if len(permissionString) > 0 && permissionString[0] == '-' {
		negative = true
		idx = 1
	}
	for ; idx < len(permissionString); idx++ {
		ch := permissionString[idx]
		if ch == 'r' {
			perm.flags[PermissionRead] = true
		} else if ch == 'w' {
			perm.flags[PermissionWrite] = true
		} else if ch == ' ' {
			segments = splitInput(permissionString[idx+1:], '/')
			break
		}
	}
	if len(segments) > 0 {
		for _, seg := range segments {
			replaced := strings.ReplaceAll(seg, "*", ".*")
			if re, err := regexp.Compile("^" + replaced + "$"); err == nil {
				perm.segments = append(perm.segments, re)
			}
		}
		if negative {
			fp.negativePermissions = append(fp.negativePermissions, perm)
		} else {
			fp.permissions = append(fp.permissions, perm)
		}
	}
}

func (fp *FilePermissions) match(path string, perm Permission) bool {
	segments := splitInput(path, '/')
	if len(segments) == 0 || len(segments) != len(perm.segments) {
		return false
	}
	for i := 0; i < len(segments); i++ {
		if !perm.segments[i].MatchString(segments[i]) {
			return false
		}
	}
	return true
}

func splitInput(input string, delimiter byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(input); i++ {
		if input[i] == delimiter {
			lines = append(lines, input[start:i])
			start = i + 1
		}
	}
	if start < len(input) {
		lines = append(lines, input[start:])
	}
	return lines
}

// ============ SERVER LIST ============
type ServerList struct {
	server                *Server
	conn                  net.Conn
	connected             bool
	sendQueue             chan []byte
	enabled               bool
	description           string
	nextConnectionAttempt time.Time
	connectionAttempts    int
	lastTimer             time.Time
	lastReceive           time.Time
	lastIdleLog           time.Time
	lastKeepalive         time.Time
	codec                 uint32
	readBuffer            []byte
}

func NewServerList(s *Server) *ServerList {
	return &ServerList{server: s, sendQueue: make(chan []byte, 100)}
}

func (sl *ServerList) doTimedEvents() {
	now := time.Now()
	sl.lastTimer = now
	if !sl.connected && now.After(sl.nextConnectionAttempt) {
		if !sl.connectServer() {
			if sl.connectionAttempts < 8 {
				sl.connectionAttempts++
			}
			waitTime := time.Duration(1<<uint(sl.connectionAttempts)) * time.Second
			if waitTime > 300*time.Second {
				waitTime = 300 * time.Second
			}
			sl.nextConnectionAttempt = now.Add(waitTime)
		} else {
			sl.connectionAttempts = 0
		}
	}
	if sl.connected && now.Sub(sl.lastKeepalive) >= time.Minute {
		sl.lastKeepalive = now
		sl.sendSetIPKeepalive()
	}
}

func (sl *ServerList) connectServer() bool {
	if sl.connected {
		return true
	}
	listip := sl.server.settings.Get("listip")
	listport := sl.server.settings.Get("listport")
	if listip == "" || listport == "" {
		return false
	}
	sl.server.logger.Write(":: Initializing listserver socket.")
	address := net.JoinHostPort(listip, listport)
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		sl.server.logger.Error("Could not connect listserver socket: %v", err)
		return false
	}
	sl.conn = conn
	sl.connected = true
	sl.lastReceive = time.Now()
	sl.lastIdleLog = time.Time{}
	sl.lastKeepalive = time.Now()
	go sl.sendLoop(conn)
	go sl.receiveLoop()
	sl.server.logger.Write(":: listserver - Connected.")
	// Set GEN_1 for first packet
	sl.codec = ENCRYPT_GEN_1
	// Packet 1: SVO_REGISTERV3 with writeGChar encoding
	buf := NewBuffer()
	buf.WriteGChar(SVO_REGISTERV3)
	buf.WriteString(APP_VERSION)
	sl.sendPacket(buf.Bytes())
	// Switch to GEN_2 for all subsequent packets
	sl.codec = ENCRYPT_GEN_2
	// Packet 2: SVO_SERVERHQPASS
	buf = NewBuffer()
	hqPassword := sl.server.adminSettings.Get("hq_password")
	buf.WriteGChar(SVO_SERVERHQPASS)
	buf.WriteString8(hqPassword)
	sl.sendPacket(buf.Bytes())
	// Packet 3: SVO_NEWSERVER
	buf = NewBuffer()
	name := sl.server.settings.Get("name")
	if name == "" {
		name = sl.server.name
	}
	desc := sl.server.settings.Get("description")
	if desc == "" {
		desc = sl.server.name
	}
	language := sl.server.settings.Get("language")
	if language == "" {
		language = "English"
	}
	url := sl.server.settings.Get("url")
	if url == "" {
		url = "http://www.graal.in/"
	}
	ip := sl.server.settings.Get("serverip")
	if ip == "" {
		ip = "AUTO"
	}
	port := sl.server.settings.Get("serverport")
	if port == "" {
		port = "14802"
	}
	localip := sl.server.settings.Get("localip")
	if localip == "" || localip == "AUTO" {
		localip = conn.LocalAddr().(*net.TCPAddr).IP.String()
	}
	if localip == "127.0.1.1" || localip == "127.0.0.1" {
		sl.server.logger.Warning("Socket returned %s for its local ip! Not sending local ip to serverlist.", localip)
		localip = ""
	}
	buf.WriteGChar(SVO_NEWSERVER)
	buf.WriteString8Encoded(name)
	buf.WriteString8Encoded(desc)
	buf.WriteString8Encoded(language)
	buf.WriteString8Encoded(APP_VERSION)
	buf.WriteString8Encoded(url)
	buf.WriteString8Encoded(ip)
	buf.WriteString8Encoded(port)
	buf.WriteString8Encoded(localip)
	sl.server.logger.Debug("[LISTSERVER] NEWSERVER packet: name=%d desc=%d lang=%d ver=%d url=%d ip=%d port=%d localip=%d", len(name), len(desc), len(language), len(APP_VERSION), len(url), len(ip), len(port), len(localip))
	var hexData string
	for i := 0; i < 30 && i < buf.Len(); i++ {
		hexData += fmt.Sprintf("%02X ", buf.Bytes()[i])
	}
	sl.server.logger.Debug("[LISTSERVER] NEWSERVER data: %s", hexData)
	sl.sendPacket(buf.Bytes())
	// Packet 4: SVO_SERVERHQLEVEL
	buf = NewBuffer()
	if sl.server.settings.GetBool("onlystaff", false) {
		buf.WriteGChar(SVO_SERVERHQLEVEL).WriteGChar(0)
	} else {
		hqLevel := sl.server.adminSettings.GetInt("hq_level", 1)
		buf.WriteGChar(SVO_SERVERHQLEVEL).WriteGChar(byte(hqLevel))
	}
	sl.sendPacket(buf.Bytes())
	sl.sendVersionConfig()
	sl.sendPlayers()
	return true
}

func (sl *ServerList) sendSetIPKeepalive() {
	ip := sl.server.settings.Get("serverip")
	if ip == "" {
		ip = "AUTO"
	}
	sl.server.logger.Debug("[LISTSERVER] Sending SVO_SETIP keepalive")
	sl.SetIp(ip)
}

func (sl *ServerList) sendVersionConfig() {
	if !sl.connected {
		return
	}
	text := "Listserver,settings,allowedversions," + sl.server.allowedVersionsListserverText()
	buf := NewBuffer()
	buf.WriteGChar(SVO_SENDTEXT).Write([]byte(text))
	sl.sendPacket(buf.Bytes())
}

func (sl *ServerList) Connect(address string) error {
	if !sl.enabled {
		return nil
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	sl.conn = conn
	sl.connected = true
	sl.lastReceive = time.Now()
	sl.lastIdleLog = time.Time{}
	sl.lastKeepalive = time.Now()
	go sl.sendLoop(conn)
	go sl.receiveLoop()
	return nil
}

func (sl *ServerList) sendLoop(conn net.Conn) {
	for packet := range sl.sendQueue {
		if !sl.connected || sl.conn != conn {
			break
		}
		if _, err := conn.Write(packet); err != nil {
			sl.server.logger.Error("[LISTSERVER] Send error: %v", err)
			if sl.conn == conn {
				sl.Disconnect()
			}
			break
		}
	}
}

func (sl *ServerList) receiveLoop() {
	buf := make([]byte, 4096)
	for sl.connected {
		sl.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := sl.conn.Read(buf)
		if err != nil {
			if err == io.EOF {
				sl.server.logger.Debug("[LISTSERVER] Connection closed by listserver")
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				now := time.Now()
				if now.Sub(sl.lastReceive) >= 30*time.Second && now.Sub(sl.lastIdleLog) >= 30*time.Second {
					sl.server.logger.Debug("[LISTSERVER] Idle waiting for listserver data for %s", now.Sub(sl.lastReceive).Round(time.Second))
					sl.lastIdleLog = now
				}
				continue
			} else {
				sl.server.logger.Error("[LISTSERVER] Receive error: %v", err)
			}
			break
		}
		sl.server.logger.Debug("[LISTSERVER] Received %d raw bytes", n)
		sl.lastReceive = time.Now()
		sl.readBuffer = append(sl.readBuffer, buf[:n]...)
		sl.processListData()
	}
	sl.connected = false
	sl.server.logger.Debug("[LISTSERVER] receiveLoop exiting")
}

func (sl *ServerList) processListData() {
	sl.server.logger.Debug("[LISTSERVER] processListData: %d bytes in buffer", len(sl.readBuffer))
	for len(sl.readBuffer) >= 2 {
		// Read 2-byte length prefix (big-endian)
		length := int(sl.readBuffer[0])<<8 | int(sl.readBuffer[1])
		sl.server.logger.Debug("[LISTSERVER] Packet length: %d, have %d", length, len(sl.readBuffer))
		if len(sl.readBuffer) < length+2 {
			break // Need more data
		}
		// Extract compressed data
		compressed := sl.readBuffer[2 : length+2]
		// Decompress
		reader, err := zlib.NewReader(bytes.NewReader(compressed))
		if err != nil {
			sl.server.logger.Error("[LISTSERVER] Decompress error: %v", err)
			break
		}
		decompressed, _ := io.ReadAll(reader)
		reader.Close()
		sl.server.logger.Debug("[LISTSERVER] Decompressed to %d bytes", len(decompressed))
		// Process decompressed packets (line-by-line)
		sl.processListPackets(decompressed)
		// Remove processed data
		sl.readBuffer = sl.readBuffer[length+2:]
	}
}

func (sl *ServerList) processListPackets(data []byte) {
	for len(data) > 0 {
		// Find newline
		nl := bytes.IndexByte(data, '\n')
		if nl == -1 {
			break
		}
		// Extract packet line
		packet := data[:nl]
		data = data[nl+1:]
		// Read packet ID (Graal-encoded: subtract 32)
		if len(packet) == 0 {
			continue
		}
		packetId := uint8(packet[0])
		if packetId >= 32 {
			packetId -= 32 // Undo Graal encoding
		}
		// Dispatch to handler
		sl.handleListPacket(packetId, packet[1:])
	}
}

func (sl *ServerList) handleListPacket(packetId uint8, data []byte) {
	sl.server.logger.Debug("[LISTSERVER] Received packet %d: %d bytes", packetId, len(data))
	switch packetId {
	case SVI_VERIACC, SVI_VERIACC2:
		sl.server.logger.Debug("List server verification response")
	case SVI_FILESTART, SVI_FILESTART2, SVI_FILESTART3:
		sl.server.logger.Debug("File transfer started")
	case SVI_FILEDATA, SVI_FILEDATA2, SVI_FILEDATA3:
	case SVI_FILEEND, SVI_FILEEND2, SVI_FILEEND3:
	case SVI_SERVERINFO:
		sl.server.logger.Debug("Server info received")
	case SVI_ERRMSG:
		sl.server.logger.Error("List server error: %s", string(data))
	case SVI_PING:
		buf := NewBuffer()
		buf.WriteGChar(SVO_PING)
		sl.SendPacket(buf.Bytes())
	}
}

func (sl *ServerList) processPacket(data []byte) {
	if len(data) == 0 {
		return
	}
	packetId := data[0]
	switch packetId {
	case SVI_VERIACC, SVI_VERIACC2:
		sl.server.logger.Debug("List server verification response")
	case SVI_FILESTART, SVI_FILESTART2, SVI_FILESTART3:
		sl.server.logger.Debug("File transfer started")
	case SVI_FILEDATA, SVI_FILEDATA2, SVI_FILEDATA3:
	case SVI_FILEEND, SVI_FILEEND2, SVI_FILEEND3:
	case SVI_SERVERINFO:
		sl.server.logger.Debug("Server info received")
	case SVI_ERRMSG:
		sl.server.logger.Error("List server error: %s", string(data[1:]))
	case SVI_PING:
		buf := NewBuffer()
		buf.WriteGChar(SVO_PING)
		sl.SendPacket(buf.Bytes())
	}
}
func (sl *ServerList) SendPacket(packet []byte) {
	if sl.connected {
		sl.sendPacket(packet)
	}
}

func (sl *ServerList) SendTextPacket(packetId byte, text string) {
	if !sl.connected {
		return
	}
	buf := NewBuffer()
	buf.WriteGChar(packetId).Write([]byte(text))
	sl.sendPacket(buf.Bytes())
}

func (sl *ServerList) sendPacket(packet []byte) {
	if !sl.connected {
		return
	}
	// Append '\n' if missing
	if len(packet) > 0 && packet[len(packet)-1] != '\n' {
		packet = append(packet, '\n')
	}
	// Apply codec
	switch sl.codec {
	case ENCRYPT_GEN_1:
		// Raw packet
		var hexOut string
		for i, b := range packet {
			if i == 0 {
				hexOut += fmt.Sprintf(" [%d] ", b)
			} else {
				hexOut += fmt.Sprintf("%02X ", b)
			}
			if i > 20 {
				hexOut += "..."
				break
			}
		}
		sl.server.logger.Debug("[LISTSERVER] GEN_1 Sending %d bytes: %s", len(packet), hexOut)
		sl.sendQueue <- packet
	case ENCRYPT_GEN_2:
		// Zlib compress + 2-byte length prefix
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		w.Write(packet)
		w.Close()
		compressed := buf.Bytes()
		data := NewBuffer()
		data.WriteShort(int16(len(compressed)))
		data.Write(compressed)
		finalPacket := data.Bytes()
		var hexOut string
		for i, b := range finalPacket {
			if i == 0 {
				hexOut += fmt.Sprintf(" [%02X] ", b)
			} else {
				hexOut += fmt.Sprintf("%02X ", b)
			}
			if i > 20 {
				hexOut += "..."
				break
			}
		}
		sl.server.logger.Debug("[LISTSERVER] GEN_2 Sending %d bytes (compressed from %d): %s", len(finalPacket), len(packet), hexOut)
		sl.sendQueue <- finalPacket
	default:
		sl.sendQueue <- packet
	}
}

func (sl *ServerList) sendPlayers() {
	if !sl.connected {
		return
	}
	sl.server.syncNPCServer()
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETPLYR)
	sl.sendPacket(buf.Bytes())
	for _, player := range sl.server.players {
		if isListserverPlayer(player) {
			sl.server.logger.Debug("[LISTSERVER] Sending player to listserver: id=%d type=%d account=%s nickname=%s level=%s x=%d y=%d", player.id, player.playerType, player.accountName, player.character.nickName, player.levelName, int(player.x), int(player.y))
			sl.sendPlayerAdd(player)
		}
	}
}
func (sl *ServerList) AddPlayer(player *Player) {
	if !sl.connected || !isListserverPlayer(player) {
		return
	}
	sl.sendPlayerAdd(player)
}

func isListserverPlayer(player *Player) bool {
	return isPlayerListPlayer(player)
}

func isPlayerListPlayer(player *Player) bool {
	return player != nil && player.playerType&PLTYPE_ANYPLAYER != 0
}

func shouldSendClientPlayerListEntry(player *Player) bool {
	if !isPlayerListPlayer(player) {
		return false
	}
	if player.playerType&PLTYPE_NPCSERVER != 0 {
		return true
	}
	if player.server == nil || player.server.settings == nil {
		return true
	}
	if !player.server.settings.GetBool("hidestaff", false) {
		return true
	}
	return !player.isStaff
}

func (s *Server) broadcastPlayerListEntryToClients(player *Player) {
	if s == nil || player == nil || !shouldSendClientPlayerListEntry(player) {
		return
	}
	propsSet := getRCLoginProps
	if player.playerType&PLTYPE_ANYCLIENT != 0 {
		propsSet = getLoginProps
	}
	props := player.sendPropsWithArray(propsSet)
	if len(props) == 0 {
		return
	}
	packet := append(player.otherPropsPacket(props), '\n')
	s.playerMu.RLock()
	defer s.playerMu.RUnlock()
	for _, other := range s.players {
		if other == nil || other == player || other.conn == nil || !other.isLoggedIn() {
			continue
		}
		if other.playerType&PLTYPE_ANYCLIENT == 0 {
			continue
		}
		other.sendPacket(packet)
	}
}

func (sl *ServerList) sendPlayerAdd(player *Player) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_PLYRADD)
	buf.WriteGShort(uint16(player.id))
	buf.WriteGChar(byte(player.playerType))
	for _, propId := range []int{PLPROP_ACCOUNTNAME, PLPROP_NICKNAME, PLPROP_CURLEVEL, PLPROP_X, PLPROP_Y, PLPROP_ALIGNMENT, PLPROP_IPADDR} {
		buf.WriteGChar(byte(propId))
		buf.Write(player.getProp(propId))
	}
	packetBytes := buf.Bytes()
	sl.server.logger.Info("[LISTSERVER] PLYRADD packet (%d bytes): %v", len(packetBytes), packetBytes)
	sl.sendPacket(packetBytes)
}
func (sl *ServerList) DeletePlayer(player *Player) {
	if !sl.connected {
		return
	}
	buf := NewBuffer()
	buf.WriteGChar(SVO_PLYRREM).WriteGShort(uint16(player.id))
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) Disconnect() {
	sl.connected = false
	if sl.conn != nil {
		sl.conn.Close()
	}
}
func (sl *ServerList) SetName(name string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETNAME).WriteString8Encoded(name)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetDesc(desc string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETDESC).WriteString8Encoded(desc)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetLang(lang string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETLANG).WriteString8Encoded(lang)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetVers(vers string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETVERS).WriteString8Encoded(vers)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetUrl(url string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETURL).WriteString8Encoded(url)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetIp(ip string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETIP).Write([]byte(ip))
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetPort(port string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETPORT).WriteString8Encoded(port)
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) SetPlyr(count int) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_SETPLYR).WriteGInt(uint32(count))
	sl.SendPacket(buf.Bytes())
}
func (sl *ServerList) VerifyAccount(account string) {
	buf := NewBuffer()
	buf.WriteGChar(SVO_VERIACC).WriteString8Encoded(account)
	sl.SendPacket(buf.Bytes())
}

// ============ UPDATE PACKAGE ============
type UpdatePackageFileEntry struct{ size, checksum uint32 }

type UpdatePackage struct {
	packageName string
	fileList    map[string]UpdatePackageFileEntry
	checksum    uint32
	packageSize uint32
	mu          sync.RWMutex
}

func NewUpdatePackage(packageName string) *UpdatePackage {
	return &UpdatePackage{packageName: packageName, fileList: make(map[string]UpdatePackageFileEntry)}
}

func (up *UpdatePackage) GetPackageName() string { return up.packageName }
func (up *UpdatePackage) GetPackageSize() uint32 {
	up.mu.RLock()
	defer up.mu.RUnlock()
	return up.packageSize
}
func (up *UpdatePackage) GetFileList() map[string]UpdatePackageFileEntry {
	up.mu.RLock()
	defer up.mu.RUnlock()
	return up.fileList
}
func (up *UpdatePackage) CompareChecksum(check uint32) bool {
	up.mu.RLock()
	defer up.mu.RUnlock()
	return up.checksum == check
}

func (up *UpdatePackage) Reload(server *Server) {
	up.mu.Lock()
	defer up.mu.Unlock()
	up.checksum = 0
	up.packageSize = 0
	up.fileList = make(map[string]UpdatePackageFileEntry)
	fileContents, err := server.config.LoadFile(up.packageName)
	if err != nil {
		return
	}
	up.checksum = calculateCrc32Checksum(fileContents)
	lines := strings.Split(string(fileContents), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FILE") {
			filePath := strings.TrimSpace(line[4:])
			baseFileName := filepath.Base(filePath)
			updateFileData, err := server.config.LoadFile(baseFileName)
			if err != nil {
				buf := NewBuffer()
				buf.WriteByte(PLO_PRIVATEMESSAGE)
				buf.WriteString8(fmt.Sprintf("[Server]: Unable to find file '%s' in package '%s'", baseFileName, up.packageName))
				server.sendPacketToType(PLTYPE_RC, buf.Bytes())
				continue
			}
			fileLength := uint32(len(updateFileData))
			up.fileList[baseFileName] = UpdatePackageFileEntry{size: fileLength, checksum: calculateCrc32Checksum(updateFileData)}
			up.packageSize += fileLength
		}
	}
}

func LoadUpdatePackage(server *Server, name string) (*UpdatePackage, bool) {
	pkg := NewUpdatePackage(name)
	pkg.Reload(server)
	return pkg, pkg.checksum != 0
}

// ============ UTILITIES ============
func trimSpace(s string) string                 { return strings.TrimSpace(s) }
func splitN(s string, sep rune, n int) []string { return strings.SplitN(s, string(sep), n) }
func atoi(s string) int                         { i, _ := strconv.Atoi(s); return i }
func parseFloat(s string) float32               { f, _ := strconv.ParseFloat(s, 32); return float32(f) }
func toLower(s string) string                   { return strings.ToLower(s) }

func shortsToBytes(shorts []int16) []byte {
	bytes := make([]byte, len(shorts)*2)
	for i, s := range shorts {
		bytes[i*2] = byte(s >> 8)
		bytes[i*2+1] = byte(s)
	}
	return bytes
}

func bytesToShorts(bytes []byte) []int16 {
	shorts := make([]int16, len(bytes)/2)
	for i := 0; i < len(shorts); i++ {
		shorts[i] = int16(bytes[i*2])<<8 | int16(bytes[i*2+1])
	}
	return shorts
}

func calculateCrc32Checksum(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

func (p *Player) sendFile(fileName string) {
	data, err := p.server.config.LoadFile(fileName)
	if err != nil {
		p.server.logger.Warning("sendFile: Failed to load %s: %v", fileName, err)
		p.sendPLO_FILESENDFAILED(fileName)
		return
	}
	modTime := time.Time{}
	if p.server != nil && p.server.config != nil {
		modTime, _ = p.server.config.FileModTime(fileName)
	}
	filePacket := NewBuffer()
	filePacket.WriteGChar(PLO_FILE)
	filePacket.WriteGInt5(uint64(modTime.Unix()))
	filePacket.WriteGChar(byte(len(fileName)))
	filePacket.Write([]byte(fileName))
	filePacket.Write(data)
	filePacket.WriteByte('\n')

	buf := NewBuffer()
	buf.WriteByte(PLO_RAWDATA)
	buf.WriteGInt(uint32(filePacket.Len()))
	buf.WriteByte('\n')
	buf.Write(filePacket.Bytes())
	p.sendPacket(buf.Bytes())
	p.server.logger.Debug("sendFile: Sent %s (%d bytes)", fileName, len(data))
}

func (p *Player) getPMServerList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var list []string
	for server := range p.channelList {
		if strings.HasPrefix(strings.ToLower(server), "pm:") {
			list = append(list, strings.TrimPrefix(server, "pm:"))
		}
	}
	return list
}

func (p *Player) addPMServer(serverName string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	channelKey := "pm:" + serverName
	if _, exists := p.channelList[channelKey]; !exists {
		p.channelList[channelKey] = true
		return true
	}
	return false
}

func (p *Player) remPMServer(serverName string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	channelKey := "pm:" + serverName
	delete(p.channelList, channelKey)
	for extId, extPlayer := range p.externalPlayers {
		if extPlayer.serverName == serverName {
			delete(p.externalPlayers, extId)
			if p.playerType == PLTYPE_RC || p.playerType == PLTYPE_RC2 {
				buf := NewBuffer()
				buf.WriteByte(PLO_DELPLAYER)
				buf.WriteGShort(uint16(extId))
				p.send(buf)
			}
		}
	}
	return true
}

func (p *Player) updatePMPlayers(serverName string, playersData string) {
	playersList := strings.Split(playersData, "\n")
	p.mu.Lock()
	defer p.mu.Unlock()
	for extId, extPlayer := range p.externalPlayers {
		if extPlayer.serverName == serverName {
			found := false
			for _, playerLine := range playersList {
				if playerLine == "" {
					continue
				}
				parts := strings.SplitN(strings.ReplaceAll(playerLine, "\x01", "\n"), "\n", 2)
				if len(parts) >= 2 {
					account := parts[0]
					if account == extPlayer.Account.accountName {
						found = true
						break
					}
				}
			}
			if !found {
				delete(p.externalPlayers, extId)
				if p.playerType == PLTYPE_RC || p.playerType == PLTYPE_RC2 {
					buf := NewBuffer()
					buf.WriteByte(PLO_DELPLAYER)
					buf.WriteGShort(uint16(extId))
					p.send(buf)
				}
			}
		}
	}
	for _, playerLine := range playersList {
		if playerLine == "" {
			continue
		}
		parts := strings.SplitN(strings.ReplaceAll(playerLine, "\x01", "\n"), "\n", 2)
		if len(parts) < 2 {
			continue
		}
		account := parts[0]
		nick := parts[1]
		found := false
		for _, extPlayer := range p.externalPlayers {
			if extPlayer.serverName == serverName && extPlayer.Account.accountName == account {
				extPlayer.character.nickName = nick + " (on " + serverName + ")"
				found = true
				break
			}
		}
		if !found {
			p.externalPlayerIdGen++
			newExt := &Player{server: p.server, id: p.externalPlayerIdGen, serverName: serverName}
			newExt.Account.accountName = account
			newExt.character.nickName = nick + " (on " + serverName + ")"
			p.externalPlayers[p.externalPlayerIdGen] = newExt
		}
	}
	if p.playerType == PLTYPE_RC || p.playerType == PLTYPE_RC2 {
		for _, extPlayer := range p.externalPlayers {
			buf := NewBuffer()
			buf.WriteByte(PLO_ADDPLAYER)
			buf.WriteGShort(extPlayer.id)
			buf.WriteByte(PLPROP_ACCOUNTNAME)
			buf.WriteString8(extPlayer.Account.accountName)
			buf.WriteByte(PLPROP_NICKNAME)
			buf.WriteString8(extPlayer.character.nickName)
			buf.WriteByte(PLPROP_UNKNOWN81)
			buf.WriteByte(1)
			p.send(buf)
		}
	} else {
		for _, extPlayer := range p.externalPlayers {
			p.sendPLO_OTHERPLPROPS(extPlayer)
		}
	}
}

func (p *Player) pmExternalPlayer(serverName, account, message string) {
	if p.server.serverList == nil || !p.server.serverList.connected {
		return
	}
	buf := NewBuffer()
	buf.WriteByte(SVO_PMPLAYER)
	buf.WriteGShort(p.id)
	buf.WriteString8(serverName)
	buf.WriteString8(p.Account.accountName)
	buf.WriteString8(p.character.nickName)
	buf.WriteString8("GraalEngine")
	buf.WriteString8("pmplayer")
	buf.WriteString8(account)
	buf.WriteString8(message)
	p.server.serverList.SendPacket(buf.Bytes())
}

func (p *Player) getExternalPlayerById(id uint16) *Player {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.externalPlayers[id]
}

func (p *Player) getExternalPlayerByAccount(accountName string) *Player {
	p.mu.Lock()
	defer p.mu.Unlock()
	lowerAccount := strings.ToLower(accountName)
	for _, extPlayer := range p.externalPlayers {
		if strings.ToLower(extPlayer.Account.accountName) == lowerAccount {
			return extPlayer
		}
	}
	return nil
}

const (
	FILTER_CHECK_CHAT  = 0x1
	FILTER_CHECK_PM    = 0x2
	FILTER_CHECK_NICK  = 0x4
	FILTER_CHECK_TOALL = 0x8
)

const (
	FILTER_POSITION_FULL  = 1
	FILTER_POSITION_START = 2
	FILTER_POSITION_PART  = 3
)

const (
	FILTER_ACTION_LOG     = 0x1
	FILTER_ACTION_TELLRC  = 0x2
	FILTER_ACTION_REPLACE = 0x4
	FILTER_ACTION_WARN    = 0x8
	FILTER_ACTION_JAIL    = 0x10
	FILTER_ACTION_BAN     = 0x20
)

type WordFilterRule struct {
	check               int
	wordPosition        int
	action              int
	precision           int
	precisionPercentage bool
	match               string
	warnMessage         string
}

type WordFilter struct {
	server             *Server
	mu                 sync.RWMutex
	showWordsToRC      bool
	defaultWarnMessage string
	rules              []WordFilterRule
}

func wfIsUpper(c byte) bool { return c >= 65 && c <= 90 }

func wfIsLower(c byte) bool { return c >= 97 && c <= 122 }

func wfToLower(c byte) byte {
	if c >= 65 && c <= 90 {
		return c + 32
	}
	return c
}

func (wf *WordFilter) load(fileName string) {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	wf.rules = nil
	lines, err := wf.server.config.LoadFileAsLines(fileName)
	if err != nil {
		return
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		if parts[0] == "RULE" {
			rule := WordFilterRule{precision: 100, precisionPercentage: true}
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "RULEEND" {
				line2 := strings.TrimSpace(lines[i])
				parts2 := strings.Fields(line2)
				if len(parts2) == 0 {
					i++
					continue
				}

				switch parts2[0] {
				case "CHECK":
					for j := 1; j < len(parts2); j++ {
						switch parts2[j] {
						case "chat":
							rule.check |= FILTER_CHECK_CHAT
						case "pm":
							rule.check |= FILTER_CHECK_PM
						case "nick":
							rule.check |= FILTER_CHECK_NICK
						case "toall":
							rule.check |= FILTER_CHECK_TOALL
						}
					}
				case "MATCH":
					if len(parts2) == 2 {
						rule.match = parts2[1]
					}
				case "PRECISION":
					if len(parts2) == 2 {
						if strings.Contains(parts2[1], "%") {
							rule.precisionPercentage = true
							parts2[1] = strings.TrimSuffix(parts2[1], "%")
						} else {
							rule.precisionPercentage = false
						}
						if val, err := strconv.Atoi(parts2[1]); err == nil {
							rule.precision = val
						}
					}
				case "WORDPOSITION":
					for j := 1; j < len(parts2); j++ {
						switch parts2[j] {
						case "full":
							rule.wordPosition |= FILTER_POSITION_FULL
						case "start":
							rule.wordPosition |= FILTER_POSITION_START
						case "part":
							rule.wordPosition |= FILTER_POSITION_PART
						}
					}
				case "ACTION":
					for j := 1; j < len(parts2); j++ {
						switch parts2[j] {
						case "log":
							rule.action |= FILTER_ACTION_LOG
						case "tellrc":
							rule.action |= FILTER_ACTION_TELLRC
						case "replace":
							rule.action |= FILTER_ACTION_REPLACE
						case "warn":
							rule.action |= FILTER_ACTION_WARN
						case "jail":
							rule.action |= FILTER_ACTION_JAIL
						case "ban":
							rule.action |= FILTER_ACTION_BAN
						}
					}
				case "WARNMESSAGE":
					if len(line2) > 12 {
						rule.warnMessage = strings.TrimSpace(line2[12:])
					}
				}
				i++
			}
			if rule.check != 0 && rule.action != 0 && rule.wordPosition != 0 {
				wf.rules = append(wf.rules, rule)
			}
		} else if parts[0] == "WARNMESSAGE" {
			if len(line) > 12 {
				wf.defaultWarnMessage = strings.TrimSpace(line[12:])
			}
		} else if parts[0] == "SHOWWORDSTORC" {
			if len(parts) == 2 && parts[1] == "true" {
				wf.showWordsToRC = true
			}
		}
	}
}

func (wf *WordFilter) apply(player *Player, chat string, check int) string {
	wf.mu.RLock()
	if len(chat) == 0 || len(wf.rules) == 0 || check == 0 {
		wf.mu.RUnlock()
		return chat
	}
	wf.mu.RUnlock()

	out := chat
	var warnMessage string
	chatWords := strings.Fields(chat)
	var wordsFound []string
	actionsFound := 0

	for _, rule := range wf.rules {
		if check&rule.check == 0 {
			continue
		}

		if rule.wordPosition != FILTER_POSITION_PART {
			for _, word := range chatWords {
				if rule.wordPosition == FILTER_POSITION_FULL && len(word) != len(rule.match) {
					continue
				}

				wordsMatched := 0
				failed := false
				for chatpos := 0; chatpos < len(rule.match) && chatpos < len(word); chatpos++ {
					letter := rule.match[chatpos]
					wordLetter := word[chatpos]
					if letter == '?' {
						wordsMatched++
						continue
					}
					if wfIsLower(byte(letter)) && byte(letter) == wfToLower(wordLetter) {
						wordsMatched++
					} else if wfIsUpper(byte(letter)) {
						if wfToLower(byte(letter)) == wfToLower(wordLetter) {
							wordsMatched++
						} else {
							failed = true
							break
						}
					}
				}
				if failed {
					continue
				}

				if !rule.precisionPercentage && wordsMatched < rule.precision {
					continue
				}
				if rule.precisionPercentage && rule.precision > int((float64(wordsMatched)/float64(len(rule.match)))*100) {
					continue
				}

				wordsFound = append(wordsFound, word)
				actionsFound |= rule.action

				if rule.action&FILTER_ACTION_WARN != 0 {
					warnMessage = rule.warnMessage
					goto WordFilterActions
				}

				if rule.action&FILTER_ACTION_REPLACE != 0 {
					censor := strings.Repeat("*", len(word))
					out = strings.ReplaceAll(out, word, censor)
				}
			}
		} else if rule.wordPosition == FILTER_POSITION_PART {
			bypass := []byte{' ', '\r', '\n'}
			for wordpos := 0; wordpos < len(chat); wordpos++ {
				wordStart := wordpos
				wordsMatched := 0
				failed := false
				var word strings.Builder
				for chatpos := 0; chatpos < len(rule.match) && wordpos+chatpos < len(chat); chatpos++ {
					if wordpos+chatpos == wordStart {
						for _, b := range bypass {
							if chat[wordpos+chatpos] == b {
								failed = true
								break
							}
						}
						if failed {
							break
						}
					}

					for _, b := range bypass {
						if chat[wordpos+chatpos] == b {
							word.WriteByte(b)
							wordpos++
						}
					}

					letter := rule.match[chatpos]
					wordLetter := chat[wordpos+chatpos]
					if letter == '?' {
						word.WriteByte(wordLetter)
						wordsMatched++
						continue
					}
					if wfIsLower(byte(letter)) && byte(letter) == wfToLower(wordLetter) {
						wordsMatched++
					} else if wfIsUpper(byte(letter)) {
						if wfToLower(byte(letter)) == wfToLower(wordLetter) {
							wordsMatched++
						} else {
							failed = true
							break
						}
					}
					word.WriteByte(wordLetter)
				}
				wordpos = wordStart
				if failed {
					continue
				}

				if !rule.precisionPercentage && wordsMatched < rule.precision {
					continue
				}
				if rule.precisionPercentage && rule.precision > int((float64(wordsMatched)/float64(len(rule.match)))*100) {
					continue
				}

				trimmedWord := strings.TrimSpace(word.String())
				wordsFound = append(wordsFound, trimmedWord)
				actionsFound |= rule.action

				if rule.action&FILTER_ACTION_WARN != 0 {
					warnMessage = rule.warnMessage
					goto WordFilterActions
				}

				if rule.action&FILTER_ACTION_REPLACE != 0 {
					censor := strings.Repeat("*", len(trimmedWord))
					out = strings.ReplaceAll(out, trimmedWord, censor)
				}
			}
		}
	}

WordFilterActions:
	if len(wordsFound) == 0 {
		return chat
	}

	badwords := strings.Join(wordsFound, ", ")

	if actionsFound&FILTER_ACTION_LOG != 0 {
		wf.server.logger.Info("[Word Filter] Player %s was caught using these words: %s", player.accountName, badwords)
	}

	if wf.showWordsToRC || actionsFound&FILTER_ACTION_TELLRC != 0 {
		wf.server.sendRCChat(fmt.Sprintf("Word Filter: Player %s was caught using these words: %s", player.accountName, badwords))
	}

	if actionsFound&FILTER_ACTION_WARN != 0 {
		if warnMessage == "" {
			return wf.defaultWarnMessage
		}
		return warnMessage
	}

	return out
}
