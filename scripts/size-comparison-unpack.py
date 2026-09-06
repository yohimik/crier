import pathlib
import sys

source, destination = map(pathlib.Path, sys.argv[1:])
data = source.read_bytes()
offset = data.find(b"\x89PNG\r\n\x1a\n")
if offset < 0:
    raise SystemExit("The render output has no PNG signature.")
destination.write_bytes(data[offset:])
destination.with_suffix(".log").write_bytes(data[:offset])
