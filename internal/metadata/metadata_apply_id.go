package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func stableApplyID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		hash.Write([]byte{0})
	}
	sum := hash.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func personID(name string) string {
	return stableApplyID("person", name)
}

func encodeApplyJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeApplyJSON(value string, out any) {
	if strings.TrimSpace(value) == "" || value == "null" {
		return
	}
	_ = json.Unmarshal([]byte(value), out)
}

func applyBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
