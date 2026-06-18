package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var defaultClientFilePatterns = []string{
	"carried.gani", "carry.gani", "carrystill.gani", "carrypeople.gani", "dead.gani", "def.gani", "ghostani.gani", "grab.gani", "gralats.gani", "hatoff.gani", "haton.gani", "hidden.gani", "hiddenstill.gani", "hurt.gani", "idle.gani", "kick.gani", "lava.gani", "lift.gani", "maps1.gani", "maps2.gani", "maps3.gani", "pull.gani", "push.gani", "ride.gani", "rideeat.gani", "ridefire.gani", "ridehurt.gani", "ridejump.gani", "ridestill.gani", "ridesword.gani", "shoot.gani", "sit.gani", "skip.gani", "sleep.gani", "spin.gani", "swim.gani", "sword.gani", "walk.gani", "walkslow.gani",
	"sword?.png", "sword?.gif",
	"shield?.png", "shield?.gif",
	"body.png", "body2.png", "body3.png",
	"arrow.wav", "arrowon.wav", "axe.wav", "bomb.wav", "chest.wav", "compudead.wav", "crush.wav", "dead.wav", "extra.wav", "fire.wav", "frog.wav", "frog2.wav", "goal.wav", "horse.wav", "horse2.wav", "item.wav", "item2.wav", "jump.wav", "lift.wav", "lift2.wav", "nextpage.wav", "put.wav", "sign.wav", "steps.wav", "steps2.wav", "stonemove.wav", "sword.wav", "swordon.wav", "thunder.wav", "water.wav",
	"pics1.png",
}

func isDefaultClientFile(fileName string) bool {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(fileName)))
	for _, pattern := range defaultClientFilePatterns {
		matched, err := filepath.Match(pattern, base)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func (p *Player) handlePlayerChatCommand(chat string) bool {
	trimmed := strings.TrimSpace(chat)
	if trimmed == "" {
		return false
	}
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return false
	}
	command := strings.ToLower(words[0])
	switch command {
	case "warpto":
		return p.handlePlayerChatWarpto(words)
	case "unstick", "unstuck":
		if len(words) == 2 && strings.EqualFold(words[1], "me") {
			return p.handlePlayerChatUnstickMe(trimmed)
		}
	case "update":
		if strings.EqualFold(trimmed, "update level") && p.hasRight(PLPERM_UPDATELEVEL) {
			if p.currentLevel != nil {
				p.currentLevel.reload(p.server)
			} else if level := p.server.GetLevel(cleanLevelName(p.levelName)); level != nil {
				level.reload(p.server)
			}
			return true
		}
	}
	return false
}

func (p *Player) handlePlayerChatWarpto(words []string) bool {
	if len(words) == 2 {
		if !p.hasRight(PLPERM_WARPTOPLAYER) && !p.server.allowsWarpToAll() {
			p.character.chatMessage = "(not authorized to warp)"
			return true
		}
		if player := p.server.findPlayerByAccountOrNick(words[1], PLTYPE_ANYCLIENT); player != nil && player.currentLevel != nil {
			levelName := player.levelName
			if levelName == "" {
				levelName = player.currentLevel.levelName
			}
			p.warp(levelName, float64(player.x)/16.0, float64(player.y)/16.0)
		}
		return true
	}
	if len(words) == 3 || len(words) == 4 {
		if !p.hasRight(PLPERM_WARPTO) && !p.server.allowsWarpToAll() {
			p.character.chatMessage = "(not authorized to warp)"
			return true
		}
		x, errX := strconv.ParseFloat(words[1], 64)
		y, errY := strconv.ParseFloat(words[2], 64)
		if errX != nil || errY != nil {
			return true
		}
		if len(words) == 4 {
			p.warp(words[3], x, y)
			return true
		}
		p.setX(float32(x))
		p.setY(float32(y))
		legacyMove := NewBuffer()
		preciseMove := NewBuffer()
		p.appendPlayerPropDelta(PLPROP_X, NewBuffer(), legacyMove, preciseMove)
		p.appendPlayerPropDelta(PLPROP_Y, NewBuffer(), legacyMove, preciseMove)
		p.sendPlayerPropDeltasToCurrentLevel(nil, legacyMove.Bytes(), preciseMove.Bytes())
		return true
	}
	return true
}

func (s *Server) allowsWarpToAll() bool {
	return s != nil && s.settings != nil && s.settings.GetBool("warptoforall", false)
}

func (p *Player) handlePlayerChatUnstickMe(originalChat string) bool {
	if p.server == nil || p.server.settings == nil {
		return false
	}
	jailLevels := strings.Split(p.server.settings.Get("jaillevels"), ",")
	for _, jailLevel := range jailLevels {
		if strings.TrimSpace(jailLevel) == p.levelName {
			return false
		}
	}
	unstickTime := p.server.settings.GetInt("unstickmetime", 30)
	if int(time.Since(p.lastMovement).Seconds()) < unstickTime {
		p.character.chatMessage = fmt.Sprintf("Don't move for %d seconds before doing '%s'!", unstickTime, originalChat)
		return true
	}
	p.lastMovement = time.Now()
	levelName := p.server.settings.Get("unstickmelevel")
	if levelName == "" {
		levelName = "onlinestartlocal.nw"
	}
	x := getSettingsFloat(p.server.settings, "unstickmex", 30.0)
	y := getSettingsFloat(p.server.settings, "unstickmey", 30.5)
	p.warp(levelName, x, y)
	p.character.chatMessage = "Warped!"
	return true
}

