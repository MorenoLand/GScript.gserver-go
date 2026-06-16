package main

import "time"

const npcServerAccountName = "(npcserver)"

const npcServerDefaultPMReply = "I am the npcserver for\nthis game server. Almost\nall npc actions are controlled\nby me."

func (s *Server) initNPCServer() {
	if s.players == nil {
		s.players = make(map[uint16]*Player)
	}
	p := &Player{conn: nil, server: s, recvBuffer: make([]byte, 0, 8192), encryption: *NewEncryption(), playerType: PLTYPE_NPCSERVER, cachedLevels: make([]*CachedLevel, 0), rcLargeFiles: make(map[string]string), singleplayerLevels: make(map[string]*Level), channelList: make(map[string]bool), knownFiles: make(map[string]bool), externalPlayers: make(map[uint16]*Player), externalPlayerIdGen: EXTERNALPLAYERID_INIT, firstLevel: true, loaded: true, packetCount: 0, invalidPackets: 0}
	p.flagList = make(map[string]string)
	p.folderRights = *NewFilePermissions()
	p.setServer(s)
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

func (p *Player) sendNPCServerPMFallback(npcServer *Player) bool {
	if p == nil || npcServer == nil {
		return false
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(npcServer.id).Write([]byte("\"\","))
	buf.Write([]byte(gtokenizeText(npcServerDefaultPMReply)))
	p.send(buf)
	return true
}
