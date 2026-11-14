package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/steady"
	"github.com/yetone/magpie/internal/update/nextrelease"
)

// Source and release builds of this fork use their own reviewed releases.
const nextDistribution = true

// Site is the Next release page, also the destination for manual-only builds.
const Site = "https://github.com/xue-ding-e/Magpie-Next/releases"
const nextFeed = "https://api.github.com/repos/xue-ding-e/Magpie-Next/releases/latest"

// The accepted manifest is kept in Next's own file, shared by GUI and CLI.
// Native file locks serialize concurrent checks without locking during network I/O.
var nextStatePath = func() string { return filepath.Join(appdir.Config(), "next-update-stable.json") }

func latestNext(ctx context.Context, lang string) (*Release, error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return nil, errors.New("Magpie Next self-updates are published for Windows x64 only")
	}
	record, err := nextrelease.LatestRecord(ctx, client)
	if err != nil {
		return nil, err
	}
	path := nextStatePath()
	unlock, held := filelock.LockContext(ctx, path+".lock", filelock.Wait)
	if !held {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("cannot lock Next update state; retry the update check")
	}
	defer unlock()
	var state = nextrelease.State{ReadOK: true}
	raw, err := steady.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("Next update state: %w", err)
	}
	if err == nil {
		var accepted nextrelease.Identity
		if err := json.Unmarshal(raw, &accepted); err != nil {
			return nil, fmt.Errorf("Next update state: %w", err)
		}
		state.HighWater = &accepted
	}
	candidate, err := nextrelease.Select(ctx, "stable", state, func(context.Context, string) ([]nextrelease.Record, bool, error) {
		return []nextrelease.Record{record}, false, nil
	})
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, errors.New("latest Next release has no new update manifest or is older than the accepted release; ZIP-only releases require manual installation")
	}
	if state.HighWater == nil || *state.HighWater != candidate.Identity {
		raw, err := json.Marshal(candidate.Identity)
		if err != nil {
			return nil, err
		}
		if err := steady.WriteFile(path, raw, 0o600); err != nil {
			return nil, fmt.Errorf("save Next update state: %w", err)
		}
	}
	var release = Release{Version: candidate.Manifest.Version, Notes: InLang(candidate.Notes, lang),
		URL: Site + "/tag/" + candidate.Tag, Assets: make(map[string]Asset)}
	for _, asset := range candidate.Manifest.Assets {
		release.Assets[asset.Name] = Asset{URL: Site + "/download/" + candidate.Tag + "/" + asset.Name, Size: asset.Size, SHA256: asset.SHA256}
	}
	return &release, nil
}

func nextNotes(ctx context.Context, after, upto, lang string) ([]Note, error) {
	releases, err := nextrelease.ListWithClient(ctx, client)
	if err != nil {
		return nil, err
	}
	var notes []Note
	for _, release := range releases {
		// Titles are for notes only. Installation always reads the manifest and
		// resolves the immutable source tag instead of trusting display text.
		version := strings.TrimPrefix(release.Name, "Magpie Next ")
		if release.Draft || release.Prerelease || nextrelease.VersionSequence(version) == 0 {
			continue
		}
		notes = append(notes, Note{Version: version, Notes: InLang(release.Notes, lang), URL: Site + "/tag/" + release.Tag})
	}
	return between(notes, after, upto), nil
}
