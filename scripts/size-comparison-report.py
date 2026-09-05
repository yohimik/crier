import hashlib
import json
import pathlib
import re
import os

root = pathlib.Path(__file__).resolve().parents[1]
out = root / "coverage/size-comparison"
result = {"source_commit": (out / "source-commit.txt").read_text().strip(), "binaries": {}, "tests": {}}
for path in sorted((out / "bin").iterdir()):
    if path.is_file():
        result["binaries"][path.name] = {
            "bytes": path.stat().st_size,
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
        }
for path in sorted(out.glob("e2e-*.log")):
    content = path.read_text()
    exit_path = path.with_suffix(".exit")
    result["tests"][path.stem] = {
        "pass": len(re.findall(r"^--- PASS:", content, re.M)),
        "fail": len(re.findall(r"^--- FAIL:", content, re.M)),
        "skip": len(re.findall(r"^--- SKIP:", content, re.M)),
        "exit": int(exit_path.read_text()) if exit_path.exists() else None,
        "failed_tests": re.findall(r"^--- FAIL: (\S+)", content, re.M),
        "elapsed_seconds": re.findall(r"^(?:ok|FAIL)\s+\S+\s+([0-9.]+)s", content, re.M),
    }
sections_path = out / "sections-linux-arm64.jsonl"
if sections_path.exists():
    rows = [json.loads(line) for line in sections_path.read_text().splitlines()]
    by_name = {pathlib.Path(row["file"]).name: row["sections"] for row in rows}
    allocated = lambda rows: {row["name"]: (row["bytes"], row.get("sha256")) for row in rows if row["allocated"]}
    result["strip_preserves_allocated_sections"] = allocated(by_name["tinygo-linux-arm64"]) == allocated(by_name["tinygo-stripped-linux-arm64"])
    result["tinygo_symbol_table_bytes"] = sum(row["bytes"] for row in by_name["tinygo-linux-arm64"] if row["name"] in (".symtab", ".strtab"))
for target in ("linux-arm64", "linux-amd64", "darwin-arm64"):
    base = result["binaries"].get("go-" + target)
    if base:
        for prefix in ("tinygo-", "tinygo-stripped-"):
            row = result["binaries"].get(prefix + target)
            if row:
                row["reduction_bytes"] = base["bytes"] - row["bytes"]
                row["reduction_percent"] = 100 * (1 - row["bytes"] / base["bytes"])
cache = pathlib.Path(os.environ.get("SIZE_COMPARISON_CACHE", "/Users/yohimik/.cache/tinygo-spike-darwin"))
dictionaries = cache / "gopath/pkg/mod/github.com/benoitkugler/webrender@v0.0.14/text/hyphen/dictionaries"
symbols = (out / "nm-tinygo-linux-arm64.txt").read_text()
result["hyphenation"] = []
for path in sorted(dictionaries.glob("*.dic")):
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    result["hyphenation"].append({"file": path.name, "bytes": path.stat().st_size, "sha256": digest, "symbol_present": "embed/file_" + digest[:32] in symbols})
print(json.dumps(result, indent=2))
