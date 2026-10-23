// Command adminserver serves www.steven3zx.com/admin: username/password
// sign-in backed by a local Turso database, the React dashboard, and an
// authenticated proxy to the host agent's snapshot.
//
//	adminserver               run the web server
//	adminserver useradd NAME  create an admin user (password read from the terminal)
//	adminserver passwd NAME   change a user's password and sign out their sessions
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	dbPath, dbKey := env("ADMIN_DB", "data/admin.db"), os.Getenv("ADMIN_DB_KEY")
	migrated, err := encryptPlainStore(dbPath, dbKey)
	if err != nil {
		log.Fatalf("encrypt database: %v", err)
	}
	if migrated {
		log.Printf("encrypted %s; the plain copy is kept at %s%s", dbPath, dbPath, plainBackupSuffix)
	}
	st, err := openStore(dbPath, dbKey)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	if len(os.Args) > 1 {
		if err := runCommand(st, os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	token := os.Getenv("HOST_AGENT_TOKEN")
	if len(token) < 32 {
		log.Fatal("HOST_AGENT_TOKEN must be set to at least 32 characters")
	}
	s := &server{
		store: st,
		// Per-IP is strict; per-user is looser so a stranger can't easily lock you out.
		ipLimiter:    newLimiter(5, 15*time.Minute),
		userLimiter:  newLimiter(20, 15*time.Minute),
		agentURL:     strings.TrimRight(env("HOST_AGENT_URL", "http://host.docker.internal:9180"), "/"),
		agentToken:   token,
		webDir:       env("ADMIN_WEB_DIR", "web"),
		cookieSecure: env("ADMIN_COOKIE_SECURE", "true") == "true",
		agent:        &http.Client{Timeout: 5 * time.Second},
		agentStream:  &http.Client{},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{
		Addr:              env("ADMIN_ADDR", ":8091"),
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("admin server listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func runCommand(st *store, args []string) error {
	if len(args) != 2 || (args[0] != "useradd" && args[0] != "passwd") {
		return errors.New("usage: adminserver useradd|passwd USERNAME")
	}
	username := strings.TrimSpace(args[1])
	if username == "" {
		return errors.New("username is required")
	}
	password, err := readNewPassword()
	if err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if args[0] == "useradd" {
		if err := st.createUser(ctx, username, hash); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		fmt.Printf("created user %q\n", username)
		return nil
	}
	if err := st.setPassword(ctx, username, hash); err != nil {
		if errors.Is(err, errNotFound) {
			return fmt.Errorf("no user named %q", username)
		}
		return err
	}
	fmt.Printf("updated password for %q and signed out its sessions\n", username)
	return nil
}

// readNewPassword prompts twice without echo on a terminal, or reads one
// line from stdin when piped.
func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	p1, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat password: ")
	p2, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords do not match")
	}
	return string(p1), nil
}
