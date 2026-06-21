package main

import (
	"fmt"
	"strconv"
	"strings"

	nativegs2vm "github.com/MorenoLand/GScript.gs2vm-go"
)

type gs2VMResult struct {
	output         []string
	clientTriggers []string
	playerFlags    []gs2VMPlayerFlag
	playerMessages []gs2VMPlayerMessage
	playerWarps    []gs2VMPlayerWarp
	this           map[string]any
	err            string
}

type gs2VMPlayerFlag struct {
	account string
	name    string
	value   string
}

type gs2VMPlayerMessage struct {
	account string
	message string
}

type gs2VMPlayerWarp struct {
	account string
	level   string
	x       float64
	y       float64
}

func (s *Server) runServerSideGS2(scriptType, scriptName, eventName, script string, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2Native(scriptType, scriptName, eventName, script, nil, eventArgs...)
}

func (s *Server) runServerSideGS2Native(scriptType, scriptName, eventName, script string, playerContext map[string]string, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2NativeWithState(scriptType, scriptName, eventName, script, nil, playerContext, eventArgs...)
}

func (s *Server) runServerSideGS2NativeWithState(scriptType, scriptName, eventName, script string, thisState map[string]any, playerContext map[string]string, eventArgs ...string) gs2VMResult {
	src := serversideGS2(script)
	if strings.TrimSpace(src) == "" {
		return gs2VMResult{}
	}
	if playerContext == nil {
		playerContext = make(map[string]string)
	}
	result := nativegs2vm.Run(nativegs2vm.Config{
		ScriptName:    scriptName,
		EventName:     eventName,
		Script:        src,
		Params:        eventArgs,
		Player:        playerContext,
		PlayerFlags:   s.snapshotGS2PlayerFlags(playerContext["account"]),
		Players:       s.snapshotGS2Players(),
		This:          thisState,
		ServerFlags:   s.snapshotServerFlags(),
		ServerOptions: s.snapshotServerOptions(),
	})
	out := gs2VMResult{output: result.Output, this: result.This, err: result.Err}
	for _, trigger := range result.ClientTriggers {
		parts := []string{trigger.Name}
		parts = append(parts, trigger.Args...)
		out.clientTriggers = append(out.clientTriggers, "clientside,"+strings.Join(parts, ","))
	}
	for _, flag := range result.PlayerFlags {
		out.playerFlags = append(out.playerFlags, gs2VMPlayerFlag{account: flag.Account, name: flag.Name, value: flag.Value})
	}
	for _, message := range result.PlayerMessages {
		out.playerMessages = append(out.playerMessages, gs2VMPlayerMessage{account: message.Account, message: message.Message})
	}
	for _, warp := range result.PlayerWarps {
		out.playerWarps = append(out.playerWarps, gs2VMPlayerWarp{account: warp.Account, level: warp.Level, x: warp.X, y: warp.Y})
	}
	return out
}

func snapshotGS2Player(player *Player) map[string]string {
	out := make(map[string]string)
	if player == nil {
		return out
	}
	account := player.accountName
	if player.deviceId > 0 && (account == "" || strings.EqualFold(account, "guest")) {
		account = "pc:" + strconv.FormatInt(player.deviceId, 10)
	}
	out["account"] = account
	out["nick"] = player.character.nickName
	out["nickname"] = player.character.nickName
	out["level"] = player.levelName
	return out
}

func (s *Server) snapshotGS2PlayerFlags(account string) map[string]string {
	if s == nil || account == "" {
		return nil
	}
	if player := s.findGS2Player(account); player != nil {
		return copyStringMap(player.flagList)
	}
	return nil
}

