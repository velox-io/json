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
#   ALLOW_FOREIGN  Set to 1 to skip lineage verification. An artifact built
#           from diverged native sources embeds a foreign engine ABI that
#           corrupts the heap when paired with this tree's Go driver (the
#           7e7e5ad0 incident); only set this for deliberate cross lineage
#           testing.
#
# Lineage verification. Every syso built by gen-natives.sh carries a stamp
# with the source commit, the content hash of the native input set, and a
# dirty flag (see scripts/native-build-inputs.sh). A stamped artifact
# installs only when its input hash matches this tree's HEAD content, its
# build was clean, and this tree carries no uncommitted input changes.
# Unstamped artifacts (built before the stamp existed) fall back to a git
# guard requiring the run's commit locally with identical native inputs.
# Artifacts stage into a temp directory first: nothing is installed until
# every file passes.
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
# Download each pgo-<module>-<os>-<arch> artifact into the staging area.
# ------------------------------------------------------------------
_filters=''
case "${MODULE:-}" in
  encvm|ndec) _filters="^pgo-${MODULE}-" ;;
  ''|all)     _filters='^pgo-' ;;
  *) echo "pgo-download-syso: MODULE must be encvm|ndec (got '$MODULE')" >&2; exit 1 ;;
esac

_artifacts=$(gh api "repos/$REPO/actions/runs/$RUN_ID/artifacts" --paginate \
  --jq '.artifacts[] | select(.expired == false) | .name' | grep -E "$_filters" || true)
[ -n "$_artifacts" ] || {
  echo "pgo-download-syso: no matching PGO artifacts in run $RUN_ID" >&2
  exit 1
}

_tmp=$(mktemp -d)
trap 'rm -rf "$_tmp"' EXIT

for _art in $_artifacts; do
  echo "==> downloading $_art"
  gh run download "$RUN_ID" -R "$REPO" -n "$_art" -D "$_tmp/$_art"
done

