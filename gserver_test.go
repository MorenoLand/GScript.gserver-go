package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestSendPacketGen5LengthExcludesLengthPrefix(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	done := make(chan error, 1)
	go func() {
		p.sendPacket([]byte{PLO_SIGNATURE, 73})
		done <- nil
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	frame := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, frame); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send packet: %v", err)
	}

	wireLen := int(frame[0])<<8 | int(frame[1])
	if wireLen != len(frame)-2 {
		t.Fatalf("GEN_5 length prefix = %d, want %d", wireLen, len(frame)-2)
	}
}

func TestSendPacketGen5EncodesOutgoingPacketID(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	done := make(chan error, 1)
	go func() {
		p.sendPacket([]byte{PLO_SIGNATURE, 73})
		done <- nil
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	frame := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, frame); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send packet: %v", err)
	}

	payload := append([]byte(nil), frame[3:]...)
	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(frame[2])
	in.Decrypt(payload)

	if payload[0] != PLO_SIGNATURE+32 {
		t.Fatalf("plaintext packet ID = 0x%02X, want encoded 0x%02X", payload[0], PLO_SIGNATURE+32)
	}
}

func TestSendCompressFlushesQueuedPacketsAsOneGen5Frame(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	p.sendPacket([]byte{PLO_SIGNATURE, 73, '\n'})
	p.sendPacket([]byte{PLO_CLEARWEAPONS, '\n'})

	done := make(chan struct{}, 1)
	go func() {
		p.sendCompress(true)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	header := make([]byte, 3)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[0] != 0 || header[1] != 6 {
		t.Fatalf("GEN_5 frame length header = %02X %02X, want big-endian 00 06", header[0], header[1])
	}
	frameLen := int(header[0])<<8 | int(header[1])
	if frameLen != 1+len([]byte{PLO_SIGNATURE + 32, 73, '\n', PLO_CLEARWEAPONS + 32, '\n'}) {
		t.Fatalf("GEN_5 frame length = %d, want one uncompressed queued frame", frameLen)
	}
	encrypted := make([]byte, frameLen-1)
	if _, err := io.ReadFull(clientConn, encrypted); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	<-done

	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(header[2])
	in.Decrypt(encrypted)

	want := []byte{PLO_SIGNATURE + 32, 73, '\n', PLO_CLEARWEAPONS + 32, '\n'}
	if string(encrypted) != string(want) {
		t.Fatalf("queued plaintext = % X, want % X", encrypted, want)
	}
}

func TestPostLoginTailSendsListProcessesAfterStatusAndPlayerExchange(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	server.settings.Set("staffguilds", "Staff")
	server.settings.Set("playerlisticons", "Online,Away")
	p := &Player{
		id:         1,
		conn:       serverConn,
		server:     server,
		playerType: PLTYPE_CLIENT3,
		versionId:  222,
		encryption: *NewEncryption(),
	}
	p.accountName = "moondeath"
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPostLoginTail()
		done <- struct{}{}
	}()

	want := []byte{PLO_STAFFGUILDS + 32}
	want = append(want, []byte("\"Staff\"")...)
	want = append(want, PLO_STATUSLIST+32)
	want = append(want, []byte("Online,Away")...)
	want = append(want, PLO_LISTPROCESSES+32, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read post-login tail: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("post-login tail = % X, want % X", got, want)
	}
	if bytes.IndexByte(got, PLO_LISTPROCESSES+32) < bytes.IndexByte(got, PLO_STATUSLIST+32) {
		t.Fatalf("PLO_LISTPROCESSES was sent before PLO_STATUSLIST: % X", got)
	}
}

func TestPostLoginTailSkipsListProcessesForG3DClient(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings(), players: make(map[uint16]*Player)},
		playerType:    PLTYPE_CLIENT3,
		versionId:     300,
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	p.sendPostLoginTail()

	if bytes.Contains(p.outQueue, []byte{PLO_LISTPROCESSES + 32}) {
		t.Fatalf("G3D client received legacy PLO_LISTPROCESSES: % X", p.outQueue)
	}
}

func TestPostLoginTailExchangesAddPlayerAndOtherProps(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	server := &Server{
		logger:   NewLogger("", false),
		settings: NewSettings(),
		players:  make(map[uint16]*Player),
	}
	p := &Player{
		id:            1,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	p.accountName = "moondeath"
	p.character.nickName = "moondeath"
	p.communityName = "moondeath"
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:            2,
		conn:          serverConn,
		server:        server,
		playerType:    PLTYPE_CLIENT3,
		versionId:     222,
		queueOutgoing: true,
	}
	other.accountName = "Z"
	other.character.nickName = "Z"
	other.communityName = "Z"
	other.levelName = "onlinestartlocal.nw"
	server.players[p.id] = p
	server.players[other.id] = other

	p.sendPostLoginTail()

	pAdd := append([]byte{PLO_ADDPLAYER + 32}, NewBuffer().WriteGShort(other.id).Bytes()...)
	pProps := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(other.id).Bytes()...)
	otherAdd := append([]byte{PLO_ADDPLAYER + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)
	otherProps := append([]byte{PLO_OTHERPLPROPS + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)

	if !bytes.Contains(p.outQueue, pAdd) {
		t.Fatalf("new player did not receive existing player's PLO_ADDPLAYER: % X", p.outQueue)
	}
	if !bytes.Contains(p.outQueue, pProps) {
		t.Fatalf("new player did not receive existing player's PLO_OTHERPLPROPS: % X", p.outQueue)
	}
	if !containsTerminatedPacket(p.outQueue, pProps) {
		t.Fatalf("existing player's PLO_OTHERPLPROPS was not newline-terminated: % X", p.outQueue)
	}
	if !bytes.Contains(other.outQueue, otherAdd) {
		t.Fatalf("existing player did not receive new player's PLO_ADDPLAYER: % X", other.outQueue)
	}
	if !bytes.Contains(other.outQueue, otherProps) {
		t.Fatalf("existing player did not receive new player's PLO_OTHERPLPROPS: % X", other.outQueue)
	}
	if !containsTerminatedPacket(other.outQueue, otherProps) {
		t.Fatalf("new player's PLO_OTHERPLPROPS was not newline-terminated: % X", other.outQueue)
	}
}

func containsTerminatedPacket(stream, prefix []byte) bool {
	idx := bytes.Index(stream, prefix)
	if idx < 0 {
		return false
	}
	end := bytes.IndexByte(stream[idx:], '\n')
	return end >= 0
}

func TestDeletePlayerBroadcastsDelPlayer(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 1, server: server, playerType: PLTYPE_CLIENT3}
	other := &Player{id: 2, server: server, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	other.conn = nil
	server.players[p.id] = p
	server.players[other.id] = other

	// A queued player may not have a live socket in tests; give it a dummy
	// non-nil connection so DeletePlayer treats it like an active client.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	other.conn = serverConn

	server.DeletePlayer(p)

	want := append([]byte{PLO_DELPLAYER + 32}, NewBuffer().WriteGShort(p.id).Bytes()...)
	want = append(want, '\n')
	if !bytes.Equal(other.outQueue, want) {
		t.Fatalf("delete player broadcast = % X, want % X", other.outQueue, want)
	}
}

func TestPlayerWriteFailureDisconnectsAndBroadcastsDelPlayer(t *testing.T) {
	deadServer, deadClient := net.Pipe()
	deadClient.Close()
	defer deadServer.Close()

	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	level := NewLevel()
	dead := &Player{
		conn:       deadServer,
		server:     server,
		id:         2,
		playerType: PLTYPE_ANYCLIENT,
		encryption: *NewEncryption(),
	}
	dead.levelName = "onlinestartlocal.nw"
	dead.encryption.SetGen(ENCRYPT_GEN_1)
	dead.currentLevel = level
	observer := &Player{
		server:        server,
		id:            3,
		playerType:    PLTYPE_ANYCLIENT,
		queueOutgoing: true,
		encryption:    *NewEncryption(),
	}
	observerServer, observerClient := net.Pipe()
	defer observerServer.Close()
	defer observerClient.Close()
	observer.conn = observerServer
	observer.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[dead.id] = dead
	server.players[observer.id] = observer
	level.addPlayer(dead)

	dead.sendPacket([]byte{PLO_SIGNATURE, 73})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, exists := server.players[dead.id]; !exists {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, exists := server.players[dead.id]; exists {
		t.Fatalf("dead player was not removed after write failure")
	}
	if got := level.getPlayers(); len(got) != 0 {
		t.Fatalf("dead player still in level players: %v", got)
	}
	want := append([]byte{PLO_DELPLAYER + 32}, NewBuffer().WriteGShort(dead.id).Bytes()...)
	want = append(want, '\n')
	if !bytes.Equal(observer.outQueue, want) {
		t.Fatalf("observer delplayer = % X, want % X", observer.outQueue, want)
	}
}

func TestOnRecvReturnsFalseAfterDisconnectNilsConnection(t *testing.T) {
	p := &Player{
		server:       &Server{logger: NewLogger("", false)},
		id:           2,
		playerType:   PLTYPE_ANYCLIENT,
		disconnected: true,
	}

	if p.OnRecv() {
		t.Fatalf("OnRecv returned true for disconnected player with nil conn")
	}
}

func TestLoadAllowedVersionsStripsCommentsAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"\\config", 0755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	data := "" +
		"// List of version strings.\r\n" +
		" GNW13110\t// 1.41r1\r\n" +
		"GNW22122:GNW28015 // range\r\n" +
		"\r\n" +
		"G3D0311C  // 6.0.3.7\r\n"
	if err := os.WriteFile(dir+"\\config\\allowedversions.txt", []byte(data), 0644); err != nil {
		t.Fatalf("write allowedversions: %v", err)
	}
	server := &Server{logger: NewLogger("", false), config: NewFileSystem(dir)}

	server.loadAllowedVersions()

	want := "GNW13110,GNW22122:GNW28015,G3D0311C"
	if got := server.allowedVersionsListserverText(); got != want {
		t.Fatalf("allowedVersionsListserverText = %q, want %q", got, want)
	}
}

func TestDefaultAllowedVersionsIncludesKnownClientSet(t *testing.T) {
	server := &Server{logger: NewLogger("", false), config: NewFileSystem("servers/default")}

	server.loadAllowedVersions()

	got := map[string]int{}
	for _, version := range server.allowedVersions {
		got[version]++
	}
	want := []string{
		"GNW13110",
		"GNW31101",
		"GNW01012",
		"GNW23012",
		"GNW30042",
		"GNW19052",
		"GNW20052",
		"GNW12102",
		"GNW22122",
		"GNW21033",
		"GNW15053",
		"GNW28063",
		"GNW01113",
		"GNW03014",
		"GNW14015",
		"GNW28015",
		"G3D16053",
		"G3D27063",
		"G3D03014",
		"G3D28095",
		"G3D09125",
		"G3D17026",
		"G3D26076",
		"G3D20126",
		"G3D22067",
		"G3D14097",
		"G3D26090",
		"G3D3007A",
		"G3D2505C",
		"G3D0311C",
		"G3D0511C",
		"G3D04048",
		"G3D18010",
		"G3D29090",
		"G3D2504D",
	}
	for _, version := range want {
		if got[version] == 0 {
			t.Fatalf("default allowedversions missing %s", version)
		}
	}
	if got["G3D3007A"] != 2 {
		t.Fatalf("default allowedversions has %d G3D3007A entries, want 2", got["G3D3007A"])
	}
	if len(server.allowedVersions) != 36 {
		t.Fatalf("default allowedversions count = %d, want 36", len(server.allowedVersions))
	}
}

func TestServerListSendsAllowedVersionsText(t *testing.T) {
	server := &Server{
		logger:          NewLogger("", false),
		allowedVersions: []string{"GNW13110", "GNW22122:GNW28015", "G3D0311C"},
	}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_1,
	}

	sl.sendVersionConfig()

	got := <-sl.sendQueue
	wantText := "Listserver,settings,allowedversions,GNW13110,GNW22122:GNW28015,G3D0311C"
	want := append([]byte{SVO_SENDTEXT + 32}, []byte(wantText)...)
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("allowed versions packet = % X, want % X", got, want)
	}
}

