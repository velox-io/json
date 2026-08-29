#!/usr/bin/env bash
#
# GC-safety stress runner (CI only; `make test` deliberately does not call this).
#
# The binder produces Go heap pointers from native code, so its GC invariant is
# reachability rather than write barriers: every pointer a parse publishes must
# land in scannable memory, or keep its backing alive through a scannable alias.
# A hole in that invariant is invisible under a normal collector schedule. It
# only shows when a mark phase runs inside the publication window, which a
# 10us parse almost never hits.
#
# Two ways to widen that window, and this script does both:
#
#   soak    Throughput. Many oversubscribed processes replay the concurrent
#           unmarshal test until the budget runs out, so the window gets tried
#           a few thousand times instead of once. This is the leg that has
#           actually caught a live bug (.local/issues/crash-reproduce-1.sh);
#           on a 14-core box with 40 workers it needed 2 to 5 minutes, so
#           reproduction scales with cores and wall clock, not with cleverness.
#   suites  Verification. The GC-sensitive suites run under a collector that
#           checks itself, so a missed pointer is reported at the mark that
#           missed it rather than as a crash somewhere downstream.
#
# Legs:
#
#   soak    tags vj_noencvm,vj_noparsercache + race, GOGC=1 and nothing else.
#           This is the proven reproducer recipe, kept intact: cold parser per
#           Unmarshal so results stand on their own reachability, fresh process
#           per round so pooled state and the allocate-black window differ.
#   cold    tags vj_noparsercache + race, GOGC=1 + gccheckmark + clobberfree.
#           gccheckmark re-marks serially to verify the concurrent mark;
#           clobberfree poisons freed objects so a stale borrowed pointer
#           faults instead of silently reading recycled bytes.
#   pooled  default build, no race, same hostile GODEBUG. Warm parser and arena
#           reuse at real speed: this is where a borrowed pointer left in
#           pooled noscan memory, or a backing that skipped its per-parse
#           recolor, shows up.
#   encode  tags vjgcstress + race, same hostile GODEBUG. The encoder VM entry
#           calls runtime.GC() before every exec, so a mark always runs while
#           the ABI ctx holds borrowed pointers.
#
# Cost: soak is whatever GC_STRESS_SOAK_MINUTES says. One pass of the three
# suite legs is ~6min on a 10-core darwin/arm64 box, most of it ./decode/bind
# under the race detector. CI runs the two groups as separate parallel jobs.
#
# Usage:
#   scripts/gc-stress.sh                            # soak + every suite leg
#   GC_STRESS_LEGS=soak GC_STRESS_SOAK_MINUTES=45 scripts/gc-stress.sh
#   GC_STRESS_LEGS="cold pooled encode" scripts/gc-stress.sh
#
# Env knobs:
#   GC_STRESS_LEGS          legs to run (default: soak cold pooled encode)
#   GC_STRESS_SOAK_MINUTES  soak wall-clock budget (default 12)
#   GC_STRESS_SOAK_WORKERS  concurrent soak processes (default 3x cores, 8..40)
#   GC_STRESS_SOAK_COUNT    -test.count per soak process (default 10)
#   GC_STRESS_SOAK_TEST     soak test name (default TestBuildDiverseTypesRace)
#   GC_STRESS_SOAK_GODEBUG  extra GODEBUG for soak (default none: fewer
#                           instrumented cycles means more rounds per minute)
#   GC_STRESS_ROUNDS        passes over the suite legs (default 1)
#   GC_STRESS_COUNT         -test.count per suite process (default 1)
#   GC_STRESS_TIMEOUT       per-process timeout (default 20m), hang guard only
#   GC_STRESS_LOGDIR        log/binary output dir (default build/gc-stress)
#   GOMAXPROCS              passed through to every test process
#
# Exit status: 0 when everything passes. A failing process stops the run, gets
# classified as CRASH (runtime/GC/race diagnostic) or FAIL (plain test failure),
# and its full log stays in $GC_STRESS_LOGDIR.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LEGS="${GC_STRESS_LEGS:-soak cold pooled encode}"
ROUNDS="${GC_STRESS_ROUNDS:-1}"
COUNT="${GC_STRESS_COUNT:-1}"
TIMEOUT="${GC_STRESS_TIMEOUT:-20m}"
LOGDIR="${GC_STRESS_LOGDIR:-$ROOT/build/gc-stress}"
BINDIR="$LOGDIR/bin"

SOAK_MINUTES="${GC_STRESS_SOAK_MINUTES:-12}"
SOAK_COUNT="${GC_STRESS_SOAK_COUNT:-10}"
SOAK_TEST="${GC_STRESS_SOAK_TEST:-TestBuildDiverseTypesRace}"
SOAK_GODEBUG="${GC_STRESS_SOAK_GODEBUG:-}"

