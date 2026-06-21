package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	nativegs2vm "github.com/MorenoLand/GScript.gs2vm-go"
)

var gs2JoinPattern = regexp.MustCompile(`(?im)^\s*join\s*(?:\(\s*["']?([^"')\s;]+)["']?\s*\)|["']?([^"'\s;]+)["']?)\s*;?\s*$`)

type gs2VMResult struct {
	output           []string
	clientTriggers   []string
	playerFlags      []gs2VMPlayerFlag
	serverFlags      []gs2VMServerFlag
	playerMessages   []gs2VMPlayerMessage
	playerWeapons    []gs2VMPlayerWeapon
	playerWarps      []gs2VMPlayerWarp
	npcFlags         []gs2VMNPCFlag
	npcFunctionCalls []gs2VMNPCFunctionCall
	npcActions       []gs2VMNPCAction
	socketActions    []gs2VMSocketAction
	socketUpdates    []gs2VMSocketUpdate
	scheduledEvents  []gs2VMScheduledEvent
	this             map[string]any
	err              string
	scriptType       string
	scriptName       string
	eventName        string
	script           string
	playerContext    map[string]string
	npcID            uint32
	vmRevision       int64
}

type gs2VMPlayerFlag struct {
	account string
	name    string
	value   string
}

type gs2VMServerFlag struct {
	name    string
	value   string
	deleted bool
}

type gs2VMPlayerMessage struct {
	account string
	message string
}

type gs2VMPlayerWeapon struct {
	account string
	name    string
	add     bool
}

type gs2VMPlayerWarp struct {
	account string
	level   string
	x       float64
	y       float64
}

type gs2VMNPCFlag struct {
	id    uint32
	name  string
	value string
}

type gs2VMNPCFunctionCall struct {
	id       uint32
	name     string
	function string
	args     []string
}

type gs2VMNPCAction struct {
	id        uint32
	shapeType int
	width     int
	height    int
	tileTypes []string
	chat      string
	warpLevel string
	warpX     float64
	warpY     float64
}

type gs2VMSocketAction struct {
	action           string
	name             string
	id               string
	port             int
	data             string
	packageDelimiter string
}

type gs2VMSocketUpdate struct {
	name             string
	id               string
	port             int
	ipAddress        string
	data             string
	packageDelimiter string
	isConnected      bool
}

type gs2VMScheduledEvent struct {
	event string
	delay float64
}

func (s *Server) runServerSideGS2(scriptType, scriptName, eventName, script string, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2Native(scriptType, scriptName, eventName, script, nil, eventArgs...)
}

func (s *Server) runServerSideGS2Native(scriptType, scriptName, eventName, script string, playerContext map[string]string, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2NativeWithState(scriptType, scriptName, eventName, script, nil, playerContext, eventArgs...)
}

func (s *Server) runServerSideGS2NativeWithState(scriptType, scriptName, eventName, script string, thisState map[string]any, playerContext map[string]string, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2NativeWithStateAndSocket(scriptType, scriptName, eventName, script, thisState, playerContext, 0, nil, eventArgs...)
}

