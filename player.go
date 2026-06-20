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
	"head*.png", "head*.gif",
	"body.png", "body2.png", "body3.png",
	"w*.png", "w*.gif",
	"plisticon*.png", "plisticon*.gif",
	"emoticon*.png", "emoticon*.gif", "emoticon*.mng",
	"-.gif",
	"arrow.wav", "arrowon.wav", "axe.wav", "bomb.wav", "chest.wav", "compudead.wav", "crush.wav", "dead.wav", "extra.wav", "fire.wav", "frog.wav", "frog2.wav", "goal.wav", "horse.wav", "horse2.wav", "item.wav", "item2.wav", "jump.wav", "lift.wav", "lift2.wav", "nextpage.wav", "put.wav", "sign.wav", "steps.wav", "steps2.wav", "stonemove.wav", "sword.wav", "swordon.wav", "thunder.wav", "water.wav",
	"pics1.png", "sprites.png", "basepackage.gupd", "tempsitcbd.ttf", "arial.ttf",
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
	if p.server != nil && p.server.logger != nil {
		p.server.logger.Debug("PLAYERCHAT command candidate from %s: %s", p.accountName, trimmed)
	}
	switch command {
	case "setnick":
		return p.handlePlayerChatSetNick(trimmed)
	case "sethead":
		return p.handlePlayerChatSetHead(words)
	case "setbody":
		return p.handlePlayerChatSetBody(words)
	case "setsword":
		return p.handlePlayerChatSetSword(words)
	case "setshield":
		return p.handlePlayerChatSetShield(words)
	case "setskin", "setcoat", "setsleeves", "setshoes", "setbelt":
		return p.handlePlayerChatSetColor(command, words)
	case "warpto":
		return p.handlePlayerChatWarpto(words)
	case "summon":
		return p.handlePlayerChatSummon(words)
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
			p.clearChatWithProps()
			return true
		}
	case "showadmins":
		return p.handlePlayerChatShowAdmins()
	case "showguild":
		return p.handlePlayerChatShowGuild(words)
	case "showkills":
		p.setChat(fmt.Sprintf("kills: %d", int(p.kills)))
		return true
	case "showdeaths":
		p.setChat(fmt.Sprintf("deaths: %d", int(p.deaths)))
		return true
	case "showonlinetime":
		p.setChat("onlinetime: " + formatOnlineTime(p.onlineTime))
		return true
	case "toguild:":
		return p.handlePlayerChatToGuild(trimmed)
	}
	return false
}

func (p *Player) handlePlayerChatSetNick(chat string) bool {
	if time.Since(p.lastNick) < 10*time.Second {
		p.server.logger.Debug("PLAYERCHAT setnick denied by cooldown for %s", p.accountName)
		p.setChat("Wait 10 seconds before changing your nick again!")
		return true
	}
	newName := strings.TrimSpace(chat[7:])
	p.lastNick = time.Now()
	p.setNickname(newName)
	p.server.logger.Debug("PLAYERCHAT setnick %s -> %s", p.accountName, p.character.nickName)
	p.clearChatWithProps(PLPROP_NICKNAME)
	return true
}

func (p *Player) handlePlayerChatSetHead(words []string) bool {
	if len(words) != 2 || !p.server.settings.GetBool("setheadallowed", true) {
		return false
	}
	p.character.headImage = words[1]
	p.clearChatWithProps(PLPROP_HEADGIF)
	return true
}

func (p *Player) handlePlayerChatSetBody(words []string) bool {
	if len(words) != 2 || !p.server.settings.GetBool("setbodyallowed", true) {
		return false
	}
	p.character.bodyImage = words[1]
	p.clearChatWithProps(PLPROP_BODYIMG)
	return true
}

func (p *Player) handlePlayerChatSetSword(words []string) bool {
	if len(words) != 2 || !p.server.settings.GetBool("setswordallowed", true) {
		return false
	}
	p.character.swordImage = words[1]
	p.clearChatWithProps(PLPROP_SWORDPOWER)
	return true
}

func (p *Player) handlePlayerChatSetShield(words []string) bool {
	if len(words) != 2 || !p.server.settings.GetBool("setshieldallowed", true) {
		return false
	}
	p.character.shieldImage = words[1]
	p.clearChatWithProps(PLPROP_SHIELDPOWER)
	return true
}

