package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type gs2VMResult struct {
	output         []string
	clientTriggers []string
	err            string
}

func (s *Server) runServerSideGS2(scriptType, scriptName, eventName, script string, eventArgs ...string) gs2VMResult {
	src := serversideGS2(script)
	if strings.TrimSpace(src) == "" {
		return gs2VMResult{}
	}
	hostPath := strings.TrimSpace(s.settings.Get("gs2vmhost"))
	if hostPath == "" {
		return gs2VMResult{err: "gs2vmhost is not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := splitCompilerArgs(s.settings.Get("gs2vmhostargs"))
	args = append(args, scriptType, scriptName, eventName)
	args = append(args, eventArgs...)
	cmd := exec.CommandContext(ctx, hostPath, args...)
	cmd.Stdin = strings.NewReader(src)
	cmd.Env = append(os.Environ(), "GS2_SCRIPT_TYPE="+scriptType, "GS2_SCRIPT_NAME="+scriptName, "GS2_SCRIPT_EVENT="+eventName)
	cmd.Env = append(cmd.Env, "GS2_SERVER_FLAGS="+encodeGS2VMMap(s.snapshotServerFlags()), "GS2_SERVER_OPTIONS="+encodeGS2VMMap(s.snapshotServerOptions()))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return gs2VMResult{err: "gs2vmhost timed out"}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return gs2VMResult{err: msg}
	}
	lines := make([]string, 0)
	clientTriggers := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if payload, ok := strings.CutPrefix(line, "ECHO\t"); ok {
			lines = append(lines, payload)
		} else if payload, ok := strings.CutPrefix(line, "TRIGGERCLIENT\t"); ok {
			parts := strings.Split(payload, "\t")
			if len(parts) >= 2 && (parts[0] == "gui" || parts[0] == "weapon") {
				clientTriggers = append(clientTriggers, "clientside,"+strings.Join(parts, ","))
			}
		} else if payload, ok := strings.CutPrefix(line, "ERROR\t"); ok {
			return gs2VMResult{output: lines, err: payload}
		} else {
			lines = append(lines, line)
		}
	}
	return gs2VMResult{output: lines, clientTriggers: clientTriggers}
}

func encodeGS2VMMap(values map[string]string) string {
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
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
	result := s.runServerSideGS2("weapon", weapon.name, eventName, weapon.script, eventArgs...)
	if result.err != "" {
		s.sendToNC(fmt.Sprintf("GS2 VM error for Weapon %s: %s", weapon.name, result.err))
		return
	}
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
