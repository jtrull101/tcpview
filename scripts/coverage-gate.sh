#!/bin/sh
# coverage-gate.sh <coverprofile> - prints per-package statement coverage
# and fails if the aggregate, or any single package, falls below its
# floor. The aggregate gate alone let one package sink well under it as
# long as the others carried the average.
#
# Floors: aggregate 90% (CLAUDE.md's bar); each internal/ package 85%;
# the root package (flag parsing, plus main() and the real OS privilege
# checks, which can't run under go test) 75%.
set -eu
profile=${1:-coverage.out}

awk -v module="$(go list -m)" '
NR == 1 { next } # "mode: ..." header
{
	split($1, loc, ":")
	file = loc[1]
	pkg = file
	sub(/\/[^\/]*$/, "", pkg)
	stmts = $2
	key = file ":" loc[2] # the same block can appear more than once
	if (!(key in seen)) {
		seen[key] = 1
		total[pkg] += stmts
	}
	if ($3 > 0 && !(key in hit)) {
		hit[key] = 1
		covered[pkg] += stmts
	}
}
END {
	fail = 0
	for (pkg in total) {
		pct = 100 * covered[pkg] / total[pkg]
		floor = (pkg == module) ? 75 : 85
		status = (pct + 0.0001 >= floor) ? "ok" : "FAIL"
		if (status == "FAIL") fail = 1
		printf "%-50s %6.1f%%  (floor %d%%)  %s\n", pkg, pct, floor, status | "sort"
		all += total[pkg]
		allcov += covered[pkg]
	}
	close("sort")
	pct = 100 * allcov / all
	status = (pct + 0.0001 >= 90) ? "ok" : "FAIL"
	if (status == "FAIL") fail = 1
	printf "%-50s %6.1f%%  (floor 90%%)  %s\n", "aggregate", pct, status
	exit fail
}' "$profile"
