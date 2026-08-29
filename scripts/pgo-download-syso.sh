#!/usr/bin/env bash
#
# Download PGO artifacts from a GitHub Actions PGO run and install them:
#   native/<module>/<module>_<mode>_<isa>_<os>-<arch>.syso   (replace committed)
#   .local/pgo-data/instr-<mode>-<os>-<arch>.profdata        (platform-suffixed)
#
# Usage:
#   scripts/pgo-download-syso.sh [run-id]
#     run-id  PGO workflow run to download from. Default: latest successful
#             run on the pgo-inst branch.
#
# Environment:
#   MODULE  Restrict to one module's artifacts (encvm|ndec). Default: all.
#   REPO    GitHub <owner>/<repo>. Default: the velox remote, falling back to
#           origin, then upstream; gh's own resolution as the last resort.
#
# After installing the sysoes, review `git status` and commit them:
# the PGO syso is meant to replace the committed non-PGO artifact.

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

command -v gh >/dev/null 2>&1 || {
  echo "pgo-download-syso: gh CLI is required (https://cli.github.com/)" >&2
  exit 1
}

# ------------------------------------------------------------------
# Resolve the GitHub repo hosting the PGO workflow runs.
# ------------------------------------------------------------------
if [ -z "${REPO:-}" ]; then
  for _remote in velox origin upstream; do
    _url=$(git remote get-url "$_remote" 2>/dev/null || true)
    case "$_url" in
      *github.com*)
        REPO=$(printf '%s' "$_url" | sed -E 's#.*github.com[:/]##; s#\.git$##')
        break
        ;;
    esac
  done
fi
if [ -z "${REPO:-}" ]; then
  REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
fi

# ------------------------------------------------------------------
# Resolve the run: explicit id, else latest successful PGO run on pgo-inst.
# ------------------------------------------------------------------
RUN_ID="${1:-}"
if [ -z "$RUN_ID" ]; then
  RUN_ID=$(gh run list -R "$REPO" --workflow pgo.yml --branch pgo-inst \
    --status success --limit 1 --json databaseId --jq '.[0].databaseId')
fi
[ -n "$RUN_ID" ] || { echo "pgo-download-syso: no successful PGO run found" >&2; exit 1; }
echo "==> pgo-download-syso: repo=$REPO run=$RUN_ID"

# ------------------------------------------------------------------
# Download each pgo-<module>-<os>-<arch> artifact and import its files.
# ------------------------------------------------------------------
_filters=''
case "${MODULE:-}" in
  encvm|ndec) _filters="^pgo-${MODULE}-" ;;
  ''|all)     _filters='^pgo-' ;;
  *) echo "pgo-download-syso: MODULE must be encvm|ndec (got '$MODULE')" >&2; exit 1 ;;
esac

_tmp=$(mktemp -d)
trap 'rm -rf "$_tmp"' EXIT

_artifacts=$(gh api "repos/$REPO/actions/runs/$RUN_ID/artifacts" --paginate \
  --jq '.artifacts[] | select(.expired == false) | .name' | grep -E "$_filters" || true)
[ -n "$_artifacts" ] || {
  echo "pgo-download-syso: no matching PGO artifacts in run $RUN_ID" >&2
  exit 1
}

mkdir -p native/encvm native/ndec .local/pgo-data
for _art in $_artifacts; do
  echo "==> downloading $_art"
  gh run download "$RUN_ID" -R "$REPO" -n "$_art" -D "$_tmp/$_art"

  # The artifact preserves the upload paths (native/<module>/...,
  # .local/pgo-data/...); locate files by name.
  for _f in $(find "$_tmp/$_art" -name '*.syso'); do
    _m=$(basename "$_f"); _m=${_m%%_*}
    cp -v "$_f" "native/$_m/"
  done

  # Profdata names repeat across platforms; keep them distinguishable with a
  # platform suffix derived from the artifact name (pgo-<module>-<os>-<arch>).
  _plat=${_art#pgo-}; _plat=${_plat#*-}
  for _f in $(find "$_tmp/$_art" -name 'instr-*.profdata'); do
    _mode=$(basename "$_f"); _mode=${_mode#instr-}; _mode=${_mode%.profdata}
    cp -v "$_f" ".local/pgo-data/instr-${_mode}-${_plat}.profdata"
  done
done

echo ""
echo "Done. Sysoes installed; profdata under .local/pgo-data/ (gitignored)."
echo "Review with:  git status -- native/"
echo "Restore committed versions with:  git checkout -- native/*/"
