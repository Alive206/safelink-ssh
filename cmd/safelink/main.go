// sshtunneld is a YAML-driven SSH tunnel daemon supporting -L / -R / -D
// forwarding modes simultaneously, with auto-reconnect, keepalive, and an
// embedded HTTP/UI control panel.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/example/sshtunneld/internal/daemon"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "passwd":
			os.Exit(runPasswd(os.Args[2:]))
		case "-h", "--help", "help":
			printUsage()
			return
		}
	}

	cfgPath := flag.String("config", "configs/sshtunneld.yaml", "path to YAML config file")
	noInit := flag.Bool("no-init", false, "do not auto-create the config file on first run")
	noOpen := flag.Bool("no-open", false, "do not open the browser at startup")
	flag.Parse()

	if err := daemon.RunWithOptions(*cfgPath, daemon.Options{
		AutoInit:    !*noInit,
		OpenBrowser: !*noOpen,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "sshtunneld:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  sshtunneld [-config PATH] [-no-init] [-no-open]
                                 run the daemon (default subcommand)
                                 first run auto-creates configs/sshtunneld.yaml
                                 with a random admin password and opens the UI
  sshtunneld passwd [USER]       prompt for a password and print
                                 a YAML user entry with a bcrypt hash`)
}

// runPasswd is a tiny helper to generate the bcrypt hash that goes into
// web.auth.users[].password_hash.  Reading from stdin (interactive terminal
// or piped) keeps the plaintext off the command line / shell history.
func runPasswd(args []string) int {
	user := ""
	if len(args) > 0 {
		user = args[0]
	}
	if user == "" {
		fmt.Fprint(os.Stderr, "Username: ")
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "read username:", err)
			return 1
		}
		user = strings.TrimSpace(line)
	}
	if user == "" {
		fmt.Fprintln(os.Stderr, "username is required")
		return 1
	}

	var pw []byte
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		var err error
		pw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read password:", err)
			return 1
		}
	} else {
		// Non-interactive: read a single line from stdin.
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			fmt.Fprintln(os.Stderr, "read password:", err)
			return 1
		}
		pw = []byte(strings.TrimRight(line, "\r\n"))
	}
	if len(pw) == 0 {
		fmt.Fprintln(os.Stderr, "password is required")
		return 1
	}

	hash, err := bcrypt.GenerateFromPassword(pw, 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcrypt:", err)
		return 1
	}

	fmt.Println("# Paste under web.auth.users in your YAML:")
	fmt.Printf("- username: %s\n", user)
	fmt.Printf("  password_hash: %q\n", string(hash))
	return 0
}
