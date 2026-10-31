#!/usr/bin/env python3
"""Read-only hashing of independent old goldens and generated local-store evidence.

This is an evidence checker, not an official parser/index/materializer implementation.
Run golden before AND after the bounded Rust target. final requires all 16 generated files.
"""
import hashlib
import json
import sys
from pathlib import Path


def digest(path):
    assert path.is_file() and not path.is_symlink(), path
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    mode, source_arg, *rest = sys.argv[1:]
    assert mode in {"golden", "final"}
    source = Path(source_arg).resolve(strict=True)
    reference = json.loads(Path(__file__).with_name("magpie_reference_hashes.json").read_text())
    assert len(reference) == 16
    actual = {str(p.relative_to(source)): digest(p) for p in sorted(source.rglob("*.jsonl"))}
    assert actual == reference, "original 16 golden files differ"
    result = {"golden": actual}
    if mode == "final":
        assert len(rest) == 1
        output = Path(rest[0]).resolve(strict=True)
        expected = {f"{v}/{p}/{r}.jsonl" for v in ("shrink", "grow")
                    for p in ("before", "after") for r in "ABCD"}
        generated = {str(p.relative_to(output)): digest(p) for p in sorted(output.rglob("*.jsonl"))}
        assert set(generated) == expected
        manifest_path = output / "manifest.json"
        manifest = json.loads(manifest_path.read_text())
        assert set(manifest) == {f"{v}/{p}" for v in ("shrink", "grow") for p in ("before", "after")}
        for case, evidence in manifest.items():
            assert evidence["source_golden"] in reference
            assert len(set(evidence["ids"].values())) == 4
            for role in "ABCD":
                data = (output / case / f"{role}.jsonl").read_bytes()
                assert data.endswith(b"\n")
                assert len(data) == evidence[role]["next_byte_offset"]
                assert evidence[role]["ordinals"], "no real projected rows"
        for variant in ("shrink", "grow"):
            before, after = manifest[f"{variant}/before"], manifest[f"{variant}/after"]
            for role in "ABCD":
                assert before[role]["ordinals"] == after[role]["ordinals"]
                assert before[role]["next_ordinal"] == after[role]["next_ordinal"]
            assert (after["A"]["next_byte_offset"] > before["A"]["next_byte_offset"]) == (variant == "grow")
            assert (after["native_summary_utf8_bytes"] > before["native_summary_utf8_bytes"]) == (variant == "grow")
            left, right = before["native_summary_record_bytes"], after["native_summary_record_bytes"]
            assert abs(right - left) >= 3072
            assert (right > left) == (variant == "grow")
        result.update(event_rich=generated, manifest_sha256=digest(manifest_path))
    else:
        assert not rest
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
