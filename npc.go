package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ============ NPC ============
type NPC struct {
	mu                            sync.Mutex
	id                            uint32
	npcType                       NPCType
	x, y, z                       int16
	width, height                 int
	image                         string
	script                        string
	npcName, scripter, scriptType string
	timeout                       int
	sprite                        byte
	visFlags, blockFlags          byte
	hurtX, hurtY                  float32
	saves                         [10]byte
	flagList                      map[string]string
	character                     Character
	weaponName                    string
	scriptData                    string
	level                         *Level
}

func NewNPC(npcType NPCType) *NPC {
	return &NPC{id: 0, npcType: npcType, x: 30 * 16, y: 30 * 16, z: 0, saves: [10]byte{}, flagList: make(map[string]string), level: nil}
}
func (n *NPC) setId(id uint32) { n.id = id }
func (n *NPC) getId() uint32   { return n.id }
func (n *NPC) runTimeout()     { /* TODO: Implement timeout event */ }
func (n *NPC) ensureFlagList() {
	if n.flagList == nil {
		n.flagList = make(map[string]string)
	}
}

func (n *NPC) variableDump() string {
	if n == nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	name := n.npcName
	if name == "" {
		name = fmt.Sprintf("npcs[%d]", n.id)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Variables dump from npc %s\n\n", name)
	if n.scriptType != "" {
		fmt.Fprintf(&out, "%s.type: %s\n", name, n.scriptType)
	}
	if n.scripter != "" {
		fmt.Fprintf(&out, "%s.scripter: %s\n", name, n.scripter)
	}
	if n.level != nil && n.level.levelName != "" {
		fmt.Fprintf(&out, "%s.level: %s\n", name, n.level.levelName)
	}

	out.WriteString("\nAttributes:\n")
	fmt.Fprintf(&out, "%s.id: %d\n", name, n.id)
	if n.image != "" {
		fmt.Fprintf(&out, "%s.image: %s\n", name, n.image)
	}
	if n.script != "" {
		fmt.Fprintf(&out, "%s.script: size: %d\n", name, len(n.script))
	}
	if n.character.headImage != "" {
		fmt.Fprintf(&out, "%s.head: %s\n", name, n.character.headImage)
	}
	if n.character.bodyImage != "" {
		fmt.Fprintf(&out, "%s.body: %s\n", name, n.character.bodyImage)
	}
	if n.npcName != "" {
		fmt.Fprintf(&out, "%s.name: %s\n", name, n.npcName)
	}
	if n.scriptType != "" {
		fmt.Fprintf(&out, "%s.type: %s\n", name, n.scriptType)
	}
	if n.level != nil && n.level.levelName != "" {
		fmt.Fprintf(&out, "%s.level: %s\n", name, n.level.levelName)
	}
	for i, save := range n.saves {
		if save > 0 {
			fmt.Fprintf(&out, "%s.save[%d]: %d\n", name, i, save)
		}
	}
	if n.timeout > 0 {
		fmt.Fprintf(&out, "%s.timeout: %.2f\n", name, float32(n.timeout)*0.05)
	}
	if len(n.flagList) > 0 {
		flags := make([]string, 0, len(n.flagList))
		for flag := range n.flagList {
			flags = append(flags, flag)
		}
		sort.Strings(flags)
		out.WriteString("\nnpc.Flags:\n")
		for _, flag := range flags {
			fmt.Fprintf(&out, "%s.flags[\"%s\"]: %s\n", name, flag, n.flagList[flag])
		}
	}
	return out.String()
}
