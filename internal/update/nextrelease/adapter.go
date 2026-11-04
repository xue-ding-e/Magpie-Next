package nextrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	maxRecordBytes    = 6 * 1024 * 1024
	maxGitObjectBytes = 256 * 1024
	maxRecordRequests = 15
	maxTagDepth       = 4
	maxManifestBytes  = 64 * 1024
)

var nextTag = regexp.MustCompile(`^next-[a-f0-9]{12}$`)

// ReadReleaseRecord materializes one manifest-bearing release observation from
// the fixed own repository. It resolves the actual Git tag to a commit, reads
// only manifest.json, checks API-declared sizes/digests, and applies Validate.
// Old ZIP-only releases remain manual-only. Asset requests may follow GitHub's
// HTTPS redirects to its release CDN; metadata requests never follow redirects.
// No executable, mutable feed URL, cookie or credential is fetched here.
//
// Re-reading release/ref identities detects observed changes, not an atomic or
// immutable snapshot (ABA changes and changes after the last read are possible).
// GitHub's digest and Git object replies are API assertions, not independently
// verified publisher signatures. Executable digests are compared as metadata;
// their actual bytes are not checked here. A Record is never permission to
// install; the parent updater validates and persists the selected identity.
func ReadReleaseRecord(ctx context.Context, observed ReleaseMetadata, channel string) (Record, error) {
	client := newMetadataClient()
	defer client.CloseIdleConnections()
	return readReleaseRecord(ctx, client, observed, channel)
}

// LatestRecord reads GitHub's latest stable release, which the Next workflow
// publishes only after source/hash readback. Sequence validation and the native
// high-water mark reject an older release being marked latest later.
func LatestRecord(ctx context.Context, client *http.Client) (Record, error) {
	var reader = recordReader{client: metadataClient(client), remaining: maxRecordBytes}
	raw, err := reader.get(ctx, "/releases/latest", "application/vnd.github+json", maxReleasePage)
	if err != nil {
		return Record{}, err
	}
	metadata, err := decodeReleasePage(append(append([]byte{'['}, raw...), ']'))
	if err != nil || len(metadata) != 1 {
		return Record{}, errors.New("invalid latest Next release")
	}
	return readReleaseRecord(ctx, reader.client, metadata[0], "stable")
}

type recordReader struct {
	client    *http.Client
	remaining int
	requests  int
}

type releaseSnapshot struct {
	metadata ReleaseMetadata
	digests  map[string]string
}

type gitTarget struct {
	kind string
	sha  string
}

type resolvedTag struct {
	commit string
	chain  []gitTarget
}