func TestServerListSendPacketUsesActiveCodec(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_2,
	}

	buf := NewBuffer()
	buf.WriteGChar(SVO_PING)
	sl.SendPacket(buf.Bytes())

	got := <-sl.sendQueue
	if len(got) < 3 {
		t.Fatalf("encoded packet too short: % X", got)
	}
	frameLen := int(got[0])<<8 | int(got[1])
	if frameLen != len(got)-2 {
		t.Fatalf("listserver frame len = %d, want %d", frameLen, len(got)-2)
	}
	plain, err := ZlibDecompress(got[2:])
	if err != nil {
		t.Fatalf("decompress ping packet: %v", err)
	}
	want := []byte{SVO_PING + 32, '\n'}
	if !bytes.Equal(plain, want) {
		t.Fatalf("decoded ping packet = % X, want % X", plain, want)
	}
}

func TestServerListSendTextPacketEncodesIDAndUsesActiveCodec(t *testing.T) {
	server := &Server{logger: NewLogger("", false)}
	sl := &ServerList{
		server:    server,
		connected: true,
		sendQueue: make(chan []byte, 1),
		codec:     ENCRYPT_GEN_2,
	}

	sl.SendTextPacket(SVO_REQUESTLIST, "lister\x01pmservers\x01all")

	got := <-sl.sendQueue
	plain, err := ZlibDecompress(got[2:])
	if err != nil {
		t.Fatalf("decompress request list packet: %v", err)
	}
	want := append([]byte{SVO_REQUESTLIST + 32}, []byte("lister\x01pmservers\x01all")...)
	want = append(want, '\n')
	if !bytes.Equal(plain, want) {
		t.Fatalf("decoded request list packet = % X, want % X", plain, want)
	}
}

func TestServerListTimedEventsSendsSetIPKeepalive(t *testing.T) {
	settings := NewSettings()
	settings.Set("serverip", "AUTO")
	server := &Server{logger: NewLogger("", false), settings: settings}
	sl := &ServerList{
		server:        server,
		connected:     true,
		sendQueue:     make(chan []byte, 1),
		codec:         ENCRYPT_GEN_1,
		lastKeepalive: time.Now().Add(-time.Minute),
	}

	sl.doTimedEvents()

	got := <-sl.sendQueue
	want := NewBuffer().WriteGChar(SVO_SETIP).WriteString8Encoded("AUTO").Bytes()
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("keepalive packet = % X, want % X", got, want)
	}
}

func TestOneSecondEventsDisconnectsStalePlayersWithoutDeadlock(t *testing.T) {
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	level := NewLevel()
	p := &Player{
		id:           2,
		server:       server,
		playerType:   PLTYPE_CLIENT3,
		currentLevel: level,
		lastData:     time.Now().Add(-6 * time.Minute),
	}
	level.addPlayer(p)
	server.players[p.id] = p

	done := make(chan struct{})
	go func() {
		server.oneSecondEvents()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("oneSecondEvents deadlocked while disconnecting stale player")
	}

	if got := server.GetPlayer(p.id); got != nil {
		t.Fatalf("stale player still registered after timeout: %#v", got)
	}
	for _, pid := range level.getPlayers() {
		if pid == p.id {
			t.Fatalf("stale player id %d still present in level players", p.id)
		}
	}
}

func TestOtherPropsPacketUsesOtherPlayerPropsHeader(t *testing.T) {
	p := &Player{id: 0x1234}
	props := []byte{byte(PLPROP_NICKNAME + 32), byte(len("moondeath") + 32)}
	props = append(props, []byte("moondeath")...)

	got := p.otherPropsPacket(props)

	want := []byte{PLO_OTHERPLPROPS, 0x44, 0x54}
	want = append(want, props...)
	if !bytes.Equal(got, want) {
		t.Fatalf("other props packet = % X, want % X", got, want)
	}
}

func TestSendPacketGen5UsesBz2ForLargeFrames(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:          serverConn,
		server:        &Server{logger: NewLogger("", false)},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.SetGen(ENCRYPT_GEN_5)
	p.outEncryption.Reset(0)

	packet := append([]byte{PLO_RAWDATA}, make([]byte, 0x2001)...)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPacket(packet)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	header := make([]byte, 3)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[2] != COMPRESS_BZ2 {
		t.Fatalf("compression type = 0x%02X, want BZ2 0x%02X", header[2], COMPRESS_BZ2)
	}
	encrypted := make([]byte, (int(header[0])<<8|int(header[1]))-1)
	if _, err := io.ReadFull(clientConn, encrypted); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	<-done

	in := *NewEncryption()
	in.SetGen(ENCRYPT_GEN_5)
	in.Reset(0)
	in.LimitFromType(header[2])
	in.Decrypt(encrypted)
	decompressed, err := Bz2Decompress(encrypted)
	if err != nil {
		t.Fatalf("decompress bz2 payload: %v", err)
	}
	if decompressed[0] != PLO_RAWDATA+32 || len(decompressed) != len(packet) {
		t.Fatalf("decompressed large frame len/id = %d/0x%02X, want %d/0x%02X", len(decompressed), decompressed[0], len(packet), PLO_RAWDATA+32)
	}
}