func (p *Player) handlePlayerChatSetColor(command string, words []string) bool {
	if len(words) != 2 || !p.server.settings.GetBool("setcolorsallowed", true) {
		return false
	}
	colorName := strings.ToLower(words[1])
	if colorName == "grey" {
		colorName = "gray"
	}
	color, ok := graalColor(colorName)
	if !ok {
		return true
	}
	colorSlot := map[string]int{"setskin": 0, "setcoat": 1, "setsleeves": 2, "setshoes": 3, "setbelt": 4}[command]
	p.character.colors[colorSlot] = color
	p.clearChatWithProps(PLPROP_COLORS)
	return true
}

func (p *Player) handlePlayerChatWarpto(words []string) bool {
	if len(words) == 2 {
		if !p.hasRight(PLPERM_WARPTOPLAYER) && !p.server.allowsWarpToAll() {
			p.server.logger.Debug("PLAYERCHAT warpto player denied for %s rights=%d warptoforall=%v", p.accountName, p.adminRights, p.server.allowsWarpToAll())
			p.setChat("(not authorized to warp)")
			return true
		}
		if player := p.server.findPlayerByAccountOrNick(words[1], PLTYPE_ANYCLIENT); player != nil && player.currentLevel != nil {
			levelName := player.levelName
			if levelName == "" {
				levelName = player.currentLevel.levelName
			}
			p.server.logger.Debug("PLAYERCHAT warpto player %s -> %s %s %.2f %.2f", p.accountName, words[1], levelName, float64(player.x)/16.0, float64(player.y)/16.0)
			p.warp(levelName, float64(player.x)/16.0, float64(player.y)/16.0)
			p.clearChatWithProps()
		}
		return true
	}
	if len(words) == 3 || len(words) == 4 {
		if !p.hasRight(PLPERM_WARPTO) && !p.server.allowsWarpToAll() {
			p.server.logger.Debug("PLAYERCHAT warpto xy denied for %s rights=%d warptoforall=%v", p.accountName, p.adminRights, p.server.allowsWarpToAll())
			p.setChat("(not authorized to warp)")
			return true
		}
		x, errX := strconv.ParseFloat(words[1], 64)
		y, errY := strconv.ParseFloat(words[2], 64)
		if errX != nil || errY != nil {
			p.server.logger.Debug("PLAYERCHAT warpto xy parse failed for %s: %v %v", p.accountName, errX, errY)
			return true
		}
		if len(words) == 4 {
			p.server.logger.Debug("PLAYERCHAT warpto level %s -> %s %.2f %.2f", p.accountName, words[3], x, y)
			p.warp(words[3], x, y)
			p.clearChatWithProps()
			return true
		}
		p.setX(float32(x))
		p.setY(float32(y))
		p.server.logger.Debug("PLAYERCHAT warpto xy %s -> %.2f %.2f encoded=%d,%d", p.accountName, x, y, p.x, p.y)
		p.clearChatWithProps(PLPROP_X, PLPROP_Y)
		return true
	}
	return true
}

func (p *Player) handlePlayerChatSummon(words []string) bool {
	if len(words) != 2 {
		return false
	}
	if !p.hasRight(PLPERM_SUMMON) {
		p.setChat("(not authorized to summon)")
		return true
	}
	if player := p.server.findPlayerByAccountOrNick(words[1], PLTYPE_ANYCLIENT); player != nil {
		player.warp(p.levelName, float64(p.x)/16.0, float64(p.y)/16.0)
	}
	p.clearChatWithProps()
	return true
}

func (s *Server) allowsWarpToAll() bool {
	return s != nil && s.settings != nil && s.settings.GetBool("warptoforall", false)
}

func (p *Player) markMovement() {
	p.status &^= PLSTATUS_PAUSED
	p.lastMovement = time.Now()
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
	elapsed := int(time.Since(p.lastMovement).Seconds())
	if elapsed < unstickTime {
		remaining := unstickTime - elapsed
		p.server.logger.Debug("PLAYERCHAT unstick delayed for %s elapsed=%ds required=%ds", p.accountName, elapsed, unstickTime)
		p.setChat(fmt.Sprintf("Don't move for %d seconds before doing '%s'!", remaining, originalChat))
		return true
	}
	p.lastMovement = time.Now()
	levelName := p.server.settings.Get("unstickmelevel")
	if levelName == "" {
		levelName = "onlinestartlocal.nw"
	}
	x := getSettingsFloat(p.server.settings, "unstickmex", 30.0)
	y := getSettingsFloat(p.server.settings, "unstickmey", 30.5)
	p.server.logger.Debug("PLAYERCHAT unstick warp %s -> %s %.2f %.2f", p.accountName, levelName, x, y)
	p.warp(levelName, x, y)
	p.setChat("Warped!")
	return true
}

