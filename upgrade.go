package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// version is replaced at build time:
//
//	go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

const (
	repoSlug    = "netapy/ntty"
	upgradeHelp = `Usage: ntty upgrade [--check]

Downloads the latest release for this platform, verifies its checksum, and
replaces the running binary. --check only reports whether a newer version
exists.`
)

func versionString() string {
	if version == "" {
		return "dev"
	}
	return version
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
}

func (r release) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

func latestRelease(ctx context.Context) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub releases: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return release{}, err
	}
	if r.Tag == "" {
		return release{}, errors.New("GitHub releases: no published release")
	}
	return r, nil
}

// compareVersions orders dotted numeric versions. A version with a suffix
// (for example a git describe string) sorts below its plain release.
func compareVersions(a, b string) int {
	split := func(v string) ([]int, string) {
		v = strings.TrimPrefix(v, "v")
		main, suffix, _ := strings.Cut(v, "-")
		parts := strings.Split(main, ".")
		numbers := make([]int, len(parts))
		for i, p := range parts {
			n, _ := strconv.Atoi(p)
			numbers[i] = n
		}
		return numbers, suffix
	}
	an, as := split(a)
	bn, bs := split(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var av, bv int
		if i < len(an) {
			av = an[i]
		}
		if i < len(bn) {
			bv = bn[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	switch {
	case as == bs:
		return 0
	case as == "":
		return 1
	case bs == "":
		return -1
	case as < bs:
		return -1
	default:
		return 1
	}
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func checksumFor(checksums, name string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// binaryFromArchive extracts the ntty executable from a release tarball.
func binaryFromArchive(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the release archive does not contain ntty")
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "ntty" {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, 64<<20))
	}
}

func runUpgrade(args []string) error {
	checkOnly := false
	for _, a := range args {
		switch a {
		case "--check", "-n":
			checkOnly = true
		case "-h", "--help":
			fmt.Println(upgradeHelp)
			return nil
		default:
			return fmt.Errorf("unknown option %q\n\n%s", a, upgradeHelp)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	latest, err := latestRelease(ctx)
	if err != nil {
		return err
	}
	current := versionString()
	fmt.Printf("ntty %s\nlatest %s\n", current, latest.Tag)
	if current != "dev" && compareVersions(current, latest.Tag) >= 0 {
		fmt.Println("Already up to date.")
		return nil
	}
	if checkOnly {
		if current == "dev" {
			fmt.Println("This is a development build; run the installer to use a release.")
			return nil
		}
		fmt.Println("A newer release is available: ntty upgrade")
		return nil
	}
	if current == "dev" {
		return errors.New("this is a development build; run the installer or `git pull && make install` instead")
	}
	version := strings.TrimPrefix(latest.Tag, "v")
	archiveName := fmt.Sprintf("ntty_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	checksumName := fmt.Sprintf("ntty_%s_checksums.txt", version)
	archive, ok := latest.asset(archiveName)
	if !ok {
		return fmt.Errorf("release %s has no %s", latest.Tag, archiveName)
	}
	checksums, ok := latest.asset(checksumName)
	if !ok {
		return fmt.Errorf("release %s has no %s", latest.Tag, checksumName)
	}
	fmt.Println("Downloading", archiveName)
	data, err := download(ctx, archive.URL)
	if err != nil {
		return err
	}
	sums, err := download(ctx, checksums.URL)
	if err != nil {
		return err
	}
	want, err := checksumFor(string(sums), archiveName)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	binary, err := binaryFromArchive(data)
	if err != nil {
		return err
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".ntty-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w (reinstall with the installer if it needs elevated permissions)", dir, err)
	}
	stagedName := staged.Name()
	defer os.Remove(stagedName)
	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Chmod(0o755); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Rename(stagedName, target); err != nil {
		return err
	}
	fmt.Printf("Upgraded %s to %s. Restart ntty to use it.\n", target, latest.Tag)
	return nil
}