func TestHandlePacketDecodesGraalEncodedPacketID(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	if !p.handlePacket([]byte{byte(PLI_LANGUAGE + 32), 'E', 'n', 'g', 'l', 'i', 's', 'h'}) {
		t.Fatal("handlePacket returned false")
	}
	if p.language != "English" {
		t.Fatalf("language = %q, want English", p.language)
	}
}

func TestHandleDecompressedPacketsSplitsNewlineDelimitedClientPackets(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	p.handleDecompressedPackets([]byte{
		byte(PLI_LANGUAGE + 32), 'E', 'n', 'g', 'l', 'i', 's', 'h', '\n',
		byte(PLI_LANGUAGE + 32), 'F', 'r', 'e', 'n', 'c', 'h', '\n',
	})

	if p.language != "French" {
		t.Fatalf("language = %q, want French", p.language)
	}
}

func TestSendPropsWithArrayEncodesPropertyIDs(t *testing.T) {
	p := &Player{}
	p.character.nickName = "moondeath"

	var props [PROPCOUNT]bool
	props[PLPROP_NICKNAME] = true

	got := p.sendPropsWithArray(props)
	want := []byte{byte(PLPROP_NICKNAME + 32), byte(len("moondeath") + 32)}
	want = append(want, []byte("moondeath")...)

	if string(got) != string(want) {
		t.Fatalf("props bytes = % X, want % X", got, want)
	}
}

func TestLoginPropsMatchReferenceByOmittingNickname(t *testing.T) {
	if sendLoginProps[PLPROP_NICKNAME] {
		t.Fatalf("sendLoginProps includes PLPROP_NICKNAME, but C++ __sendLogin omits it")
	}
	if !sendLoginProps[PLPROP_MAXPOWER] || !sendLoginProps[PLPROP_CURPOWER] {
		t.Fatalf("sendLoginProps lost core health props")
	}
}

func TestLoadAccountParsesDecimalAndRepairsInvalidHealth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"\\accounts", 0755); err != nil {
		t.Fatalf("create accounts dir: %v", err)
	}
	account := "GRACC001\r\n" +
		"NAME moondeath\r\n" +
		"NICK moondeath\r\n" +
		"LEVEL onlinestartlocal.nw\r\n" +
		"X 32.00\r\n" +
		"Y 32.00\r\n" +
		"MAXHP 3.00\r\n" +
		"HP 3.00\r\n"
	if err := os.WriteFile(dir+"\\accounts\\moondeath.txt", []byte(account), 0644); err != nil {
		t.Fatalf("write account: %v", err)
	}
	server := &Server{logger: NewLogger("", false), config: NewFileSystem(dir)}
	p := &Player{server: server}
	p.setServer(server)

	if !p.LoadAccount("moondeath", false) {
		t.Fatalf("LoadAccount returned false")
	}
	if p.maxHitpoints != 3 || p.character.hitpoints != 3 {
		t.Fatalf("health = max %d current %d, want 3/3", p.maxHitpoints, p.character.hitpoints)
	}

	broken := "GRACC001\r\n" +
		"NAME Z\r\n" +
		"NICK Z\r\n" +
		"LEVEL onlinestartlocal.nw\r\n" +
		"MAXHP 0\r\n" +
		"HP 0\r\n"
	if err := os.WriteFile(dir+"\\accounts\\Z.txt", []byte(broken), 0644); err != nil {
		t.Fatalf("write broken account: %v", err)
	}
	q := &Player{server: server}
	q.setServer(server)
	if !q.LoadAccount("Z", false) {
		t.Fatalf("LoadAccount broken returned false")
	}
	if q.maxHitpoints != 3 || q.character.hitpoints != 3 {
		t.Fatalf("repaired health = max %d current %d, want 3/3", q.maxHitpoints, q.character.hitpoints)
	}
}

func TestLevelBoardPacketUsesEncodedPacketID(t *testing.T) {
	level := NewLevel()
	level.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}
	level.tiles[0].tiles[0] = 0x1234
	got := level.getBoardPacket()

	if len(got) != 8194 {
		t.Fatalf("board packet length = %d, want 8194", len(got))
	}
	if got[0] != PLO_BOARDPACKET+32 {
		t.Fatalf("board packet ID = 0x%02X, want encoded 0x%02X", got[0], PLO_BOARDPACKET+32)
	}
	if got[1] != 0x34 || got[2] != 0x12 {
		t.Fatalf("first board tile bytes = %02X %02X, want little-endian 34 12", got[1], got[2])
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("board packet missing newline terminator")
	}
}

func TestPlayerWarpUsesClientWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), settings: NewSettings()},
		encryption: *NewEncryption(),
	}
	p.server.settings.Set("bigmap", "worldmap.txt, worldmap.png, 30, 40")
	p.server.settings.Set("minimap", "mini.txt, mini.png, 5, 6")
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.setX(32)
		p.setY(32)
		p.sendPLO_PLAYERWARP(p.x, p.y, p.z, "onlinestartlocal.nw")
		done <- struct{}{}
	}()

	want := append([]byte{PLO_PLAYERWARP + 32, 64 + 32, 64 + 32}, []byte("onlinestartlocal.nw\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read warp packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("warp packet = % X, want % X", got, want)
	}
}

func TestLevelWarpParsesRestOfPacketAsLevelName(t *testing.T) {
	level := NewLevel()
	level.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}
	server := &Server{
		logger:   NewLogger("", false),
		config:   NewFileSystem("."),
		settings: NewSettings(),
		levels:   map[string]*Level{"inside house": level},
	}
	p := &Player{
		id:            2,
		server:        server,
		encryption:    *NewEncryption(),
		queueOutgoing: true,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_LEVELWARP)
	packet.WriteGChar(64)
	packet.WriteGChar(65)
	packet.Write([]byte("inside house.nw"))

	want := append([]byte{PLO_PLAYERWARP + 32, 64 + 32, 65 + 32}, []byte("inside house.nw\n")...)
	if !p.msgPLI_LEVELWARP(packet.Bytes()) {
		t.Fatalf("msgPLI_LEVELWARP returned false")
	}

	if !bytes.HasPrefix(p.outQueue, want) {
		t.Fatalf("level warp response prefix = % X, want % X", p.outQueue[:minInt(len(p.outQueue), len(want))], want)
	}
	levelNamePacket := append([]byte{PLO_LEVELNAME + 32}, []byte("inside house.nw\n")...)
	if !bytes.Contains(p.outQueue, levelNamePacket) {
		t.Fatalf("level warp did not send level data after warp: % X", p.outQueue[:minInt(len(p.outQueue), 64)])
	}
	if !bytes.Contains(p.outQueue, []byte{PLO_RAWDATA + 32}) {
		t.Fatalf("level warp did not send raw board data")
	}
	if p.levelName != "inside house.nw" {
		t.Fatalf("levelName = %q, want inside house.nw", p.levelName)
	}
	if !p.loaded {
		t.Fatalf("player was not marked loaded after level warp")
	}
}

func TestBoardModifyUsesGCharRegionHeader(t *testing.T) {
	level := NewLevel()
	p := &Player{
		server: &Server{logger: NewLogger("", false), settings: NewSettings(), levels: map[string]*Level{"onlinestartlocal.nw": level}},
	}
	p.levelName = "onlinestartlocal.nw"

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2)
	packet.WriteGChar(3)
	packet.WriteGChar(1)
	packet.WriteGChar(1)
	packet.WriteShort(0x1234)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}

	if len(level.boardChanges) != 1 {
		t.Fatalf("boardChanges = %d, want 1", len(level.boardChanges))
	}
	change := level.boardChanges[0]
	if change.x != 2 || change.y != 3 || change.width != 1 || change.height != 1 {
		t.Fatalf("change region = %d,%d %dx%d, want 2,3 1x1", change.x, change.y, change.width, change.height)
	}
	if len(change.newTiles) != 2 || change.newTiles[0] != 0x12 || change.newTiles[1] != 0x34 {
		t.Fatalf("change tiles = % X, want 12 34", change.newTiles)
	}
}

