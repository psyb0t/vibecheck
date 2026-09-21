#!/bin/bash
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: audit-compose.sh (--file COMPOSE | --rendered-json FILE)

Validates the shared production-service floor after Docker Compose has resolved
anchors, merges, environment substitution, and profiles.
EOF
}

compose_file=""
rendered_json=""
while (($# > 0)); do
	case "$1" in
	--file | --rendered-json)
		(($# >= 2)) || {
			printf 'missing value for %s\n' "$1" >&2
			exit 2
		}
		if [[ "$1" == "--file" ]]; then compose_file="$2"; else rendered_json="$2"; fi
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'unknown option: %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [[ -n "$compose_file" && -n "$rendered_json" ]] || [[ -z "$compose_file" && -z "$rendered_json" ]]; then
	usage >&2
	exit 2
fi

tmp=""
if [[ -n "$compose_file" ]]; then
	[[ -f "$compose_file" ]] || {
		printf 'compose file not found: %s\n' "$compose_file" >&2
		exit 2
	}
	tmp="$(mktemp)"
	trap 'rm -f "$tmp"' EXIT
	docker compose -f "$compose_file" config --format json >"$tmp"
	rendered_json="$tmp"
fi

jq -e '.services | type == "object" and length > 0' "$rendered_json" >/dev/null || {
	printf 'rendered compose has no services\n' >&2
	exit 1
}

failures=0
fail() {
	printf '%s: %s\n' "$1" "$2" >&2
	failures=$((failures + 1))
}

while IFS= read -r service; do
	query=".services[\"$service\"]"
	jq -e "$query.cap_drop // [] | index(\"ALL\") != null" "$rendered_json" >/dev/null || fail "$service" 'cap_drop must contain ALL'
	jq -e "$query.security_opt // [] | index(\"no-new-privileges:true\") != null" "$rendered_json" >/dev/null || fail "$service" 'no-new-privileges is required'
	jq -e "$query.read_only == true" "$rendered_json" >/dev/null || fail "$service" 'read_only must be true'
	jq -e "$query.init == true" "$rendered_json" >/dev/null || fail "$service" 'init must be true'
	jq -e "$query.pids_limit != null and $query.deploy.resources.limits.memory != null and $query.deploy.resources.limits.cpus != null" "$rendered_json" >/dev/null || fail "$service" 'PID, memory, and CPU limits are required'
	jq -e "$query.logging.driver == \"json-file\" and $query.logging.options[\"max-size\"] == \"10m\" and $query.logging.options[\"max-file\"] == \"5\"" "$rendered_json" >/dev/null || fail "$service" 'json-file logs must be capped at 10m x 5'

	one_shot="$(jq -r "$query.labels[\"com.psyb0t.one-shot\"] // \"false\"" "$rendered_json")"
	if [[ "$one_shot" == "true" ]]; then
		jq -e "$query.restart == \"on-failure\"" "$rendered_json" >/dev/null || fail "$service" 'one-shot jobs must use restart: on-failure'
	else
		jq -e "$query.restart == \"unless-stopped\"" "$rendered_json" >/dev/null || fail "$service" 'services must use restart: unless-stopped'
		jq -e "$query.healthcheck.test != null and $query.healthcheck.retries == 5 and $query.healthcheck.interval == 10000000000 and $query.healthcheck.timeout == 3000000000 and $query.healthcheck.start_period == 30000000000" "$rendered_json" >/dev/null || fail "$service" 'healthcheck defaults must be 10s/3s/5/30s'
	fi

	while IFS= read -r host_ip; do
		[[ -n "$host_ip" && "$host_ip" != "0.0.0.0" && "$host_ip" != "::" ]] || fail "$service" 'published ports must bind a specific non-wildcard address'
	done < <(jq -r "$query.ports // [] | .[].host_ip // \"\"" "$rendered_json")

	if jq -e "$query.build == null and $query.image != null" "$rendered_json" >/dev/null; then
		image="$(jq -r "$query.image" "$rendered_json")"
		[[ "$image" == *@sha256:* ]] || fail "$service" 'external image must be digest-pinned'
	fi
done < <(jq -r '.services | keys[]' "$rendered_json")

((failures == 0)) || {
	printf 'compose audit failed: %d issue(s)\n' "$failures" >&2
	exit 1
}
printf 'compose audit passed: %s\n' "$rendered_json"
