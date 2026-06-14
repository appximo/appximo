#!/bin/sh
# Image entrypoint: ONE image runs the engine (default) or the worker.
#
#   docker run … <image>                 → appitools serve --schema … (the CMD)
#   docker run … <image> serve --port 9  → appitools serve --port 9
#   docker run … <image> version         → appitools version
#   docker run … <image> worker          → appitools-worker  (Class-2 outbox consumer)
#
# The `worker` keyword is the ONLY new behavior; every other invocation is
# prefixed with `appitools` exactly as the old `ENTRYPOINT ["appitools"]` did, so
# existing compose `command:` overrides and `docker exec … appitools token` are
# unchanged. `exec` so the process is PID 1 and receives SIGTERM directly (the
# engine drains, the worker finishes its batch + closes its connections cleanly).
set -e

if [ "${1:-}" = "worker" ]; then
	shift
	exec appitools-worker "$@"
fi

exec appitools "$@"
