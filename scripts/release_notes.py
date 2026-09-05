#!/usr/bin/env python3
"""Create release notes with a machine-readable curriculum compatibility marker."""

from __future__ import annotations

import argparse
import base64
import json
import re
from pathlib import Path

VERSION = re.compile(r"^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$")
CURRICULUM = re.compile(r"^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source", type=Path, default=Path("SOURCE.json"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not VERSION.fullmatch(args.tag):
        parser.error("tag must be stable semantic versioning such as v0.4.3")
    source = json.loads(args.source.read_text(encoding="utf-8"))
    curriculum = source.get("curriculum_version")
    if source.get("schema_version") != 1 or not isinstance(curriculum, str) or not CURRICULUM.fullmatch(curriculum):
        parser.error("SOURCE.json has no valid curriculum version")
    metadata = {
        "schema_version": 1,
        "release_version": args.tag,
        "compatible_curriculum_versions": [curriculum],
    }
    encoded = base64.urlsafe_b64encode(
        json.dumps(metadata, separators=(",", ":"), sort_keys=True).encode("utf-8")
    ).decode("ascii").rstrip("=")
    notes = (
        "Cross-platform IDEAL Lab IT Passport launcher.\n\n"
        f"Compatible curriculum: `{curriculum}`. Existing local progress is preserved.\n\n"
        "Downloads are verified against the SHA-256 digest published by GitHub.\n\n"
        f"<!-- ideal-passport-release:v1 {encoded} -->\n"
    )
    args.output.write_text(notes, encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
