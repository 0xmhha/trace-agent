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
