package nextrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubAPIBase    = "https://api.github.com/repos/" + repository
	maxReleasePages  = 100
	maxReleasePage   = 2 * 1024 * 1024
	maxReleaseScan   = 16 * 1024 * 1024
	releaseScanLimit = time.Minute
	releaseReadLimit = 20 * time.Second
)

// ReleaseMetadata is an unverified observation, not a Record or an update
// candidate. In particular, GitHub's target_commitish is not a resolved tag.
// Listing does not resolve tags, fetch manifests or prove asset completeness.
type ReleaseMetadata struct {
	ID         int64
	Tag        string
	Name       string
	Notes      string
	Draft      bool
	Prerelease bool
	Assets     []AssetMetadata
}

// AssetMetadata describes an advertised asset without fetching its contents.
// Neither the listing nor an advertised digest authenticates a publisher.
type AssetMetadata struct {
	ID   int64
	Name string
	URL  string
	Size int64
}

// ListReleaseMetadata reads public release-list metadata from the fixed own
// repository without credentials, cookies or redirects. It returns no partial
// result on failure. The native updater supplies its proxy-aware client instead.
//
// Requests use consecutive page numbers until an empty page, never response
// URLs. A full page is not an end marker, nor is a short page. The scan has time,
// byte and page caps. Duplicate IDs/tags expose some concurrent changes, but
// GitHub's paginated listing is NOT an immutable or complete historical snapshot:
// removals, insertions and edits between reads can still escape detection.
func ListReleaseMetadata(ctx context.Context) ([]ReleaseMetadata, error) {
	client := newMetadataClient()
	defer client.CloseIdleConnections()
	return listReleaseMetadata(ctx, client)
}

// ListWithClient lists notes through the native updater's configured transport.
// Its feed is fixed; an environment-supplied update URL is never consulted.
func ListWithClient(ctx context.Context, client *http.Client) ([]ReleaseMetadata, error) {
	return listReleaseMetadata(ctx, metadataClient(client))
}

