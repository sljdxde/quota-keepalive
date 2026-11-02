package selfupdate

import (
	"strconv"
	"strings"
)

// compareVersions 比较两个点分版本号：a>b 返回 1，a<b 返回 -1，相等返回 0。
func compareVersions(a, b string) int {
	pa := strings.FieldsFunc(normalizeVersion(a), func(r rune) bool { return r == '.' })
	pb := strings.FieldsFunc(normalizeVersion(b), func(r rune) bool { return r == '.' })

	for i := 0; i < len(pa) || i < len(pb); i++ {
		var va, vb int
		if i < len(pa) {
			va = segment(pa[i])
		}
		if i < len(pb) {
			vb = segment(pb[i])
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1 && (value[0] == 'v' || value[0] == 'V') {
		return value[1:]
	}
	return value
}

func segment(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
