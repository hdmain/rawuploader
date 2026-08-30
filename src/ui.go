package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiCyan   = "\033[36m"
)

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func style(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}
	return code + s + ansiReset
}

// sendUI keeps transfer feedback on a single live line (TTY) or sparse lines (pipe).
type sendUI struct {
	name     string
	size     int64
	arrow    string
	tty      bool
	start    time.Time
	last     time.Time
	lastDone int64
	live     bool // true while a \r status line is active
	barWidth int
	showBar  bool
}

func newSendUI(name string, size int64) *sendUI {
	return newXferUI("↑", name, size)
}

func newRecvUI(name string, size int64) *sendUI {
	return newXferUI("↓", name, size)
}

func newXferUI(arrow, name string, size int64) *sendUI {
	return &sendUI{
		name:     name,
		size:     size,
		arrow:    arrow,
		tty:      stdoutIsTTY(),
		start:    time.Now(),
		barWidth: 14,
		showBar:  size >= 256*1024,
	}
}

func (u *sendUI) paint(line string) {
	if u.tty {
		fmt.Printf("\r%s\033[K", line)
		u.live = true
		return
	}
	// pipes/logs: avoid phase spam; progress prints itself
	fmt.Println(line)
	u.live = false
}

func (u *sendUI) endLive() {
	if u.live && u.tty {
		fmt.Println()
		u.live = false
	}
}

func (u *sendUI) replaceLine(line string) {
	if u.tty {
		fmt.Printf("\r%s\033[K\n", line)
		u.live = false
		return
	}
	fmt.Println(line)
	u.live = false
}

func (u *sendUI) beginTransfer() {
	u.start = time.Now()
	u.last = time.Time{}
	u.lastDone = 0
}

// Status shows a short phase on the live line, e.g. "probing", "connecting".
func (u *sendUI) Status(phase string) {
	if !u.tty {
		return
	}
	line := fmt.Sprintf("%s %s  %s  %s",
		style(true, ansiBlue, u.arrow),
		style(true, ansiBold, u.name),
		style(true, ansiDim, formatBytes(float64(u.size))),
		style(true, ansiDim, phase),
	)
	u.paint(line)
}

// Progress updates the live line with a compact bar. label is unused visually (kept for call sites).
func (u *sendUI) Progress(_ string, done, total int64) {
	if !u.showBar {
		return
	}
	now := time.Now()
	force := total > 0 && done >= total
	if !force && !u.last.IsZero() && now.Sub(u.last) < 50*time.Millisecond {
		return
	}
	u.last = now
	u.lastDone = done
	if total <= 0 {
		total = 1
	}
	if done > total {
		done = total
	}

	pct := float64(done) / float64(total) * 100
	elapsed := now.Sub(u.start).Seconds()
	speedStr := ""
	etaStr := ""
	if elapsed >= 0.08 && done > 0 {
		speed := float64(done) / elapsed
		speedStr = "  " + formatBytes(speed) + "/s"
		left := total - done
		if speed > 0 && left > 0 {
			etaStr = "  " + formatETA(float64(left)/speed)
		}
	}

	filled := int(float64(u.barWidth) * float64(done) / float64(total))
	if filled > u.barWidth {
		filled = u.barWidth
	}
	if done > 0 && filled == 0 {
		filled = 1
	}
	barBody := strings.Repeat("━", filled) + strings.Repeat("─", u.barWidth-filled)
	bar := style(u.tty, ansiCyan, barBody)

	line := fmt.Sprintf("%s %s  %s  %s %4.0f%%%s%s",
		style(u.tty, ansiBlue, u.arrow),
		style(u.tty, ansiBold, u.name),
		bar,
		style(u.tty, ansiDim, formatBytes(float64(done))+"/"+formatBytes(float64(total))),
		pct,
		style(u.tty, ansiDim, speedStr),
		style(u.tty, ansiDim, etaStr),
	)
	u.paint(line)
}

func (u *sendUI) FinishProgress(total int64) {
	if !u.showBar {
		return
	}
	if u.lastDone < total {
		u.last = time.Time{}
		u.Progress("", total, total)
	}
}

func (u *sendUI) Done(code string, storageDurationSec uint32) {
	keep := formatValidDuration(storageDurationSec)
	u.replaceLine(fmt.Sprintf("%s File sent (encrypted). Your code: %s (%s)",
		style(u.tty, ansiGreen+ansiBold, "✓"),
		style(u.tty, ansiBold+ansiCyan, code),
		style(u.tty, ansiDim, keep),
	))
}

func (u *sendUI) DoneSecure(code, keyHex string, storageDurationSec uint32) {
	keep := formatValidDuration(storageDurationSec)
	u.replaceLine(fmt.Sprintf("%s File sent (secure). Your code: %s (%s)",
		style(u.tty, ansiGreen+ansiBold, "✓"),
		style(u.tty, ansiBold+ansiCyan, code),
		style(u.tty, ansiDim, keep),
	))
	fmt.Printf("Key (save it – needed to download): %s\n", style(u.tty, ansiBold+ansiYellow, keyHex))
	fmt.Println("Without the key the file cannot be decrypted.")
}

func (u *sendUI) DoneSaved(path string) {
	u.replaceLine(fmt.Sprintf("%s Downloaded: %s",
		style(u.tty, ansiGreen+ansiBold, "✓"),
		style(u.tty, ansiBold, path),
	))
}

func (u *sendUI) DoneShared() {
	u.replaceLine(fmt.Sprintf("%s Sent to peer  %s  %s",
		style(u.tty, ansiGreen+ansiBold, "✓"),
		style(u.tty, ansiBold, u.name),
		style(u.tty, ansiDim, formatBytes(float64(u.size))),
	))
}

func (u *sendUI) Fail() {
	u.endLive()
}

func (u *sendUI) progressWriter(w io.Writer, total int64) io.Writer {
	return &xferCounter{w: w, ui: u, total: total}
}

type xferCounter struct {
	w     io.Writer
	ui    *sendUI
	done  int64
	total int64
}

func (c *xferCounter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.done += int64(n)
		c.ui.Progress("", c.done, c.total)
	}
	return n, err
}

func formatETA(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if seconds < 3600 {
		m := int(seconds) / 60
		s := int(seconds) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// printSendPhase is a one-off dim line for prep steps (e.g. zip) before sendUI exists.
func printSendPhase(msg string) {
	tty := stdoutIsTTY()
	fmt.Printf("%s %s\n", style(tty, ansiDim, "·"), style(tty, ansiDim, msg))
}
