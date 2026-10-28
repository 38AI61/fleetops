#!/bin/sh
# One-shot OSRM prepare (D-10, GF-305): extract, partition and customize a regional
# PBF into the osrm_data volume as region.osrm. Operator-run by hand; not part of CI
# or make e2e. The operator note is docs/osrm.md.
#
# Usage: sh deploy/osrm/prepare.sh [--force] <pbf-file-name>
#   The PBF is read from deploy/osrm/data/ (gitignored).
#   Skips when region.osrm.mldgr already exists; --force re-runs the pipeline.
set -eu

OSRM_IMAGE="${OSRM_IMAGE:-osrm/osrm-backend}"
# Compose names the volume <project>_osrm_data; the project defaults to the deploy/ directory name.
VOLUME="${COMPOSE_PROJECT_NAME:-deploy}_osrm_data"

usage() {
	echo "usage: sh deploy/osrm/prepare.sh [--force] <pbf-file-name>" >&2
	exit 2
}

FORCE=0
if [ "${1:-}" = "--force" ]; then
	FORCE=1
	shift
fi
[ $# -eq 1 ] || usage
PBF="$1"

# The name is pasted into a container shell command, so keep it to a plain file name.
case "$PBF" in
	"" | .* | *[!A-Za-z0-9._-]*) usage ;;
esac

HERE=$(cd "$(dirname "$0")" && pwd)
DATA="$HERE/data"
if [ ! -f "$DATA/$PBF" ]; then
	echo "prepare.sh: $DATA/$PBF not found" >&2
	exit 1
fi

# run <shell command>: one-shot container with the osrm_data volume at /data and the PBF dir read-only at /input.
run() {
	docker run --rm \
		-v "$VOLUME:/data" \
		-v "$DATA:/input:ro" \
		--entrypoint sh "$OSRM_IMAGE" -c "set -e; $1"
}

if [ "$FORCE" -eq 0 ] && run "test -f /data/region.osrm.mldgr" >/dev/null 2>&1; then
	echo "prepare.sh: region.osrm.mldgr already in $VOLUME; nothing to do (use --force to re-run)"
	exit 0
fi

echo "prepare.sh: 1/4 copy $PBF to $VOLUME as region.osrm.pbf"
run "cp /input/$PBF /data/region.osrm.pbf"

echo "prepare.sh: 2/4 osrm-extract (car profile)"
run "osrm-extract -p /opt/car.lua /data/region.osrm.pbf"

echo "prepare.sh: 3/4 osrm-partition"
run "osrm-partition /data/region.osrm"

echo "prepare.sh: 4/4 osrm-customize (mld, matches compose --algorithm mld)"
run "osrm-customize /data/region.osrm"

echo "prepare.sh: asserting outputs"
run "for f in mldgr cells cell_metrics partition; do test -s /data/region.osrm.\$f || { echo \"prepare.sh: missing region.osrm.\$f\" >&2; exit 1; }; done"

echo "prepare.sh: done; region.osrm is ready in $VOLUME"
