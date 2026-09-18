#!/usr/bin/env python3
"""Validate the public Pig AI Skill without third-party Python packages."""

from __future__ import annotations

import argparse
import re
import sys
import zipfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
SKILL_ROOT = REPO_ROOT / "skills" / "pig-ai"
SKILL_FILE = SKILL_ROOT / "SKILL.md"
AGENT_FILE = SKILL_ROOT / "agents" / "openai.yaml"
API_FILE = SKILL_ROOT / "references" / "api.md"

parser = argparse.ArgumentParser()
parser.add_argument(
    "--archive",
    type=Path,
    help="also validate a packaged Skill ZIP produced by GoReleaser",
)
args = parser.parse_args()


def fail(message: str) -> None:
    print(f"skill validation failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def read_required(path: Path) -> str:
    if not path.is_file():
        fail(f"missing required file: {path.relative_to(REPO_ROOT)}")
    content = path.read_text(encoding="utf-8")
    if not content.strip():
        fail(f"required file is empty: {path.relative_to(REPO_ROOT)}")
    return content


skill = read_required(SKILL_FILE)
agent = read_required(AGENT_FILE)
api = read_required(API_FILE)

lines = skill.splitlines()
if not lines or lines[0] != "---":
    fail("SKILL.md must start with YAML front matter")

try:
    front_matter_end = lines.index("---", 1)
except ValueError:
    fail("SKILL.md front matter is not closed")

front_matter: dict[str, str] = {}
for line in lines[1:front_matter_end]:
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    key, separator, value = line.partition(":")
    if not separator:
        fail(f"unsupported SKILL.md front matter line: {line!r}")
    front_matter[key.strip()] = value.strip().strip('"\'')

if front_matter.get("name") != "pig-ai":
    fail("SKILL.md front matter name must be 'pig-ai'")
if not front_matter.get("description"):
    fail("SKILL.md front matter requires a non-empty description")

required_agent_keys = ("display_name", "short_description", "default_prompt")
for key in required_agent_keys:
    if not re.search(rf"^\s*{re.escape(key)}\s*:\s*\S", agent, re.MULTILINE):
        fail(f"agents/openai.yaml is missing {key}")

required_contract_fragments = (
    "X-API-Key",
    "GET /openapi/v1/ai/models",
    "POST /openapi/v1/ai/billing-preview",
    "POST /openapi/v1/ai/images",
    "POST /openapi/v1/ai/videos",
    "GET /openapi/v1/ai/operations/{operation_id}",
    "/results/{result_id}/content",
    "/segments/ensure",
    "/segments",
)
for fragment in required_contract_fragments:
    if fragment not in api:
        fail(f"references/api.md is missing contract fragment: {fragment}")

markdown_link = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
for markdown_file in SKILL_ROOT.rglob("*.md"):
    content = markdown_file.read_text(encoding="utf-8")
    for raw_target in markdown_link.findall(content):
        target = raw_target.strip().split(maxsplit=1)[0].strip("<>")
        if not target or target.startswith(("#", "https://", "http://", "mailto:")):
            continue
        resolved = (markdown_file.parent / target.split("#", 1)[0]).resolve()
        try:
            resolved.relative_to(SKILL_ROOT.resolve())
        except ValueError:
            fail(f"link escapes the Skill directory in {markdown_file}: {target}")
        if not resolved.is_file():
            fail(f"broken local link in {markdown_file}: {target}")

private_path_patterns = (
    r"/Users/[^\s)`]+",
    r"/home/[^\s)`]+",
    r"[A-Za-z]:\\Users\\[^\s)`]+",
)
all_public_text = "\n".join(
    path.read_text(encoding="utf-8")
    for path in SKILL_ROOT.rglob("*")
    if path.is_file()
)
for pattern in private_path_patterns:
    if re.search(pattern, all_public_text, re.IGNORECASE):
        fail(f"Skill contains a private repository or workstation path ({pattern})")

if args.archive:
    archive = args.archive.resolve()
    if not archive.is_file():
        fail(f"missing packaged Skill archive: {archive}")

    expected_files = {
        "pig-ai/SKILL.md": SKILL_FILE,
        "pig-ai/agents/openai.yaml": AGENT_FILE,
        "pig-ai/references/api.md": API_FILE,
    }
    with zipfile.ZipFile(archive) as skill_zip:
        archived_files = {name for name in skill_zip.namelist() if not name.endswith("/")}
        missing_files = expected_files.keys() - archived_files
        if missing_files:
            fail(f"packaged Skill is missing: {', '.join(sorted(missing_files))}")
        for name in archived_files:
            parts = Path(name).parts
            if name.startswith("/") or ".." in parts:
                fail(f"packaged Skill contains an unsafe path: {name}")
        for name, source in expected_files.items():
            if skill_zip.read(name) != source.read_bytes():
                fail(f"packaged Skill does not match source: {name}")

print("Pig AI Skill validation passed")
