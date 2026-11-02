package scheduler

import (
	"errors"
	"time"
)

var errInvalidClock = errors.New("scheduler: invalid time")

func parseClock(value string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", value)
	if err != nil || t.Format("15:04") != value {
		return 0, 0, errInvalidClock
	}
	return t.Hour(), t.Minute(), nil
}
