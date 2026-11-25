#!/bin/sh
# Build and test on a remote Docker host over SSH.
# Set DMD_HOST (for example user@docker-host) in the environment or in a gitignored .env.remote file.
# This script does not stop, remove, or reconfigure any container outside this project.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
if [ -f "$ROOT/.env.remote" ]; then
	# shellcheck source=/dev/null
	. "$ROOT/.env.remote"
fi

HOST="${DMD_HOST:-}"
DEST="${DMD_DEST:-build/dreame-dashboard}"
REGION="${DMD_REGION:-us}"
EMAIL="${DMD_EMAIL:-}"

usage() {
	echo "usage: DMD_HOST=user@host scripts/remote.sh {sync|test|check-refresh|verify-map|probe-writes|build|version|smoke|up|clean}" >&2
	exit 2
}

require_host() {
	if [ -z "$HOST" ]; then
		echo "error: set DMD_HOST (e.g. user@docker-host) or put DMD_HOST=... in .env.remote" >&2
		exit 2
	fi
}

sync_tree() {
	require_host
	# shellcheck disable=SC2029
	ssh "$HOST" "mkdir -p ~/$DEST"
	if command -v rsync >/dev/null 2>&1; then
		rsync -az --delete --exclude .git --exclude private --exclude '.env' --exclude '.env.*' --exclude data --exclude secrets "$ROOT"/ "$HOST:~/$DEST/"
	else
		# shellcheck disable=SC2029
		ssh "$HOST" "rm -rf ~/$DEST && mkdir -p ~/$DEST"
		# shellcheck disable=SC2029
		tar -C "$ROOT" --exclude .git --exclude private --exclude .env --exclude '.env.*' --exclude data --exclude secrets -czf - . | ssh "$HOST" "tar -C ~/$DEST -xzf -"
	fi
}

remote() {
	require_host
	# shellcheck disable=SC2029
	ssh "$HOST" "cd ~/$DEST && $*"
}

run_dmd_cmd() {
	subcmd="$1"
	sync_tree
	if [ -n "$EMAIL" ]; then
		remote "mkdir -p \"\$HOME/dreame-dashboard/data\" \"\$HOME/dreame-dashboard/secrets\" && docker run --rm --user \"\$(id -u):\$(id -g)\" -e HOME=/tmp -v \"\$PWD\":/src:z -w /src --network host -e GOTOOLCHAIN=local -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache -v \"\$HOME/dreame-dashboard/data\":/data:Z -v \"\$HOME/dreame-dashboard/secrets\":/secrets:ro,Z -e DMD_DATA_DIR=/data -e DMD_PASSWORD_FILE=/secrets/dreame.pw -e DMD_EMAIL='${EMAIL}' -e DMD_REGION='${REGION}' golang:1.27-bookworm go run ./cmd/dmd ${subcmd}"
	else
		remote "mkdir -p \"\$HOME/dreame-dashboard/data\" && docker run --rm --user \"\$(id -u):\$(id -g)\" -e HOME=/tmp -v \"\$PWD\":/src:z -w /src --network host -e GOTOOLCHAIN=local -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache -v \"\$HOME/dreame-dashboard/data\":/data:Z -e DMD_DATA_DIR=/data -e DMD_REGION='${REGION}' golang:1.27-bookworm go run ./cmd/dmd ${subcmd}"
	fi
}

[ $# -ge 1 ] || usage
case "$1" in
sync)
	sync_tree
	;;
test)
	sync_tree
	# shellcheck disable=SC2016
	remote 'docker run --rm -v "$PWD":/src:z -w /src -e GOTOOLCHAIN=local -e CGO_ENABLED=0 -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache golang:1.27-bookworm sh scripts/unit.sh'
	;;
check-refresh)
	run_dmd_cmd "check-refresh"
	;;
verify-map)
	run_dmd_cmd "verify-map"
	;;
probe-writes)
	echo "probe-writes tests writable PM20 controls (mode, fan, oscillation, toggles, power) without energizing the heater, then restores them." >&2
	run_dmd_cmd "probe-writes --yes-actuate"
	;;
build)
	sync_tree
	remote 'docker buildx build --load --build-arg SUPERVISOR_VERSION=dev -t dmd:dev .'
	;;
version)
	remote 'docker run --rm dmd:dev version'
	;;
smoke)
	remote 'sh scripts/smoke.sh'
	;;
up)
	sync_tree
	remote 'mkdir -p ~/dreame-dashboard/data ~/dreame-dashboard/secrets && cp docker-compose.yml ~/dreame-dashboard/docker-compose.yml && cd ~/dreame-dashboard && if [ -f compose.gate.yml ]; then DMD_IMAGE=dmd:dev docker compose -f docker-compose.yml -f compose.gate.yml up -d; else DMD_IMAGE=dmd:dev docker compose up -d; fi'
	;;
clean)
	require_host
	# shellcheck disable=SC2029
	ssh "$HOST" "rm -rf ~/$DEST"
	;;
*)
	usage
	;;
esac