func (s *Server) runServerSideGS2NativeWithStateAndSocket(scriptType, scriptName, eventName, script string, thisState map[string]any, playerContext map[string]string, npcID uint32, socket *nativegs2vm.SocketContext, eventArgs ...string) gs2VMResult {
	src := serversideGS2(script)
	if strings.TrimSpace(src) == "" {
		return gs2VMResult{}
	}
	src = s.expandJoinedClasses(src, nil)
	if playerContext == nil {
		playerContext = make(map[string]string)
	}
	fileRoot := ""
	if s.config != nil {
		fileRoot = s.config.GetBasePath()
	}
	result := nativegs2vm.Run(nativegs2vm.Config{
		ScriptName:    scriptName,
		EventName:     eventName,
		Script:        src,
		Params:        eventArgs,
		Player:        playerContext,
		PlayerFlags:   s.snapshotGS2PlayerFlags(playerContext["account"]),
		Players:       s.snapshotGS2Players(),
		Weapons:       s.snapshotGS2Weapons(),
		NPCs:          s.snapshotGS2NPCs(),
		NPCID:         npcID,
		This:          thisState,
		ServerFlags:   s.snapshotServerFlags(),
		ServerOptions: s.snapshotServerOptions(),
		FileRoot:      fileRoot,
		FileRights:    s.snapshotGS2FileRights(),
		Socket:        socket,
	})
	out := gs2VMResult{output: result.Output, this: result.This, err: result.Err, scriptType: scriptType, scriptName: scriptName, eventName: eventName, script: script, playerContext: copyStringMap(playerContext), npcID: npcID}
	for _, trigger := range result.ClientTriggers {
		parts := []string{trigger.Name}
		parts = append(parts, trigger.Args...)
		out.clientTriggers = append(out.clientTriggers, "clientside,"+strings.Join(parts, ","))
	}
	for _, flag := range result.PlayerFlags {
		out.playerFlags = append(out.playerFlags, gs2VMPlayerFlag{account: flag.Account, name: flag.Name, value: flag.Value})
	}
	for _, flag := range result.ServerFlags {
		out.serverFlags = append(out.serverFlags, gs2VMServerFlag{name: flag.Name, value: flag.Value, deleted: flag.Deleted})
	}
	for _, message := range result.PlayerMessages {
		out.playerMessages = append(out.playerMessages, gs2VMPlayerMessage{account: message.Account, message: message.Message})
	}
	for _, weapon := range result.PlayerWeapons {
		out.playerWeapons = append(out.playerWeapons, gs2VMPlayerWeapon{account: weapon.Account, name: weapon.Name, add: weapon.Add})
	}
	for _, warp := range result.PlayerWarps {
		out.playerWarps = append(out.playerWarps, gs2VMPlayerWarp{account: warp.Account, level: warp.Level, x: warp.X, y: warp.Y})
	}
	for _, flag := range result.NPCFlags {
		out.npcFlags = append(out.npcFlags, gs2VMNPCFlag{id: flag.ID, name: flag.Name, value: flag.Value})
	}
	for _, call := range result.NPCFunctionCalls {
		out.npcFunctionCalls = append(out.npcFunctionCalls, gs2VMNPCFunctionCall{id: call.ID, name: call.Name, function: call.Function, args: call.Args})
	}
	for _, action := range result.NPCActions {
		out.npcActions = append(out.npcActions, gs2VMNPCAction{id: action.ID, shapeType: action.ShapeType, width: action.Width, height: action.Height, tileTypes: action.TileTypes, chat: action.Chat, warpLevel: action.WarpLevel, warpX: action.WarpX, warpY: action.WarpY})
	}
	for _, action := range result.SocketActions {
		out.socketActions = append(out.socketActions, gs2VMSocketAction{action: action.Action, name: action.Name, id: action.ID, port: action.Port, data: action.Data, packageDelimiter: action.PackageDelimiter})
	}
	for _, update := range result.SocketUpdates {
		out.socketUpdates = append(out.socketUpdates, gs2VMSocketUpdate{name: update.Name, id: update.ID, port: update.Port, ipAddress: update.IPAddress, data: update.Data, packageDelimiter: update.PackageDelimiter, isConnected: update.IsConnected})
	}
	for _, event := range result.ScheduledEvents {
		out.scheduledEvents = append(out.scheduledEvents, gs2VMScheduledEvent{event: event.Event, delay: event.Delay})
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
	out["id"] = strconv.Itoa(int(player.id))
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
		out = append(out, nativegs2vm.PlayerContext{ID: player.id, Account: account, Nick: player.character.nickName, Nickname: player.character.nickName, Level: player.levelName, Flags: copyStringMap(player.flagList)})
	}
	return out
}

func (s *Server) snapshotGS2Weapons() []nativegs2vm.WeaponContext {
	if s == nil {
		return nil
	}
	s.weaponMu.RLock()
	defer s.weaponMu.RUnlock()
	out := make([]nativegs2vm.WeaponContext, 0, len(s.weapons))
	seen := make(map[*Weapon]bool, len(s.weapons))
	for _, weapon := range s.weapons {
		if weapon == nil || seen[weapon] {
			continue
		}
		out = append(out, nativegs2vm.WeaponContext{Name: weapon.name, Image: weapon.image})
		seen[weapon] = true
	}
	return out
}

