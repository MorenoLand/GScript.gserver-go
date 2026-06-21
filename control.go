package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const scriptHelpCacheTTL = 30 * time.Minute

type ScriptHelpEntry struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Params      []string `json:"params"`
	Returns     string   `json:"returns"`
	Scope       string   `json:"scope"`
	Description string   `json:"description"`
}

func rcChatPacket(message string) []byte {
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_CHAT).Write([]byte(message))
	return buf.Bytes()
}

func rcCommandAccountPacket(account string) []byte {
	return NewBuffer().WriteGString(strings.TrimSpace(account)).Bytes()
}

func rcPayload(packet []byte, packetId byte) []byte {
	if len(packet) > 0 && packet[0] == packetId {
		return packet[1:]
	}
	return packet
}

func readRCAccountPayload(packet []byte, packetId byte) string {
	payload := rcPayload(packet, packetId)
	if len(payload) == 0 {
		return ""
	}
	if account := readRCEncodedStringPayload(payload); account != "" {
		return sanitizeRCAccountName(account)
	}
	buf := NewBufferFromBytes(payload)
	if account := buf.ReadGString(); account != "" {
		return sanitizeRCAccountName(account)
	}
	return sanitizeRCAccountName(string(payload))
}

func (s *Server) deleteAccountFile(accountName string) {
	accountName = sanitizeRCAccountName(accountName)
	if accountName == "" {
		return
	}
	accountPath := "accounts/" + accountName + ".txt"
	if s != nil && s.config != nil {
		_ = s.config.DeleteFile(accountPath)
		return
	}
	_ = os.Remove(accountPath)
}

func readRCString8AccountPayload(packet []byte, packetId byte) string {
	payload := rcPayload(packet, packetId)
	if len(payload) == 0 {
		return ""
	}
	if account := readRCEncodedStringPayload(payload); account != "" {
		return sanitizeRCAccountName(account)
	}
	nameLen := int(payload[0])
	if nameLen <= len(payload)-1 && payload[0] < 32 {
		return sanitizeRCAccountName(string(payload[1 : 1+nameLen]))
	}
	buf := NewBufferFromBytes(payload)
	if account := buf.ReadGString(); account != "" {
		return sanitizeRCAccountName(account)
	}
	return sanitizeRCAccountName(string(payload))
}

func readRCEncodedStringPayload(payload []byte) string {
	if len(payload) == 0 || payload[0] < 32 {
		return ""
	}
	nameLen := int(payload[0] - 32)
	if nameLen != len(payload)-1 {
		return ""
	}
	return string(payload[1 : 1+nameLen])
}

func readRCEncodedString(buf *Buffer) string {
	if buf == nil {
		return ""
	}
	strLen := int(buf.ReadGChar())
	if strLen > buf.Remaining() {
		strLen = buf.Remaining()
	}
	return string(buf.ReadBytes(strLen))
}

func writeRCEncodedString(buf *Buffer, value string) {
	buf.WriteString8Encoded(value)
}

func sanitizeRCAccountName(accountName string) string {
	accountName = strings.TrimSpace(accountName)
	if idx := strings.Index(accountName, "/"); idx != -1 {
		accountName = accountName[:idx]
	}
	if idx := strings.Index(accountName, "\\"); idx != -1 {
		accountName = accountName[:idx]
	}
	return accountName
}

func isRCOnlyPacket(packetId int) bool {
	if packetId >= PLI_RC_SERVEROPTIONSGET && packetId <= PLI_RC_FILEBROWSER_RENAME {
		return packetId != PLI_PROFILEGET && packetId != PLI_PROFILESET
	}
	return packetId == PLI_RC_FOLDERDELETE
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
	targetPlayer.disconnect()
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
	buf.WriteGShort(uint16(len(validFlags)))
	for flag, value := range validFlags {
		flagStr := flag + "=" + value
		writeRCEncodedString(buf, flagStr)
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
		flagStr := readRCEncodedString(buf)
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
			p.server.broadcastServerFlagSet(flag, value)
		}
	}
	for flag := range oldFlags {
		if _, exists := p.server.flags[flag]; !exists {
			p.server.broadcastServerFlagDelete(flag)
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
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_ACCOUNTADD))
	accountName := readRCEncodedString(buf)
	_ = readRCEncodedString(buf)
	email := readRCEncodedString(buf)
	banned := buf.ReadGChar() != 0
	loadOnly := buf.ReadGChar() != 0
	_ = buf.ReadGChar()
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
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_ACCOUNTLISTGET))
	name := readRCEncodedString(buf)
	_ = readRCEncodedString(buf)
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
				writeRCEncodedString(buf2, accountName)
			}
		}
	}
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSGET2(packet []byte) bool {
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_PLAYERPROPSGET2))
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
	buf2.WriteGShort(targetPlayer.id)
	buf2.Write(targetPlayer.getPropsRC())
	p.send(buf2)
	p.server.sendRCChat(p.accountName + " has opened the attributes of " + targetPlayer.accountName)
	return true
}

