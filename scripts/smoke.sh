#!/bin/sh
# Isolated smoke test. Uses its own compose project and does not touch dreame-dashboard.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
NAME=dmd-smoke
PORT=19094

cleanup() {
	docker compose -p "$NAME" -f /tmp/dmd-smoke-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
	rm -rf /tmp/dmd-smoke-data /tmp/dmd-smoke-compose.yml /tmp/dmd-smoke-hdr /tmp/dmd-smoke-events
}
trap cleanup EXIT
cleanup

if ss -ltn | awk '{print $4}' | grep -q ":${PORT}$"; then
	echo "port ${PORT} is in use" >&2
	exit 1
fi

docker buildx build --load --build-arg SUPERVISOR_VERSION=dev -t dmd:smoke .
docker run --rm dmd:smoke version | grep -q '^dmd '

mkdir -p /tmp/dmd-smoke-data
uid=$(id -u)
gid=$(id -g)
cat > /tmp/dmd-smoke-compose.yml <<EOF
services:
  dmd:
    image: dmd:smoke
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    user: "${uid}:${gid}"
    ports: ["127.0.0.1:${PORT}:9094"]
    tmpfs:
      - /run:uid=${uid},gid=${gid},mode=0700
      - /tmp
    environment:
      TZ: UTC
      DMD_READ_ONLY: "true"
      DMD_ENABLE_MQTT: "false"
      DMD_DATA_DIR: /data
    volumes:
      - /tmp/dmd-smoke-data:/data:Z
EOF

docker compose -p "$NAME" -f /tmp/dmd-smoke-compose.yml up -d
cid=$(docker compose -p "$NAME" -f /tmp/dmd-smoke-compose.yml ps -q dmd)

for _ in 1 2 3 4 5 6 7 8 9 10; do
	if curl -fsS -o /dev/null "http://127.0.0.1:${PORT}/"; then
		break
	fi
	sleep 1
done

code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example' "http://127.0.0.1:${PORT}/")
[ "$code" = "421" ]
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${PORT}/poll")
[ "$code" = "403" ] || [ "$code" = "503" ]

html=$(curl -fsS -D /tmp/dmd-smoke-hdr "http://127.0.0.1:${PORT}/")
grep -q "Content-Security-Policy" /tmp/dmd-smoke-hdr
printf '%s' "$html" | grep -Eq "Connecting to Dreame|Sign in to Dreame|Air Purifier"
curl -fsS -D - -o /dev/null "http://127.0.0.1:${PORT}/device" | head -n 1 | grep -Eq ' (303|200) '
code=$(curl -s -o /tmp/dmd-smoke-events -w '%{http_code}' --max-time 2 "http://127.0.0.1:${PORT}/events")
[ "$code" = "503" ]
grep -q auth_required /tmp/dmd-smoke-events
docker exec "$cid" /usr/bin/dmd healthcheck

docker kill --signal TERM "$cid"
for _ in 1 2 3 4 5 6 7 8 9 10; do
	if ! docker inspect -f '{{.State.Running}}' "$cid" 2>/dev/null | grep -q true; then
		break
	fi
	sleep 1
done
status=$(docker inspect -f '{{.State.ExitCode}}' "$cid")
[ "$status" = "0" ]
echo "smoke ok"
