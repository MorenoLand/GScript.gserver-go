package main

import "strings"

type listserverEndpoint struct {
	host string
	port string
}

func (s *Server) configureServerLists() {
	enabled := s.settings.GetBool("listserver", true)
	endpoints := s.listserverEndpoints()
	if len(endpoints) == 0 {
		endpoints = []listserverEndpoint{{host: "listserver.graal.in", port: "14900"}}
	}
	s.serverLists = make([]*ServerList, 0, len(endpoints))
	for _, endpoint := range endpoints {
		serverList := NewServerListEndpoint(s, endpoint.host, endpoint.port)
		serverList.enabled = enabled
		s.serverLists = append(s.serverLists, serverList)
	}
	s.serverList = s.serverLists[0]
}

func (s *Server) listserverEndpoints() []listserverEndpoint {
	hosts := splitCommaList(s.settings.Get("listip"))
	ports := splitCommaList(s.settings.Get("listport"))
	if len(hosts) == 0 {
		return nil
	}
	if len(ports) == 0 {
		ports = []string{"14900"}
	}
	endpoints := make([]listserverEndpoint, 0, len(hosts))
	for i, host := range hosts {
		port := ports[0]
		if i < len(ports) {
			port = ports[i]
		}
		endpoints = append(endpoints, listserverEndpoint{host: host, port: port})
	}
	return endpoints
}

func (s *Server) sendPlayerTextToListservers(packetId byte, playerID uint16, text string) bool {
	if s == nil {
		return false
	}
	sent := false
	seen := make(map[*ServerList]bool)
	for _, serverList := range s.serverLists {
		if serverList == nil || seen[serverList] {
			continue
		}
		seen[serverList] = true
		if !serverList.connected {
			continue
		}
		serverList.SendPlayerTextPacket(packetId, playerID, text)
		sent = true
	}
	if s.serverList != nil && !seen[s.serverList] && s.serverList.connected {
		s.serverList.SendPlayerTextPacket(packetId, playerID, text)
		sent = true
	}
	return sent
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
