#!/usr/bin/env python3
"""Compare the set of SQL statements before and after a move.

Read-only. Extracts backtick-quoted Go raw strings that look like SQL, from
both a git revision and the working tree, normalizes whitespace, and reports
which statements were lost, gained, or silently altered.

Usage:
    sqlcheck.py <git-rev> <path>...   # sqlcheck.py HEAD internal/scanner internal/scannerstore
"""
import re
import subprocess
import sys
from pathlib import Path

SQL_START = re.compile(
    r"^\s*(SELECT|INSERT|UPDATE|DELETE|WITH)\b", re.IGNORECASE | re.MULTILINE
)
RAW_STRING = re.compile(r"`([^`]*)`", re.DOTALL)


def normalize(sql: str) -> str:
    """Collapse whitespace so indentation changes don't register as edits."""
    return " ".join(sql.split())


def statements(text: str) -> list:
    return [
        normalize(m.group(1))
        for m in RAW_STRING.finditer(text)
        if SQL_START.match(m.group(1))
    ]


def from_git(rev: str, roots: list) -> list:
    listing = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", rev],
        capture_output=True, text=True, check=True,
    ).stdout.splitlines()
    out = []
    for name in listing:
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        if not any(name.startswith(root) for root in roots):
            continue
        blob = subprocess.run(
            ["git", "show", f"{rev}:{name}"],
            capture_output=True, text=True, check=True,
        ).stdout
        out.extend(statements(blob))
    return out


def from_tree(roots: list) -> list:
    out = []
    for root in roots:
        for path in sorted(Path(root).rglob("*.go")):
            if path.name.endswith("_test.go"):
                continue
            out.extend(statements(path.read_text()))
    return out


def main() -> int:
    rev, roots = sys.argv[1], sys.argv[2:]
    before, after = from_git(rev, roots), from_tree(roots)

    lost = [s for s in before if s not in after]
    gained = [s for s in after if s not in before]

    print(f"{rev}: {len(before)} statements    working tree: {len(after)} statements")
    if not lost and not gained:
        print("IDENTICAL - every statement survived the move unchanged.")
        return 0

    for label, group in ((f"LOST (in {rev}, not in tree)", lost),
                         ("GAINED (new in tree)", gained)):
        if group:
            print(f"\n=== {len(group)} {label} ===")
            for s in group:
                print(f"  {s[:300]}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
