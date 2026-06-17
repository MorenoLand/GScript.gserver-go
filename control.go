package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func rcChatPacket(message string) []byte {
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_CHAT).Write([]byte(message))
	return buf.Bytes()
}

func rcCommandAccountPacket(account string) []byte {
	return NewBuffer().WriteGString(strings.TrimSpace(account)).Bytes()
}

func isRCOnlyPacket(packetId int) bool {
	if packetId >= PLI_RC_SERVEROPTIONSGET && packetId <= PLI_RC_FILEBROWSER_RENAME {
		return packetId != PLI_PROFILEGET && packetId != PLI_PROFILESET
	}
	return packetId == PLI_RC_FOLDERDELETE || packetId == PLI_RC_UNKNOWN162
}

func isNCOnlyPacket(packetId int) bool {
	return packetId == PLI_NC_LISTNPCS ||
		(packetId >= PLI_NC_NPCGET && packetId <= PLI_NC_CLASSDELETE) ||
		packetId == PLI_NC_LEVELLISTGET ||
		packetId == PLI_NC_LEVELLISTSET
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
	adminOptions := []string{"name", "description", "url", "serverip", "serverport", "localip", "listip", "listport", "listserver", "loginserver", "maxplayers", "onlystaff", "nofoldersconfig", "oldcreated", "serverside", "triggerhack_weapons", "triggerhack_guilds", "triggerhack_groups", "triggerhack_files", "triggerhack_rc", "flaghack_movement", "flaghack_ip", "sharefolder", "language"}
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
	if p.playerType&PLTYPE_ANYRC == 0 {
		p.server.logger.Warning("[Hack] %s attempted PLAYERPROPSSET (non-RC)", p.accountName)
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	targetPlayer := p.server.getPlayerById(buf.ReadGShort())
	if targetPlayer == nil {
		return true
	}
	if (targetPlayer.accountName != p.accountName && !p.hasRight(PLPERM_SETATTRIBUTES)) ||
		(targetPlayer.accountName == p.accountName && !p.hasRight(PLPERM_SETSELFATTRIBUTES)) {
		p.server.logger.Warning("%s attempted PLAYERPROPSSET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to set the properties of " + targetPlayer.accountName)))
		return true
	}
	targetPlayer.setPropsFromRC(buf, p)
	targetPlayer.SaveAccount()
	p.server.logger.Info("%s set the attributes of player %s", p.accountName, targetPlayer.accountName)
	p.server.sendRCChat(p.accountName + " set the attributes of player " + targetPlayer.accountName)
	return true
}
func (p *Player) msgPLI_RC_DISCONNECTPLAYER(packet []byte) bool {
	buf := NewBufferFromBytes(packet[1:])
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
	if p.playerType&PLTYPE_ANYRC == 0 {
		p.server.logger.Warning("[Hack] %s attempted UPDATELEVELS (non-RC)", p.accountName)
		return true
	}
	if !p.hasRight(PLPERM_UPDATELEVEL) {
		p.server.logger.Warning("%s attempted UPDATELEVELS without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to update levels.")))
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	levelCount := int(buf.ReadGShort())
	for i := 0; i < levelCount; i++ {
		levelName := buf.ReadGCharString()
		if level := p.server.GetLevel(levelName); level != nil {
			level.reload(p.server)
		}
	}
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	buf := NewBufferFromBytes(packet[1:])
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
	if (targetPlayer.accountName != p.accountName && !p.hasRight(PLPERM_SETATTRIBUTES)) ||
		(targetPlayer.accountName == p.accountName && !p.hasRight(PLPERM_SETSELFATTRIBUTES)) {
		p.server.logger.Warning("%s attempted PLAYERPROPSSET2 without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: " + p.accountName + " is not authorized to set the properties of " + targetPlayer.accountName)))
		return true
	}
	if !p.hasRight(PLPERM_MODIFYSTAFFACCOUNT) && (targetPlayer.isStaff || accountName == "defaultaccount") {
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to modify staff accounts.")))
		return true
	}
	targetPlayer.setPropsFromRC(buf, p)
	targetPlayer.SaveAccount()
	p.server.logger.Info("%s modified props for account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " set the attributes of player " + targetPlayer.accountName)
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
	if p.playerType&PLTYPE_ANYCLIENT != 0 {
		p.server.logger.Warning("[Hack] %s attempted RC_CHAT (non-RC)", p.accountName)
		return true
	}
	if p.playerType&PLTYPE_ANYNC != 0 {
		return true
	}
	message := strings.TrimSpace(string(packet[1:]))
	if message == "" {
		return true
	}
	p.server.logger.Info("RC CHAT: %s", message)
	if strings.HasPrefix(message, "/") {
		return p.handleRCCommand(message)
	}
	p.server.sendRCChat(p.accountName + ": " + message)
	return true
}