func (s *Server) snapshotGS2FileRights() []string {
	if s == nil || s.npcServer == nil || s.npcServer.Player() == nil {
		return nil
	}
	return append([]string(nil), s.npcServer.Player().folderList...)
}

func copyStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (s *Server) snapshotGS2NPCs() []nativegs2vm.NPCContext {
	if s == nil {
		return nil
	}
	s.npcMu.RLock()
	defer s.npcMu.RUnlock()
	out := make([]nativegs2vm.NPCContext, 0, len(s.npcs))
	for _, npc := range s.npcs {
		if npc == nil || npc.npcName == "" || npc.npcType != DBNPC {
			continue
		}
		npc.mu.Lock()
		out = append(out, nativegs2vm.NPCContext{ID: npc.id, Name: npc.npcName, Script: npc.script, This: mergeNPCVMState(npc)})
		npc.mu.Unlock()
	}
	return out
}

func mergeNPCVMState(npc *NPC) map[string]any {
	out := make(map[string]any)
	if npc == nil {
		return out
	}
	for key, value := range npc.vmThis {
		out[key] = value
	}
	for key, value := range npc.flagList {
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

func (s *Server) expandJoinedClasses(script string, seen map[string]bool) string {
	if s == nil || strings.TrimSpace(script) == "" {
		return script
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	var joins []string
	cleaned := gs2JoinPattern.ReplaceAllStringFunc(script, func(line string) string {
		match := gs2JoinPattern.FindStringSubmatch(line)
		if len(match) > 2 {
			name := strings.TrimSpace(match[1])
			if name == "" {
				name = strings.TrimSpace(match[2])
			}
			if name != "" {
				joins = append(joins, name)
			}
		}
		return ""
	})
	var out strings.Builder
	out.WriteString(cleaned)
	for _, name := range joins {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		classObj := s.GetClass(name)
		if classObj == nil || strings.TrimSpace(classObj.script) == "" {
			continue
		}
		out.WriteString("\n")
		out.WriteString(s.expandJoinedClasses(classObj.script, seen))
	}
	return out.String()
}

func (s *Server) GetClass(name string) *ScriptClass {
	if s == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	s.weaponMu.RLock()
	defer s.weaponMu.RUnlock()
	if classObj := s.classes[name]; classObj != nil {
		return classObj
	}
	if classObj := s.classes[strings.ToLower(name)]; classObj != nil {
		return classObj
	}
	for _, classObj := range s.classes {
		if classObj != nil && strings.EqualFold(classObj.name, name) {
			return classObj
		}
	}
	return nil
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

func (s *Server) runServerSideNPCEventForPlayer(npc *NPC, eventName string, player *Player, eventArgs ...string) {
	if s == nil || npc == nil || npc.script == "" || !s.npcServerRunning() {
		return
	}
	result := s.runServerSideGS2NativeWithStateAndSocket("npc", npc.npcName, eventName, npc.script, npc.vmThis, snapshotGS2Player(player), npc.id, nil, eventArgs...)
	result.vmRevision = npc.vmRevision
	if result.err != "" {
		s.sendGS2VMErrorToNC("NPC "+npc.npcName, result.err)
		return
	}
	npc.vmThis = result.this
	s.applyGS2VMResult(result)
	if player != nil {
		for _, action := range result.clientTriggers {
			player.sendPLO_TRIGGERACTION(0, 0, 0, 0, action)
		}
	}
	for _, line := range result.output {
		s.logger.Info("[GS2:%s] %s", npc.npcName, line)
		s.sendToNC(line)
	}
}

func (s *Server) runLevelNPCTriggerAction(player *Player, npcID uint32, x, y int, parts []string) {
	if s == nil || player == nil || !s.npcServerRunning() || len(parts) == 0 {
		return
	}
	level := player.currentLevel
	if level == nil {
		level = s.GetLevel(cleanLevelName(player.levelName))
	}
	if level == nil {
		return
	}
	eventName := "onAction" + strings.TrimSpace(parts[0])
	args := []string{}
	if len(parts) > 1 {
		args = append(args, parts[1:]...)
	}
	for _, npc := range level.npcs {
		if npc == nil || strings.TrimSpace(npc.script) == "" {
			continue
		}
		if npcID != 0 && npc.id != npcID {
			continue
		}
		if npcID == 0 && !npcMatchesTriggerPoint(npc, x, y) {
			continue
		}
		s.runServerSideNPCEventForPlayer(npc, eventName, player, args...)
	}
}

func npcMatchesTriggerPoint(npc *NPC, x, y int) bool {
	if npc == nil {
		return false
	}
	nx := int(npc.x)
	ny := int(npc.y)
	width := npc.width
	height := npc.height
	if width <= 0 {
		width = 16
	}
	if height <= 0 {
		height = 16
	}
	return x >= nx && y >= ny && x < nx+width && y < ny+height
}

func (s *Server) runServerSideEventForActiveScripts(eventName string, player *Player, eventArgs ...string) {
	if s == nil || !s.npcServerRunning() {
		return
	}
	for _, weapon := range s.serverSideWeapons() {
		s.runServerSideWeaponEventForPlayer(weapon, eventName, player, eventArgs...)
	}
	for _, npc := range s.serverSideNPCs() {
		s.runServerSideNPCEventForPlayer(npc, eventName, player, eventArgs...)
	}
}

func (s *Server) serverSideWeapons() []*Weapon {
	if s == nil {
		return nil
	}
	s.weaponMu.RLock()
	defer s.weaponMu.RUnlock()
	out := make([]*Weapon, 0, len(s.weapons))
	seen := make(map[*Weapon]bool, len(s.weapons))
	for _, weapon := range s.weapons {
		if weapon != nil && !weapon.defPlayer && strings.TrimSpace(weapon.script) != "" && !seen[weapon] {
			out = append(out, weapon)
			seen[weapon] = true
		}
	}
	return out
}

func (s *Server) serverSideNPCs() []*NPC {
	if s == nil {
		return nil
	}
	s.npcMu.RLock()
	defer s.npcMu.RUnlock()
	out := make([]*NPC, 0, len(s.npcs))
	for _, npc := range s.npcs {
		if npc != nil && (npc.npcType == DBNPC || npc.npcType == LEVELNPC) && strings.TrimSpace(npc.script) != "" {
			out = append(out, npc)
		}
	}
	return out
}

func (s *Server) runServerSideGS2ForPlayer(scriptType, scriptName, eventName, script string, player *Player, eventArgs ...string) gs2VMResult {
	return s.runServerSideGS2Native(scriptType, scriptName, eventName, script, snapshotGS2Player(player), eventArgs...)
}

func (s *Server) runServerSideWeaponGS2ForPlayer(weapon *Weapon, eventName string, player *Player, eventArgs ...string) gs2VMResult {
	if weapon == nil {
		return gs2VMResult{}
	}
	result := s.runServerSideGS2NativeWithState("weapon", weapon.name, eventName, weapon.script, weapon.vmThis, snapshotGS2Player(player), eventArgs...)
	result.vmRevision = weapon.vmRevision
	return result
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
	for _, flag := range result.serverFlags {
		if flag.deleted {
			s.DeleteServerFlagLive(flag.name)
		} else {
			s.SetServerFlagLive(flag.name, flag.value)
		}
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
	for _, weapon := range result.playerWeapons {
		if player := s.findGS2Player(weapon.account); player != nil {
			if weapon.add {
				player.addWeapon(weapon.name)
				player.sendAccountWeapon(weapon.name)
			} else {
				player.deleteWeapon(weapon.name)
				player.sendPLO_NPCWEAPONDEL(weapon.name)
			}
		}
	}
	for _, warp := range result.playerWarps {
		if player := s.findGS2Player(warp.account); player != nil && warp.level != "" {
			player.warp(warp.level, warp.x, warp.y)
		}
	}
	for _, flag := range result.npcFlags {
		s.applyGS2NPCFlag(flag)
	}
	for _, call := range result.npcFunctionCalls {
		s.applyGS2NPCFunctionCall(result, call)
	}
	for _, action := range result.npcActions {
		s.applyGS2NPCAction(action)
	}
	if s.gs2Sockets != nil {
		s.gs2Sockets.Apply(result)
	}
	for _, event := range result.scheduledEvents {
		delay := time.Duration(event.delay * float64(time.Second))
		if delay < 0 {
			delay = 0
		}
		go func(event gs2VMScheduledEvent, state map[string]any) {
			time.Sleep(delay)
			if !s.gs2VMRevisionStillCurrent(result) {
				return
			}
			next := s.runServerSideGS2NativeWithStateAndSocket(result.scriptType, result.scriptName, event.event, result.script, state, result.playerContext, result.npcID, nil)
			next.vmRevision = result.vmRevision
			if next.err != "" {
				s.sendGS2VMErrorToNC(result.scriptType+" "+result.scriptName, next.err)
				return
			}
			s.applyGS2VMResult(next)
			s.emitGS2VMOutput(next)
		}(event, result.this)
	}
}

func (s *Server) gs2VMRevisionStillCurrent(result gs2VMResult) bool {
	if s == nil {
		return false
	}
	switch result.scriptType {
	case "weapon":
		weapon := s.GetWeapon(result.scriptName)
		return weapon != nil && weapon.vmRevision == result.vmRevision
	case "npc":
		npc := s.GetNPC(result.npcID)
		return npc != nil && npc.vmRevision == result.vmRevision
	default:
		return true
	}
}

func (s *Server) applyGS2NPCFlag(flag gs2VMNPCFlag) {
	npc := s.GetNPC(flag.id)
	if npc == nil || flag.name == "" {
		return
	}
	npc.mu.Lock()
	if npc.flagList == nil {
		npc.flagList = make(map[string]string)
	}
	npc.flagList[flag.name] = flag.value
	if npc.vmThis == nil {
		npc.vmThis = make(map[string]any)
	}
	npc.vmThis[flag.name] = flag.value
	npc.mu.Unlock()
	if npc.npcType == DBNPC {
		if err := s.saveDatabaseNPCFile(npc); err != nil {
			s.logger.Warning("Failed to save NPC %s flags: %v", npc.npcName, err)
		}
	}
}

func (s *Server) applyGS2NPCFunctionCall(source gs2VMResult, call gs2VMNPCFunctionCall) {
	npc := s.GetNPC(call.id)
	if npc == nil || call.function == "" {
		return
	}
	npc.mu.Lock()
	thisState := copyAnyMap(npc.vmThis)
	script := npc.script
	name := npc.npcName
	id := npc.id
	revision := npc.vmRevision
	npc.mu.Unlock()
	event := call.function
	next := s.runServerSideGS2NativeWithStateAndSocket("npc", name, event, script, thisState, source.playerContext, id, nil, call.args...)
	next.vmRevision = revision
	if next.err != "" {
		s.sendGS2VMErrorToNC("NPC "+name, next.err)
		return
	}
	s.applyGS2VMResult(next)
	s.emitGS2VMOutput(next)
	npc.mu.Lock()
	npc.vmThis = next.this
	npc.mu.Unlock()
}

func (s *Server) applyGS2NPCAction(action gs2VMNPCAction) {
	npc := s.GetNPC(action.id)
	if npc == nil {
		return
	}
	npc.mu.Lock()
	if action.shapeType > 0 {
		npc.blockFlags = byte(action.shapeType)
		npc.width = action.width
		npc.height = action.height
	}
	if action.chat != "" {
		npc.character.chatMessage = action.chat
	}
	npc.mu.Unlock()
	if strings.TrimSpace(action.warpLevel) != "" {
		level := s.loadLevel(cleanLevelName(action.warpLevel))
		s.warpDatabaseNPC(npc, level, int16(action.warpX), int16(action.warpY))
		return
	}
	s.sendNPCPropsToLevel(npc)
}

func (s *Server) sendNPCPropsToLevel(npc *NPC) {
	if npc == nil || npc.level == nil {
		return
	}
	for _, id := range npc.level.getPlayers() {
		if player, ok := s.players[id]; ok && player != nil && player.conn != nil {
			player.sendPLO_NPCPROPS(npc)
		}
	}
}

func (s *Server) emitGS2VMOutput(result gs2VMResult) {
	for _, line := range result.output {
		s.logger.Info("[GS2:%s] %s", result.scriptName, line)
		s.sendToNC(line)
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
