#!/usr/bin/env bash
# Cross-compile beacon for every release target and package it the way the
# GoReleaser config does: dist/beacon_<version>_<os>_<arch>.{tar.gz,zip} plus
# dist/checksums.txt. Needs only Go (and tar, plus zip or python3).
#
#   scripts/build-all.sh            # version from `git describe`
#   VERSION=0.2.0 scripts/build-all.sh
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
COMMIT="$(git rev-parse HEAD 2>/dev/null || true)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"
TARGETS=("linux/amd64" "linux/arm64" "windows/amd64")
FILES=(README.md LICENSE .env.example)

rm -rf dist && mkdir -p dist

zip_dir() { # zip_dir <dir> <out.zip>
  if command -v zip >/dev/null 2>&1; then (cd "$1" && zip -qr "$2" .); else
    python3 - "$1" "$2" <<'PY'
import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(src):
        for f in files:
            p = os.path.join(root, f)
            z.write(p, os.path.relpath(p, src))
PY
  fi
}

for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  name="beacon_${VERSION}_${os}_${arch}"
  stage="dist/.stage/${name}"
  mkdir -p "$stage"
  bin="beacon"; [ "$os" = windows ] && bin="beacon.exe"
  echo "building ${name}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "${stage}/${bin}" ./cmd/beacon
  cp "${FILES[@]}" "$stage/"
  if [ "$os" = windows ]; then
    (zip_dir "$stage" "$PWD/dist/${name}.zip")
  else
    tar -C "$stage" -czf "dist/${name}.tar.gz" .
  fi
done
rm -rf dist/.stage

(cd dist && sha256sum -- *.tar.gz *.zip > checksums.txt)
echo; echo "dist/:"; ls -l dist