func readReleaseRecord(ctx context.Context, client *http.Client, observed ReleaseMetadata, channel string) (Record, error) {
	var empty Record
	if ctx == nil || observed.ID <= 0 || !nextTag.MatchString(observed.Tag) || observed.Draft ||
		(channel != "stable" && channel != "preview") || observed.Prerelease != (channel == "preview") {
		return empty, errors.New("invalid Next release observation or channel")
	}
	ctx, cancel := context.WithTimeout(ctx, releaseScanLimit)
	defer cancel()
	var reader = recordReader{client: client, remaining: maxRecordBytes}
	first, err := reader.release(ctx, observed.ID)
	if err != nil {
		return empty, err
	}
	if !sameRelease(first.metadata, observed) {
		return empty, errors.New("Next release no longer matches its listing observation")
	}
	ref, err := reader.reference(ctx, observed.Tag)
	if err != nil {
		return empty, err
	}
	resolved, err := reader.resolve(ctx, observed.Tag, ref)
	if err != nil {
		return empty, err
	}
	if tagFor(resolved.commit) != observed.Tag {
		return empty, errors.New("Next tag name does not identify its resolved commit")
	}
	var record = Record{Tag: observed.Tag, Notes: first.metadata.Notes, CommitSHA: resolved.commit, Draft: observed.Draft, Prerelease: observed.Prerelease}
	var manifest AssetMetadata
	for _, asset := range first.metadata.Assets {
		record.Assets = append(record.Assets, PublishedAsset{Name: asset.Name, URL: asset.URL, Size: asset.Size})
		if asset.Name == "manifest.json" {
			manifest = asset
		}
	}
	if manifest.ID <= 0 || manifest.Size <= 0 || manifest.Size > maxManifestBytes {
		return empty, errors.New("missing or oversized Next manifest; use manual update")
	}
	record.Manifest, err = reader.get(ctx, "/releases/assets/"+strconv.FormatInt(manifest.ID, 10), "application/octet-stream", int(manifest.Size))
	if err != nil {
		return empty, err
	}
	hash := sha256.Sum256(record.Manifest)
	if int64(len(record.Manifest)) != manifest.Size || (first.digests["manifest.json"] != "" && hex.EncodeToString(hash[:]) != first.digests["manifest.json"]) {
		return empty, errors.New("Next manifest bytes differ from release asset size or digest")
	}
	candidate, err := Validate(record, channel)
	if err != nil {
		return empty, err
	}
	for _, asset := range candidate.Manifest.Assets {
		if digest := first.digests[asset.Name]; digest != "" && digest != asset.SHA256 {
			return empty, errors.New("Next executable digest differs between API and manifest metadata")
		}
	}
	last, err := reader.release(ctx, observed.ID)
	if err != nil {
		return empty, err
	}
	if !sameRelease(first.metadata, last.metadata) || !sameDigests(first.digests, last.digests) {
		return empty, errors.New("Next release metadata changed during materialization")
	}
	lastRef, err := reader.reference(ctx, observed.Tag)
	if err != nil {
		return empty, err
	}
	if lastRef != ref {
		return empty, errors.New("Next Git tag moved during materialization")
	}
	lastResolved, err := reader.resolve(ctx, observed.Tag, lastRef)
	if err != nil {
		return empty, err
	}
	if lastResolved.commit != resolved.commit || !slices.Equal(lastResolved.chain, resolved.chain) {
		return empty, errors.New("Next annotated tag chain changed during materialization")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return record, nil
}

// get accepts only paths assembled by this adapter from checked IDs/tags/SHAs.
// No URL received from JSON, a manifest or Location is passed here.
func (r *recordReader) get(ctx context.Context, path, accept string, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit = min(limit, r.remaining)
	if limit <= 0 || r.requests >= maxRecordRequests {
		return nil, errors.New("Next record request or byte limit reached")
	}
	r.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Magpie-Next-release-metadata")
	requestClient := r.client
	if accept == "application/octet-stream" {
		// A public asset API normally answers 302. Follow only the release CDN,
		// with no cookie jar or credentials, using the same proxy-aware transport.
		copy := *r.client
		copy.Jar = nil
		copy.CheckRedirect = AssetRedirect
		requestClient = &copy
	}
	resp, err := requestClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Next record HTTP status %d; no record accepted", resp.StatusCode)
	}
	if resp.ContentLength > int64(limit) {
		return nil, errors.New("Next record response exceeds byte limit")
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, errors.New("unexpected Next record content encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("Next record response exceeds byte limit")
	}
	r.remaining -= len(raw)
	return raw, ctx.Err()
}

// AssetRedirect permits public GitHub asset redirects only to its HTTPS release
// hosts. The caller still verifies size and SHA-256 before using the body.
func AssetRedirect(req *http.Request, via []*http.Request) error {
	u := req.URL
	if len(via) > 3 || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" ||
		(u.Host != "release-assets.githubusercontent.com" && u.Host != "objects.githubusercontent.com") {
		return errors.New("Next asset redirected outside GitHub's release CDN")
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
		req.Header.Del(name)
	}
	return nil
}

func (r *recordReader) release(ctx context.Context, id int64) (releaseSnapshot, error) {
	var snapshot releaseSnapshot
	raw, err := r.get(ctx, "/releases/"+strconv.FormatInt(id, 10), "application/vnd.github+json", maxReleasePage)
	if err != nil {
		return snapshot, err
	}
	// Reuse the listing's exact-key, duplicate and own-repository validators;
	// wrapping a single response does not accept trailing JSON documents.
	list, err := decodeReleasePage(append(append([]byte{'['}, raw...), ']'))
	if err != nil || len(list) != 1 || list[0].ID != id {
		return snapshot, errors.New("invalid Next release response")
	}
	snapshot.metadata = list[0]
	fields, err := metadataObject(raw)
	if err != nil {
		return snapshot, err
	}
	var assets []json.RawMessage
	if err := metadataField(fields, "assets", &assets); err != nil {
		return snapshot, err
	}
	snapshot.digests = make(map[string]string)
	for _, rawAsset := range assets {
		fields, err := metadataObject(rawAsset)
		if err != nil {
			return snapshot, err
		}
		var name string
		if err := metadataField(fields, "name", &name); err != nil {
			return snapshot, err
		}
		if name != "manifest.json" && name != "Magpie-Next.exe" && name != "magpie-next-cli.exe" {
			continue
		}
		// GitHub can omit digest on older assets. The manifest still binds both
		// executable hashes; check the API digest too whenever it is available.
		var digest string
		if raw := fields["digest"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &digest) != nil || !strings.HasPrefix(digest, "sha256:") || !hex64.MatchString(strings.TrimPrefix(digest, "sha256:")) {
				return snapshot, errors.New("invalid Next asset digest")
			}
		}
		snapshot.digests[name] = strings.TrimPrefix(digest, "sha256:")
	}
	if len(snapshot.digests) != 3 {
		return snapshot, errors.New("Next release lacks the manifest and both bare executables; use manual update")
	}
	return snapshot, nil
}