# ------------------------------------------------------------------
# Lineage verification over the staged sysoes.
# ------------------------------------------------------------------
if [ "${ALLOW_FOREIGN:-0}" != "1" ]; then
  source "$REPO_ROOT/scripts/native-build-inputs.sh"
  _local_hash=$(native_inputs_hash)
  if [ "$_local_hash" = "unknown" ]; then
    echo "pgo-download-syso: refusing to install: this is not a git work tree," >&2
    echo "  so the local native inputs cannot be hashed for comparison." >&2
    exit 1
  fi
  if native_inputs_dirty; then
    echo "pgo-download-syso: refusing to install over uncommitted native input changes:" >&2
    git status --porcelain --untracked-files=no -- "${NATIVE_INPUT_PATHS[@]}" ':(exclude)*.syso' |
      sed 's/^/    /' >&2
    echo "  Commit them first: an artifact must pair with the sources it was built from." >&2
    exit 1
  fi

  _unstamped=0
  for _f in $(find "$_tmp" -name '*.syso'); do
    # || true on each extraction: an unstamped file has no match and grep
    # exits 1, which pipefail would otherwise treat as fatal.
    _st_commit=$(grep -aoE 'commit=[0-9a-f]{40}|commit=unknown' "$_f" | head -1 | cut -d= -f2 || true)
    _st_inputs=$(grep -aoE 'inputs=[0-9a-f]{40}|inputs=unknown' "$_f" | head -1 | cut -d= -f2 || true)
    _st_dirty=$(grep -aoE 'dirty=[01]' "$_f" | head -1 | cut -d= -f2 || true)
    if [ -z "$_st_commit" ] || [ -z "$_st_inputs" ] || [ -z "$_st_dirty" ]; then
      echo "==> $(basename "$_f"): no build stamp, falling back to the git lineage guard"
      _unstamped=1
      continue
    fi
    if [ "$_st_dirty" != "0" ]; then
      echo "pgo-download-syso: refusing $(basename "$_f"): built from a dirty tree" >&2
      echo "  (stamp: commit=$_st_commit dirty=$_st_dirty)." >&2
      exit 1
    fi
    if [ "$_st_inputs" != "$_local_hash" ]; then
      echo "pgo-download-syso: lineage mismatch, refusing to install." >&2
      echo "  $(basename "$_f") was built from commit $_st_commit" >&2
      echo "    with native inputs $_st_inputs" >&2
      echo "  this tree is $(git rev-parse --short HEAD) with native inputs $_local_hash" >&2
      echo "  Re-dispatch the PGO workflow from this tree, or set ALLOW_FOREIGN=1" >&2
      echo "  for deliberate cross lineage testing." >&2
      exit 1
    fi
    echo "==> $(basename "$_f"): stamp ok (commit $(git rev-parse --short "$_st_commit" 2>/dev/null || echo "?"), inputs ${_st_inputs:0:12})"
  done

  # Fallback for artifacts predating the stamp: the run's own commit must
  # be present locally and its native inputs identical to HEAD.
  if [ "$_unstamped" = 1 ]; then
    _meta=$(gh run view "$RUN_ID" -R "$REPO" --json headSha,headBranch,createdAt \
      --jq '.headSha + " " + .headBranch + " " + .createdAt')
    RUN_SHA=${_meta%% *}
    _meta=${_meta#* }
    RUN_BRANCH=${_meta%% *}
    RUN_DATE=${_meta#* }

    if ! git cat-file -e "${RUN_SHA}^{commit}" 2>/dev/null; then
      # Fetch through a remote that points at $REPO: the run's branch first,
      # then the bare SHA (GitHub serves both).
      for _r in $(git remote); do
        _norm=$(git remote get-url "$_r" 2>/dev/null | sed -E 's#\.git$##; s#.*github.com[:/]##')
        [ "$_norm" = "$REPO" ] || continue
        git fetch --quiet "$_r" "$RUN_BRANCH" 2>/dev/null || true
        if git cat-file -e "${RUN_SHA}^{commit}" 2>/dev/null; then break; fi
        git fetch --quiet "$_r" "$RUN_SHA" 2>/dev/null || true
        if git cat-file -e "${RUN_SHA}^{commit}" 2>/dev/null; then break; fi
      done
    fi
    if ! git cat-file -e "${RUN_SHA}^{commit}" 2>/dev/null; then
      echo "pgo-download-syso: refusing to install an unverifiable artifact." >&2
      echo "  run $RUN_ID has unstamped files and was built from $RUN_SHA" >&2
      echo "  (branch $RUN_BRANCH, $RUN_DATE), which is not present locally" >&2
      echo "  and could not be fetched. Fetch that commit and retry, or set" >&2
      echo "  ALLOW_FOREIGN=1." >&2
      exit 1
    fi
    _changed=$(git diff --name-only "$RUN_SHA" HEAD -- "${NATIVE_INPUT_PATHS[@]}" ':(exclude)*.syso')
    if [ -n "$_changed" ]; then
      echo "pgo-download-syso: lineage mismatch, refusing to install." >&2
      echo "  run $RUN_ID built from $(git rev-parse --short "$RUN_SHA") (branch $RUN_BRANCH, $RUN_DATE)" >&2
      echo "  current tree is $(git rev-parse --short HEAD), native inputs differ:" >&2
      printf '%s\n' "$_changed" | sed 's/^/    /'
      echo "  Re-dispatch the PGO workflow from this tree, or set ALLOW_FOREIGN=1" >&2
      echo "  for deliberate cross lineage testing." >&2
      exit 1
    fi
    echo "==> git lineage ok: run $(git rev-parse --short "$RUN_SHA") native inputs match HEAD"
  fi
fi

# ------------------------------------------------------------------
# Install: sysoes replace the committed artifacts, profdata lands under
# .local/pgo-data with a platform suffix (names repeat across platforms).
# ------------------------------------------------------------------
mkdir -p native/encvm native/ndec .local/pgo-data
for _f in $(find "$_tmp" -name '*.syso'); do
  _m=$(basename "$_f"); _m=${_m%%_*}
  cp -v "$_f" "native/$_m/"
done

for _art in $_artifacts; do
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