# Oversubscribe: the proven recipe was 40 workers on 14 cores, and the point is
# scheduler and GC interleaving, not throughput per process. Keep that ratio and
# cap it so a small runner does not drown in race-detector memory.
default_soak_workers() {
	local cpus workers
	cpus="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
	workers=$(( cpus * 3 ))
	[ "$workers" -lt 8 ] && workers=8
	[ "$workers" -gt 40 ] && workers=40
	echo "$workers"
}
SOAK_WORKERS="${GC_STRESS_SOAK_WORKERS:-$(default_soak_workers)}"

# Suites that carry the GC-sensitive tests: binder and allocator internals, the
# end-to-end unmarshal/marshal safety tests, and the streaming paths whose
# retained set is scoped per batch.
DECODE_PKGS=". ./decode/bind ./vbind ./tests ./stream ./value"
ENCODE_PKGS="./venc ./tests ./examples/marshal"
SOAK_PKG="./tests"

leg_tags() {
	case "$1" in
	soak) echo "vj_noencvm,vj_noparsercache" ;;
	cold) echo "vj_noparsercache" ;;
	pooled) echo "" ;;
	encode) echo "vjgcstress" ;;
	esac
}

leg_race() {
	case "$1" in
	soak | cold | encode) echo "1" ;;
	pooled) echo "" ;;
	esac
}

leg_pkgs() {
	case "$1" in
	soak) echo "$SOAK_PKG" ;;
	cold | pooled) echo "$DECODE_PKGS" ;;
	encode) echo "$ENCODE_PKGS" ;;
	esac
}

# A GC-safety violation surfaces as a runtime diagnostic, not a t.Error, so the
# log decides whether a nonzero exit is a real crash or an ordinary failure.
CRASH_RE='fatal error:|panic:|DATA RACE|found pointer to free object|marked free object|checkmark|unexpected fault address|invalid pointer|SIGSEGV|SIGBUS'

pkg_slug() { echo "$1" | sed 's|^\./||; s|^\.$|root|; s|/|_|g'; }

classify() { # classify <log>
	if grep -Eq "$CRASH_RE" "$1"; then echo CRASH; else echo FAIL; fi
}

report() { # report <log> <label> <rc> <elapsed>
	local log="$1" label="$2" rc="$3" elapsed="$4" kind dst
	kind="$(classify "$log")"
	dst="$LOGDIR/$kind-$label.log"
	mv "$log" "$dst"
	echo "gc-stress: $label $kind rc=$rc (${elapsed}s)" >&2
	echo "--- tail of $dst ---" >&2
	tail -n 60 "$dst" >&2
}

SOAK_PIDS=""
cleanup() {
	local rc=$? pids
	[ -n "$SOAK_PIDS" ] && kill $SOAK_PIDS 2>/dev/null
	# Whatever the run mode, no test binary may outlive this script: a killed
	# CI job leaves them behind otherwise. Match the binaries, not the script,
	# whose own command line contains the same path prefix.
	pids="$(pgrep -f "$BINDIR/.*[.]test -test" 2>/dev/null)"
	[ -n "$pids" ] && kill $pids 2>/dev/null
	wait 2>/dev/null
	return "$rc"
}
trap cleanup EXIT INT TERM

for leg in $LEGS; do
	if [ -z "$(leg_pkgs "$leg")" ]; then
		echo "gc-stress: unknown leg '$leg' (want: soak cold pooled encode)" >&2
		exit 2
	fi
done

has_leg() {
	for l in $LEGS; do [ "$l" = "$1" ] && return 0; done
	return 1
}

echo "gc-stress: legs=[$LEGS] timeout=$TIMEOUT GOMAXPROCS=${GOMAXPROCS:-default}"
if has_leg cold || has_leg pooled || has_leg encode; then
	echo "gc-stress: suites rounds=$ROUNDS count=$COUNT GODEBUG=gccheckmark=1,clobberfree=1"
fi
if has_leg soak; then
	echo "gc-stress: soak $SOAK_TEST ${SOAK_MINUTES}m workers=$SOAK_WORKERS count=$SOAK_COUNT GODEBUG=${SOAK_GODEBUG:-none}"
fi
go version

rm -rf "$LOGDIR"
mkdir -p "$BINDIR"

# Build up front, under a normal collector: the hostile GODEBUG belongs to the
# tests, not to the toolchain.
for leg in $LEGS; do
	tags="$(leg_tags "$leg")"
	race="$(leg_race "$leg")"
	for pkg in $(leg_pkgs "$leg"); do
		slug="$(pkg_slug "$pkg")"
		echo "gc-stress: build $leg/$slug${tags:+ (tags $tags)}${race:+ (race)}"
		if ! go test -c -o "$BINDIR/$leg-$slug.test" \
			${tags:+-tags "$tags"} ${race:+-race} "$pkg" \
			>"$LOGDIR/build-$leg-$slug.log" 2>&1; then
			cat "$LOGDIR/build-$leg-$slug.log" >&2
			echo "gc-stress: build failed ($leg $pkg)" >&2
			exit 2
		fi
	done
done

