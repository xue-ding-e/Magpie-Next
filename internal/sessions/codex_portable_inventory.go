package sessions

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// This entry point accepts only caller-owned immutable synthetic filesystems.
// fs.FS does not establish snapshot consistency, honest entry modes, physical
// filenames or freedom from symlink races on live disks. No result here grants
// permission to publish, repair, or inspect a real user's history.
// Limits describe this conservative candidate, not native Codex format limits.
const syntheticRolloutFileLimit = 8 << 20

// Original and staged bytes have separate 32 MiB budgets, not a combined one.
const syntheticRolloutTotalLimit = 32 << 20
const syntheticRolloutEntryLimit = 4096

type syntheticRolloutCandidate struct {
	relativePath       string
	expectedBeforeHash [32]byte
	after              []byte
}

type syntheticInventoryRequest struct {
	schemaPin, activeRoot, archivedRoot string
	candidates                          []syntheticRolloutCandidate
}

type syntheticSnapshotReferencePlan struct {
	references   readOnlyReferencePlan
	roots        [2]string
	paths        []string
	scannedBytes uint64
}

// validateSyntheticSnapshotPlan derives inventory completeness from traversal
// of the two configured roots, not from a caller's complete boolean. Coverage
// is limited to those roots and that supplied immutable synthetic snapshot.
func validateSyntheticSnapshotPlan(source fs.FS, request syntheticInventoryRequest) (syntheticSnapshotReferencePlan, error) {
	var empty syntheticSnapshotReferencePlan
	if source == nil || request.schemaPin != referenceSchemaPin {
		return empty, fmt.Errorf("unsupported synthetic source or schema")
	}
	roots := [2]string{request.activeRoot, request.archivedRoot}
	for _, root := range roots {
		if !fs.ValidPath(root) || root == "." || strings.Contains(root, "\\") {
			return empty, fmt.Errorf("unsupported inventory root")
		}
	}
	if roots[0] == roots[1] || strings.HasPrefix(roots[0], roots[1]+"/") || strings.HasPrefix(roots[1], roots[0]+"/") {
		return empty, fmt.Errorf("overlapping inventory roots")
	}
	if len(request.candidates) > syntheticRolloutEntryLimit {
		return empty, fmt.Errorf("too many candidates")
	}
	var candidates = make(map[string]syntheticRolloutCandidate, len(request.candidates))
	var afterTotal int
	for _, candidate := range request.candidates {
		if !fs.ValidPath(candidate.relativePath) || len(candidate.after) > syntheticRolloutFileLimit {
			return empty, fmt.Errorf("unsupported candidate")
		}
		if _, exists := candidates[candidate.relativePath]; exists {
			return empty, fmt.Errorf("duplicate candidate path")
		}
		afterTotal += len(candidate.after)
		if afterTotal > syntheticRolloutTotalLimit {
			return empty, fmt.Errorf("candidate byte budget exceeded")
		}
		candidates[candidate.relativePath] = candidate
	}
	for _, root := range roots {
		if err := checkSyntheticRoot(source, root); err != nil {
			return empty, err
		}
	}
	var inventory = stagedRolloutInventory{schemaPin: request.schemaPin}
	var result syntheticSnapshotReferencePlan
	result.roots = roots
	var stack = []string{roots[1], roots[0]}
	entryCount := 0
	for len(stack) > 0 {
		directory := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := readSyntheticDirectory(source, directory, syntheticRolloutEntryLimit-entryCount)
		if err != nil {
			return empty, err
		}
		entryCount += len(entries)
		for _, entry := range entries {
			if entry.Name() == "." || !fs.ValidPath(entry.Name()) || path.Base(entry.Name()) != entry.Name() || strings.Contains(entry.Name(), "\\") {
				return empty, fmt.Errorf("unsupported directory entry")
			}
			info, err := entry.Info()
			if err != nil {
				return empty, err
			}
			if entry.Type()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeSymlink != 0 {
				return empty, fmt.Errorf("symlink unsupported")
			}
			name := path.Join(directory, entry.Name())
			if info.IsDir() {
				stack = append(stack, name)
				continue
			}
			if !info.Mode().IsRegular() {
				return empty, fmt.Errorf("special file unsupported")
			}
			_, id, err := stagedRolloutIdentity(entry.Name())
			if err != nil {
				return empty, err
			}
			candidate, exists := candidates[name]
			if !exists {
				return empty, fmt.Errorf("discovered rollout has no candidate")
			}
			limit := syntheticRolloutFileLimit
			if remaining := syntheticRolloutTotalLimit - int(result.scannedBytes); remaining < limit {
				limit = remaining
			}
			data, err := readSyntheticRollout(source, name, limit)
			if err != nil {
				return empty, err
			}
			if sha256.Sum256(data) != candidate.expectedBeforeHash {
				return empty, fmt.Errorf("candidate source hash mismatch")
			}
			for _, content := range [][]byte{data, candidate.after} {
				if err := validateSyntheticMetadataOrdinal(content); err != nil {
					return empty, err
				}
			}
			result.scannedBytes += uint64(len(data))
			result.paths = append(result.paths, name)
			inventory.rollouts = append(inventory.rollouts, stagedRolloutBytes{plainBasename: entry.Name(), claimedRolloutID: id, before: data, after: candidate.after})
			delete(candidates, name)
		}
	}
	if len(candidates) != 0 {
		return empty, fmt.Errorf("candidate not present in configured snapshot")
	}
	// This internal flag means all configured synthetic roots were traversed.
	// It does not assert global Codex coverage, quiescence or native validity.
	inventory.complete = true
	references, err := validateStagedRolloutReferences(inventory)
	if err != nil {
		return empty, err
	}
	result.references = references
	sort.Strings(result.paths)
	return result, nil
}

