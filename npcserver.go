package main

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const npcServerAccountName = "(npcserver)"

const npcServerDefaultPMReply = "I am the npcserver for\nthis game server. Almost\nall npc actions are controlled\nby me."

// NPCServer owns the in-process NPC-server player and NPC-control behaviors.
// Keep the GServer-facing methods small so an external NPC-server transport can
// implement the same boundary later without rewriting the game-server callers.
type NPCServer struct {
	host       *Server
	watcher    *fsnotify.Watcher
	watchStop  chan struct{}
	watching   bool
	watchMu    sync.Mutex
	debounce   map[string]time.Time
	debounceMu sync.Mutex
}

func NewNPCServer(host *Server) *NPCServer {
	return &NPCServer{host: host, debounce: make(map[string]time.Time)}
}

func (s *Server) ensureNPCServer() *NPCServer {
	if s.npcServer == nil {
		s.npcServer = NewNPCServer(s)
	}
	return s.npcServer
}

func (s *Server) initNPCServer() {
	s.ensureNPCServer().Start()
}

func (s *Server) syncNPCServer() {
	s.ensureNPCServer().Sync()
}

func (n *NPCServer) Enabled() bool {
	return n != nil && n.host != nil && n.host.settings != nil && n.host.settings.GetBool("serverside", false)
}

func (n *NPCServer) Sync() {
	if n == nil || n.host == nil {
		return
	}
	if n.Enabled() {
		if player := n.Player(); player != nil {
			n.applyPlayerSettings(player)
			n.host.broadcastPlayerListEntryToClients(player)
		} else {
			n.Start()
		}
		n.startWatching()
		return
	}
	n.stopWatching()
	if player := n.Player(); player != nil {
		n.host.DeletePlayer(player)
	}
}

func (n *NPCServer) Player() *Player {
	if n == nil || n.host == nil {
		return nil
	}
	n.host.playerMu.RLock()
	defer n.host.playerMu.RUnlock()
	for _, player := range n.host.players {
		if player != nil && player.playerType == PLTYPE_NPCSERVER {
			return player
		}
	}
	return nil
}

func (n *NPCServer) Start() *Player {
	if n == nil || n.host == nil {
		return nil
	}
	if n.host.players == nil {
		n.host.players = make(map[uint16]*Player)
	}
	if existing := n.Player(); existing != nil {
		n.applyPlayerSettings(existing)
		return existing
	}

	p := n.newNPCPlayer()
	n.host.playerMu.Lock()
	n.host.players[p.id] = p
	n.host.playerMu.Unlock()
	for _, serverList := range n.host.serverLists {
		if serverList != nil {
			serverList.AddPlayer(p)
		}
	}
	n.host.broadcastPlayerListEntryToClients(p)
	n.host.logger.Info("NPC-Server initialized (id=%d account=%s nickname=%s type=%d x=%d y=%d)", p.id, p.accountName, p.character.nickName, p.playerType, int(p.x), int(p.y))
	return p
}

func (n *NPCServer) configuredNickname() string {
	nickName := ""
	if n != nil && n.host != nil && n.host.settings != nil {
		nickName = strings.TrimSpace(n.host.settings.Get("nickname"))
	}
	if nickName == "" {
		nickName = "NPC-Server"
	}
	if !strings.Contains(strings.ToLower(nickName), "(server)") {
		nickName += " (Server)"
	}
	return nickName
}

func (n *NPCServer) applyPlayerSettings(p *Player) {
	if n == nil || n.host == nil || p == nil {
		return
	}
	p.accountName = npcServerAccountName
	p.id = 1
	p.playerType = PLTYPE_NPCSERVER
	p.loaded = true
	p.accountIp = 0
	p.accountIpStr = "0"
	headImage := n.host.settings.Get("staffhead")
	if headImage == "" {
		headImage = "head25.png"
	}
	p.character.headImage = headImage
	p.setNickname(n.configuredNickname())
}

func (n *NPCServer) SendNCAddress(to *Player, queryPacket []byte) bool {
	if n == nil || n.host == nil || to == nil {
		return false
	}
	if to.playerType&PLTYPE_ANYRC == 0 || !to.hasRight(PLPERM_NPCCONTROL) {
		return false
	}
	if !n.isLocationQuery(queryPacket) {
		return false
	}
	npcPlayer := n.Player()
	if npcPlayer == nil {
		npcPlayer = n.Start()
	}
	if npcPlayer == nil {
		return false
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NPCSERVERADDR).WriteGShort(npcPlayer.id).Write([]byte(n.AddressFor(to)))
	to.send(buf)
	return true
}