func metadataClient(client *http.Client) *http.Client {
	return &http.Client{Transport: client.Transport, Timeout: releaseReadLimit,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func newMetadataClient() *http.Client {
	// Do not inherit the app's authenticated transport, cookie jar, client
	// certificates or proxy credentials. No caller can supply a different feed.
	var transport = &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
		DisableCompression:     true,
		MaxIdleConns:           2,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   releaseReadLimit,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func listReleaseMetadata(ctx context.Context, client *http.Client) ([]ReleaseMetadata, error) {
	if ctx == nil {
		return nil, errors.New("missing Next metadata context")
	}
	ctx, cancel := context.WithTimeout(ctx, releaseScanLimit)
	defer cancel()
	var releases = make([]ReleaseMetadata, 0)
	var ids = make(map[int64]bool)
	var tags = make(map[string]bool)
	remaining := maxReleaseScan
	for page := 1; page <= maxReleasePages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, more, err := readReleasePage(ctx, client, page, remaining)
		if err != nil {
			return nil, fmt.Errorf("Next release metadata page %d: %w", page, err)
		}
		remaining -= len(raw)
		batch, err := decodeReleasePage(raw)
		if err != nil {
			return nil, fmt.Errorf("Next release metadata page %d: %w", page, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			if more {
				return nil, errors.New("empty Next release page advertises a next page")
			}
			return releases, nil
		}
		for _, release := range batch {
			if ids[release.ID] || tags[release.Tag] {
				return nil, errors.New("duplicate Next release ID or tag; listing may have changed")
			}
			ids[release.ID], tags[release.Tag] = true, true
			releases = append(releases, release)
		}
	}
	return nil, errors.New("incomplete Next metadata scan: page limit reached")
}

func readReleasePage(ctx context.Context, client *http.Client, page, remaining int) ([]byte, bool, error) {
	limit := min(maxReleasePage, remaining)
	if limit <= 0 {
		return nil, false, errors.New("Next metadata scan exceeds byte limit")
	}
	endpoint := fmt.Sprintf("%s/releases?per_page=100&page=%d", githubAPIBase, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Magpie-Next-release-metadata")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Do not echo response bodies, Location or remote error text.
		return nil, false, fmt.Errorf("HTTP status %d; no metadata accepted", resp.StatusCode)
	}
	if resp.ContentLength > int64(limit) {
		return nil, false, errors.New("Next metadata response exceeds byte limit")
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, false, errors.New("unexpected Next metadata content encoding")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > limit {
		return nil, false, errors.New("Next metadata response exceeds byte limit")
	}
	more, err := nextLink(resp.Header.Values("Link"))
	if err != nil {
		return nil, false, err
	}
	return raw, more, ctx.Err()
}

// nextLink reads relation markers only. Link destinations (including numeric
// repository aliases) are deliberately never parsed into request URLs.
func nextLink(headers []string) (bool, error) {
	more := false
	for _, header := range headers {
		for _, part := range strings.Split(header, ",") {
			part = strings.TrimSpace(part)
			_, params, ok := strings.Cut(part, ">")
			if !ok || !strings.HasPrefix(part, "<") {
				return false, errors.New("malformed Next metadata pagination")
			}
			_, attributes, err := mime.ParseMediaType("link" + params)
			if err != nil || attributes["rel"] == "" {
				return false, errors.New("malformed Next metadata pagination")
			}
			for _, relation := range strings.Fields(attributes["rel"]) {
				more = more || relation == "next"
			}
		}
	}
	return more, nil
}

func decodeReleasePage(raw []byte) ([]ReleaseMetadata, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil || len(entries) > 100 {
		return nil, errors.New("invalid Next release-list JSON")
	}
	var releases = make([]ReleaseMetadata, 0, len(entries))
	for _, entry := range entries {
		fields, err := metadataObject(entry)
		if err != nil {
			return nil, err
		}
		var release ReleaseMetadata
		var apiURL string
		var assets []json.RawMessage
		if metadataField(fields, "id", &release.ID) != nil || release.ID <= 0 ||
			metadataField(fields, "tag_name", &release.Tag) != nil || !metadataSegment(release.Tag) ||
			metadataField(fields, "draft", &release.Draft) != nil ||
			metadataField(fields, "prerelease", &release.Prerelease) != nil ||
			metadataField(fields, "url", &apiURL) != nil || apiURL != githubAPIBase+"/releases/"+strconv.FormatInt(release.ID, 10) ||
			metadataField(fields, "assets", &assets) != nil {
			return nil, errors.New("invalid Next release metadata identity")
		}
		// GitHub's title/body are optional display text, never update identity.
		if raw := fields["name"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &release.Name); err != nil {
				return nil, err
			}
		}
		if raw := fields["body"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &release.Notes); err != nil {
				return nil, err
			}
		}
		var assetIDs = make(map[int64]bool)
		var assetNames = make(map[string]bool)
		for _, rawAsset := range assets {
			asset, err := decodeAssetMetadata(rawAsset, release.Tag)
			if err != nil {
				return nil, err
			}
			if assetIDs[asset.ID] || assetNames[asset.Name] {
				return nil, errors.New("duplicate Next release asset")
			}
			assetIDs[asset.ID], assetNames[asset.Name] = true, true
			release.Assets = append(release.Assets, asset)
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func decodeAssetMetadata(raw []byte, tag string) (AssetMetadata, error) {
	var asset AssetMetadata
	fields, err := metadataObject(raw)
	if err != nil {
		return asset, err
	}
	var apiURL, state string
	if metadataField(fields, "id", &asset.ID) != nil || asset.ID <= 0 ||
		metadataField(fields, "name", &asset.Name) != nil || !metadataSegment(asset.Name) ||
		metadataField(fields, "size", &asset.Size) != nil || asset.Size < 0 ||
		metadataField(fields, "state", &state) != nil || state != "uploaded" ||
		metadataField(fields, "url", &apiURL) != nil || apiURL != githubAPIBase+"/releases/assets/"+strconv.FormatInt(asset.ID, 10) ||
		metadataField(fields, "browser_download_url", &asset.URL) != nil || asset.URL != downloadBase+url.PathEscape(tag)+"/"+url.PathEscape(asset.Name) {
		return AssetMetadata{}, errors.New("invalid Next asset metadata identity")
	}
	return asset, nil
}

func metadataSegment(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 255 && !strings.ContainsAny(value, "/\\\x00\r\n")
}

// metadataObject tolerates GitHub's unrelated fields but rejects duplicate keys.
// Access by exact key avoids encoding/json's case-insensitive struct matching.
func metadataObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected Next metadata object")
	}
	var fields = make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return nil, errors.New("duplicate Next metadata key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing Next metadata JSON")
	}
	return fields, nil
}

func metadataField(fields map[string]json.RawMessage, key string, target any) error {
	raw := fields[key]
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("missing Next metadata field")
	}
	return json.Unmarshal(raw, target)
}