// Inspect each root component as an entry instead of assuming a following
// Stat proves that the root is not a symlink. fs.FS honesty/immutability remains
// a precondition; this is not Lstat or a live-filesystem security boundary.
func checkSyntheticRoot(source fs.FS, root string) error {
	parent := "."
	for _, part := range strings.Split(root, "/") {
		entries, err := readSyntheticDirectory(source, parent, syntheticRolloutEntryLimit)
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			if entry.Name() != part {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("expected root directory unsupported")
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("expected root missing")
		}
		parent = path.Join(parent, part)
	}
	return nil
}

func readSyntheticDirectory(source fs.FS, name string, limit int) ([]fs.DirEntry, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	reader, ok := file.(fs.ReadDirFile)
	if !ok {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("directory reader unsupported")
	}
	var entries []fs.DirEntry
	var readErr error
	for {
		batch, err := reader.ReadDir(limit - len(entries) + 1)
		entries = append(entries, batch...)
		if len(entries) > limit {
			readErr = fmt.Errorf("directory entry budget exceeded")
			break
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			readErr = err
			break
		}
		if len(batch) == 0 {
			readErr = fmt.Errorf("directory reader made no progress")
			break
		}
	}
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func readSyntheticRollout(source fs.FS, name string, limit int) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > limit {
		return nil, fmt.Errorf("rollout byte budget exceeded")
	}
	return data, nil
}

// Pinned native recorder writes session_meta at history_base's exclusive
// ordinal, then advances for payload records; lineage's +1 skips metadata.
// This accepts only the pinned new-rollout initialization shape: a root
// starts at zero and a referenced child starts at the inherited cutoff. Other
// initialization shapes are outside this subset, not globally invalid native
// history. This is not full native schema/projector validation.
// Source: openai/codex@970111f6, rollout/src/ordinal.rs:29 and recorder.rs:2057,
// thread-store/src/local/thread_history_materialization.rs:64.
func validateSyntheticMetadataOrdinal(data []byte) error {
	meta, err := readStagedMetadata(data)
	if err != nil {
		return err
	}
	expected := uint64(0)
	if meta.parentID != "" {
		expected = meta.parentOrdinal
	}
	header, _ := referenceHeader(data)
	ordinal, err := rolloutLineOrdinal(header)
	if err != nil {
		return err
	}
	if ordinal != expected {
		return fmt.Errorf("metadata ordinal differs from pinned initialization boundary")
	}
	return nil
}