func (r *recordReader) reference(ctx context.Context, tag string) (gitTarget, error) {
	var empty gitTarget
	raw, err := r.get(ctx, "/git/ref/tags/"+tag, "application/vnd.github+json", maxGitObjectBytes)
	if err != nil {
		return empty, err
	}
	fields, err := metadataObject(raw)
	if err != nil {
		return empty, err
	}
	var ref, apiURL string
	if metadataField(fields, "ref", &ref) != nil || ref != "refs/tags/"+tag ||
		metadataField(fields, "url", &apiURL) != nil || apiURL != githubAPIBase+"/git/refs/tags/"+tag {
		return empty, errors.New("Next Git reference identity mismatch")
	}
	return decodeGitTarget(fields["object"])
}

func (r *recordReader) resolve(ctx context.Context, tag string, target gitTarget) (resolvedTag, error) {
	var resolved resolvedTag
	var seen = make(map[string]bool)
	for depth := 0; target.kind == "tag"; depth++ {
		if depth >= maxTagDepth || seen[target.sha] {
			return resolvedTag{}, errors.New("Next annotated tag chain is cyclic or too deep")
		}
		resolved.chain = append(resolved.chain, target)
		seen[target.sha] = true
		raw, err := r.get(ctx, "/git/tags/"+target.sha, "application/vnd.github+json", maxGitObjectBytes)
		if err != nil {
			return resolvedTag{}, err
		}
		fields, err := checkedGitObject(raw, "tags", target.sha)
		if err != nil {
			return resolvedTag{}, err
		}
		var name string
		if metadataField(fields, "tag", &name) != nil || !metadataSegment(name) || (depth == 0 && name != tag) {
			return resolvedTag{}, errors.New("Next annotated tag name mismatch")
		}
		target, err = decodeGitTarget(fields["object"])
		if err != nil {
			return resolvedTag{}, err
		}
	}
	if target.kind != "commit" {
		return resolvedTag{}, errors.New("Next tag does not resolve to a commit")
	}
	raw, err := r.get(ctx, "/git/commits/"+target.sha, "application/vnd.github+json", maxGitObjectBytes)
	if err != nil {
		return resolvedTag{}, err
	}
	if _, err := checkedGitObject(raw, "commits", target.sha); err != nil {
		return resolvedTag{}, err
	}
	resolved.commit = target.sha
	resolved.chain = append(resolved.chain, target)
	return resolved, nil
}

func checkedGitObject(raw []byte, kind, sha string) (map[string]json.RawMessage, error) {
	fields, err := metadataObject(raw)
	if err != nil {
		return nil, err
	}
	var gotSHA, apiURL string
	if metadataField(fields, "sha", &gotSHA) != nil || gotSHA != sha ||
		metadataField(fields, "url", &apiURL) != nil || apiURL != githubAPIBase+"/git/"+kind+"/"+sha {
		return nil, errors.New("Next Git object identity mismatch")
	}
	return fields, nil
}

func decodeGitTarget(raw []byte) (gitTarget, error) {
	var target gitTarget
	fields, err := metadataObject(raw)
	if err != nil {
		return target, err
	}
	var apiURL string
	if metadataField(fields, "type", &target.kind) != nil || (target.kind != "tag" && target.kind != "commit") ||
		metadataField(fields, "sha", &target.sha) != nil || !hex40.MatchString(target.sha) ||
		metadataField(fields, "url", &apiURL) != nil || apiURL != githubAPIBase+"/git/"+target.kind+"s/"+target.sha {
		return gitTarget{}, errors.New("invalid Next Git target")
	}
	return target, nil
}

func sameRelease(a, b ReleaseMetadata) bool {
	if a.ID != b.ID || a.Tag != b.Tag || a.Draft != b.Draft || a.Prerelease != b.Prerelease || len(a.Assets) != len(b.Assets) {
		return false
	}
	var assets = make(map[int64]AssetMetadata)
	for _, asset := range a.Assets {
		assets[asset.ID] = asset
	}
	for _, asset := range b.Assets {
		if expected, ok := assets[asset.ID]; !ok || expected != asset {
			return false
		}
		delete(assets, asset.ID)
	}
	return len(assets) == 0
}

func sameDigests(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for name, digest := range a {
		if b[name] != digest {
			return false
		}
	}
	return true
}
