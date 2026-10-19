#!/bin/bash
# Double-click in Finder to open the guided menu.
set -e
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
exec /bin/bash scripts/od2gp.sh menu
