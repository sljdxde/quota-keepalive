package evidence

import (
	"encoding/json"
	"strings"
)

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func containsAny(data []byte, words ...string) bool {
	lower := strings.ToLower(string(data))
	for _, w := range words {
		if strings.Contains(lower, strings.ToLower(w)) {
			return true
		}
	}
	return false
}
