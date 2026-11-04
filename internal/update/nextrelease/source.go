// Package nextrelease validates releases from the fixed Magpie Next repository.
// The parent updater downloads verified bare executables through its native
// staging, installation, rollback and restart path.
package nextrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

const repository = "xue-ding-e/Magpie-Next"
const downloadBase = "https://github.com/" + repository + "/releases/download/"

var hex40 = regexp.MustCompile(`^[a-f0-9]{40}$`)
var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-next\.([1-9][0-9]*)$`)

// Manifest is the unsigned Windows x64 contract published by the Next workflow.
// Sequence is GITHUB_RUN_NUMBER, also the numeric suffix of Version. The release
// tag separately identifies SourceSHA. Published sequences must never be reused.
type Manifest struct {
	Schema     int     `json:"schema"`
	Repository string  `json:"repository"`
	Channel    string  `json:"channel"`
	Sequence   uint64  `json:"sequence"`
	Version    string  `json:"version"`
	SourceSHA  string  `json:"source_sha"`
	Platform   string  `json:"platform"`
	Integrity  string  `json:"integrity"` // exactly "sha256-unsigned", not a signature
	Assets     []Asset `json:"assets"`
}

// Asset describes a bare executable, not the old portable ZIP.
type Asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// PublishedAsset is metadata from the same GitHub release as the manifest.
type PublishedAsset struct {
	Name string
	URL  string
	Size int64
}

// Record is supplied by a transport after resolving the release's tag to
// its commit (target_commitish may be a branch and is not sufficient). Manifest
// bytes must be from that release's manifest.json, not a separate mutable feed.
// ReadReleaseRecord implements this bounded metadata-only transport without
// trusting network URLs supplied by the manifest. It does not verify publisher
// signatures. No record is an instruction to install anything.
type Record struct {
	Tag        string
	Notes      string
	CommitSHA  string
	Draft      bool
	Prerelease bool
	Manifest   []byte
	Assets     []PublishedAsset
}

// Identity pins the exact manifest accepted for a channel. Its digest binds all
// platforms' asset identities in this contract, not merely a display version.
type Identity struct {
	Channel        string
	Sequence       uint64
	Version        string
	SourceSHA      string
	ManifestSHA256 string
}

// Candidate binds an executable's hashes and sizes to a resolved source commit.
type Candidate struct {
	Identity Identity
	Manifest Manifest
	Tag      string
	Notes    string
}

// State distinguishes a missing high-water file from a failed read. The parent
// persists HighWater before offering a release; installation and restart failures
// never lower it. Installed is optional: native callers compare their running
// version separately. An accepted but uninstalled candidate remains retryable.
type State struct {
	ReadOK           bool
	InstalledVersion string
	InstalledSHA     string
	Installed        *Identity
	HighWater        *Identity // highest accepted, possibly not installed yet
}

// Page returns materialised metadata from the fixed own repository and reports
// failed reads as errors. Full scans return more=true until exhaustion. The native
// stable updater supplies the single LatestRecord, protected by its high-water
// mark, instead of fetching the manifest of every historical release on each poll.
type Page func(context.Context, string) ([]Record, bool, error)

// Select scans every page, including prereleases (GitHub releases/latest omits
// them). Stable and preview are separate explicit channels. Partial scans fail
// closed instead of claiming an old page is the latest release.
func Select(ctx context.Context, channel string, state State, page Page) (*Candidate, error) {
	if channel != "stable" && channel != "preview" {
		return nil, errors.New("unknown Next channel")
	}
	if page == nil || !state.ReadOK {
		return nil, errors.New("unknown installation or update state; use manual update")
	}
	if state.Installed == nil && (state.InstalledSHA != "" || state.InstalledVersion != "") {
		if !knownLegacy(state.InstalledSHA) || state.InstalledVersion != tagFor(state.InstalledSHA) {
			return nil, errors.New("unknown legacy build; use manual update")
		}
	} else if state.Installed != nil && (!validIdentity(*state.Installed, channel) || state.Installed.SourceSHA != state.InstalledSHA || state.Installed.Version != state.InstalledVersion) {
		return nil, errors.New("invalid installed identity")
	}
	if state.HighWater != nil && !validIdentity(*state.HighWater, channel) {
		return nil, errors.New("invalid update high-water mark")
	}
	if state.Installed != nil && (state.HighWater == nil || state.HighWater.Sequence < state.Installed.Sequence || (state.HighWater.Sequence == state.Installed.Sequence && *state.HighWater != *state.Installed)) {
		return nil, errors.New("accepted state is older than or conflicts with the installation")
	}
	var best *Candidate
	var sequences = make(map[uint64]Identity)
	var versions = make(map[string]Identity)
	for _, id := range []*Identity{state.Installed, state.HighWater} {
		if id == nil {
			continue
		}
		if old, ok := versions[id.Version]; ok && old != *id {
			return nil, errors.New("accepted state reuses the installed version")
		}
		sequences[id.Sequence], versions[id.Version] = *id, *id
	}
	for n := 1; n <= 100; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", repository, n)
		records, more, err := page(ctx, url)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err != nil {
			return nil, fmt.Errorf("Next release page %d: %w", n, err)
		}
		for _, r := range records {
			if r.Draft || r.Prerelease != (channel == "preview") {
				continue
			}
			// ZIP-only releases predate self-updates and remain manual-only.
			if len(r.Manifest) == 0 {
				continue
			}
			c, err := Validate(r, channel)
			if err != nil {
				return nil, err
			}
			id := c.Identity
			if old, ok := sequences[id.Sequence]; ok && old != id {
				return nil, errors.New("Next sequence changed identity")
			}
			if old, ok := versions[id.Version]; ok && old != id {
				return nil, errors.New("Next version changed identity")
			}
			sequences[id.Sequence], versions[id.Version] = id, id
			if state.HighWater != nil && id.Sequence < state.HighWater.Sequence {
				continue
			}
			if id.SourceSHA == state.InstalledSHA || (state.Installed != nil && id.Sequence <= state.Installed.Sequence) {
				continue
			}
			if best == nil || id.Sequence > best.Identity.Sequence {
				best = c
			}
		}
		if !more {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return best, nil
		}
	}
	return nil, errors.New("incomplete Next release scan; no update selected")
}

func tagFor(sha string) string {
	if len(sha) < 12 {
		return ""
	}
	return "next-" + sha[:12]
}

func knownLegacy(sha string) bool {
	return sha == "d65e4026f0856215a35232fabc4cbb3f98fa52d7" || sha == "2aa4fe3bd4033efaad972ded01ecfb964aca7777"
}

func validIdentity(id Identity, channel string) bool {
	return id.Channel == channel && id.Sequence > 0 && VersionSequence(id.Version) == id.Sequence && hex40.MatchString(id.SourceSHA) && hex64.MatchString(id.ManifestSHA256)
}

// VersionSequence returns the workflow sequence in a released Next version,
// or zero for source builds, legacy tags and other distributions.
func VersionSequence(version string) uint64 {
	parts := versionPattern.FindStringSubmatch(version)
	if parts == nil {
		return 0
	}
	n, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Validate binds manifest, resolved tag, repository, platform and release assets.
// It verifies metadata consistency, not executable bytes or a publisher signature.
func Validate(r Record, channel string) (*Candidate, error) {
	if channel != "stable" && channel != "preview" || r.Draft || r.Prerelease != (channel == "preview") {
		return nil, errors.New("release is not on the requested Next channel")
	}
	if len(r.Manifest) == 0 || len(r.Manifest) > 64*1024 {
		return nil, errors.New("missing or oversized Next manifest")
	}
	if err := uniqueJSON(r.Manifest); err != nil {
		return nil, err
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(r.Manifest))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("Next manifest: %w", err)
	}
	if m.Schema != 1 || m.Repository != repository || m.Channel != channel || m.Sequence == 0 || VersionSequence(m.Version) != m.Sequence || m.Platform != "windows-amd64" || m.Integrity != "sha256-unsigned" || !hex40.MatchString(m.SourceSHA) || m.SourceSHA != r.CommitSHA || r.Tag != tagFor(m.SourceSHA) {
		return nil, errors.New("Next manifest identity mismatch")
	}
	if len(m.Assets) != 2 {
		return nil, errors.New("Next manifest needs both bare Windows executables")
	}
	var published = make(map[string]PublishedAsset)
	for _, a := range r.Assets {
		if _, exists := published[a.Name]; exists {
			return nil, errors.New("duplicate release asset")
		}
		published[a.Name] = a
	}
	manifestAsset, ok := published["manifest.json"]
	if !ok || manifestAsset.Size != int64(len(r.Manifest)) || manifestAsset.URL != downloadBase+r.Tag+"/manifest.json" {
		return nil, errors.New("manifest is not bound to this release")
	}
	var names = make(map[string]bool)
	for _, a := range m.Assets {
		if (a.Name != "Magpie-Next.exe" && a.Name != "magpie-next-cli.exe") || names[a.Name] || a.Size <= 0 || !hex64.MatchString(a.SHA256) {
			return nil, errors.New("invalid Next executable metadata")
		}
		names[a.Name] = true
		p, ok := published[a.Name]
		if !ok || p.Size != a.Size || p.URL != downloadBase+r.Tag+"/"+a.Name {
			return nil, errors.New("Next executable is missing or from another release")
		}
	}
	hash := sha256.Sum256(r.Manifest)
	var c Candidate
	c.Manifest = m
	c.Tag, c.Notes = r.Tag, r.Notes
	c.Identity = Identity{Channel: channel, Sequence: m.Sequence, Version: m.Version, SourceSHA: m.SourceSHA, ManifestSHA256: hex.EncodeToString(hash[:])}
	return &c, nil
}

// uniqueJSON enforces exact schema keys before encoding/json's case-insensitive
// struct matching can treat Sequence as sequence. It also rejects duplicate keys,
// nested values in scalar fields, and trailing documents.
func uniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(string) error
	walk = func(kind string) error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, object := t.(json.Delim)
		if kind == "scalar" {
			if object {
				return errors.New("expected scalar manifest field")
			}
			return nil
		}
		if !object || (kind == "assets" && delim != '[') || (kind != "assets" && delim != '{') {
			return errors.New("invalid Next manifest shape")
		}
		var keys = make(map[string]bool)
		for d.More() {
			next := "asset"
			if kind != "assets" {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] || !schemaKey(kind, name) {
					return errors.New("duplicate or unknown Next manifest key")
				}
				keys[name] = true
				next = "scalar"
				if kind == "manifest" && name == "assets" {
					next = "assets"
				}
			}
			if err := walk(next); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	if err := walk("manifest"); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing Next manifest data")
	}
	return nil
}

func schemaKey(kind, name string) bool {
	if kind == "asset" {
		return name == "name" || name == "size" || name == "sha256"
	}
	switch name {
	case "schema", "repository", "channel", "sequence", "version", "source_sha", "platform", "integrity", "assets":
		return true
	}
	return false
}