func TestBoardModifyBroadcastsAndDropsBushItem(t *testing.T) {
	settings := NewSettings()
	settings.Set("bushitems", "true")
	settings.Set("tiledroprate", "100")
	settings.Set("respawntime", "15")
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal.nw": level},
	}
	p := &Player{id: 1, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, versionId: 222}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{id: 2, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	other.conn, _ = net.Pipe()
	defer other.conn.Close()
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2).WriteGChar(3).WriteGChar(1).WriteGChar(1)
	packet.WriteShort(0)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}
	if level.getTileAt(2, 3) != 0 {
		t.Fatalf("tile did not change to 0")
	}
	boardModify := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 0, 0, '\n'}
	if !bytes.Contains(other.outQueue, boardModify) {
		t.Fatalf("observer did not receive board modify % X in % X", boardModify, other.outQueue)
	}
	if !bytes.Contains(other.outQueue, []byte{PLO_ITEMADD + 32}) {
		t.Fatalf("observer did not receive item drop: % X", other.outQueue)
	}
	if len(level.items) != 1 {
		t.Fatalf("level items = %d, want 1", len(level.items))
	}
}

func TestBoardModifyUsesCurrentLevelWhenNameLookupMisses(t *testing.T) {
	settings := NewSettings()
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{},
	}
	p := &Player{id: 1, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, versionId: 222}
	p.levelName = "onlinestartlocal.nw"
	otherConn, otherPeer := net.Pipe()
	defer otherConn.Close()
	defer otherPeer.Close()
	other := &Player{id: 2, conn: otherConn, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_BOARDMODIFY)
	packet.WriteGChar(2).WriteGChar(3).WriteGChar(1).WriteGChar(1)
	packet.WriteShort(0)

	if !p.msgPLI_BOARDMODIFY(packet.Bytes()) {
		t.Fatalf("msgPLI_BOARDMODIFY returned false")
	}
	if level.getTileAt(2, 3) != 0 {
		t.Fatalf("tile did not change through currentLevel fallback")
	}
	boardModify := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 0, 0, '\n'}
	if !bytes.Contains(other.outQueue, boardModify) {
		t.Fatalf("observer did not receive board modify via currentLevel % X in % X", boardModify, other.outQueue)
	}
}

func TestBoardModifyRespawnsOldTile(t *testing.T) {
	settings := NewSettings()
	settings.Set("respawntime", "0")
	level := NewLevel()
	level.setTileAt(2, 3, 2)
	server := &Server{
		logger:   NewLogger("", false),
		settings: settings,
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal.nw": level},
	}
	observer := &Player{id: 2, server: server, currentLevel: level, playerType: PLTYPE_CLIENT3, queueOutgoing: true}
	observer.conn, _ = net.Pipe()
	defer observer.conn.Close()
	server.players[observer.id] = observer
	level.players = []uint16{observer.id}

	if !level.alterBoard(server, 2, 3, 1, 1, []int16{0}) {
		t.Fatalf("alterBoard returned false")
	}
	level.processBoardRespawns(server)

	if level.getTileAt(2, 3) != 2 {
		t.Fatalf("tile did not respawn to 2")
	}
	respawnPacket := []byte{PLO_BOARDMODIFY + 32, 2 + 32, 3 + 32, 1 + 32, 1 + 32, 0, 2, '\n'}
	if !bytes.Contains(observer.outQueue, respawnPacket) {
		t.Fatalf("observer did not receive respawn packet % X in % X", respawnPacket, observer.outQueue)
	}
}

func TestRequestUpdateBoardParsesLevelAndModTimeHeader(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	levelName := "onlinestartlocal.nw"
	changeTime := time.Unix(1712345680, 0)
	level := NewLevel()
	level.boardChanges = append(level.boardChanges, LevelBoardChange{
		x:        2,
		y:        3,
		width:    1,
		height:   1,
		newTiles: []byte{0x12, 0x34},
		time:     changeTime,
	})
	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), levels: map[string]*Level{levelName: level}},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_REQUESTUPDATEBOARD)
	packet.WriteGChar(byte(len(levelName))).Write([]byte(levelName))
	packet.WriteGInt5(uint64(changeTime.Unix() - 1))
	packet.WriteGShort(2)
	packet.WriteGShort(3)
	packet.WriteGShort(1)
	packet.WriteGShort(1)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTUPDATEBOARD(packet.Bytes())
		done <- struct{}{}
	}()

	want := NewBuffer()
	want.WriteByte(PLO_BOARDPACKET + 32)
	want.WriteShort(2).WriteShort(3).WriteShort(1).WriteShort(1)
	want.WriteShort(0x1234)
	want.WriteByte('\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, want.Len())
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read board update packet: %v", err)
	}
	<-done

	if string(got) != string(want.Bytes()) {
		t.Fatalf("board update packet = % X, want % X", got, want.Bytes())
	}
}

func TestAdjacentLevelSendsRequestedLevelThenRestoresCurrentLevel(t *testing.T) {
	current := NewLevel()
	current.levelName = "onlinestartlocal"
	adjacent := NewLevel()
	adjacent.levelName = "side"
	adjacent.modTime = time.Unix(1712345680, 0)
	adjacent.tiles[0] = &LevelTiles{width: 64, height: 64, tiles: make([]int16, 4096)}

	server := &Server{
		logger:     NewLogger("", false),
		levels:     map[string]*Level{"side": adjacent, "onlinestartlocal": current},
		settings:   NewSettings(),
		serverTime: 12345,
	}
	p := &Player{
		server:        server,
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
		currentLevel:  current,
	}
	p.levelName = "onlinestartlocal.nw"
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_ADJACENTLEVEL)
	packet.WriteGInt5(0)
	packet.Write([]byte("side.nw"))

	if !p.msgPLI_ADJACENTLEVEL(packet.Bytes()) {
		t.Fatalf("msgPLI_ADJACENTLEVEL returned false")
	}

	got := p.outQueue
	if !bytes.HasPrefix(got, append([]byte{PLO_LEVELNAME + 32}, []byte("side.nw\n")...)) {
		t.Fatalf("adjacent response starts with % X, want adjacent level name", got[:min(len(got), 16)])
	}
	if !bytes.Contains(got, []byte{PLO_RAWDATA + 32}) {
		t.Fatalf("adjacent response did not include requested board rawdata: % X", got[:min(len(got), 32)])
	}
	if !bytes.HasSuffix(got, append([]byte{PLO_LEVELNAME + 32}, []byte("onlinestartlocal.nw\n")...)) {
		t.Fatalf("adjacent response did not restore current level name, tail=% X", got[max(0, len(got)-32):])
	}
}

func TestHitObjectsForwardsSourcePlayerAndHitLocation(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
	}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := NewBuffer()
	packet.WriteByte(PLI_HITOBJECTS)
	packet.WriteGChar(6)
	packet.WriteGChar(8)
	packet.WriteGChar(10)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_HITOBJECTS(packet.Bytes())
		done <- struct{}{}
	}()

	want := []byte{PLO_HITOBJECTS + 32, 32, 33, 38, 40, 42, '\n'}
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read hitobjects packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("hitobjects packet = % X, want % X", got, want)
	}
}

func TestNpcPropsForwardRawPropertiesToOtherLevelPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	npc := NewNPC(LEVELNPC)
	npc.id = 123
	level := NewLevel()
	level.players = []uint16{1, 2}
	level.npcs[123] = npc
	server := &Server{
		logger:   NewLogger("", false),
		players:  make(map[uint16]*Player),
		levels:   map[string]*Level{"onlinestartlocal": level},
		settings: NewSettings(),
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
	}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	props := NewBuffer()
	props.WriteGChar(NPCPROP_X).WriteGChar(20)
	props.WriteGChar(NPCPROP_Y).WriteGChar(24)
	packet := NewBuffer()
	packet.WriteByte(PLI_NPCPROPS)
	packet.WriteGInt(123)
	packet.Write(props.Bytes())

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_NPCPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	want := NewBuffer()
	want.WriteByte(PLO_NPCPROPS + 32)
	want.WriteGInt(123)
	want.Write(props.Bytes())
	want.WriteByte('\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, want.Len())
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read npcprops packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("npcprops packet = % X, want % X", got, want.Bytes())
	}
}

func TestBombAddForwardsPlayerIdAndRawBombPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{id: 1, server: server, currentLevel: level}
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := []byte{PLI_BOMBADD, 64, 65, 3, 55}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_BOMBADD(packet)
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(1)
	want := append([]byte{PLO_BOMBADD + 32}, idBuf.Bytes()...)
	want = append(want, packet[1:]...)
	want = append(want, '\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read bomb add packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("bomb add packet = % X, want % X", got, want)
	}
}

func TestPositionPropsUsePixelBackedCoordinates(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false)}}
	p.setX(32)
	p.setY(32)

	x := p.getProp(PLPROP_X)
	y := p.getProp(PLPROP_Y)

	if len(x) != 1 || x[0] != 64+32 {
		t.Fatalf("PLPROP_X = % X, want encoded 64", x)
	}
	if len(y) != 1 || y[0] != 64+32 {
		t.Fatalf("PLPROP_Y = % X, want encoded 64", y)
	}
}

