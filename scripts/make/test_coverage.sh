#!/bin/bash

set -euo pipefail
trap 'log ERROR "command failed exit=$?"' ERR

# Project override for the framework's coverage step.
#
# The framework script hardcodes `go test -timeout=600s`. That budget fits a
# unit suite. Vibecheck's coverage run also builds the production image and
# drives it over real HTTP through testcontainers, with every package
# instrumented by -coverpkg and the race detector on, so the integration
# binary needs far longer than ten minutes. It was being killed mid-suite and
# the whole gate then reported "Tests failed" for a suite that passes.
#
# Nothing here weakens the gate. The coverage floor, the exclusion list, and
# the merge of out-of-process covdata all remain the framework's. A `go` shim
# ahead of PATH rewrites the one timeout flag and nothing else, then the
# unmodified framework script runs.
#
# Delete this file once the framework makes that timeout configurable.

readonly FRAMEWORK_TIMEOUT="-timeout=600s"
readonly COVERAGE_TIMEOUT="-timeout=7200s"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

# Diagnostics go to stderr so the framework script's stdout stays clean for
# whatever reads it.
log() {
	local level="$1"
	shift

	local timestamp
	timestamp=$(date -u '+%Y-%m-%dT%H:%M:%S.%3NZ')

	printf '{"time":"%s","level":"%s","file":"%s","line":%d,"msg":"%s"}\n' \
		"$timestamp" "$level" "${BASH_SOURCE[1]##*/}" "${BASH_LINENO[0]}" \
		"$*" >&2
}

usage() {
	cat <<'USAGE'
Usage: test_coverage.sh

Runs the framework coverage gate with a test timeout large enough for the
testcontainers suite. Takes no arguments.
USAGE
}

if [[ "$#" -gt 0 ]]; then
	usage

	exit 1
fi

REAL_GO="$(command -v go)"
readonly REAL_GO

SHIM_DIR="$(mktemp -d)"
readonly SHIM_DIR

cleanup() {
	rm -rf "$SHIM_DIR"
}

trap cleanup EXIT

cat >"${SHIM_DIR}/go" <<SHIM
#!/bin/bash

# Rewrites only the framework's hardcoded test timeout. Every other argument,
# and every other go subcommand, passes through untouched.
args=()
for arg in "\$@"; do
	if [[ "\$arg" == "${FRAMEWORK_TIMEOUT}" ]]; then
		args+=("${COVERAGE_TIMEOUT}")
	else
		args+=("\$arg")
	fi
done

exec "${REAL_GO}" "\${args[@]}"
SHIM

chmod +x "${SHIM_DIR}/go"

export PATH="${SHIM_DIR}:${PATH}"

log INFO "raising the coverage test timeout to ${COVERAGE_TIMEOUT}"

bash "${SCRIPT_DIR}/servicepack/test_coverage.sh"
