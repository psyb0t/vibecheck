#!/bin/bash

set -euo pipefail

# Project override for the framework's service-registration step.
#
# gofindimpl configures slogging as it starts, and the development image bakes
# LOG_LEVEL=debug, so the tool writes a "slogconf: configured" DEBUG record to
# STDOUT. The framework script pipes gofindimpl's stdout straight into jq, so
# that record makes jq fail with a parse error and takes `make own`,
# `make service`, and `make service-registration` down with it.
#
# Nothing here changes the generator. It only lowers the log level for the one
# command that has to emit clean JSON, then runs the unmodified framework
# script. Delete this file once the tool logs to stderr upstream.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

export LOG_LEVEL=info

exec bash "${SCRIPT_DIR}/servicepack/service_registration.sh"
