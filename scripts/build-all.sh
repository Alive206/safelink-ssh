#!/bin/bash
# Build safelink for all platforms.
# Output: dist/safelink-{os}-{arch}[/safelink.exe]

set -e
VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
OUTDIR="dist"

PLATFORMS=(
  "windows/amd64"
  "windows/arm64"
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

echo "==> Building safelink ${VERSION}"
mkdir -p "${OUTDIR}"

for PLATFORM in "${PLATFORMS[@]}"; do
  GOOS="${PLATFORM%/*}"
  GOARCH="${PLATFORM#*/}"
  OUTPUT="${OUTDIR}/safelink-${GOOS}-${GOARCH}"

  if [ "${GOOS}" = "windows" ]; then
    OUTPUT="${OUTPUT}.exe"
  fi

  echo "  → ${GOOS}/${GOARCH}"
  GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 \
    go build -o "${OUTPUT}" -ldflags="-s -w" ./cmd/safelink

  # Also build the keyscan helper.
  KEYSCAN_OUT="${OUTDIR}/keyscan-${GOOS}-${GOARCH}"
  if [ "${GOOS}" = "windows" ]; then
    KEYSCAN_OUT="${KEYSCAN_OUT}.exe"
  fi
  GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 \
    go build -o "${KEYSCAN_OUT}" -ldflags="-s -w" ./cmd/keyscan
done

echo "==> Done! Binaries in ${OUTDIR}/"
ls -lh "${OUTDIR}/"
