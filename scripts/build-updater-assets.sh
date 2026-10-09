#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="${1:-${ROOT_DIR}/dist/updater}"
APP_VERSION="${APP_VERSION:-${RELEASE_TAG:-dev}}"
APP_COMMIT="${APP_COMMIT:-unknown}"
LDFLAGS="-s -w -X agent2api/internal/buildinfo.Version=${APP_VERSION} -X agent2api/internal/buildinfo.Commit=${APP_COMMIT}"

mkdir -p "${OUTPUT_DIR}"

# Windows 支持面已裁选（install-updater 仅支持 Linux/macOS；见 AGENTS §8.4），
# 不再产出 windows 资产——此前会生成无人消费的 .exe。
for target in \
  linux/amd64 linux/arm64 \
  darwin/amd64 darwin/arm64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  output="${OUTPUT_DIR}/agent2api-updater_${goos}_${goarch}"
	  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
	    go build -trimpath -ldflags="${LDFLAGS}" -o "${output}" "${ROOT_DIR}/cmd/updater"
done

checksum_file="${OUTPUT_DIR}/agent2api-updater_checksums.txt"
: > "${checksum_file}"
for asset in "${OUTPUT_DIR}"/agent2api-updater_*; do
  if [[ "${asset}" == "${checksum_file}" ]]; then
    continue
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    hash="$(sha256sum "${asset}" | awk '{print $1}')"
  else
    hash="$(shasum -a 256 "${asset}" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "${hash}" "$(basename "${asset}")" >> "${checksum_file}"
done