func (s *Server) snapshotGS2Players() []nativegs2vm.PlayerContext {
	if s == nil {
		return nil
	}
	players := s.GetAllPlayers()
	out := make([]nativegs2vm.PlayerContext, 0, len(players))
	for _, player := range players {
		account := gs2PlayerAccount(player)
		if player == nil || account == "" || player.playerType&(PLTYPE_ANYPLAYER|PLTYPE_ANYNC|PLTYPE_NPCSERVER) == 0 {
			continue
		}
		out = append(out, nativegs2vm.PlayerContext{Account: account, Nick: player.character.nickName, Nickname: player.character.nickName, Level: player.levelName, Flags: copyStringMap(player.flagList)})
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func gs2PlayerAccount(player *Player) string {
	if player == nil {
		return ""
	}
	if player.deviceId > 0 && (player.accountName == "" || strings.EqualFold(player.accountName, "guest")) {
		return "pc:" + strconv.FormatInt(player.deviceId, 10)
	}
	return player.accountName
}

func (s *Server) findGS2Player(account string) *Player {
	if s == nil || account == "" {
		return nil
	}
	for _, player := range s.GetAllPlayers() {
		if player == nil || player.playerType&(PLTYPE_ANYPLAYER|PLTYPE_ANYNC|PLTYPE_NPCSERVER) == 0 {
			continue
		}
		if strings.EqualFold(player.accountName, account) || strings.EqualFold(gs2PlayerAccount(player), account) || strings.EqualFold(player.character.nickName, account) {
			return player
		}
	}
	return nil
}

func (s *Server) snapshotServerFlags() map[string]string {
	out := make(map[string]string)
	if s == nil {
		return out
	}
	s.flagMu.RLock()
	defer s.flagMu.RUnlock()
	for key, value := range s.flags {
		out[key] = value
	}
	return out
}

func (s *Server) snapshotServerOptions() map[string]string {
	out := make(map[string]string)
	if s == nil || s.settings == nil {
		return out
	}
	s.settings.mu.RLock()
	defer s.settings.mu.RUnlock()
	for key, value := range s.settings.settings {
		out[key] = value
	}
	return out
}

func serversideGS2(script string) string {
	normalized := strings.ReplaceAll(script, "\r\n", "\n")
	lower := strings.ToLower(normalized)
	idx := strings.Index(lower, "//#clientside")
	if idx >= 0 {
		return strings.TrimSpace(normalized[:idx])
	}
	return normalized
}

func (s *Server) runServerSideWeaponEvent(weapon *Weapon, eventName string) {
	s.runServerSideWeaponEventForPlayer(weapon, eventName, nil)
}

func (s *Server) runServerSideWeaponEventForPlayer(weapon *Weapon, eventName string, player *Player, eventArgs ...string) {
	if s == nil || weapon == nil || weapon.script == "" {
		return
	}
	if !s.npcServerRunning() {
		return
	}
	result := s.runServerSideWeaponGS2ForPlayer(weapon, eventName, player, eventArgs...)
	if result.err != "" {
		s.sendGS2VMErrorToNC("Weapon "+weapon.name, result.err)
		return
	}
	weapon.vmThis = result.this
	s.applyGS2VMResult(result)
	if player != nil {
		for _, action := range result.clientTriggers {
			player.sendPLO_TRIGGERACTION(0, 0, 0, 0, action)
		}
	}
	for _, line := range result.output {
		s.logger.Info("[GS2:%s] %s", weapon.name, line)
		s.sendToNC(line)
	}
}

func (s *Server) runServerSideGS2ForPlayer(scriptType, scriptName, eventName, script string, player *Player, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2Native(scriptType, scriptName, eventName, script, snapshotGS2Player(player), eventArgs...)
}

func (s *Server) runServerSideWeaponGS2ForPlayer(weapon *Weapon, eventName string, player *Player, eventArgs ...string) gs2VMResult {
	if weapon == nil {
		return gs2VMResult{}
	}
	return s.runServerSideGS2NativeWithState("weapon", weapon.name, eventName, weapon.script, weapon.vmThis, snapshotGS2Player(player), eventArgs...)
}

func (s *Server) sendGS2VMErrorToNC(origin, text string) {
	if s == nil {
		return
	}
	s.sendToNC(fmt.Sprintf("Compiler error for %s:", origin))
	wroteLine := false
	for _, line := range strings.Split(text, "\n") {
		line = normalizeGS2VMErrorLine(line)
		if line == "" {
			continue
		}
		s.sendToNC("error: " + line)
		wroteLine = true
	}
	if !wroteLine {
		s.sendToNC("error: runtime failed")
	}
}

func normalizeGS2VMErrorLine(line string) string {
	line = normalizeCompilerOutputLine(line)
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "typeerror:") {
		line = strings.TrimSpace(line[len("TypeError:"):])
	}
	if idx := strings.Index(line, " at "); idx >= 0 {
		if eval := strings.Index(line[idx:], "(<eval>:"); eval >= 0 {
			evalStart := idx + eval + len("(<eval>:")
			evalEnd := evalStart
			for evalEnd < len(line) && line[evalEnd] >= '0' && line[evalEnd] <= '9' {
				evalEnd++
			}
			if evalEnd > evalStart {
				return strings.TrimSpace(line[:idx]) + " at line " + line[evalStart:evalEnd]
			}
		}
	}
	return strings.TrimSpace(line)
}

func (s *Server) applyGS2VMResult(result gs2VMResult) {
	if s == nil {
		return
	}
	for _, flag := range result.playerFlags {
		if player := s.findGS2Player(flag.account); player != nil {
			player.SetFlag(flag.name, flag.value)
			player.sendPLO_FLAGSET(flag.name, flag.value)
		}
	}
	for _, message := range result.playerMessages {
		if player := s.findGS2Player(message.account); player != nil {
			s.sendGS2PlayerPM(player, message.message)
		}
	}
	for _, warp := range result.playerWarps {
		if player := s.findGS2Player(warp.account); player != nil && warp.level != "" {
			player.warp(warp.level, warp.x, warp.y)
		}
	}
}

func (s *Server) sendGS2PlayerPM(player *Player, message string) {
	if s == nil || player == nil || !s.npcServerRunning() {
		return
	}
	senderId := uint16(1)
	if npcServer := s.ensureNPCServer().Player(); npcServer != nil {
		senderId = npcServer.id
	}
	buf := NewBuffer()
	buf.WriteByte(PLO_PRIVATEMESSAGE).WriteGShort(senderId).Write([]byte("\"\",")).
		Write([]byte(gtokenizeText(message)))
	player.send(buf)
}