func TestPrecisePositionPropsUseSignedShiftedCoordinates(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false)}}
	p.x = 512
	p.y = -384
	p.z = 1600

	wantX := NewBuffer().WriteGShort(1024).Bytes()
	wantY := NewBuffer().WriteGShort(769).Bytes()
	wantZ := NewBuffer().WriteGShort(2720).Bytes()

	if got := p.getProp(PLPROP_X2); !bytes.Equal(got, wantX) {
		t.Fatalf("PLPROP_X2 = % X, want % X", got, wantX)
	}
	if got := p.getProp(PLPROP_Y2); !bytes.Equal(got, wantY) {
		t.Fatalf("PLPROP_Y2 = % X, want % X", got, wantY)
	}
	if got := p.getProp(PLPROP_Z2); !bytes.Equal(got, wantZ) {
		t.Fatalf("PLPROP_Z2 = % X, want clamped % X", got, wantZ)
	}
}

func TestPlayerPropsParsesSignedPreciseCoordinates(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_X2).WriteGShort(1024)
	packet.WriteGChar(PLPROP_Y2).WriteGShort(769)
	packet.WriteGChar(PLPROP_Z2).WriteGShort(33)

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}
	if p.x != 512 {
		t.Fatalf("x = %d, want 512", p.x)
	}
	if p.y != -384 {
		t.Fatalf("y = %d, want -384", p.y)
	}
	if p.z != -16 {
		t.Fatalf("z = %d, want -16", p.z)
	}
}

func TestPlayerPropsConsumesFullKnownPropertyStream(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_RUPEESCOUNT).WriteGInt(1234)
	packet.WriteGChar(PLPROP_APCOUNTER).WriteGShort(77)
	packet.WriteGChar(PLPROP_GATTRIB10).WriteGChar(3).Write([]byte("hat"))
	packet.WriteGChar(PLPROP_TEXTCODEPAGE).WriteGInt(1252)
	packet.WriteGChar(PLPROP_UNKNOWN81).WriteGChar(3)
	packet.WriteGChar(PLPROP_CURCHAT).WriteGChar(5).Write([]byte("hello"))
	packet.WriteGChar(PLPROP_PSTATUSMSG).WriteGChar(7)

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}
	if p.rupees != 1234 {
		t.Fatalf("rupees = %d, want 1234", p.rupees)
	}
	if p.apCounter != 77 {
		t.Fatalf("apCounter = %d, want 77", p.apCounter)
	}
	if p.gAttribs[9] != "hat" {
		t.Fatalf("gAttribs[9] = %q, want hat", p.gAttribs[9])
	}
	if p.envCodePage != 1252 {
		t.Fatalf("envCodePage = %d, want 1252", p.envCodePage)
	}
	if p.character.chatMessage != "hello" {
		t.Fatalf("chatMessage = %q, want hello", p.character.chatMessage)
	}
	if p.statusMsg != 7 {
		t.Fatalf("statusMsg = %d, want 7", p.statusMsg)
	}
	if got := p.getProp(PLPROP_PSTATUSMSG); !bytes.Equal(got, []byte{7 + 32}) {
		t.Fatalf("PSTATUSMSG prop = % X, want encoded status byte % X", got, []byte{7 + 32})
	}
}

func TestPlayerPropsParsesTypedClientPropertyStream(t *testing.T) {
	p := &Player{
		server: &Server{logger: NewLogger("", false)},
	}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_NICKNAME).WriteGChar(byte(len("moondeath"))).Write([]byte("moondeath"))
	packet.WriteGChar(PLPROP_X).WriteGChar(64)
	packet.WriteGChar(PLPROP_Y).WriteGChar(65)
	packet.WriteGChar(PLPROP_COLORS)
	for _, color := range []byte{1, 2, 3, 4, 5} {
		packet.WriteGChar(color)
	}
	packet.WriteGChar(PLPROP_SPRITE).WriteGChar(12)
	packet.WriteGChar(PLPROP_CURLEVEL).WriteGChar(byte(len("onlinestartlocal.nw"))).Write([]byte("onlinestartlocal.nw"))

	if !p.msgPLI_PLAYERPROPS(packet.Bytes()) {
		t.Fatalf("msgPLI_PLAYERPROPS returned false")
	}

	if p.character.nickName != "moondeath" {
		t.Fatalf("nickname = %q, want moondeath", p.character.nickName)
	}
	if p.x != 64*8 || p.y != 65*8 {
		t.Fatalf("position = %d,%d, want %d,%d", p.x, p.y, 64*8, 65*8)
	}
	if p.character.colors != [5]uint8{1, 2, 3, 4, 5} {
		t.Fatalf("colors = %v, want [1 2 3 4 5]", p.character.colors)
	}
	if p.character.sprite != 12 {
		t.Fatalf("sprite = %d, want 12", p.character.sprite)
	}
	if p.levelName != "onlinestartlocal.nw" {
		t.Fatalf("levelName = %q, want onlinestartlocal.nw", p.levelName)
	}
}

func TestPlayerPropsForwardsChangedPropsToOtherPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		loaded:       true,
	}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:           2,
		conn:         serverConn,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    222,
		encryption:   *NewEncryption(),
	}
	other.levelName = "onlinestartlocal.nw"
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_GANI).WriteGChar(4).Write([]byte("walk"))
	packet.WriteGChar(PLPROP_CURCHAT).WriteGChar(2).Write([]byte("hi"))
	packet.WriteGChar(PLPROP_PSTATUSMSG).WriteGChar(7)
	packet.WriteGChar(PLPROP_X).WriteGChar(64)
	packet.WriteGChar(PLPROP_Y).WriteGChar(65)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PLAYERPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	id := NewBuffer()
	id.WriteGShort(p.id)
	want := append([]byte{PLO_OTHERPLPROPS + 32}, id.Bytes()...)
	want = append(want, PLPROP_GANI+32, 4+32)
	want = append(want, []byte("walk")...)
	want = append(want, PLPROP_CURCHAT+32, 2+32)
	want = append(want, []byte("hi")...)
	want = append(want, PLPROP_PSTATUSMSG+32, 7+32)
	want = append(want, PLPROP_X+32, 64+32, PLPROP_Y+32, 65+32)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read other player prop delta: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("other player prop delta = % X, want % X", got, want)
	}
}

func TestPlayerPropsForwardPreciseMovementToModernPlayers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.levelName = "onlinestartlocal.nw"
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
		levels:  map[string]*Level{"onlinestartlocal": level},
	}
	p := &Player{
		id:           1,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    300,
		loaded:       true,
	}
	p.levelName = "onlinestartlocal.nw"
	other := &Player{
		id:           2,
		conn:         serverConn,
		server:       server,
		currentLevel: level,
		playerType:   PLTYPE_CLIENT3,
		versionId:    300,
		encryption:   *NewEncryption(),
	}
	other.levelName = "onlinestartlocal.nw"
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.players = []uint16{p.id, other.id}

	packet := NewBuffer()
	packet.WriteByte(PLI_PLAYERPROPS)
	packet.WriteGChar(PLPROP_GANI).WriteGChar(4).Write([]byte("walk"))
	packet.WriteGChar(PLPROP_X2).WriteGShort(64 * 8 * 2)
	packet.WriteGChar(PLPROP_Y2).WriteGShort(65 * 8 * 2)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PLAYERPROPS(packet.Bytes())
		done <- struct{}{}
	}()

	id := NewBuffer()
	id.WriteGShort(p.id)
	want := append([]byte{PLO_OTHERPLPROPS + 32}, id.Bytes()...)
	want = append(want, PLPROP_GANI+32, 4+32)
	want = append(want, []byte("walk")...)
	wantX2 := NewBuffer()
	wantX2.WriteGShort(64 * 8 * 2)
	wantY2 := NewBuffer()
	wantY2.WriteGShort(65 * 8 * 2)
	want = append(want, PLPROP_X2+32)
	want = append(want, wantX2.Bytes()...)
	want = append(want, PLPROP_Y2+32)
	want = append(want, wantY2.Bytes()...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read modern player prop delta: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("modern player prop delta = % X, want % X", got, want)
	}
}