func (n *NPCServer) isLocationQuery(packet []byte) bool {
	if len(packet) == 0 {
		return true
	}
	if packet[0] == PLI_NPCSERVERQUERY {
		packet = packet[1:]
	}
	if len(packet) >= 2 {
		packet = packet[2:]
	}
	message := strings.Trim(string(packet), "\x00\r\n\t ")
	return message == "" || strings.EqualFold(message, "location")
}

func (n *NPCServer) AddressFor(requester *Player) string {
	host := ""
	if n.host.adminSettings != nil {
		host = n.host.adminSettings.Get("ns_ip")
	}
	if host == "" && n.host.settings != nil {
		host = n.host.settings.Get("ns_ip")
	}
	if (host == "" || strings.EqualFold(host, "auto")) && n.host.settings != nil {
		host = n.host.settings.Get("serverip")
	}
	if requester != nil && requester.accountIpStr != "" && n.host.settings != nil && host == n.host.settings.Get("localip") {
		host = requester.accountIpStr
	}
	if host == "" || strings.EqualFold(host, "auto") {
		host = "127.0.0.1"
	}
	port := ""
	if n.host.settings != nil {
		port = n.host.settings.Get("serverport")
	}
	if port == "" {
		port = "14802"
	}
	return host + "," + port
}

func (n *NPCServer) SendNPCList(to *Player) {
	if n == nil || n.host == nil || to == nil {
		return
	}
	n.host.npcMu.RLock()
	npcs := make([]*NPC, 0, len(n.host.npcs))
	for _, npc := range n.host.npcs {
		if npc != nil && npc.npcType == DBNPC {
			npcs = append(npcs, npc)
		}
	}
	n.host.npcMu.RUnlock()
	for _, npc := range npcs {
		n.SendNPCAdd(to, npc)
	}
}

func (n *NPCServer) SendNPCAdd(to *Player, npc *NPC) {
	if to == nil || npc == nil {
		return
	}
	levelName := ""
	if npc.level != nil {
		levelName = npc.level.levelName
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_NPCADD)
	buf.WriteGInt(npc.id)
	buf.WriteGChar(NPCPROP_NAME)
	buf.WriteGChar(byte(len(npc.npcName))).Write([]byte(npc.npcName))
	buf.WriteGChar(NPCPROP_TYPE)
	buf.WriteGChar(byte(len(npc.scriptType))).Write([]byte(npc.scriptType))
	buf.WriteGChar(NPCPROP_CURLEVEL)
	buf.WriteGChar(byte(len(levelName))).Write([]byte(levelName))
	to.send(buf)
}

func (n *NPCServer) startWatching() {
	if n == nil || n.host == nil || n.host.config == nil {
		return
	}
	n.watchMu.Lock()
	defer n.watchMu.Unlock()
	if n.watching {
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		n.host.logger.Warning("Could not start file watcher: %v", err)
		return
	}
	n.watcher = w
	n.watchStop = make(chan struct{})
	n.watching = true

	base := n.host.config.GetBasePath()
	for _, dir := range []string{"weapons", "scripts"} {
		full := filepath.Join(base, dir)
		if err := w.Add(full); err != nil {
			n.host.logger.Warning("Could not watch %s: %v", dir, err)
		}
	}

	n.host.logger.Info("Watching weapons/ and scripts/ for live reload")
	go n.watchLoop()
}

func (n *NPCServer) stopWatching() {
	if n == nil {
		return
	}
	n.watchMu.Lock()
	defer n.watchMu.Unlock()
	if !n.watching {
		return
	}
	n.watching = false
	if n.watchStop != nil {
		close(n.watchStop)
		n.watchStop = nil
	}
	if n.watcher != nil {
		n.watcher.Close()
		n.watcher = nil
	}
}

func (n *NPCServer) watchLoop() {
	for {
		select {
		case <-n.watchStop:
			return
		case event, ok := <-n.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			n.debounceMu.Lock()
			if time.Since(n.debounce[event.Name]) < 500*time.Millisecond {
				n.debounceMu.Unlock()
				continue
			}
			n.debounce[event.Name] = time.Now()
			n.debounceMu.Unlock()

			time.Sleep(100 * time.Millisecond)
			n.handleFileEvent(event.Name)

		case err, ok := <-n.watcher.Errors:
			if !ok {
				return
			}
			n.host.logger.Warning("File watcher error: %v", err)
		}
	}
}

