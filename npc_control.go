package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

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
	buf := NewBufferFromBytes(packet[1:])
	if buf.Remaining() == 0 {
		return true
	}
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCATTRIBUTES)
		buf2.Write([]byte(gtokenizeText(npc.variableDump())))
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil && npc.npcType == DBNPC {
		npcName := npc.npcName
		p.server.DeleteNPC(npcId)
		if err := p.server.deleteDatabaseNPCFile(npcName); err != nil {
			p.server.logger.Warning("Failed to delete NPC file %s: %v", npcName, err)
		}
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCDELETE)
		buf2.WriteGInt(uint32(npcId))
		p.server.sendBufferToType(PLTYPE_ANYNC, buf2)
		logMsg := fmt.Sprintf("NPC %s deleted by %s", npcName, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCRESET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCRESET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil && npc.npcType == DBNPC {
		npc.script = ""
		if err := p.server.saveDatabaseNPCFile(npc); err != nil {
			p.server.logger.Warning("Failed to save NPC %s: %v", npc.npcName, err)
		}
		logMsg := fmt.Sprintf("NPC script of %s reset by %s", npc.npcName, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCSCRIPTGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCSCRIPTGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		code := npc.script
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCSCRIPT)
		buf2.WriteGInt(uint32(npcId))
		buf2.Write([]byte(gtokenizeText(code)))
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCWARP(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCWARP (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npcX := float32(buf.ReadGChar()) / 2.0
	npcY := float32(buf.ReadGChar()) / 2.0
	npcLevelName := buf.ReadString()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		level := p.server.GetLevel(npcLevelName)
		if level != nil {
			p.server.warpDatabaseNPC(npc, level, int16(npcX*16), int16(npcY*16))
			if npc.npcType == DBNPC {
				if err := p.server.saveDatabaseNPCFile(npc); err != nil {
					p.server.logger.Warning("Failed to save NPC %s: %v", npc.npcName, err)
				}
			}
		}
	}
	return true
}
func (p *Player) msgPLI_NC_NPCFLAGSGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCFLAGSGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCFLAGS)
		buf2.WriteGInt(uint32(npcId))
		var flagsStr string
		npc.mu.Lock()
		if len(npc.flagList) > 0 {
			flags := make([]string, 0, len(npc.flagList))
			for flag := range npc.flagList {
				flags = append(flags, flag)
			}
			sort.Strings(flags)
			for _, flag := range flags {
				flagsStr += fmt.Sprintf("%s=%s\n", flag, npc.flagList[flag])
			}
		}
		npc.mu.Unlock()
		buf2.Write([]byte(gtokenizeText(flagsStr)))
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCSCRIPTSET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCSCRIPTSET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npcScript := guntokenizeText(buf.ReadString())
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		npc.script = npcScript
		if err := p.server.saveDatabaseNPCFile(npc); err != nil {
			p.server.logger.Warning("Failed to save NPC %s: %v", npc.npcName, err)
		}
		logMsg := fmt.Sprintf("NPC script of %s updated by %s", npc.npcName, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCFLAGSSET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCFLAGSSET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npcFlags := guntokenizeText(buf.ReadString())
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		newFlags := make(map[string]string)
		for _, line := range strings.Split(npcFlags, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			flagName := strings.TrimSpace(parts[0])
			if flagName == "" {
				continue
			}
			flagValue := ""
			if len(parts) == 2 {
				flagValue = parts[1]
			}
			newFlags[flagName] = flagValue
		}
		npc.mu.Lock()
		npc.flagList = newFlags
		npc.mu.Unlock()
		if err := p.server.saveDatabaseNPCFile(npc); err != nil {
			p.server.logger.Warning("Failed to save NPC %s: %v", npc.npcName, err)
		}
		logMsg := fmt.Sprintf("NPC flags of %s updated by %s", npc.npcName, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_NPCADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted NPCADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	npcData := guntokenizeText(buf.ReadString())
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
		message := "Error adding database npc: Level does not exist"
		p.server.logger.Info(message)
		p.server.sendToNC(message)
		return true
	}
	x, _ := strconv.ParseFloat(npcX, 32)
	y, _ := strconv.ParseFloat(npcY, 32)
	npcId, _ := strconv.ParseUint(npcIdStr, 10, 32)
	newNpc := NewNPC(DBNPC)
	if npcId >= 1000 {
		newNpc.setId(uint32(npcId))
	}
	newNpc.npcName = npcName
	newNpc.scriptType = parts[2]
	newNpc.scripter = npcScripter
	newNpc.x = int16(x * 16)
	newNpc.y = int16(y * 16)
	newNpc.level = level
	if !p.server.AddNPC(newNpc) {
		message := "Error adding database npc: Id is in use"
		p.server.logger.Info(message)
		p.server.sendToNC(message)
		return true
	}
	p.server.attachNPCToLevel(newNpc, level)
	for _, playerId := range level.getPlayers() {
		p.server.playerMu.RLock()
		player := p.server.players[playerId]
		p.server.playerMu.RUnlock()
		if player != nil && player.playerType&PLTYPE_ANYCLIENT != 0 {
			player.sendPLO_NPCPROPS(newNpc)
		}
	}
	if err := p.server.saveDatabaseNPCFile(newNpc); err != nil {
		p.server.logger.Warning("Failed to save NPC %s: %v", newNpc.npcName, err)
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_NC_NPCADD)
	buf2.WriteGInt(newNpc.id)
	buf2.WriteGChar(NPCPROP_NAME)
	buf2.WriteGChar(byte(len(newNpc.npcName))).Write([]byte(newNpc.npcName))
	buf2.WriteGChar(NPCPROP_TYPE)
	buf2.WriteGChar(byte(len(newNpc.scriptType))).Write([]byte(newNpc.scriptType))
	buf2.WriteGChar(NPCPROP_CURLEVEL)
	buf2.WriteGChar(byte(len(npcLevelName))).Write([]byte(npcLevelName))
	p.server.sendBufferToType(PLTYPE_ANYNC, buf2)
	logMsg := fmt.Sprintf("NPC %s added by %s", npcName, p.accountName)
	p.server.logger.Info(logMsg)
	p.server.sendToNC(logMsg)
	return true
}
func (p *Player) msgPLI_NC_CLASSEDIT(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSEDIT (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	className := buf.ReadString()
	p.server.weaponMu.RLock()
	classObj, exists := p.server.classes[className]
	p.server.weaponMu.RUnlock()
	if exists {
		classCode := gtokenizeText(classObj.script)
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSGET)
		buf2.WriteByte(byte(len(className))).Write([]byte(className))
		buf2.Write([]byte(classCode))
		p.send(buf2)
	}
	return true
}
func (p *Player) msgPLI_NC_CLASSADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	classNameLen := buf.ReadGChar()
	className := string(buf.ReadBytes(int(classNameLen)))
	classCode := guntokenizeText(buf.ReadString())
	p.server.weaponMu.Lock()
	_, hasClass := p.server.classes[className]
	p.server.classes[className] = &ScriptClass{name: className, script: classCode}
	p.server.weaponMu.Unlock()
	if err := p.server.saveClassFile(className, classCode); err != nil {
		p.server.logger.Warning("Failed to save class %s: %v", className, err)
	}
	p.server.updateClassForPlayers(p.server.classes[className])
	if !hasClass {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSADD)
		buf2.Write([]byte(className))
		p.server.sendBufferToType(PLTYPE_ANYNC, buf2)
	}
	logMsg := fmt.Sprintf("Script %s %s by %s", className, map[bool]string{true: "added", false: "updated"}[!hasClass], p.accountName)
	p.server.logger.Info(logMsg)
	p.server.sendToNC(logMsg)
	return true
}
func (p *Player) msgPLI_NC_LOCALNPCSGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LOCALNPCSGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	levelName := buf.ReadString()
	if levelName == "" {
		return true
	}
	level := p.server.GetLevel(levelName)
	if level != nil {
		var npcDump string
		npcDump += "Variables dump from level " + levelName + "\n"
		for _, npc := range level.npcs {
			if npc != nil {
				npcDump += "\n" + npc.variableDump() + "\n"
			}
		}
		npcDump = gtokenizeText(npcDump)
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_LEVELDUMP)
		buf2.Write([]byte(npcDump))
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
	names := make([]string, 0, len(p.server.weapons))
	seen := make(map[string]bool, len(p.server.weapons))
	p.server.weaponMu.RLock()
	for weaponName, weapon := range p.server.weapons {
		if weapon != nil {
			weaponName = weapon.name
		}
		if weaponName != "" && (weapon == nil || !weapon.defPlayer) {
			key := strings.ToLower(weaponName)
			if !seen[key] {
				seen[key] = true
				names = append(names, weaponName)
			}
		}
	}
	p.server.weaponMu.RUnlock()
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, weaponName := range names {
		buf.WriteString8Encoded(weaponName)
	}
	p.server.logger.PacketDebug("NC weapon list for %s: names=%q raw=% X", p.accountName, names, buf.Bytes())
	p.send(buf)
	return true
}
func (p *Player) msgPLI_NC_WEAPONGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONGET (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	weaponName := buf.ReadString()
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil && !weapon.defPlayer {
		script := strings.ReplaceAll(weapon.script, "\n", "\xa7")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_WEAPONGET)
		buf2.WriteString8Encoded(weaponName)
		buf2.WriteString8Encoded(weapon.image)
		buf2.Write([]byte(script))
		p.send(buf2)
	} else {
		p.server.sendToNC(fmt.Sprintf("%s prob: weapon %s doesn't exist", p.accountName, weaponName))
	}
	return true
}
func (p *Player) msgPLI_NC_WEAPONADD(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONADD (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	weaponNameLen := buf.ReadGChar()
	weaponName := string(buf.ReadBytes(int(weaponNameLen)))
	weaponImageLen := buf.ReadGChar()
	weaponImage := string(buf.ReadBytes(int(weaponImageLen)))
	weaponCode := buf.ReadString()
	weaponCode = decodeNCScriptText(weaponCode)
	compileResult := p.server.compileGS2ForFeedback("weapon", weaponName, weaponCode)
	if compileResult.errText != "" {
		p.server.sendGS2CompilerOutputToNC("Weapon "+weaponName, "error", compileResult.errText)
		return true
	}
	if compileResult.warningText != "" {
		p.server.sendGS2CompilerOutputToNC("Weapon "+weaponName, "warning", compileResult.warningText)
	}
	actionTaken := ""
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil {
		if weapon.defPlayer {
			return true
		}
		weapon.image = weaponImage
		weapon.script = weaponCode
		weapon.bytecode = compileResult.bytecode
		p.server.updateWeaponForPlayers(weapon)
		actionTaken = "updated"
	} else {
		newWeapon := NewWeapon(weaponName)
		newWeapon.image = weaponImage
		newWeapon.script = weaponCode
		newWeapon.bytecode = compileResult.bytecode
		p.server.AddWeapon(newWeapon)
		weapon = newWeapon
		actionTaken = "added"
	}
	if actionTaken != "" {
		if err := p.server.saveWeaponFile(weapon); err != nil {
			p.server.logger.Warning("Failed to save weapon %s: %v", weaponName, err)
		}
		logMsg := fmt.Sprintf("Weapon/GUI-script %s %s by %s", weaponName, actionTaken, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}

func decodeNCScriptText(script string) string {
	if strings.Contains(script, "\xa7") {
		return strings.ReplaceAll(script, "\xa7", "\n")
	}
	if strings.HasPrefix(script, "\"") || strings.Contains(script, "\",") || strings.Contains(script, ",\"") {
		return guntokenizeText(script)
	}
	return script
}

func (s *Server) sendGS2CompilerOutputToNC(origin, level, text string) {
	if s == nil {
		return
	}
	s.sendToNC(fmt.Sprintf("Script compiler output for %s:", origin))
	wroteLine := false
	for _, line := range strings.Split(text, "\n") {
		line = normalizeCompilerOutputLine(line)
		if line == "" {
			continue
		}
		s.sendToNC(fmt.Sprintf("%s: %s", level, line))
		wroteLine = true
	}
	if !wroteLine {
		s.sendToNC(fmt.Sprintf("%s: compiler failed", level))
	}
}

func normalizeCompilerOutputLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "->")
	line = strings.TrimSpace(line)
	for {
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "[error]"):
			line = strings.TrimSpace(line[len("[error]"):])
		case strings.HasPrefix(lower, "error:"):
			line = strings.TrimSpace(line[len("error:"):])
		case strings.HasPrefix(lower, "[warning]"):
			line = strings.TrimSpace(line[len("[warning]"):])
		case strings.HasPrefix(lower, "warning:"):
			line = strings.TrimSpace(line[len("warning:"):])
		case strings.HasPrefix(lower, "[info]"):
			line = strings.TrimSpace(line[len("[info]"):])
		case strings.HasPrefix(lower, "info:"):
			line = strings.TrimSpace(line[len("info:"):])
		default:
			return line
		}
	}
}
func (p *Player) msgPLI_NC_WEAPONDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted WEAPONDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	weaponName := buf.ReadString()
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil && !weapon.defPlayer {
		p.server.DeleteWeapon(weaponName)
		if err := p.server.deleteWeaponFile(weaponName); err != nil {
			p.server.logger.Warning("Failed to delete weapon file %s: %v", weaponName, err)
		}
		del := NewBuffer()
		del.WriteByte(PLO_NPCWEAPONDEL)
		del.Write([]byte(weaponName))
		p.server.sendBufferToType(PLTYPE_ANYCLIENT, del)
		logMsg := fmt.Sprintf("Weapon %s deleted by %s", weaponName, p.accountName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	} else {
		logMsg := fmt.Sprintf("%s prob: weapon %s doesn't exist", p.accountName, weaponName)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_CLASSDELETE(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted CLASSDELETE (non-NC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	className := buf.ReadString()
	p.server.weaponMu.Lock()
	_, exists := p.server.classes[className]
	delete(p.server.classes, className)
	p.server.weaponMu.Unlock()
	if exists {
		if err := p.server.deleteClassFile(className); err != nil {
			p.server.logger.Warning("Failed to delete class file %s: %v", className, err)
		}
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSDELETE)
		buf2.Write([]byte(className))
		p.server.sendBufferToType(PLTYPE_ANYNC, buf2)
		logMsg := fmt.Sprintf("%s has deleted class %s", p.accountName, className)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	} else {
		logMsg := fmt.Sprintf("error: %s does not exist on this server!", className)
		p.server.logger.Info(logMsg)
		p.server.sendToNC(logMsg)
	}
	return true
}
func (p *Player) msgPLI_NC_LEVELLISTGET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LEVELLISTGET (non-NC)", p.accountName)
		return true
	}
	p.server.levelMu.RLock()
	levelNames := make([]string, 0, len(p.server.levels))
	for levelName := range p.server.levels {
		levelNames = append(levelNames, levelName)
	}
	p.server.levelMu.RUnlock()
	sort.Strings(levelNames)
	levelList := ""
	for _, levelName := range levelNames {
		levelList += levelName + "\n"
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_NC_LEVELLIST)
	buf2.Write([]byte(gtokenizeText(levelList)))
	p.send(buf2)
	return true
}

func (p *Player) msgPLI_NC_LEVELLISTSET(packet []byte) bool {
	if p.playerType&PLTYPE_ANYNC == 0 {
		p.server.logger.Warning("[Hack] %s attempted LEVELLISTSET (non-NC)", p.accountName)
		return true
	}
	p.server.logger.Debug("NC LEVELLISTSET ignored from %s (%d bytes)", p.accountName, len(packet))
	return true
}
