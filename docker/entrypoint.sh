#!/bin/sh
# Start the tracker as an unprivileged user.
#
# Two ways to control the identity are supported, because deployments differ:
#
#   * PUID/PGID, applied here while still root. The data directory is chowned
#     to match, so a bind mount on the host ends up owned by the right user.
#   * docker run --user, which starts the container non-root. Nothing can be
#     changed at that point, so this script simply gets out of the way.
set -eu

DATA_DIR="${IFT_DATA_DIR:-/data}"

if [ "$(id -u)" -ne 0 ]; then
    # Already running as an unprivileged user: honour that and do not try to
    # change ownership, which would fail anyway.
    mkdir -p "$DATA_DIR" 2>/dev/null || true
    if [ ! -w "$DATA_DIR" ]; then
        echo "entrypoint: warning: $DATA_DIR is not writable by uid $(id -u)" >&2
    fi
    echo "entrypoint: starting as uid $(id -u), gid $(id -g)"
    exec "$@"
fi

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

case "$PUID$PGID" in
    *[!0-9]*)
        echo "entrypoint: PUID and PGID must be numeric, got PUID=$PUID PGID=$PGID" >&2
        exit 1
        ;;
esac

# Reuse the group if that gid already exists, otherwise create it.
if ! group_name="$(getent group "$PGID" | cut -d: -f1)" || [ -z "$group_name" ]; then
    group_name=app
    addgroup -g "$PGID" "$group_name"
fi

# Same for the user.
if ! user_name="$(getent passwd "$PUID" | cut -d: -f1)" || [ -z "$user_name" ]; then
    user_name=app
    adduser -D -H -u "$PUID" -G "$group_name" -s /sbin/nologin "$user_name"
fi

mkdir -p "$DATA_DIR"

# Only chown when the ownership is actually wrong: on a large bind mount an
# unconditional recursive chown is slow and rewrites timestamps for nothing.
current_owner="$(stat -c '%u:%g' "$DATA_DIR")"
if [ "$current_owner" != "$PUID:$PGID" ]; then
    echo "entrypoint: setting ownership of $DATA_DIR to $PUID:$PGID"
    chown -R "$PUID:$PGID" "$DATA_DIR"
fi

echo "entrypoint: starting as $user_name ($PUID:$PGID)"
exec su-exec "$PUID:$PGID" "$@"
