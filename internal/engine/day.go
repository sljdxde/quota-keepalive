package engine

import "time"

// Beijing 是业务日界固定时区（Asia/Shanghai，UTC+8），与机器时区无关。
var Beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

// BeijingDay 以 Asia/Shanghai 计算当日日期键（YYYY-MM-DD）。
func BeijingDay(now time.Time) string {
	return now.In(Beijing).Format("2006-01-02")
}

// IsBeforeBeijingNoon 报告当前是否处于北京正午之前（用于午前/午后划分）。
func IsBeforeBeijingNoon(now time.Time) bool {
	return now.In(Beijing).Hour() < 12
}
