package cli

import (
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
)

// timerName names the renewal's systemd units and launchd agent.
const (
	timerName   = "berthlight-cert"
	launchLabel = "nl.stormlantern.berthlight-cert"
)

// timerEnv is the configuration a renewal run needs from the environment
// it was installed from: where things are, and how to reach the runtime.
// Never credentials: those stay in cert.env, read at each run.
var timerEnv = []string{
	"BERTHLIGHT_BASE", "BERTHLIGHT_PORT", "BERTHLIGHT_PROXY_NAME", "BERTHLIGHT_RUNTIME", "BERTHLIGHT_IMAGE",
	"DOCKER_HOST", "DOCKER_CONTEXT", "CONTAINER_HOST", "CONTAINER_CONNECTION", "LEGO_CA_CERTIFICATES", "PATH",
}

// timerEnvironment returns NAME=value for the renewal run.
func timerEnvironment(c Config) []string {
	env := []string{"BERTHLIGHT_STATE_DIR=" + c.StateDir, "BERTHLIGHT_ACME_DIR=" + c.ACMEDir}
	for _, k := range timerEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// executable is the berthlight to run later: the one on PATH, which a
// package manager keeps current, else this one.
func executable() (string, error) {
	if p, err := exec.LookPath("berthlight"); err == nil {
		return filepath.Abs(p)
	}
	return os.Executable()
}

// installTimer installs a daily `berthlight cert` with args (PRD 0001, R7).
func installTimer(c Config, args []string, envFile string, stdout io.Writer) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	env := timerEnvironment(c)
	if goruntime.GOOS == "darwin" {
		path := filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")
		logFile := filepath.Join(home, "Library", "Logs", timerName+".log")
		if err := writeUnit(path, launchdPlist(exe, args, env, logFile, minuteOf(c.StateDir))); err != nil {
			return err
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = exec.Command("launchctl", "bootout", domain+"/"+launchLabel).Run()
		if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap %s: %v %s", path, err, out)
		}
		fmt.Fprintf(stdout, "installed %s: renews daily, logs to %s\n", path, logFile)
		fmt.Fprintf(stdout, "DNS credentials for the renewal go in %s (KEY=VALUE lines, chmod 600)\n", envFile)
		return nil
	}
	if goruntime.GOOS != "linux" {
		return errors.New("--install-timer knows systemd (Linux) and launchd (macOS); schedule `berthlight cert` daily another way")
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	dir = filepath.Join(dir, "systemd", "user")
	service, timer := systemdUnits(exe, args, env)
	if err := writeUnit(filepath.Join(dir, timerName+".service"), service); err != nil {
		return err
	}
	if err := writeUnit(filepath.Join(dir, timerName+".timer"), timer); err != nil {
		return err
	}
	for _, a := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", timerName + ".timer"}} {
		if out, err := exec.Command("systemctl", a...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v %s", strings.Join(a, " "), err, out)
		}
	}
	fmt.Fprintf(stdout, "installed %s.timer in %s: renews daily (journalctl --user -u %s)\n", timerName, dir, timerName)
	fmt.Fprintf(stdout, "DNS credentials for the renewal go in %s (KEY=VALUE lines, chmod 600)\n", envFile)
	return nil
}

func writeUnit(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// systemdUnits returns the service and timer. Persistent catches up on a
// run missed while the machine was off; the delay spreads the load on the
// CA, as Let's Encrypt asks.
func systemdUnits(exe string, args, env []string) (service, timer string) {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Renew berthlight's certificate\nWants=network-online.target\nAfter=network-online.target\n\n[Service]\nType=oneshot\n")
	for _, e := range env {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(e))
	}
	b.WriteString("ExecStart=" + systemdQuote(exe))
	for _, a := range args {
		b.WriteString(" " + systemdQuote(a))
	}
	b.WriteString("\n")
	service = b.String()
	timer = "[Unit]\nDescription=Renew berthlight's certificate daily\n\n[Timer]\nOnCalendar=daily\nRandomizedDelaySec=1h\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	return service, timer
}

// systemdQuote quotes a word for systemd when it needs it; % is systemd's
// specifier character and is doubled.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if s != "" && !strings.ContainsAny(s, " \t\"'\\;$") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`).Replace(s) + `"`
}

// launchdPlist returns the launchd agent: daily at a minute past 10 that
// differs per machine, run at wake if the Mac slept through it.
func launchdPlist(exe string, args, env []string, logFile string, minute int) string {
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchLabel + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{exe}, args...) {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(a))
	}
	b.WriteString("\t</array>\n\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", esc(k), esc(v))
	}
	fmt.Fprintf(&b, "\t</dict>\n\t<key>StartCalendarInterval</key>\n\t<dict>\n\t\t<key>Hour</key>\n\t\t<integer>10</integer>\n\t\t<key>Minute</key>\n\t\t<integer>%d</integer>\n\t</dict>\n", minute)
	fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n</dict>\n</plist>\n", esc(logFile), esc(logFile))
	return b.String()
}

// minuteOf spreads machines over the hour.
func minuteOf(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32() % 60)
}
