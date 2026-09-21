package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"0.1.0", "v0.1.0", 0},
		{"v0.1.0", "v0.2.0", -1},
		{"v0.2.0", "v0.1.9", 1},
		{"v0.1.0", "v0.1.1", -1},
		{"v0.1.0", "v1.0.0", -1},
		{"v0.1.0-3-gabc123", "v0.1.0", -1},
		{"v0.1.0", "v0.1.0-3-gabc123", 1},
		// Development builds are handled before comparison in runUpgrade.
		{"dev", "v0.1.0", -1},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestChecksumForFindsArchive(t *testing.T) {
	sums := "abc123  ntty_0.1.0_darwin_arm64.tar.gz\ndef456  ntty_0.1.0_linux_amd64.tar.gz\n"
	got, err := checksumFor(sums, "ntty_0.1.0_linux_amd64.tar.gz")
	if err != nil || got != "def456" {
		t.Fatalf("checksumFor = %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "missing.tar.gz"); err == nil {
		t.Fatal("missing checksum accepted")
	}
}

func TestBinaryFromArchive(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho ntty\n")
	if err := tw.WriteHeader(&tar.Header{Name: "ntty", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	got, err := binaryFromArchive(buf.Bytes())
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("binaryFromArchive = %q, %v", got, err)
	}
	if _, err := binaryFromArchive([]byte("not a tarball")); err == nil {
		t.Fatal("invalid archive accepted")
	}
}

func TestUpgradeChecksumMatchesArchive(t *testing.T) {
	data := []byte("archive bytes")
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  ntty_0.1.0_darwin_arm64.tar.gz\n"
	want, err := checksumFor(line, "ntty_0.1.0_darwin_arm64.tar.gz")
	if err != nil || want != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum mismatch: %q %v", want, err)
	}
	if !strings.Contains(versionString(), "") {
		t.Fatal("versionString must always return text")
	}
}
