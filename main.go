package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gofrs/flock"
	"ntty/internal/notion"
	"ntty/internal/store"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "upgrade", "update":
			if err := runUpgrade(os.Args[2:]); err != nil {
				fatal(err)
			}
			return
		case "version", "--version", "-version":
			fmt.Println("ntty " + versionString())
			return
		}
	}
	demo := flag.Bool("demo", false, "local playground; makes no Notion calls")
	parent := flag.String("parent", "", "default notebook: page:<id> or data-source:<id>")
	profile := flag.String("profile", "default", "local cache namespace (use one per Notion workspace)")
	dataDir := flag.String("data-dir", "", "override local cache/draft directory")
	check := flag.Bool("check", false, "check ntn connectivity without opening the UI (read-only)")
	flag.Parse()
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(*profile) {
		fatal(fmt.Errorf("invalid profile name"))
	}
	if *parent != "" && !regexp.MustCompile(`^(page|data-source):[a-fA-F0-9-]{32,36}$`).MatchString(*parent) {
		fatal(fmt.Errorf("use --parent page:<uuid> or data-source:<uuid>"))
	}
	if !*demo {
		if _, err := exec.LookPath("ntn"); err != nil {
			fatal(fmt.Errorf("ntn is required: install from https://ntn.dev, then run ntn login"))
		}
	}
	if *dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fatal(err)
		}
		name := *profile
		if *demo {
			name += "-demo"
		}
		*dataDir = filepath.Join(base, "ntty", name)
		migrateDataDir(filepath.Join(base, "ntn-tui", name), *dataDir)
	}
	s, err := store.Open(*dataDir)
	if err != nil {
		fatal(err)
	}
	lock := flock.New(filepath.Join(s.Dir, "app.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		fatal(err)
	}
	if !ok {
		fatal(fmt.Errorf("this profile is already open; close the other ntty window first"))
	}
	defer lock.Unlock()
	state, err := s.LoadState()
	if err != nil {
		fatal(err)
	}
	drafts, err := s.Drafts()
	if err != nil {
		fatal(err)
	}
	var backend notion.Backend = notion.NewClient()
	if *demo {
		cached := append([]notion.Doc{}, drafts...)
		for _, p := range state.Pages {
			if doc, err := s.LoadDoc(p.ID); err == nil {
				cached = append(cached, doc)
			}
		}
		backend = notion.NewDemo(cached)
	}
	if *check {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		l, err := backend.Search(ctx, "", "")
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Connected. %d recent pages/data sources; more=%v.\n", len(l.Pages), l.Cursor != "")
		fmt.Printf("Drafts: %s\nRequests: serialized, at least 650ms apart. Autosave: 3s idle; clean-page refresh adapts to activity and changes; transient failures retry safely.\n", s.Dir)
		return
	}
	a := newApp(backend, s, state, drafts, *parent, *demo)
	if err := a.run(); err != nil {
		fatal(err)
	}
	fmt.Printf("Local drafts: %s\n", s.Dir)
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "ntty:", err); os.Exit(1) }

// migrateDataDir moves local drafts from the pre-rename ntn-tui directory once.
func migrateDataDir(oldDir, newDir string) {
	if _, err := os.Stat(newDir); err == nil {
		return
	}
	if _, err := os.Stat(oldDir); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0700); err != nil {
		fatal(err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		fatal(fmt.Errorf("move local data from %s: %w", oldDir, err))
	}
	fmt.Fprintln(os.Stderr, "ntty: moved local data to", newDir)
}
