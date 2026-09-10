# shellcheck shell=bash
# Native build input set: the paths whose tracked content determines a
# native module artifact. gen-natives.sh stamps their content hash into
# every syso it builds and pgo-download-syso.sh verifies a downloaded
# artifact against the installing tree with the same definition, so the
# set must stay single-sourced here.
#
# Sourced by both scripts, which may run from any directory (module
# sub-makes in particular): every git call resolves the repository through
# this script's own location. Pure definitions only, no side effects.
#
# The *.syso exclusion matters: the artifacts being replaced must not
# change the hash, and ls-tree lacks pathspec exclude magic, so that
# filter runs on the listing lines.

NATIVE_INPUT_PATHS=(native scripts/gen-natives.sh scripts/prelink.sh scripts/cmd/prelink-obj
  scripts/pgo-collect-instr.sh)

_native_inputs_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# native_inputs_hash: git blob hash over the tracked listing of the input
# set at HEAD. Content addressed, so histories carrying identical native
# trees (rebases, cherry picks) hash equally and an artifact built from
# one stays valid for the others. Prints "unknown" outside a repository.
native_inputs_hash() {
  git -C "$_native_inputs_root" rev-parse --git-dir >/dev/null 2>&1 || { echo unknown; return; }
  git -C "$_native_inputs_root" ls-tree -r HEAD -- "${NATIVE_INPUT_PATHS[@]}" | grep -v '\.syso$' |
    git hash-object --stdin
}

# native_inputs_dirty: succeeds when tracked input files carry uncommitted
# modifications. An artifact must not be installed over them: the eventual
# commit would pair it with sources it was never built from.
native_inputs_dirty() {
  [ -n "$(git -C "$_native_inputs_root" status --porcelain --untracked-files=no -- \
    "${NATIVE_INPUT_PATHS[@]}" ':(exclude)*.syso')" ]
}
