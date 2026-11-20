package collect

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// SessionKey identifies a confirmed session within a client profile across adapters.
func SessionKey(profile, session string) string {
	if profile == "" || session == "" {
		return ""
	}
	return entityKey("session", profile, session)
}

// TaskKey identifies a confirmed turn or human prompt within its session.
func TaskKey(profile, session, turn string) string {
	if profile == "" || session == "" || turn == "" {
		return ""
	}
	return entityKey("task", profile, session, turn)
}

func entityKey(parts ...string) string {
	h := sha256.New()
	for _, s := range parts {
		_, _ = h.Write([]byte(strconv.Itoa(len(s)) + ":"))
		_, _ = h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HumanPrompt reports an external user's prompt boundary from safe transcript
// metadata. Claude Code 2.1.329 omits isMeta on ordinary, non-sidechain input.
func HumanPrompt(e Evidence) bool {
	m := e.Metadata
	if e.Kind != Transcript || str(m, "type") != "user" || str(m, "userType") != "external" || str(m, "promptId") == "" || str(m, "agentId") != "" || m["isSidechain"] == true {
		return false
	}
	msg := obj(m["message"])
	if _, ok := msg["content"].(string); !ok || str(msg, "role") != "user" {
		return false
	}
	if flag, exists := m["isMeta"]; exists {
		return flag == false
	}
	return e.ClientVersion == "2.1.329" && m["isSidechain"] == false
}
