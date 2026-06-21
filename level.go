package main

import "time"

func (l *Level) reload(server *Server) bool {
	levelName := l.fileName
	if levelName == "" {
		levelName = l.levelName
	}
	for _, npc := range l.npcs {
		if npc != nil {
			server.DeleteNPC(npc.id)
		}
	}
	players := append([]uint16(nil), l.players...)
	l.fileVersion = ""
	l.actualLevelName = ""
	l.modTime = time.Time{}
	l.isSparringZone = false
	l.isSingleplayer = false
	l.mapX = 0
	l.mapY = 0
	l.mapRef = nil
	l.tiles = make(map[uint8]*LevelTiles)
	l.baddies = make(map[uint8]*LevelBaddy)
	l.boardChanges = nil
	l.chests = nil
	l.horses = nil
	l.items = nil
	l.links = nil
	l.signs = nil
	l.npcs = make(map[uint32]*NPC)
	l.players = players
	return l.loadLevel(server, levelName)
}
