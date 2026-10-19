#!/usr/bin/env python3
import re
import sys
from pathlib import Path


def fail(message: str) -> None:
    raise SystemExit(message)


def update_digest(path: Path, schema_name: str, digest: str) -> None:
    lines = path.read_text().splitlines(keepends=True)
    schema_headers = []
    header_pattern = re.compile(r"^(?P<indent>\s*)- Name:\s*(?P<name>\S+)\s*$")

    for index, line in enumerate(lines):
        match = header_pattern.match(line.rstrip("\r\n"))
        if match:
            schema_headers.append((index, len(match.group("indent")), match.group("name")))

    targets = [item for item in schema_headers if item[2] == schema_name]
    if len(targets) != 1:
        fail(f"Expected exactly one schema named {schema_name!r} in {path}, found {len(targets)}")

    start, indent, _ = targets[0]
    end = len(lines)
    for index, candidate_indent, _ in schema_headers:
        if index > start and candidate_indent == indent:
            end = index
            break

    digest_pattern = re.compile(r"^(?P<prefix>\s*DescriptorSHA256:\s*)\S+(?P<suffix>\s*)$")
    digest_lines = []
    for index in range(start + 1, end):
        if digest_pattern.match(lines[index].rstrip("\r\n")):
            digest_lines.append(index)

    if len(digest_lines) != 1:
        fail(
            f"Expected exactly one DescriptorSHA256 in schema {schema_name!r} "
            f"in {path}, found {len(digest_lines)}"
        )

    index = digest_lines[0]
    newline = "\r\n" if lines[index].endswith("\r\n") else "\n" if lines[index].endswith("\n") else ""
    match = digest_pattern.match(lines[index].rstrip("\r\n"))
    lines[index] = f"{match.group('prefix')}{digest}{match.group('suffix')}{newline}"
    path.write_text("".join(lines))


if __name__ == "__main__":
    if len(sys.argv) != 4:
        fail(f"usage: {Path(sys.argv[0]).name} CONFIG_PATH SCHEMA_NAME SHA256")
    update_digest(Path(sys.argv[1]), sys.argv[2], sys.argv[3])
