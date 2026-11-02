package scheduler

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// WindowsTaskXML renders a least-privilege per-user Task Scheduler definition
// with a single repeating trigger. StartWhenAvailable is enabled so a missed
// run catches up after sleep; battery restrictions are not used.
func WindowsTaskXML(spec Spec, now time.Time, userID string) ([]byte, error) {
	if !executableOK(spec.Executable) {
		return nil, fmt.Errorf("scheduler: invalid executable")
	}
	if spec.IntervalMinutes < 5 || spec.IntervalMinutes > 7*24*60 {
		return nil, fmt.Errorf("scheduler: invalid interval")
	}
	local := now.In(time.Local)
	start := local.Truncate(time.Minute)
	if start.Before(local) {
		start = start.Add(time.Minute)
	}
	esc := func(value string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(value))
		return b.String()
	}
	userLine := ""
	if userID != "" {
		userLine = "\n      <UserId>" + esc(userID) + "</UserId>"
	}
	xmlBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Author>QKA</Author>
    <Description>QKA built-in interval auto-run</Description>
  </RegistrationInfo>
  <Triggers>
    <TimeTrigger>
      <StartBoundary>%s</StartBoundary>
      <Enabled>true</Enabled>
      <Repetition>
        <Interval>PT%dM</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
    </TimeTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">%s
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <WakeToRun>true</WakeToRun>
    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd></IdleSettings>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, start.Format("2006-01-02T15:04:05"), spec.IntervalMinutes, userLine,
		esc(spec.Executable),
		esc("task-run --scheduled --config-dir "+quoteWindowsArgument(spec.ConfigDir)))
	return []byte(xmlBody), nil
}

// quoteWindowsArgument encodes one argument using the CommandLineToArgvW /
// MS C runtime rules. In particular, a trailing backslash must be doubled
// before the closing quote so it cannot escape that quote.
func quoteWindowsArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var out strings.Builder
	out.WriteByte('"')
	backslashes := 0
	for _, r := range value {
		switch r {
		case '\\':
			backslashes++
		case '"':
			out.WriteString(strings.Repeat(`\`, backslashes*2+1))
			out.WriteByte('"')
			backslashes = 0
		default:
			out.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
			out.WriteRune(r)
		}
	}
	out.WriteString(strings.Repeat(`\`, backslashes*2))
	out.WriteByte('"')
	return out.String()
}
