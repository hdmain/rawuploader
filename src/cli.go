package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	exitOK    = 0
	exitUsage = 1
	exitError = 2
)

func progName() string {
	return filepath.Base(os.Args[0])
}

func isHelpArg(s string) bool {
	switch s {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, name := range names {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// expandLongFlags rewrites --long-form flags to their short equivalents before flag.Parse.
func expandLongFlags(args []string, aliases map[string]string) []string {
	if len(aliases) == 0 {
		return args
	}
	flagName := func(short string) string {
		if strings.HasPrefix(short, "-") {
			return short
		}
		return "-" + short
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			out = append(out, arg)
			continue
		}
		body := strings.TrimPrefix(arg, "--")
		if short, ok := aliases[body]; ok {
			short = flagName(short)
			if idx := strings.IndexByte(body, '='); idx >= 0 {
				out = append(out, short+"="+body[idx+1:])
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				out = append(out, short, args[i+1])
				i++
			} else {
				out = append(out, short)
			}
			continue
		}
		if idx := strings.IndexByte(body, '='); idx >= 0 {
			key := body[:idx]
			if short, ok := aliases[key]; ok {
				out = append(out, flagName(short)+"="+body[idx+1:])
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

type commandFlagSet struct {
	*flag.FlagSet
	showHelp bool
}

func newCommandFlagSet(name string, usage func()) *commandFlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfs := &commandFlagSet{FlagSet: fs}
	fs.Usage = func() {
		if usage != nil {
			usage()
		} else {
			fs.PrintDefaults()
		}
	}
	fs.BoolVar(&cfs.showHelp, "h", false, "show help")
	return cfs
}

func (cfs *commandFlagSet) Parse(args []string, aliases map[string]string) (help bool, err error) {
	if err = cfs.FlagSet.Parse(expandLongFlags(args, aliases)); err != nil {
		return false, err
	}
	return cfs.showHelp, nil
}

func exitHelp(usage func()) {
	usage()
	os.Exit(exitOK)
}

func exitUsageMsg(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", progName(), fmt.Sprintf(format, args...))
	os.Exit(exitUsage)
}

func exitCmdUsage(cmd, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s: %s\n", progName(), cmd, fmt.Sprintf(format, args...))
	os.Exit(exitUsage)
}

func exitCmdError(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s: %v\n", progName(), cmd, err)
	os.Exit(exitError)
}

var commonLongFlags = map[string]string{
	"help":     "h",
	"output":   "o",
	"local":    "l",
	"longterm": "t",
	"server":   "s",
	"zip":      "z",
	"unzip":    "u",
	"id":       "i",
	"port":     "p",
	"dir":      "d",
	"web":      "w",
	"maxsize":  "m",
}

func printGlobalUsage() {
	name := progName()
	fmt.Printf("%s – TCP file send/receive; client generates 6-digit code, data encrypted on server\n", name)
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  server       listen for uploads; store encrypted data")
	fmt.Println("  servers      test each server: ping, space, speed, BGP ASN/path")
	fmt.Println("  send         generate code, encrypt file, upload; you get the 6-digit code")
	fmt.Println("  get          download by code; decrypt with same code (or with key for secure uploads)")
	fmt.Println("  secure send  encrypt with your own 256-bit key; server assigns code; use get + key to download")
	fmt.Println()
	fmt.Printf("Run '%s <command> -h' for command-specific help.\n", name)
	fmt.Println()
	fmt.Println("Quick shortcuts:")
	fmt.Printf("  %s <file> -l            send file on local LAN\n", name)
	fmt.Printf("  %s get -l               receive file on local LAN\n", name)
	fmt.Println()
	fmt.Println("Servers are read from the address list (first digit of code = server id).")
	fmt.Printf("Data kept %v, cleanup every %v, max upload %d MB, rate limit %d codes/%v then %v ban\n",
		StorageDuration, CleanupInterval, MaxBlobSize/(1024*1024), RateLimitAttempts, RateLimitWindow, BanDuration)
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Printf("  %s server -p 9999\n", name)
	fmt.Printf("  %s send document.pdf\n", name)
	fmt.Printf("  %s get 482917 -o myfile.pdf\n", name)
}

func printServerUsage() {
	name := progName()
	fmt.Printf("Usage: %s server [options]\n\n", name)
	fmt.Println("Options:")
	fmt.Println("  -i, --id=N         server id 0–9 (first digit of generated codes); default 0")
	fmt.Println("  -p, --port=PORT    TCP listen port (default 9999)")
	fmt.Println("  -d, --dir=PATH     directory for stored encrypted blobs (default ./data)")
	fmt.Println("  -w, --web=PORT     HTTP port for browser download page; empty = disabled")
	fmt.Println("  -m, --maxsize=MB   max upload size in MB (0 = default from code)")
	fmt.Println("  -t, --longterm     allow long-term storage (client --longterm=e.g. 7d; max 150 MB)")
	fmt.Println("  -h, --help         show this help")
}

func printSendUsage() {
	name := progName()
	fmt.Printf("Usage: %s send [options] <file> [host:port]\n\n", name)
	fmt.Println("Encrypt a file locally, upload it, and print a 6-digit download code.")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -s, --server=N     server id 0–9 to use (default: auto-probe)")
	fmt.Println("  -t, --longterm=D   store for e.g. 7d or 24h (max 150 MB; server must support --longterm)")
	fmt.Println("  -z, --zip          pack file or directory into tar.gz before sending")
	fmt.Println("  -l, --local        local LAN send mode (no server)")
	fmt.Println("  -h, --help         show this help")
	fmt.Println()
	fmt.Println("Arguments:")
	fmt.Println("  <file>             path to file or directory to send")
	fmt.Println("  [host:port]        optional server address (overrides address list)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Printf("  %s send document.pdf\n", name)
	fmt.Printf("  %s send -z ./photos\n", name)
	fmt.Printf("  %s send -t 7d report.pdf\n", name)
}

func printGetUsage() {
	name := progName()
	fmt.Printf("Usage: %s get [options] <6-digit-code>\n\n", name)
	fmt.Println("Options:")
	fmt.Println("  -o, --output=FILE   output file (default: name from server)")
	fmt.Println("  -u, --unzip         after download, extract tar.gz and remove archive")
	fmt.Println("  -l, --local         local LAN receive mode (no server)")
	fmt.Println("  -h, --help          show this help")
	fmt.Println()
	fmt.Println("Positional:")
	fmt.Println("  <6-digit-code>  code returned when uploading")
}

func printSecureSendUsage() {
	name := progName()
	fmt.Printf("Usage: %s secure send [options] <file> [host:port]\n\n", name)
	fmt.Println("Options:")
	fmt.Println("  -s, --server=N     server id 0–9 to use (default: auto-probe)")
	fmt.Println("  -t, --longterm=D   store for e.g. 7d or 24h (max 150 MB; server must support --longterm)")
	fmt.Println("  -z, --zip          pack file or directory into tar.gz before sending")
	fmt.Println("  -h, --help         show this help")
	fmt.Println()
	fmt.Println("Positional:")
	fmt.Println("  <file>        path to file or directory to send")
	fmt.Println("  [host:port]   optional server address (overrides address list)")
}

func printServersUsage() {
	name := progName()
	fmt.Printf("Usage: %s servers [options]\n\n", name)
	fmt.Println("Test each server: ping, free space, 2s download and 2s upload of random data.")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -h, --help    show this help")
}

func runServerCommand(args []string) {
	fs := newCommandFlagSet("server", printServerUsage)
	serverID := fs.Int("i", 0, "server id 0–9 (first digit of generated codes)")
	serverPort := fs.String("p", "9999", "listen port")
	serverDir := fs.String("d", "./data", "directory for stored encrypted blobs")
	serverWeb := fs.String("w", "", "web port for browser download page (e.g. 8080); empty = disabled")
	serverMaxSizeMB := fs.Int64("m", 0, "max upload size in MB (0 = use default from code)")
	serverLongTerm := fs.Bool("t", false, "allow long-term storage (client --longterm=e.g. 7d; max 150 MB)")

	help, err := fs.Parse(args, commonLongFlags)
	if help {
		exitHelp(printServerUsage)
	}
	if err != nil {
		exitCmdUsage("server", "%s", err)
	}
	if fs.NArg() > 0 {
		exitCmdUsage("server", "unexpected argument: %s", fs.Arg(0))
	}

	id := *serverID
	if id < 0 || id > 9 {
		exitCmdUsage("server", "server id must be 0–9")
	}
	maxBlob := MaxBlobSize
	if *serverMaxSizeMB > 0 {
		maxBlob = *serverMaxSizeMB * 1024 * 1024
	}
	if err := runServer(id, *serverPort, *serverDir, *serverWeb, maxBlob, *serverLongTerm); err != nil {
		exitCmdError("server", err)
	}
}

func runSendCommand(args []string) {
	fs := newCommandFlagSet("send", printSendUsage)
	clientSendServerID := fs.Int("s", -1, "server id 0–9 to use (default: auto-probe)")
	clientSendLongTerm := fs.String("t", "", "store for e.g. 7d or 24h (max 150 MB; server must support --longterm)")
	clientSendZip := fs.Bool("z", false, "pack file or directory into tar.gz before sending")
	clientSendLocal := fs.Bool("l", false, "local LAN send mode")

	help, err := fs.Parse(args, commonLongFlags)
	if help {
		exitHelp(printSendUsage)
	}
	if err != nil {
		exitCmdUsage("send", "%s", err)
	}
	pos := fs.Args()
	if len(pos) < 1 {
		exitCmdUsage("send", "missing file argument")
	}
	if *clientSendLocal {
		if err := runLocalSender(pos[0]); err != nil {
			exitCmdError("local", err)
		}
		return
	}
	addr := ""
	if len(pos) >= 2 {
		addr = pos[1]
	}
	longTermSec := uint32(0)
	if *clientSendLongTerm != "" {
		sec, err := parseLongTermDuration(*clientSendLongTerm)
		if err != nil {
			exitCmdError("send", err)
		}
		longTermSec = sec
	}
	sendPath, cleanup, err := prepareSendPath(pos[0], *clientSendZip)
	if err != nil {
		exitCmdError("send", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if err := runClientSend(sendPath, addr, *clientSendServerID, longTermSec); err != nil {
		exitCmdError("send", err)
	}
}

func reorderFlagsBeforePositional(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if takesSeparateValue(arg) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

func takesSeparateValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	name := strings.TrimLeft(arg, "-")
	if idx := strings.IndexByte(name, '='); idx >= 0 {
		name = name[:idx]
	}
	switch name {
	case "o", "output", "s", "server", "t", "longterm", "p", "port", "d", "dir", "w", "web", "m", "maxsize", "i", "id":
		return true
	default:
		return false
	}
}

func runGetCommand(args []string) {
	fs := newCommandFlagSet("get", printGetUsage)
	clientGetOut := fs.String("o", "", "output file (default: name from server)")
	clientGetUnzip := fs.Bool("u", false, "after download, extract tar.gz and remove archive")
	clientGetLocal := fs.Bool("l", false, "local LAN receive mode")

	help, err := fs.Parse(reorderFlagsBeforePositional(args), commonLongFlags)
	if help {
		exitHelp(printGetUsage)
	}
	if err != nil {
		exitCmdUsage("get", "%s", err)
	}
	if *clientGetLocal {
		if err := runLocalReceiver(); err != nil {
			exitCmdError("local", err)
		}
		return
	}
	pos := fs.Args()
	if len(pos) < 1 {
		exitCmdUsage("get", "missing 6-digit code argument")
	}
	if err := runClientGet(pos[0], *clientGetOut, *clientGetUnzip); err != nil {
		exitCmdError("get", err)
	}
}

func runSecureSendCommand(args []string) {
	fs := newCommandFlagSet("secure send", printSecureSendUsage)
	secureServerID := fs.Int("s", -1, "server id 0–9 to use (default: auto-probe)")
	secureLongTerm := fs.String("t", "", "store for e.g. 7d or 24h (max 150 MB; server must support --longterm)")
	secureZip := fs.Bool("z", false, "pack file or directory into tar.gz before sending")

	help, err := fs.Parse(args, commonLongFlags)
	if help {
		exitHelp(printSecureSendUsage)
	}
	if err != nil {
		exitCmdUsage("secure send", "%s", err)
	}
	pos := fs.Args()
	if len(pos) < 1 {
		exitCmdUsage("secure send", "missing file argument")
	}
	addr := ""
	if len(pos) >= 2 {
		addr = pos[1]
	}
	longTermSec := uint32(0)
	if *secureLongTerm != "" {
		sec, err := parseLongTermDuration(*secureLongTerm)
		if err != nil {
			exitCmdError("secure send", err)
		}
		longTermSec = sec
	}
	sendPath, cleanup, err := prepareSendPath(pos[0], *secureZip)
	if err != nil {
		exitCmdError("secure send", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if err := runClientSecureSend(sendPath, addr, *secureServerID, longTermSec); err != nil {
		exitCmdError("secure send", err)
	}
}

func runServersCommand(args []string) {
	fs := newCommandFlagSet("servers", printServersUsage)
	help, err := fs.Parse(args, commonLongFlags)
	if help {
		exitHelp(printServersUsage)
	}
	if err != nil {
		exitCmdUsage("servers", "%s", err)
	}
	if fs.NArg() > 0 {
		exitCmdUsage("servers", "unexpected argument: %s", fs.Arg(0))
	}
	if err := runClientServers(); err != nil {
		exitCmdError("servers", err)
	}
}

// parseLongTermDuration parses e.g. "7d", "24h" to seconds. Max 30 days. Returns 0 if invalid.
func parseLongTermDuration(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var mult time.Duration
	if strings.HasSuffix(s, "d") {
		mult = 24 * time.Hour
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "h") {
		mult = time.Hour
		s = s[:len(s)-1]
	} else {
		return 0, fmt.Errorf("long-term: use e.g. 7d or 24h")
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("long-term: positive number required")
	}
	d := time.Duration(n) * mult
	max := 30 * 24 * time.Hour
	if d > max {
		d = max
	}
	return uint32(d.Seconds()), nil
}