func TestTriggerActionParsesNpcPositionAndRawAction(t *testing.T) {
	called := false
	server := &Server{logger: NewLogger("", false)}
	server.triggerCommands = map[string]func(*Player, []string) bool{
		"clientweapon": func(p *Player, args []string) bool {
			called = true
			command := args[0]
			if command != "clientweapon" {
				t.Fatalf("command = %q, want clientweapon", command)
			}
			if len(args) != 3 || args[1] != "-System" || args[2] != "loaded" {
				t.Fatalf("args = %#v, want clientweapon,-System,loaded", args)
			}
			return true
		},
	}
	p := &Player{server: server}

	packet := NewBuffer()
	packet.WriteByte(PLI_TRIGGERACTION)
	packet.WriteGInt(0)
	packet.WriteGChar(31)
	packet.WriteGChar(32)
	packet.Write([]byte("clientweapon,-System,loaded"))

	if !p.msgPLI_TRIGGERACTION(packet.Bytes()) {
		t.Fatalf("msgPLI_TRIGGERACTION returned false")
	}
	if !called {
		t.Fatalf("trigger handler was not called")
	}
}

func TestTriggerActionForwardsUnhandledActionToLevel(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	level.players = []uint16{1, 2}
	server := &Server{
		logger:          NewLogger("", false),
		players:         make(map[uint16]*Player),
		levels:          map[string]*Level{"onlinestartlocal": level},
		triggerCommands: make(map[string]func(*Player, []string) bool),
	}
	p := &Player{id: 1, server: server, currentLevel: level}
	p.levelName = "onlinestartlocal"
	other := &Player{
		id:         2,
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
	}
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[1] = p
	server.players[2] = other

	packet := NewBuffer()
	packet.WriteByte(PLI_TRIGGERACTION)
	packet.WriteGInt(0)
	packet.WriteGChar(31)
	packet.WriteGChar(32)
	packet.Write([]byte("npcaction,param"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_TRIGGERACTION(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(1)
	want := append([]byte{PLO_TRIGGERACTION + 32}, idBuf.Bytes()...)
	want = append(want, packet.Bytes()[1:]...)
	want = append(want, '\n')
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read triggeraction packet: %v", err)
	}
	<-done

	if !bytes.Equal(got, want) {
		t.Fatalf("triggeraction packet = % X, want % X", got, want)
	}
}

func TestLevelBoardChangesSendsEmptyMarker(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELBOARDCHANGES(NewLevel(), time.Time{})
		done <- struct{}{}
	}()

	want := []byte{PLO_LEVELBOARD + 32, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level board changes packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level board changes packet = % X, want % X", got, want)
	}
}

func TestLevelLinkUsesRawTextLineFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	link := &LevelLink{destLevel: "inside house.nw", x: 1, y: 2, width: 3, height: 4, destX: 30, destY: 31}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELLINK_FULL(link)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_LEVELLINK + 32}, []byte("inside house.nw 1 2 3 4 30 31\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level link packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level link packet = % X, want % X", got, want)
	}
}

func TestLevelSignUsesGCharCoordinatesAndEncodedText(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	sign := NewLevelSign(7, 8, "Hi", false)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_SIGN(sign)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_LEVELSIGN + 32}, sign.GetSignStr(p)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level sign packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level sign packet = % X, want % X", got, want)
	}
}

func TestFireSpyAndThrowCarriedForwardRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		id:         12,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	fireBody := []byte{0x21, 0x22, 0x23}
	throwBody := []byte{0x24, 0x25}

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_FIRESPY(append([]byte{PLI_FIRESPY}, fireBody...))
		p.msgPLI_THROWCARRIED(append([]byte{PLI_THROWCARRIED}, throwBody...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(12)
	want := append([]byte{PLO_FIRESPY + 32}, idBuf.Bytes()...)
	want = append(want, fireBody...)
	want = append(want, '\n', PLO_THROWCARRIED+32)
	want = append(want, idBuf.Bytes()...)
	want = append(want, throwBody...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read raw forward packets: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("raw forward packets = % X, want % X", got, want)
	}
}

func TestSetActiveLevelUsesRawLevelName(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_SETACTIVELEVEL("onlinestartlocal.nw")
		done <- struct{}{}
	}()

	want := append([]byte{PLO_SETACTIVELEVEL + 32}, []byte("onlinestartlocal.nw\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read set active level packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("set active level packet = % X, want % X", got, want)
	}
}

func TestLevelHorseAddUsesLevelWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	horse := LevelHorse{image: "horse.png", x: 12.5, y: 7, dir: 2, bushes: 1}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELHORSEADD(horse)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_HORSEADD + 32, 25, 14, 6}, []byte("horse.png\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level horse packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level horse packet = % X, want % X", got, want)
	}
}

func TestLevelBaddyPropsUseLevelWireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		versionId:  222,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	level := NewLevel()
	baddy := NewLevelBaddy(4, 5, 1, level, nil)
	baddy.id = 7
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELBADDYPROPS(baddy)
		done <- struct{}{}
	}()

	want := []byte{PLO_BADDYPROPS + 32, 7 + 32}
	want = append(want, baddy.getProps(222)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level baddy packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level baddy packet = % X, want % X", got, want)
	}
}

func TestLevelChestUsesGCharFields(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	chest := &LevelChest{x: 20, y: 24, itemType: ItemGreenRupee, signIndex: 0}
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELCHEST(chest, false)
		done <- struct{}{}
	}()

	want := []byte{PLO_LEVELCHEST + 32, 0 + 32, 20 + 32, 24 + 32, byte(ItemGreenRupee) + 32, 0 + 32, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level chest packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level chest packet = % X, want % X", got, want)
	}
}

func TestNpcPropsUseTypedPropertyStream(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	npc := NewNPC(PUTNPC)
	npc.id = 3
	npc.x = 16 * 16
	npc.y = 17 * 16
	npc.image = "block.png"
	npc.script = "message hi;"
	npc.npcName = "guide"
	npc.character.gani = "idle"

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NPCPROPS(npc)
		done <- struct{}{}
	}()

	npcID := NewBuffer()
	npcID.WriteGInt(3)
	scriptLen := NewBuffer()
	scriptLen.WriteGShort(uint16(len(npc.script)))
	want := append([]byte{PLO_NPCPROPS + 32}, npcID.Bytes()...)
	want = append(want, NPCPROP_X+32, byte(npc.x/8)+32, NPCPROP_Y+32, byte(npc.y/8)+32)
	want = append(want, NPCPROP_IMAGE+32, byte(len(npc.image))+32)
	want = append(want, []byte(npc.image)...)
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLen.Bytes()...)
	want = append(want, []byte(npc.script)...)
	want = append(want, NPCPROP_NICKNAME+32, byte(len(npc.npcName))+32)
	want = append(want, []byte(npc.npcName)...)
	want = append(want, NPCPROP_GANI+32, byte(len(npc.character.gani))+32)
	want = append(want, []byte(npc.character.gani)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read npc props packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("npc props packet = % X, want % X", got, want)
	}
}

func TestShowImgForwardsRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	level := NewLevel()
	server := &Server{
		logger:  NewLogger("", false),
		players: make(map[uint16]*Player),
	}
	p := &Player{
		server:       server,
		currentLevel: level,
		encryption:   *NewEncryption(),
		id:           7,
	}
	other := &Player{
		conn:       serverConn,
		server:     server,
		encryption: *NewEncryption(),
		id:         8,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)
	other.encryption.SetGen(ENCRYPT_GEN_1)
	server.players[p.id] = p
	server.players[other.id] = other
	level.addPlayer(p)
	level.addPlayer(other)

	body := []byte{0x21, 0x22, 'i', 'm', 'g'}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOWIMG(append([]byte{PLI_SHOWIMG}, body...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(7)
	want := append([]byte{PLO_SHOWIMG + 32}, idBuf.Bytes()...)
	want = append(want, body...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read showimg packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("showimg packet = % X, want % X", got, want)
	}
}

func TestToAllUsesPlayerIdAndGCharLengthMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), players: map[uint16]*Player{}},
		encryption: *NewEncryption(),
		id:         5,
	}
	p.server.players[5] = p
	p.encryption.SetGen(ENCRYPT_GEN_1)
	p.levelName = "onlinestartlocal.nw"

	packet := NewBuffer()
	packet.WriteByte(PLI_TOALL)
	packet.WriteGChar(byte(len("hello")))
	packet.Write([]byte("hello"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_TOALL(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(5)
	want := append([]byte{PLO_TOALL + 32}, idBuf.Bytes()...)
	want = append(want, byte(len("hello"))+32)
	want = append(want, []byte("hello\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read toall packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("toall packet = % X, want % X", got, want)
	}
}

func TestPrivateMessageUsesTargetIdsAndRawMessage(t *testing.T) {
	senderConn, targetConn := net.Pipe()
	defer senderConn.Close()
	defer targetConn.Close()

	p := &Player{
		conn:       senderConn,
		server:     &Server{logger: NewLogger("", false), players: map[uint16]*Player{}},
		encryption: *NewEncryption(),
		id:         5,
	}
	target := &Player{
		conn:       targetConn,
		server:     p.server,
		encryption: *NewEncryption(),
		id:         6,
	}
	p.server.players[5] = p
	p.server.players[6] = target
	target.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_PRIVATEMESSAGE)
	packet.WriteGShort(1)
	packet.WriteGShort(6)
	packet.Write([]byte("\"hi\""))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_PRIVATEMESSAGE(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(5)
	want := append([]byte{PLO_PRIVATEMESSAGE + 32}, idBuf.Bytes()...)
	want = append(want, []byte("\"\",\"Private message:\",\"hi\"\n")...)

	senderConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(senderConn, got); err != nil {
		t.Fatalf("read private message packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("private message packet = % X, want % X", got, want)
	}
}

func TestWeaponAddAndDeleteMutateWeaponList(t *testing.T) {
	p := &Player{server: &Server{logger: NewLogger("", false), npcs: map[uint32]*NPC{}}}

	addPacket := NewBuffer()
	addPacket.WriteByte(PLI_WEAPONADD)
	addPacket.WriteGChar(0)
	addPacket.WriteGChar(byte(ItemSpinattack))
	if !p.msgPLI_WEAPONADD(addPacket.Bytes()) {
		t.Fatalf("msgPLI_WEAPONADD returned false")
	}
	if len(p.weaponList) != 1 || p.weaponList[0] != getItemName(ItemSpinattack) {
		t.Fatalf("weaponList after add = %#v", p.weaponList)
	}

	if !p.msgPLI_NPCWEAPONDEL(append([]byte{PLI_NPCWEAPONDEL}, []byte(getItemName(ItemSpinattack))...)) {
		t.Fatalf("msgPLI_NPCWEAPONDEL returned false")
	}
	if len(p.weaponList) != 0 {
		t.Fatalf("weaponList after delete = %#v, want empty", p.weaponList)
	}
}

func TestExplosionAddsPlayerIdAndTypedPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		id:         9,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_EXPLOSION)
	packet.WriteGChar(3)
	packet.WriteGChar(40)
	packet.WriteGChar(41)
	packet.WriteGChar(2)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_EXPLOSION(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(9)
	want := append([]byte{PLO_EXPLOSION + 32}, idBuf.Bytes()...)
	want = append(want, 3+32, 40+32, 41+32, 2+32, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read explosion packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("explosion packet = % X, want % X", got, want)
	}
}

func TestClientVersionIDUsesSemanticVersion(t *testing.T) {
	if got := clientVersionID("GNW03014"); got != 222 {
		t.Fatalf("clientVersionID(GNW03014) = %d, want 222", got)
	}
	if got := clientVersionID("G3D03014"); got != 300 {
		t.Fatalf("clientVersionID(G3D03014) = %d, want 300", got)
	}
}

func TestNPCWeaponDelUsesRawNameWithoutNul(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NPCWEAPONDEL("Bomb")
		done <- struct{}{}
	}()

	want := []byte{PLO_NPCWEAPONDEL + 32, 'B', 'o', 'm', 'b', '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read weapon delete packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("weapon delete packet = % X, want % X", got, want)
	}
}

func TestZlibFixWeaponMatchesLegacyClientPacketShape(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_ZLIBFIXWEAPON()
		done <- struct{}{}
	}()

	want := []byte{PLO_NPCWEAPONADD + 32, 12 + 32}
	want = append(want, []byte("-gr_zlib_fix")...)
	want = append(want, NPCPROP_IMAGE+32, 1+32, '-')
	scriptLen := len(zlibFixScript)
	scriptLenBuf := NewBuffer()
	scriptLenBuf.WriteGShort(uint16(scriptLen))
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLenBuf.Bytes()...)
	want = append(want, []byte(zlibFixScript)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read zlib fix weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("zlib fix weapon packet = % X, want % X", got, want)
	}
}

func TestSendWeaponUsesNpcWeaponPropertyStream(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	weapon := &Weapon{name: "sword", image: "sword.png", script: "function onCreated() {}"}
	done := make(chan struct{}, 1)
	go func() {
		p.sendWeapon(weapon)
		done <- struct{}{}
	}()

	scriptLen := NewBuffer()
	scriptLen.WriteGShort(uint16(len(weapon.script)))
	want := []byte{PLO_NPCWEAPONADD + 32, byte(len(weapon.name)) + 32}
	want = append(want, []byte(weapon.name)...)
	want = append(want, NPCPROP_IMAGE+32, byte(len(weapon.image))+32)
	want = append(want, []byte(weapon.image)...)
	want = append(want, NPCPROP_SCRIPT+32)
	want = append(want, scriptLen.Bytes()...)
	want = append(want, []byte(weapon.script)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("weapon packet = % X, want % X", got, want)
	}
}

func TestLevelModTimeUsesGInt5WireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	modTime := int64(1712345678)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_LEVELMODTIME(modTime)
		done <- struct{}{}
	}()

	expectedTime := NewBuffer()
	expectedTime.WriteGInt5(uint64(modTime))
	want := append([]byte{PLO_LEVELMODTIME + 32}, expectedTime.Bytes()...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read level modtime packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("level modtime packet = % X, want % X", got, want)
	}
}

func TestShootParsesCompactProjectilePacket(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		id:         14,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_SHOOT)
	packet.WriteGInt(0)
	packet.WriteGChar(20)
	packet.WriteGChar(21)
	packet.WriteGChar(50)
	packet.WriteGChar(10)
	packet.WriteGChar(11)
	packet.WriteGChar(12)
	packet.WriteGChar(4)
	packet.Write([]byte("fire"))
	packet.WriteGChar(3)
	packet.Write([]byte("abc"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOOT(packet.Bytes())
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(14)
	zero := NewBuffer()
	zero.WriteGInt(0)
	want := append([]byte{PLO_SHOOT + 32}, idBuf.Bytes()...)
	want = append(want, zero.Bytes()...)
	want = append(want, 20+32, 21+32, 50+32, 10+32, 11+32, 12+32, 4+32)
	want = append(want, []byte("fire")...)
	want = append(want, 3+32)
	want = append(want, []byte("abc\n")...)

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read shoot packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("shoot packet = % X, want % X", got, want)
	}
}

func TestShoot2ForwardsRawBodyWithPlayerId(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
		id:         15,
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	body := []byte{0x20, 0x21, 0x22, 0x23}
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_SHOOT2(append([]byte{PLI_SHOOT2}, body...))
		done <- struct{}{}
	}()

	idBuf := NewBuffer()
	idBuf.WriteGShort(15)
	want := append([]byte{PLO_SHOOT2 + 32}, idBuf.Bytes()...)
	want = append(want, body...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read shoot2 packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("shoot2 packet = % X, want % X", got, want)
	}
}

func TestFileStatusPacketsUseRawFilenamePayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	fileName := "sprites/player.png"
	want := append([]byte{PLO_FILEUPTODATE + 32}, []byte(fileName)...)
	want = append(want, '\n', PLO_FILESENDFAILED+32)
	want = append(want, []byte(fileName)...)
	want = append(want, '\n')

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_FILEUPTODATE(fileName)
		p.sendPLO_FILESENDFAILED(fileName)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read file status packets: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("file status packets = % X, want % X", got, want)
	}
}

