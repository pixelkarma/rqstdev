#!/usr/bin/env sh
set -eu

echo "Build and install the server manually for now."
echo
echo "Suggested build command:"
echo "  (cd api && go build -o rqstdev-api ./cmd/rqstdev-api)"
echo
echo "Suggested host paths:"
echo "  binary: /usr/local/bin/rqstdev-api"
echo "  config: /etc/rqstdev/config.json"
echo "  data:   /var/lib/rqstdev"