func getSettingsFloat(settings *Settings, key string, defaultValue float64) float64 {
	if settings == nil {
		return defaultValue
	}
	if val := settings.Get(key); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

var playerPropsRC = [PROPCOUNT]bool{
	PLPROP_NICKNAME:    true,
	PLPROP_MAXPOWER:    true,
	PLPROP_CURPOWER:    true,
	PLPROP_RUPEESCOUNT: true,
	PLPROP_ARROWSCOUNT: true,
	PLPROP_BOMBSCOUNT:  true,
	PLPROP_GLOVEPOWER:  true,
	PLPROP_SWORDPOWER:  true,
	PLPROP_SHIELDPOWER: true,
	PLPROP_GANI:        true,
	PLPROP_HEADGIF:     true,
	PLPROP_COLORS:      true,
	PLPROP_X:           true,
	PLPROP_Y:           true,
	PLPROP_STATUS:      true,
	PLPROP_CURLEVEL:    true,
	PLPROP_APCOUNTER:   true,
	PLPROP_MAGICPOINTS: true,
	PLPROP_KILLSCOUNT:  true,
	PLPROP_DEATHSCOUNT: true,
	PLPROP_ONLINESECS:  true,
	PLPROP_IPADDR:      true,
	PLPROP_ALIGNMENT:   true,
	PLPROP_ACCOUNTNAME: true,
	PLPROP_BODYIMG:     true,
	PLPROP_RATING:      true,
}

func (p *Player) getPropsRC() []byte {
	ret := NewBuffer()
	ret.WriteString8Encoded(p.accountName)
	ret.WriteString8Encoded("main")

	props := NewBuffer()
	for propId, enabled := range playerPropsRC {
		if !enabled {
			continue
		}
		props.WriteGChar(byte(propId))
		props.Write(p.getProp(propId))
	}
	propData := props.Bytes()
	if len(propData) > 255 {
		propData = propData[:255]
	}
	ret.WriteGChar(byte(len(propData)))
	ret.Write(propData)

	ret.WriteGShort(uint16(len(p.flagList)))
	for flag, value := range p.flagList {
		flagText := flag
		if value != "" {
			flagText += "=" + value
		}
		if len(flagText) > 0xDF {
			flagText = flagText[:0xDF]
		}
		ret.WriteString8Encoded(flagText)
	}

	ret.WriteGShort(uint16(len(p.chestList)))
	for _, chest := range p.chestList {
		parts := strings.SplitN(chest, ":", 3)
		if len(parts) != 3 {
			continue
		}
		chestData := NewBuffer()
		chestData.WriteGChar(byte(atoi(parts[0])))
		chestData.WriteGChar(byte(atoi(parts[1])))
		chestData.Write([]byte(parts[2]))
		ret.WriteString8Encoded(string(chestData.Bytes()))
	}

	ret.WriteGChar(byte(len(p.weaponList)))
	for _, weapon := range p.weaponList {
		ret.WriteString8Encoded(weapon)
	}
	return ret.Bytes()
}

func (p *Player) setPropsFromRC(buf *Buffer, rc *Player) {
	_ = buf.ReadGCharString()
	propLen := int(buf.ReadGChar())
	props := buf.ReadBytes(propLen)
	if len(props) > 0 {
		p.msgPLI_PLAYERPROPS(append([]byte{PLI_PLAYERPROPS}, props...))
	}

	for flag, value := range p.flagList {
		if p.id != 0 {
			del := NewBuffer()
			del.WriteByte(PLO_FLAGDEL).Write([]byte(flag))
			if value != "" {
				del.WriteByte('=').Write([]byte(value))
			}
			p.send(del)
		}
	}
	p.flagList = make(map[string]string)
	flagCount := int(buf.ReadGShort())
	for i := 0; i < flagCount; i++ {
		flag := buf.ReadGCharString()
		name, value, _ := strings.Cut(flag, "=")
		p.SetFlag(name, value)
	}

	p.chestList = p.chestList[:0]
	chestCount := int(buf.ReadGShort())
	for i := 0; i < chestCount; i++ {
		chestLen := int(buf.ReadGChar())
		if chestLen < 2 {
			_ = buf.ReadBytes(chestLen)
			continue
		}
		x := int(buf.ReadGChar())
		y := int(buf.ReadGChar())
		levelName := string(buf.ReadBytes(chestLen - 2))
		p.chestList = append(p.chestList, fmt.Sprintf("%d:%d:%s", x, y, levelName))
	}

	hadBomb := false
	hadBow := false
	for _, weaponName := range p.weaponList {
		if p.id != 0 {
			p.sendPLO_NPCWEAPONDEL(weaponName)
			switch strings.ToLower(weaponName) {
			case "bomb":
				p.sendPLO_NPCWEAPONDEL("Bomb")
				hadBomb = true
			case "bow":
				p.sendPLO_NPCWEAPONDEL("Bow")
				hadBow = true
			}
		}
	}
	p.weaponList = p.weaponList[:0]
	weaponCount := int(buf.ReadGChar())
	for i := 0; i < weaponCount; i++ {
		weaponLen := int(buf.ReadGChar())
		if weaponLen == 0 {
			continue
		}
		weaponName := string(buf.ReadBytes(weaponLen))
		switch strings.ToLower(weaponName) {
		case "bomb":
			hadBomb = true
		case "bow":
			hadBow = true
		}
		p.addWeapon(weaponName)
	}
	if p.id != 0 {
		if !hadBomb {
			p.sendPLO_NPCWEAPONDEL("Bomb")
		}
		if !hadBow {
			p.sendPLO_NPCWEAPONDEL("Bow")
		}
		if rc != nil {
			p.sendPlayerWarp(p.x, p.y, p.z, p.levelName)
		}
	}
}