func (n *NPCServer) handleFileEvent(fullPath string) {
	base := n.host.config.GetBasePath()
	rel, err := filepath.Rel(base, fullPath)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	fileBase := filepath.Base(fullPath)

	switch {
	case strings.HasPrefix(rel, "weapons/") && strings.HasSuffix(strings.ToLower(fileBase), ".txt"):
		n.reloadWeaponFromDisk(rel)
	case strings.HasPrefix(rel, "scripts/") && strings.HasSuffix(strings.ToLower(fileBase), ".txt"):
		n.reloadClassFromDisk(rel, fileBase)
	}
}

func (n *NPCServer) reloadWeaponFromDisk(relPath string) {
	data, err := n.host.config.LoadFile(relPath)
	if err != nil {
		return
	}
	w := parseWeapon(string(data))
	if w == nil {
		return
	}
	if w.bytecodeFile != "" {
		if bc, err := n.host.config.LoadFile("weapon_bytecode/" + w.bytecodeFile); err == nil {
			w.bytecode = bc
		}
	}

	n.host.weaponMu.Lock()
	existing := n.host.weapons[strings.ToLower(w.name)]
	if existing != nil {
		existing.image = w.image
		existing.script = w.script
		existing.bytecode = nil
		existing.bytecodeFile = w.bytecodeFile
	} else {
		n.host.weapons[strings.ToLower(w.name)] = w
		existing = w
	}
	n.host.weaponMu.Unlock()

	n.host.ensureWeaponBytecode(existing)
	n.host.updateWeaponForPlayers(existing)
	n.host.logger.Info("Reloaded weapon %s from disk", w.name)
}

func (n *NPCServer) reloadClassFromDisk(relPath, fileName string) {
	data, err := n.host.config.LoadFile(relPath)
	if err != nil {
		return
	}
	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	code := strings.ReplaceAll(string(data), "\r\n", "\n")

	n.host.weaponMu.Lock()
	n.host.classes[name] = &ScriptClass{name: name, script: code}
	cls := n.host.classes[name]
	n.host.weaponMu.Unlock()

	n.host.updateClassForPlayers(cls)
	n.host.logger.Info("Reloaded class %s from disk", name)
}

func (n *NPCServer) newNPCPlayer() *Player {
	p := &Player{conn: nil, server: n.host, recvBuffer: make([]byte, 0, 8192), encryption: *NewEncryption(), playerType: PLTYPE_NPCSERVER, cachedLevels: make([]*CachedLevel, 0), rcLargeFiles: make(map[string]string), singleplayerLevels: make(map[string]*Level), channelList: make(map[string]bool), knownFiles: make(map[string]bool), externalPlayers: make(map[uint16]*Player), externalPlayerIdGen: EXTERNALPLAYERID_INIT, firstLevel: true, loaded: true, packetCount: 0, invalidPackets: 0}
	p.flagList = make(map[string]string)
	p.folderRights = *NewFilePermissions()
	p.setServer(n.host)
	p.accountName = npcServerAccountName
	p.id = 1
	if p.LoadAccount(npcServerAccountName, false) {
		p.accountName = npcServerAccountName
		p.id = 1
		p.playerType = PLTYPE_NPCSERVER
		p.loaded = true
		p.accountIp = 0
		p.accountIpStr = "0"
	}
	n.applyPlayerSettings(p)
	now := time.Now()
	p.lastData = now
	p.lastMovement = now
	p.lastSave = now
	p.last1m = now
	p.alignment = 50
	return p
}

func (p *Player) sendNPCServerPMFallback(npcServer *Player) bool {
	if p == nil || p.server == nil {
		return false
	}
	return p.server.ensureNPCServer().SendPMFallback(p, npcServer)
}

func (n *NPCServer) SendPMFallback(to *Player, npcServer *Player) bool {
	if to == nil || npcServer == nil {
		return false
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(npcServer.id).Write([]byte("\"\","))
	buf.Write([]byte(gtokenizeText(npcServerDefaultPMReply)))
	to.send(buf)
	return true
}