func (p *Player) handleRCCommand(message string) bool {
	words := strings.Fields(message)
	if len(words) == 0 {
		return true
	}
	command := strings.ToLower(words[0])
	arg := strings.TrimSpace(strings.TrimPrefix(message, words[0]))
	switch command {
	case "/help":
		if len(words) == 1 {
			p.sendRCHelp()
		}
	case "/open":
		if arg != "" {
			return p.msgPLI_RC_PLAYERPROPSGET3(rcCommandAccountPacket(arg))
		}
	case "/openacc":
		if arg != "" {
			return p.msgPLI_RC_ACCOUNTGET(rcCommandAccountPacket(arg))
		}
	case "/opencomments":
		if arg != "" {
			return p.msgPLI_RC_PLAYERCOMMENTSGET(rcCommandAccountPacket(arg))
		}
	case "/openban":
		if arg != "" {
			return p.msgPLI_RC_PLAYERBANGET(rcCommandAccountPacket(arg))
		}
	case "/openrights":
		if arg != "" {
			return p.msgPLI_RC_PLAYERRIGHTSGET(rcCommandAccountPacket(arg))
		}
	case "/reset":
		if arg != "" {
			return p.msgPLI_RC_PLAYERPROPSRESET(rcCommandAccountPacket(arg))
		}
	}
	return true
}

func (p *Player) sendRCHelp() {
	data, err := p.server.config.LoadFile("config/rchelp.txt")
	if err != nil {
		p.sendPLO_RC_CHAT("No RC help is available.")
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			p.sendPLO_RC_CHAT(line)
		}
	}
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
	p.server.ensureNPCServer().SendNCAddress(p, packet)
	return true
}
func (p *Player) msgPLI_RC_UNKNOWN162(packet []byte) bool {
	p.server.logger.Debug("RC_UNKNOWN162")
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
	buf := NewBufferFromBytes(packet[1:])
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
		flagsStr = gtokenizeText(flagsStr)
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCATTRIBUTES)
		buf2.Write([]byte(flagsStr))
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
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCDELETE)
		buf2.WriteGInt(uint32(npcId))
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
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
	buf := NewBufferFromBytes(packet[1:])
	npcId := buf.ReadGInt()
	npc := p.server.GetNPC(npcId)
	if npc != nil {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_NPCFLAGS)
		buf2.WriteGInt(uint32(npcId))
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
	buf2.WriteGChar(NPCPROP_NAME)
	buf2.WriteGChar(byte(len(newNpc.npcName))).Write([]byte(newNpc.npcName))
	buf2.WriteGChar(NPCPROP_TYPE)
	buf2.WriteGChar(byte(len(newNpc.scriptType))).Write([]byte(newNpc.scriptType))
	buf2.WriteGChar(NPCPROP_CURLEVEL)
	buf2.WriteGChar(byte(len(npcLevelName))).Write([]byte(npcLevelName))
	p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
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
	if !hasClass {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_CLASSADD)
		buf2.Write([]byte(className))
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
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
				npcDump += fmt.Sprintf("\nNPC %d: %s\n", npc.id, npc.npcName)
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
	p.server.weaponMu.RLock()
	for weaponName, weapon := range p.server.weapons {
		if weapon != nil {
			weaponName = weapon.name
		}
		if weaponName != "" && (weapon == nil || !weapon.defPlayer) {
			buf.WriteByte(byte(len(weaponName))).Write([]byte(weaponName))
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
	buf := NewBufferFromBytes(packet[1:])
	weaponName := buf.ReadString()
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil {
		script := strings.ReplaceAll(weapon.script, "\n", "\xa7")
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_NC_WEAPONGET)
		buf2.WriteByte(byte(len(weaponName))).Write([]byte(weaponName))
		buf2.WriteByte(byte(len(weapon.image))).Write([]byte(weapon.image))
		buf2.Write([]byte(script))
		p.send(buf2)
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
	weaponCode = strings.ReplaceAll(weaponCode, "\xa7", "\n")
	actionTaken := ""
	weapon := p.server.GetWeapon(weaponName)
	if weapon != nil {
		if weapon.defPlayer || weapon.bytecodeFile != "" {
			return true
		}
		weapon.image = weaponImage
		weapon.script = weaponCode
		p.server.updateWeaponForPlayers(weapon)
		actionTaken = "updated"
	} else {
		newWeapon := NewWeapon(weaponName)
		newWeapon.image = weaponImage
		newWeapon.script = weaponCode
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
		p.server.sendPacketToType(PLTYPE_ANYCLIENT, del.Bytes())
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
		p.server.sendPacketToType(PLTYPE_ANYNC, buf2.Bytes())
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
	buf := NewBuffer()
	buf.WriteByte(PLO_NC_LEVELLIST)
	p.server.levelMu.RLock()
	for levelName := range p.server.levels {
		buf.Write([]byte(levelName))
		buf.WriteByte('\n')
	}
	p.server.levelMu.RUnlock()
	levelList := strings.ReplaceAll(string(buf.Bytes()[1:]), "\n", "\x01")
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_NC_LEVELLIST)
	buf2.Write([]byte(levelList))
	p.send(buf2)
	return true
}