func TestSendFileWrapsLegacyFilePacketInRawData(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	fileName := "sprites/player.png"
	fileData := []byte("png-ish")
	if err := os.MkdirAll(dir+"\\sprites", 0755); err != nil {
		t.Fatalf("create sprites dir: %v", err)
	}
	if err := os.WriteFile(dir+"\\"+fileName, fileData, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	modTime := time.Unix(1712345678, 0)
	if err := os.Chtimes(dir+"\\"+fileName, modTime, modTime); err != nil {
		t.Fatalf("set test file modtime: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	embedded := NewBuffer()
	embedded.WriteGChar(PLO_FILE)
	embedded.WriteGInt5(uint64(modTime.Unix()))
	embedded.WriteGChar(byte(len(fileName)))
	embedded.Write([]byte(fileName))
	embedded.Write(fileData)
	embedded.WriteByte('\n')

	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(embedded.Len()))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, embedded.Bytes()...)

	done := make(chan struct{}, 1)
	go func() {
		p.sendFile(fileName)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read file packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("file packet = % X, want % X", got, want)
	}
}

func TestUpdateFileRequestSendsFileWhenModTimeDiffers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	fileName := "sprites/player.png"
	fileData := []byte("newer")
	if err := os.MkdirAll(dir+"\\sprites", 0755); err != nil {
		t.Fatalf("create sprites dir: %v", err)
	}
	if err := os.WriteFile(dir+"\\"+fileName, fileData, 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	modTime := time.Unix(1712345678, 0)
	if err := os.Chtimes(dir+"\\"+fileName, modTime, modTime); err != nil {
		t.Fatalf("set test file modtime: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := NewBuffer()
	request.WriteByte(PLI_UPDATEFILE)
	request.WriteGInt5(uint64(modTime.Unix() - 1))
	request.Write([]byte(fileName))

	embedded := NewBuffer()
	embedded.WriteGChar(PLO_FILE)
	embedded.WriteGInt5(uint64(modTime.Unix()))
	embedded.WriteGChar(byte(len(fileName)))
	embedded.Write([]byte(fileName))
	embedded.Write(fileData)
	embedded.WriteByte('\n')
	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(embedded.Len()))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, embedded.Bytes()...)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATEFILE(request.Bytes())
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update file response: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update file response = % X, want % X", got, want)
	}
}

func TestUpdateGaniReadsGInt5AndSendsRawSetbackPacket(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	dir := t.TempDir()
	ganiData := []byte("GANI0001\nSETBACKTO walk\n")
	if err := os.WriteFile(dir+"\\walk.gani", ganiData, 0644); err != nil {
		t.Fatalf("write gani: %v", err)
	}

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), config: NewFileSystem(dir)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := NewBuffer()
	packet.WriteByte(PLI_UPDATEGANI)
	packet.WriteGInt5(uint64(calculateCrc32Checksum(ganiData)))
	packet.Write([]byte("walk"))

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATEGANI(packet.Bytes())
		done <- struct{}{}
	}()

	want := []byte{PLO_UNKNOWN195 + 32, byte(len("walk")) + 32}
	want = append(want, []byte("walk\"SETBACKTO walk\"")...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update gani packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update gani packet = % X, want % X", got, want)
	}
}

func TestUpdateScriptWrapsBytecodeInRawData(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	bytecode := []byte{0x01, 0x02, 0x03}
	p := &Player{
		conn: serverConn,
		server: &Server{
			logger:  NewLogger("", false),
			weapons: map[string]*Weapon{"-test": {bytecode: bytecode}},
		},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	packet := append([]byte{PLI_UPDATESCRIPT}, []byte("-test")...)
	payload := append([]byte{PLO_NPCWEAPONSCRIPT + 32}, bytecode...)
	expectedLen := NewBuffer()
	expectedLen.WriteGInt(uint32(len(payload)))
	want := append([]byte{PLO_RAWDATA + 32}, expectedLen.Bytes()...)
	want = append(want, '\n')
	want = append(want, payload...)

	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_UPDATESCRIPT(packet)
		done <- struct{}{}
	}()

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read update script packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("update script packet = % X, want % X", got, want)
	}
}

func TestRequestTextParsesAfterPacketIDAndSendsRawServerText(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := "GraalEngine\x01lister\x01subscriptions\x01"
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...))
		done <- struct{}{}
	}()

	response := "GraalEngine\x01lister\x01subscriptions\x01unlimited\x01Unlimited Subscription\x01\"\"\x01"
	want := append([]byte{PLO_SERVERTEXT + 32}, []byte(response)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read request text packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("request text packet = % X, want % X", got, want)
	}
}

func TestRequestTextRespondsLocallyWhenListserverUnavailable(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false), name: "Orion-Go"},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	request := "GraalEngine\x01pmservers\x01all\x01"
	done := make(chan struct{}, 1)
	go func() {
		p.msgPLI_REQUESTTEXT(append([]byte{PLI_REQUESTTEXT}, []byte(request)...))
		done <- struct{}{}
	}()

	response := "GraalEngine\x01pmservers\x01all\x01"
	want := append([]byte{PLO_SERVERTEXT + 32}, []byte(response)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read request text fallback packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("request text fallback = % X, want % X", got, want)
	}
}

func TestNewWorldTimeUsesGInt4WireFormat(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	worldTime := uint(0x123456)
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_NEWWORLDTIME(worldTime)
		done <- struct{}{}
	}()

	want := []byte{PLO_NEWWORLDTIME + 32, 0x20, 0x68, 0x88, 0x76, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read new world time packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("new world time packet = % X, want % X", got, want)
	}
}

func TestGInt4MatchesCStringWireFormat(t *testing.T) {
	buf := NewBuffer()
	buf.WriteGInt4(0xAB4)

	want := []byte{0x20, 0x20, 0x35, 0x54}
	if string(buf.Bytes()) != string(want) {
		t.Fatalf("GInt4 bytes = % X, want % X", buf.Bytes(), want)
	}

	got := NewBufferFromBytes(want).ReadGInt4()
	if got != 0xAB4 {
		t.Fatalf("ReadGInt4 = %X, want AB4", got)
	}
}

func TestGIntReadersRoundTripWriterEncoding(t *testing.T) {
	shortBuf := NewBuffer()
	shortBuf.WriteGShort(0x1234)
	if got := NewBufferFromBytes(shortBuf.Bytes()).ReadGShort(); got != 0x1234 {
		t.Fatalf("ReadGShort = %X, want 1234", got)
	}

	intBuf := NewBuffer()
	intBuf.WriteGInt(0x34567)
	if got := NewBufferFromBytes(intBuf.Bytes()).ReadGInt(); got != 0x34567 {
		t.Fatalf("ReadGInt = %X, want 34567", got)
	}

	int5Buf := NewBuffer()
	int5Buf.WriteGInt5(0x12345678)
	if got := NewBufferFromBytes(int5Buf.Bytes()).ReadGInt5(); got != 0x12345678 {
		t.Fatalf("ReadGInt5 = %X, want 12345678", got)
	}
}

func TestGhostIconUsesSingleBytePayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_GHOSTICON(false)
		done <- struct{}{}
	}()

	want := []byte{PLO_GHOSTICON + 32, 0, '\n'}

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read ghost icon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("ghost icon packet = % X, want % X", got, want)
	}
}

func TestDefaultWeaponUsesSingleGCharPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_DEFAULTWEAPON(7)
		done <- struct{}{}
	}()

	want := []byte{PLO_DEFAULTWEAPON + 32, 7 + 32, '\n'}
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read default weapon packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("default weapon packet = % X, want % X", got, want)
	}
}

func TestMapPacketsUseRawCommaTextPayload(t *testing.T) {
	p := &Player{
		server:        &Server{logger: NewLogger("", false), settings: NewSettings()},
		encryption:    *NewEncryption(),
		outEncryption: *NewEncryption(),
		queueOutgoing: true,
	}
	p.server.settings.Set("bigmap", "worldmap.txt, worldmap.png, 30, 40")
	p.server.settings.Set("minimap", "mini.txt, mini.png, 5, 6")
	p.sendPLO_BIGMAP()
	p.sendPLO_MINIMAP()

	want := append([]byte{PLO_BIGMAP + 32}, []byte("worldmap.txt,worldmap.png,30,40\n")...)
	want = append(want, PLO_MINIMAP+32)
	want = append(want, []byte("mini.txt,mini.png,5,6\n")...)

	if string(p.outQueue) != string(want) {
		t.Fatalf("map packets = % X, want % X", p.outQueue, want)
	}
}

func TestRpgWindowUsesCStringCompatibleTextPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	message := "\"Welcome to Orion.\",\"Go Code GServer.\""
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_RPGWINDOW(message)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_RPGWINDOW + 32}, []byte(message)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read rpg window packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("rpg window packet = % X, want % X", got, want)
	}
}

func TestStartMessageUsesRawConfiguredMessage(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	p := &Player{
		conn:       serverConn,
		server:     &Server{logger: NewLogger("", false)},
		encryption: *NewEncryption(),
	}
	p.encryption.SetGen(ENCRYPT_GEN_1)

	message := "<html><body>Welcome</body></html>"
	done := make(chan struct{}, 1)
	go func() {
		p.sendPLO_STARTMESSAGE(message)
		done <- struct{}{}
	}()

	want := append([]byte{PLO_STARTMESSAGE + 32}, []byte(message)...)
	want = append(want, '\n')

	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(clientConn, got); err != nil {
		t.Fatalf("read start message packet: %v", err)
	}
	<-done

	if string(got) != string(want) {
		t.Fatalf("start message packet = % X, want % X", got, want)
	}
}