# One soak worker: fresh process per round until the deadline, first failure
# stops the whole soak through the stop file.
soak_worker() { # soak_worker <id> <deadline>
	local id="$1" deadline="$2" round=0 log rc t0
	local bin="$BINDIR/soak-$(pkg_slug "$SOAK_PKG").test"
	while [ "$(date +%s)" -lt "$deadline" ]; do
		[ -e "$LOGDIR/soak-stop" ] && return 0
		round=$(( round + 1 ))
		log="$LOGDIR/soak-w$id-r$round.log"
		t0="$(date +%s)"
		(cd "$ROOT/$SOAK_PKG" && env GOGC=1 ${SOAK_GODEBUG:+GODEBUG=$SOAK_GODEBUG} \
			"$bin" -test.run="^${SOAK_TEST}\$" \
			-test.count="$SOAK_COUNT" -test.timeout="$TIMEOUT") >"$log" 2>&1
		rc=$?
		if [ "$rc" -eq 0 ]; then
			rm -f "$log"
			echo "w$id r$round" >>"$LOGDIR/soak-rounds"
			continue
		fi
		: >"$LOGDIR/soak-stop"
		report "$log" "soak-w$id-r$round" "$rc" "$(( $(date +%s) - t0 ))"
		return 1
	done
	return 0
}

run_soak() {
	local deadline start now elapsed rounds poll=0
	start="$(date +%s)"
	deadline=$(( start + SOAK_MINUTES * 60 ))
	rm -f "$LOGDIR/soak-stop" "$LOGDIR/soak-rounds"
	: >"$LOGDIR/soak-rounds"

	local w
	for w in $(seq 1 "$SOAK_WORKERS"); do
		soak_worker "$w" "$deadline" &
		SOAK_PIDS="$SOAK_PIDS $!"
	done

	# Heartbeat: a silent 12 minutes in a CI log is indistinguishable from a
	# hang, and the round count is the only measure of how hard the window
	# actually got hit on this runner.
	local alive p
	while :; do
		alive=0
		for p in $SOAK_PIDS; do kill -0 "$p" 2>/dev/null && alive=1; done
		[ "$alive" -eq 0 ] && break
		sleep 15
		poll=$(( poll + 1 ))
		if [ $(( poll % 8 )) -eq 0 ]; then
			now="$(date +%s)"
			echo "gc-stress: soak $(( (now - start) / 60 ))m/${SOAK_MINUTES}m, $(wc -l <"$LOGDIR/soak-rounds" | tr -d ' ') rounds done"
		fi
	done
	wait
	SOAK_PIDS=""

	rounds="$(wc -l <"$LOGDIR/soak-rounds" | tr -d ' ')"
	elapsed=$(( $(date +%s) - start ))
	rm -f "$LOGDIR/soak-rounds"
	if [ -e "$LOGDIR/soak-stop" ]; then
		rm -f "$LOGDIR/soak-stop"
		echo "gc-stress: soak failed after ${elapsed}s, $rounds clean rounds" >&2
		return 1
	fi
	echo "gc-stress: soak ok, $rounds rounds x count=$SOAK_COUNT in ${elapsed}s"
	return 0
}

run_suite_leg() { # run_suite_leg <leg> <round>
	local leg="$1" round="$2" pkg slug log rc t0
	for pkg in $(leg_pkgs "$leg"); do
		slug="$(pkg_slug "$pkg")"
		log="$LOGDIR/r$round-$leg-$slug.log"
		t0="$(date +%s)"
		# Tests read testdata relative to their own package dir, so the
		# prebuilt binary must run from there.
		(cd "$ROOT/$pkg" && env GOGC=1 GODEBUG=gccheckmark=1,clobberfree=1 \
			"$BINDIR/$leg-$slug.test" \
			-test.count="$COUNT" -test.timeout="$TIMEOUT") >"$log" 2>&1
		rc=$?
		if [ "$rc" -eq 0 ]; then
			echo "gc-stress: r$round $leg $pkg ok ($(( $(date +%s) - t0 ))s)"
			rm -f "$log"
			continue
		fi
		report "$log" "r$round-$leg-$slug" "$rc" "$(( $(date +%s) - t0 ))"
		return 1
	done
	return 0
}

failed=0
start="$(date +%s)"

for leg in $LEGS; do
	[ "$leg" = soak ] || continue
	run_soak || failed=1
done

if [ "$failed" -eq 0 ]; then
	for round in $(seq 1 "$ROUNDS"); do
		for leg in $LEGS; do
			[ "$leg" = soak ] && continue
			run_suite_leg "$leg" "$round" || failed=1
			[ "$failed" -ne 0 ] && break 2
		done
	done
fi

total=$(( $(date +%s) - start ))
if [ "$failed" -ne 0 ]; then
	echo "gc-stress: FAILED after ${total}s; full logs in $LOGDIR" >&2
	exit 1
fi
echo "gc-stress: all legs passed in ${total}s"
