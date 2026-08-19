#!/usr/bin/env bash
# Local dev build with the same version derivation the release workflow uses:
# patch = nearest exact semver tag's patch + commits since that tag.
set -euo pipefail
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

BASE_TAG="$(git tag --merged HEAD --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)"
if [[ -z "${BASE_TAG}" ]]; then
  VERSION="v0.0.1"
else
  COMMITS_SINCE="$(git rev-list "${BASE_TAG}..HEAD" --count)"
  IFS='.' read -r MAJOR MINOR PATCH <<< "${BASE_TAG#v}"
  VERSION="v${MAJOR}.${MINOR}.$((PATCH + COMMITS_SINCE))"
fi
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
COMMIT="$(git rev-parse HEAD)"
DIRTY="false"
[[ -n "$(git status --porcelain)" ]] && DIRTY="true"

LDFLAGS="-s -w"
LDFLAGS="${LDFLAGS} -X github.com/nemo/Tsumugi/internal/version.version=${VERSION}"
LDFLAGS="${LDFLAGS} -X github.com/nemo/Tsumugi/internal/version.branch=${BRANCH}"
LDFLAGS="${LDFLAGS} -X github.com/nemo/Tsumugi/internal/version.commit=${COMMIT}"
LDFLAGS="${LDFLAGS} -X github.com/nemo/Tsumugi/internal/version.dirty=${DIRTY}"

OUT="${1:-Tsumugi.exe}"
go build -trimpath -ldflags="${LDFLAGS}" -o "${OUT}" ./cmd/tsumugi
"./${OUT}" --version