func (p *Player) handlePlayerChatShowAdmins() bool {
	names := make([]string, 0)
	for _, player := range p.server.players {
		if player.playerType&PLTYPE_ANYRC != 0 {
			names = append(names, player.accountName)
		}
	}
	if len(names) == 0 {
		p.setChat("admins: (no one)")
	} else {
		p.setChat("admins: " + strings.Join(names, ", "))
	}
	return true
}

func (p *Player) handlePlayerChatShowGuild(words []string) bool {
	guild := p.guild
	if len(words) == 2 {
		guild = words[1]
	}
	if guild == "" {
		return false
	}
	names := make([]string, 0)
	for _, player := range p.server.players {
		if player.guild == guild {
			names = append(names, strings.TrimSpace(strings.Split(player.character.nickName, "(")[0]))
		}
	}
	if len(names) == 0 {
		p.setChat(fmt.Sprintf("members of '%s': (no one)", guild))
	} else {
		p.setChat(fmt.Sprintf("members of '%s': %s", guild, strings.Join(names, ", ")))
	}
	return true
}

func (p *Player) handlePlayerChatToGuild(chat string) bool {
	if p.guild == "" {
		return false
	}
	pm := strings.TrimSpace(chat[8:])
	if pm == "" {
		return false
	}
	count := 0
	for _, player := range p.server.players {
		if player.guild != p.guild || player.conn == nil {
			continue
		}
		out := NewBuffer()
		out.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(p.id).Write([]byte("\"\",\"Guild message:\",\"")).Write([]byte(pm)).WriteByte('"')
		player.send(out)
		count++
	}
	suffix := ""
	if count != 0 {
		suffix = "s"
	}
	p.setChat(fmt.Sprintf("(%d guild member%s received your message)", count, suffix))
	return true
}

func (p *Player) clearChatWithProps(propIds ...int) {
	p.character.chatMessage = ""
	propIds = append(propIds, PLPROP_CURCHAT)
	p.sendPlayerPropChanges(propIds...)
}

func (p *Player) setChat(chat string) {
	if len(chat) > 223 {
		chat = chat[:223]
	}
	p.character.chatMessage = chat
	p.sendPlayerPropChanges(PLPROP_CURCHAT)
}

func (p *Player) sendPlayerPropChange(propId int) {
	p.sendPlayerPropChanges(propId)
}

func (p *Player) sendPlayerPropChanges(propIds ...int) {
	if p.server != nil && p.server.logger != nil {
		p.server.logger.Debug("PLAYERCHAT send props %v to %s", propIds, p.accountName)
	}
	common := NewBuffer()
	legacy := NewBuffer()
	precise := NewBuffer()
	for _, propId := range propIds {
		switch propId {
		case PLPROP_X, PLPROP_Y, PLPROP_Z, PLPROP_X2, PLPROP_Y2, PLPROP_Z2:
			p.appendPlayerPropDelta(propId, common, legacy, precise)
		default:
			common.WriteGChar(byte(propId))
			common.Write(p.getProp(propId))
		}
	}
	selfMove := legacy.Bytes()
	if playerSupportsPreciseMovement(p) {
		selfMove = precise.Bytes()
	}
	selfProps := append(append([]byte(nil), common.Bytes()...), selfMove...)
	if len(selfProps) > 0 {
		packet := append([]byte{PLO_PLAYERPROPS}, selfProps...)
		if p.server != nil && p.server.logger != nil {
			p.server.logger.Debug("PLAYERCHAT source props queueOutgoing=%v processing=%v len=%d", p.queueOutgoing, p.processingPackets, len(packet))
		}
		if p.processingPackets {
			p.deferSelfPacket(packet)
		} else {
			p.sendPacket(packet)
		}
	}
	p.sendPlayerPropDeltasToCurrentLevel(common.Bytes(), legacy.Bytes(), precise.Bytes())
}

func graalColor(name string) (byte, bool) {
	colors := map[string]byte{"orange": 0, "white": 1, "blue": 2, "red": 3, "black": 4, "lightblue": 5, "green": 6, "yellow": 7, "pink": 8, "gray": 9, "brown": 10, "darkred": 11, "darkgreen": 12, "darkblue": 13, "purple": 14, "darkgray": 15, "cyan": 16}
	color, ok := colors[name]
	return color, ok
}

func formatOnlineTime(total int) string {
	seconds := total % 60
	minutes := (total / 60) % 60
	hours := total / 3600
	if hours != 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes != 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
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