func (p *Player) loadOfflineRCAccount(accountName string) (*Player, bool) {
	accountName = strings.TrimSpace(accountName)
	if accountName == "" || strings.ContainsAny(accountName, `/\`) || !p.server.accountExists(accountName) {
		p.sendPLO_RC_CHAT("Server: Account " + accountName + " does not exist.")
		return nil, false
	}
	tempPlayer := NewPlayer(nil, p.server)
	if !tempPlayer.LoadAccount(accountName, false) {
		p.sendPLO_RC_CHAT("Server: Account " + accountName + " does not exist.")
		return nil, false
	}
	return tempPlayer, true
}

func (p *Player) msgPLI_RC_PLAYERPROPSGET3(packet []byte) bool {
	accountName := readRCString8AccountPayload(packet, PLI_RC_PLAYERPROPSGET3)
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYPLAYER|PLTYPE_NPCSERVER)
	if targetPlayer == nil {
		var ok bool
		targetPlayer, ok = p.loadOfflineRCAccount(accountName)
		if !ok {
			return true
		}
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
	buf2.WriteGShort(targetPlayer.id)
	buf2.Write(targetPlayer.getPropsRC())
	p.send(buf2)
	p.server.sendRCChat(p.accountName + " has opened the attributes of " + accountName)
	return true
}
func (p *Player) msgPLI_RC_PLAYERPROPSRESET(packet []byte) bool {
	accountName := readRCAccountPayload(packet, PLI_RC_PLAYERPROPSRESET)
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
		p.server.deleteAccountFile(accountName)
		p.server.logger.Info("%s reset account: %s", p.accountName, accountName)
		p.server.sendRCChat(p.accountName + " has reset the attributes of account: " + accountName)
		return true
	}
	if targetPlayer.id != 0 {
		targetPlayer.sendPacket([]byte{PLO_DISCMESSAGE, 0})
		targetPlayer.writeString8("Your account was reset by " + p.accountName)
		targetPlayer.mu.Lock()
		if !targetPlayer.disconnected {
			targetPlayer.disconnected = true
			conn := targetPlayer.conn
			targetPlayer.conn = nil
			level := targetPlayer.currentLevel
			targetPlayer.currentLevel = nil
			targetPlayer.loaded = false
			targetPlayer.mu.Unlock()
			if conn != nil {
				conn.Close()
			}
			if level != nil {
				level.removePlayer(targetPlayer)
			}
			p.server.DeletePlayer(targetPlayer)
		} else {
			targetPlayer.loaded = false
			targetPlayer.mu.Unlock()
		}
	}
	targetPlayer.resetAccount()
	p.server.deleteAccountFile(accountName)
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
		targetPlayer = NewPlayer(nil, p.server)
		if !targetPlayer.LoadAccount(accountName, false) {
			return true
		}
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
	accountName := readRCAccountPayload(packet, PLI_RC_ACCOUNTGET)
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted ACCOUNTGET (non-RC): %s", p.accountName, accountName)
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		var ok bool
		targetPlayer, ok = p.loadOfflineRCAccount(accountName)
		if !ok {
			return true
		}
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_ACCOUNTGET)
	writeRCEncodedString(buf2, accountName)
	buf2.WriteGChar(0)
	writeRCEncodedString(buf2, targetPlayer.email)
	banned := byte(0)
	if targetPlayer.isBanned {
		banned = 1
	}
	buf2.WriteGChar(banned)
	loadOnly := byte(0)
	if targetPlayer.isLoadOnly {
		loadOnly = 1
	}
	buf2.WriteGChar(loadOnly)
	buf2.WriteGChar(0)
	writeRCEncodedString(buf2, "main")
	writeRCEncodedString(buf2, targetPlayer.banLength)
	writeRCEncodedString(buf2, targetPlayer.banReason)
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_ACCOUNTSET(packet []byte) bool {
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_ACCOUNTSET))
	accountName := readRCEncodedString(buf)
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
	_ = readRCEncodedString(buf)
	email := readRCEncodedString(buf)
	banned := buf.ReadGChar() != 0
	loadOnly := buf.ReadGChar() != 0
	buf.ReadGChar()
	_ = readRCEncodedString(buf)
	banReason := readRCEncodedString(buf)
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		var ok bool
		targetPlayer, ok = p.loadOfflineRCAccount(accountName)
		if !ok {
			return true
		}
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
	p.server.sendRCChat(p.rcChatName() + ": " + message)
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
	case "/scripthelp":
		p.sendScriptHelp(arg)
	case "/npc":
		p.server.runRCNPCChat(p, arg)
	case "/open":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_PLAYERPROPSGET3(rcCommandAccountPacket(arg))
		}
	case "/openacc":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_ACCOUNTGET(rcCommandAccountPacket(arg))
		}
	case "/opencomments":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_PLAYERCOMMENTSGET(rcCommandAccountPacket(arg))
		}
	case "/openban":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_PLAYERBANGET(rcCommandAccountPacket(arg))
		}
	case "/openrights":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_PLAYERRIGHTSGET(rcCommandAccountPacket(arg))
		}
	case "/reset":
		if arg == "" {
			arg = p.accountName
		}
		if arg != "" {
			return p.msgPLI_RC_PLAYERPROPSRESET(rcCommandAccountPacket(arg))
		}
	case "/npcshutdown":
		if p.requireNPCControlCommand(command) {
			count, err := p.server.savePutNPCs()
			if err != nil {
				p.sendPLO_RC_CHAT("Server: Failed to save map NPCs: " + err.Error())
				return true
			}
			p.server.ensureNPCServer().Shutdown()
			p.server.sendRCChat(fmt.Sprintf("%s shut down the NPC-Server. Saved %d map NPC(s).", p.accountName, count))
		}
	case "/savenpcs":
		if p.requireNPCControlCommand(command) {
			count, err := p.server.savePutNPCs()
			if err != nil {
				p.sendPLO_RC_CHAT("Server: Failed to save map NPCs: " + err.Error())
				return true
			}
			p.server.sendRCChat(fmt.Sprintf("%s saved %d map NPC(s).", p.accountName, count))
		}
	case "/npckill":
		if p.requireNPCControlCommand(command) {
			p.server.ensureNPCServer().Shutdown()
			p.server.sendRCChat(p.accountName + " killed the NPC-Server.")
		}
	case "/shutdown":
		if p.requireAllRightsCommand(command) {
			p.server.sendRCChat(p.accountName + " shut down the server.")
			go p.server.StopSoon(false)
		}
	case "/restart":
		if p.requireAllRightsCommand(command) {
			p.server.sendRCChat(p.accountName + " restarted the server.")
			go p.server.StopSoon(true)
		}
	}
	return true
}

func (p *Player) requireNPCControlCommand(command string) bool {
	if p == nil || p.server == nil || !p.hasRight(PLPERM_NPCCONTROL) || !p.adminIPMatchesRemote() {
		if p != nil {
			p.sendPLO_RC_CHAT("Server: You are not authorized to use " + command + ".")
		}
		return false
	}
	return true
}

func (p *Player) requireAllRightsCommand(command string) bool {
	if p == nil || p.server == nil || p.adminRights&allLocalRights() != allLocalRights() || !p.adminIPMatchesRemote() {
		if p != nil {
			p.sendPLO_RC_CHAT("Server: You are not authorized to use " + command + ".")
		}
		return false
	}
	return true
}

func (s *Server) refreshScriptHelpCache() {
	if s == nil {
		return
	}
	s.scriptHelpMu.Lock()
	if s.scriptHelpBusy {
		s.scriptHelpMu.Unlock()
		return
	}
	s.scriptHelpBusy = true
	s.scriptHelpMu.Unlock()
	defer func() {
		s.scriptHelpMu.Lock()
		s.scriptHelpBusy = false
		s.scriptHelpCheck = time.Now()
		s.scriptHelpMu.Unlock()
	}()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.gscript.dev")
	if err != nil {
		if s.logger != nil {
			s.logger.Warning("Script help cache failed: %v", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if s.logger != nil {
			s.logger.Warning("Script help cache HTTP %d", resp.StatusCode)
		}
		return
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if s.logger != nil {
			s.logger.Warning("Script help cache read failed: %v", err)
		}
		return
	}
	rawText := string(data)
	s.scriptHelpMu.RLock()
	unchanged := s.scriptHelpReady && s.scriptHelpRaw == rawText
	s.scriptHelpMu.RUnlock()
	if unchanged {
		return
	}
	var raw map[string]ScriptHelpEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		if s.logger != nil {
			s.logger.Warning("Script help cache decode failed: %v", err)
		}
		return
	}
	entries := make([]ScriptHelpEntry, 0, len(raw))
	for key, entry := range raw {
		if entry.Name == "" {
			entry.Name = key
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name) })
	s.scriptHelpMu.Lock()
	s.scriptHelp = entries
	s.scriptHelpRaw = rawText
	s.scriptHelpReady = true
	s.scriptHelpMu.Unlock()
}

func (s *Server) refreshScriptHelpCacheIfStale() {
	if s == nil {
		return
	}
	s.scriptHelpMu.RLock()
	stale := !s.scriptHelpReady || time.Since(s.scriptHelpCheck) >= scriptHelpCacheTTL
	busy := s.scriptHelpBusy
	s.scriptHelpMu.RUnlock()
	if stale && !busy {
		go s.refreshScriptHelpCache()
	}
}

func (p *Player) sendScriptHelp(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		p.sendPLO_RC_CHAT("Usage: /scripthelp <name or wildcard>")
		return true
	}
	p.server.refreshScriptHelpCacheIfStale()
	p.server.scriptHelpMu.RLock()
	ready := p.server.scriptHelpReady
	entries := append([]ScriptHelpEntry(nil), p.server.scriptHelp...)
	p.server.scriptHelpMu.RUnlock()
	if !ready {
		p.sendPLO_RC_CHAT("Script help cache is not loaded yet.")
		return true
	}
	re, err := wildcardRegex(query)
	if err != nil {
		p.sendPLO_RC_CHAT("Invalid script help wildcard.")
		return true
	}
	serverside := make([]string, 0)
	clientside := make([]string, 0)
	for _, entry := range entries {
		line := entry.scriptHelpLine()
		if line == "" || !re.MatchString(strings.ToLower(entry.Name)) {
			continue
		}
		if strings.EqualFold(entry.Scope, "clientside") {
			clientside = append(clientside, line)
		} else {
			serverside = append(serverside, line)
		}
	}
	p.sendPLO_RC_CHAT("Script help for '" + query + "':")
	if len(serverside) == 0 && len(clientside) == 0 {
		p.sendPLO_RC_CHAT("No script help found.")
		return true
	}
	const limit = 40
	count := 0
	for _, line := range serverside {
		if count >= limit {
			p.sendPLO_RC_CHAT("More results omitted.")
			return true
		}
		p.sendPLO_RC_CHAT(line)
		count++
	}
	if len(clientside) > 0 {
		p.sendPLO_RC_CHAT("Clientside:")
	}
	for _, line := range clientside {
		if count >= limit {
			p.sendPLO_RC_CHAT("More results omitted.")
			return true
		}
		p.sendPLO_RC_CHAT(line)
		count++
	}
	return true
}

func wildcardRegex(query string) (*regexp.Regexp, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	pattern := regexp.QuoteMeta(query)
	if strings.Contains(query, "*") {
		pattern = strings.ReplaceAll(pattern, "\\*", ".*")
	}
	pattern = "^.*" + pattern + ".*$"
	return regexp.Compile(pattern)
}

func (e ScriptHelpEntry) scriptHelpLine() string {
	name := strings.TrimSpace(e.Name)
	if name == "" {
		return ""
	}
	line := name
	if strings.EqualFold(e.Type, "function") {
		line += "(" + strings.Join(e.Params, ", ") + ")"
	}
	returns := strings.TrimSpace(e.Returns)
	desc := cleanScriptHelpDescription(e.Description)
	if returns != "" && !strings.EqualFold(returns, "void") {
		line += " - returns " + returns
	}
	if desc != "" {
		if strings.Contains(strings.ToLower(desc), strings.ToLower(line)) {
			return desc
		}
		line += " - " + desc
	}
	return line
}

func cleanScriptHelpDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	switch strings.ToLower(desc) {
	case "", "clientside:", "serverside:", "no matching script function found!":
		return ""
	}
	return desc
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
	if p == nil || p.server == nil {
		return true
	}
	payload := string(packet[1:])
	p.server.logger.Debug("PROFILEGET: %s", payload)
	p.server.sendPlayerTextToListservers(SVO_GETPROF, p.id, payload)
	return true
}
func (p *Player) msgPLI_PROFILESET(packet []byte) bool {
	if p == nil || p.server == nil || len(packet) <= 1 {
		return true
	}
	buf := NewBufferFromBytes(packet[1:])
	account := string(buf.ReadBytes(int(buf.ReadByte())))
	if !strings.EqualFold(account, p.accountName) {
		return true
	}
	p.server.logger.Debug("PROFILESET: %s", account)
	p.server.sendTextToListservers(SVO_SETPROF, string(packet[1:]))
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
	accountName := readRCAccountPayload(packet, PLI_RC_PLAYERRIGHTSGET)
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERRIGHTSGET (non-RC): %s", p.accountName, accountName)
		return true
	}
	if accountName != p.accountName && !p.hasRight(PLPERM_SETRIGHTS) {
		p.server.logger.Warning("%s attempted PLAYERRIGHTSGET without permission", p.accountName)
		p.send(NewBufferFromBytes(rcChatPacket("Server: You are not authorized to view that player's rights.")))
		return true
	}
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYPLAYER|PLTYPE_NPCSERVER)
	if targetPlayer == nil {
		var ok bool
		targetPlayer, ok = p.loadOfflineRCAccount(accountName)
		if !ok {
			return true
		}
	}
	folders := gtokenizeText(strings.Join(targetPlayer.folderList, "\n"))
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERRIGHTSGET)
	writeRCEncodedString(buf2, accountName)
	buf2.WriteGInt5(uint64(targetPlayer.adminRights))
	writeRCEncodedString(buf2, targetPlayer.adminIp)
	buf2.WriteGShort(uint16(len(folders)))
	buf2.Write([]byte(folders))
	p.send(buf2)
	p.server.sendRCChat(p.accountName + " has opened the rights of " + accountName)
	return true
}
func (p *Player) msgPLI_RC_PLAYERRIGHTSSET(packet []byte) bool {
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_PLAYERRIGHTSSET))
	accountName := readRCEncodedString(buf)
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
	newAdminRights := int32(buf.ReadGInt5())
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
	adminIp := readRCEncodedString(buf)
	targetPlayer.adminIp = adminIp
	foldersLen := buf.ReadGShort()
	folders := guntokenizeText(string(buf.ReadBytes(int(foldersLen))))
	folderList := strings.Split(folders, "\n")
	validFolders := []string{}
	for _, folder := range folderList {
		folder = strings.TrimSpace(folder)
		if strings.Contains(folder, ":") || strings.Contains(folder, "..") || strings.Contains(folder, " /*") {
			continue
		}
		if !p.hasRight(PLPERM_SETFOLDERRIGHTS) && accountName != p.accountName && !p.canGrantFolderRight(folder) {
			continue
		}
		if folder != "" {
			validFolders = append(validFolders, folder)
		}
	}
	targetPlayer.folderList = validFolders
	targetPlayer.SaveAccount()
	for _, player := range p.server.players {
		if strings.EqualFold(player.accountName, accountName) {
			player.adminRights = targetPlayer.adminRights
			player.adminIp = targetPlayer.adminIp
			player.folderList = append([]string(nil), targetPlayer.folderList...)
		}
	}
	p.server.logger.Info("%s set rights for account: %s", p.accountName, accountName)
	p.server.sendRCChat(p.accountName + " has set the rights of " + accountName)
	return true
}

func (p *Player) canGrantFolderRight(folder string) bool {
	requestRights, requestPattern, ok := parseRCFolderRight(folder)
	if !ok {
		return false
	}
	for _, ownedFolder := range p.folderList {
		ownedRights, ownedPattern, ok := parseRCFolderRight(ownedFolder)
		if !ok || !folderRightsContain(ownedRights, requestRights) {
			continue
		}
		if folderPatternContains(ownedPattern, requestPattern) {
			return true
		}
	}
	return false
}

func parseRCFolderRight(folder string) (string, string, bool) {
	folder = strings.TrimSpace(strings.ReplaceAll(folder, "\\", "/"))
	if folder == "" {
		return "", "", false
	}
	rights := "r"
	pattern := folder
	if parts := strings.SplitN(folder, " ", 2); len(parts) == 2 {
		rights = strings.ToLower(strings.TrimSpace(parts[0]))
		pattern = strings.TrimSpace(parts[1])
	}
	if rights == "" || pattern == "" || strings.Contains(pattern, "..") || strings.Contains(pattern, ":") {
		return "", "", false
	}
	return rights, pattern, true
}

func folderRightsContain(ownedRights, requestRights string) bool {
	for _, right := range requestRights {
		if right != 'r' && right != 'w' {
			return false
		}
		if !strings.ContainsRune(ownedRights, right) {
			return false
		}
	}
	return true
}

func folderPatternContains(ownedPattern, requestPattern string) bool {
	ownedPattern = strings.ToLower(filepath.ToSlash(strings.TrimSpace(ownedPattern)))
	requestPattern = strings.ToLower(filepath.ToSlash(strings.TrimSpace(requestPattern)))
	if ownedPattern == requestPattern {
		return true
	}
	matched, err := filepath.Match(ownedPattern, requestPattern)
	return err == nil && matched
}

func (p *Player) msgPLI_RC_PLAYERCOMMENTSGET(packet []byte) bool {
	if p.playerType != PLTYPE_RC && p.playerType != PLTYPE_RC2 && p.playerType != PLTYPE_ANYRC {
		p.server.logger.Warning("[Hack] %s attempted PLAYERCOMMENTSGET (non-RC)", p.accountName)
		return true
	}
	accountName := readRCAccountPayload(packet, PLI_RC_PLAYERCOMMENTSGET)
	targetPlayer := p.server.getPlayerByAccount(accountName, PLTYPE_ANYCLIENT)
	if targetPlayer == nil {
		var ok bool
		targetPlayer, ok = p.loadOfflineRCAccount(accountName)
		if !ok {
			return true
		}
	}
	buf2 := NewBuffer()
	buf2.WriteByte(PLO_RC_PLAYERCOMMENTSGET)
	writeRCEncodedString(buf2, accountName)
	buf2.Write([]byte(targetPlayer.accountComments))
	p.send(buf2)
	p.server.sendRCChat(p.accountName + " has opened the comments of " + accountName)
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
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_PLAYERCOMMENTSSET))
	accountName := readRCEncodedString(buf)
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
	comment := buf.ReadString()
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
	accountName := readRCAccountPayload(packet, PLI_RC_PLAYERBANGET)
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
	writeRCEncodedString(buf2, accountName)
	banned := byte(0)
	if targetPlayer.isBanned {
		banned = 1
	}
	buf2.WriteGChar(banned)
	buf2.Write([]byte(targetPlayer.banReason))
	p.send(buf2)
	return true
}
func (p *Player) msgPLI_RC_PLAYERBANSET(packet []byte) bool {
	buf := NewBufferFromBytes(rcPayload(packet, PLI_RC_PLAYERBANSET))
	accountName := readRCEncodedString(buf)
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
	banned := buf.ReadGChar() != 0
	reason := buf.ReadString()
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
	folderMap := p.rcFolderMap()
	folders := p.rcFileBrowserFolderList(folderMap)
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FILEBROWSER_DIRLIST)
	buf.Write([]byte(gtokenizeText(folders)))
	p.send(buf)
	if !p.isFtp {
		buf2 := NewBuffer()
		buf2.WriteByte(PLO_RC_FILEBROWSER_MESSAGE)
		serverName := strings.TrimSpace(p.server.settings.Get("name"))
		if serverName == "" {
			serverName = "this server"
		}
		buf2.Write([]byte("Welcome to the File Browser for " + serverName + "."))
		p.send(buf2)
	}
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
		if !strings.Contains(rights, "r") {
			continue
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
		if folderPath == "" && strings.ContainsAny(wild, "*?") {
			folderPath = "world/"
		} else {
			folderPath = rcFileBrowserPath(folderPath)
		}
		for _, realFolder := range p.expandRCFolderPath(folderPath) {
			folderMap[realFolder] += rights + ":" + wild + "\n"
		}
	}
	return folderMap
}

func (p *Player) rcFileBrowserFolderList(folderMap map[string]string) string {
	var folders []string
	seen := make(map[string]bool)
	for folder, entries := range folderMap {
		for _, entry := range strings.Split(entries, "\n") {
			if entry == "" {
				continue
			}
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 {
				continue
			}
			line := parts[0] + " " + folder + parts[1]
			if !seen[line] {
				folders = append(folders, line)
				seen[line] = true
			}
		}
	}
	sort.Strings(folders)
	return strings.Join(folders, "\n")
}

func (p *Player) rcFileRights(filePath string) string {
	filePath = rcFileBrowserPath(filePath)
	if filePath == "" || strings.Contains(filePath, "..") || strings.Contains(filePath, ":") {
		return ""
	}
	rightSet := make(map[rune]bool)
	for _, entry := range p.folderList {
		rights := "r"
		pattern := entry
		if parts := strings.SplitN(entry, " ", 2); len(parts) == 2 {
			rights = strings.ToLower(strings.TrimSpace(parts[0]))
			pattern = strings.TrimSpace(parts[1])
		}
		pattern = rcFileBrowserPattern(pattern)
		if pattern == "" || strings.Contains(pattern, "..") || strings.Contains(pattern, ":") {
			continue
		}
		matched, err := path.Match(pattern, filePath)
		if err != nil || !matched {
			continue
		}
		for _, right := range rights {
			if right == 'r' || right == 'w' {
				rightSet[right] = true
			}
		}
	}
	rights := ""
	if rightSet['r'] {
		rights += "r"
	}
	if rightSet['w'] {
		rights += "w"
	}
	return rights
}

func rcFileBrowserPath(filePath string) string {
	filePath = filepath.ToSlash(strings.TrimLeft(strings.TrimSpace(filePath), "/"))
	if filePath == "" || strings.Contains(filePath, "..") || strings.Contains(filePath, ":") {
		return filePath
	}
	root := filePath
	if idx := strings.Index(root, "/"); idx >= 0 {
		root = root[:idx]
	}
	if root == "accounts" || root == "config" || root == "logs" || root == "world" {
		return filePath
	}
	if strings.ContainsAny(root, "*?") {
		return filePath
	}
	return "world/" + filePath
}

func rcFileBrowserPattern(pattern string) string {
	pattern = filepath.ToSlash(strings.TrimLeft(strings.TrimSpace(pattern), "/"))
	if pattern == "" || strings.Contains(pattern, "..") || strings.Contains(pattern, ":") {
		return pattern
	}
	root := pattern
	hasSlash := strings.Contains(pattern, "/")
	if idx := strings.Index(root, "/"); idx >= 0 {
		root = root[:idx]
	}
	if root == "accounts" || root == "config" || root == "logs" || root == "world" {
		return pattern
	}
	if hasSlash && strings.ContainsAny(root, "*?") {
		return pattern
	}
	return "world/" + pattern
}

func (p *Player) rcFileHasRight(filePath string, right rune) bool {
	return strings.ContainsRune(p.rcFileRights(filePath), right)
}

func (p *Player) rcFolderHasRight(folderPath string, right rune) bool {
	folderPath = filepath.ToSlash(strings.Trim(folderPath, "/"))
	if folderPath == "" {
		return p.rcFileHasRight("x", right)
	}
	return p.rcFileHasRight(folderPath+"/x", right)
}

func (p *Player) expandRCFolderPath(folderPath string) []string {
	folderPath = filepath.ToSlash(strings.TrimSpace(folderPath))
	if folderPath == "" {
		return []string{""}
	}
	if !strings.ContainsAny(folderPath, "*?") {
		return []string{folderPath}
	}
	parts := strings.Split(strings.TrimSuffix(folderPath, "/"), "/")
	var out []string
	var walk func(string, int)
	walk = func(prefix string, idx int) {
		if idx >= len(parts) {
			out = append(out, prefix)
			return
		}
		dirs, err := p.rcFileBrowserListDirs(prefix)
		if err != nil {
			return
		}
		for _, dir := range dirs {
			matched, err := filepath.Match(parts[idx], dir)
			if err == nil && matched {
				walk(prefix+dir+"/", idx+1)
			}
		}
	}
	startPrefix := ""
	startIndex := 0
	if len(parts) > 0 && parts[0] == "world" {
		startPrefix = "world/"
		startIndex = 1
	}
	walk(startPrefix, startIndex)
	return out
}

func (p *Player) rcFileBrowserListDirs(prefix string) ([]string, error) {
	if prefix != "" {
		return p.server.config.ListDirs(prefix)
	}
	entries, err := os.ReadDir(p.server.config.GetBasePath())
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs, nil
}

func (p *Player) sendRCFileBrowserDir(folderMap map[string]string) {
	files, err := p.server.config.ListFiles(p.lastFolder)
	if err != nil {
		p.server.logger.Error("Failed to list files in %s: %v", p.lastFolder, err)
		return
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_RC_FILEBROWSER_DIR)
	buf.WriteGChar(byte(len(p.lastFolder)))
	buf.Write([]byte(p.lastFolder))
	for _, file := range files {
		if strings.HasPrefix(file, ".") {
			continue
		}
		filePath := p.lastFolder + file
		rights := p.rcFileRights(filePath)
		if !strings.Contains(rights, "r") {
			continue
		}
		fileInfo, err := p.server.config.FileInfo(filePath)
		if err != nil {
			continue
		}
		entry := NewBuffer()
		entry.WriteGChar(byte(len(file)))
		entry.Write([]byte(file))
		entry.WriteGChar(byte(len(rights)))
		entry.Write([]byte(rights))
		entry.WriteGInt5(uint64(fileInfo.Size()))
		entry.WriteGInt5(uint64(fileInfo.ModTime().Unix()))
		entryData := entry.Bytes()
		buf.WriteByte(' ')
		buf.WriteGChar(byte(len(entryData)))
		buf.Write(entryData)
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
	if !p.rcFileHasRight(filePath, 'r') {
		p.send(fileBrowserMessagePacket("Insufficient rights to download/view " + filePath))
		return true
	}
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
	nameLen := int(buf.ReadGChar())
	fileName := string(buf.ReadBytes(nameLen))
	filePath := p.lastFolder + fileName
	if !p.rcFileHasRight(filePath, 'w') {
		p.send(fileBrowserMessagePacket("Insufficient rights to upload " + filePath))
		return true
	}
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
		p.updateUploadedFile(p.lastFolder, fileName)
	} else {
		p.rcLargeFiles[fileName] = append(p.rcLargeFiles[fileName], fileData...)
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
	if !p.rcFileHasRight(source, 'w') || !p.rcFileHasRight(destination, 'w') {
		p.send(fileBrowserMessagePacket("Not allowed to move file " + source))
		return true
	}
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
	if !p.rcFileHasRight(filePath, 'w') {
		p.send(fileBrowserMessagePacket("Not allowed to delete file " + filePath))
		return true
	}
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
	if !p.rcFileHasRight(oldPath, 'w') || !p.rcFileHasRight(newPath, 'w') {
		p.send(fileBrowserMessagePacket("Not allowed to rename/overwrite file " + oldPath + " or " + newPath))
		return true
	}
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
	if !p.rcFileHasRight(p.lastFolder+fileName, 'w') {
		p.send(fileBrowserMessagePacket("Insufficient rights to upload " + p.lastFolder + fileName))
		return true
	}
	p.rcLargeFiles[fileName] = nil
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
	if !p.rcFileHasRight(filePath, 'w') {
		p.send(fileBrowserMessagePacket("Insufficient rights to upload " + filePath))
		return true
	}
	fileData, exists := p.rcLargeFiles[fileName]
	if !exists {
		return true
	}
	if err := p.server.config.SaveFile(filePath, fileData); err != nil {
		p.server.logger.Error("Failed to save large file %s: %v", filePath, err)
		return true
	}
	delete(p.rcLargeFiles, fileName)
	p.updateUploadedFile(p.lastFolder, fileName)
	p.server.logger.Info("%s uploaded large file %s", p.accountName, fileName)
	p.send(fileBrowserMessagePacket("Uploaded large file " + fileName))
	return true
}

func (p *Player) updateUploadedFile(dir, fileName string) {
	if p == nil || p.server == nil {
		return
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext != ".nw" && ext != ".graal" && ext != ".zelda" {
		return
	}
	fullPath := filepath.ToSlash(dir + fileName)
	baseName := filepath.Base(fullPath)
	candidates := []string{fullPath, strings.TrimPrefix(fullPath, "world/levels/"), baseName, cleanLevelName(fullPath), cleanLevelName(strings.TrimPrefix(fullPath, "world/levels/")), cleanLevelName(baseName)}
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if level := p.server.GetLevel(candidate); level != nil {
			if level.reload(p.server) {
				p.server.resendLevelData(level)
			}
		}
	}
}

func (s *Server) resendLevelData(level *Level) {
	if s == nil || level == nil {
		return
	}
	for _, id := range append([]uint16(nil), level.getPlayers()...) {
		if player, ok := s.players[id]; ok && player != nil && player.conn != nil {
			levelName := player.levelName
			if levelName == "" {
				levelName = level.levelName
			}
			if strings.ContainsAny(levelName, `/\`) {
				levelName = filepath.Base(filepath.ToSlash(levelName))
			}
			player.warp(levelName, float64(player.x)/16, float64(player.y)/16)
		}
	}
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
	if !p.rcFolderHasRight(folder, 'w') {
		p.send(fileBrowserMessagePacket("Not allowed to delete folder " + folder))
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
