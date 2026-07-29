#!/usr/bin/env sh
set -eu

if ! command -v go >/dev/null 2>&1; then
  echo 'Go 1.22+ is required: https://go.dev/dl/' >&2
  exit 1
fi

REPO=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$REPO"

echo '[1/3] Building nlwproxy...'
go build -trimpath -ldflags='-s -w' -o nlwproxy ./cmd/nlwproxy

echo '[2/3] Installing binary...'
./nlwproxy install

HOME_DIR=${NLWPROXY_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/nlwproxy}
PROXY_DIR="$HOME_DIR/data/proxies"
mkdir -p "$PROXY_DIR" "$HOME_DIR/profiles"
if [ ! -f "$HOME_DIR/config.json" ]; then
  cp "$REPO/nlwproxy.example.json" "$HOME_DIR/config.json"
fi

echo '[3/3] Installation complete.'
echo "Config:  $HOME_DIR/config.json"
echo "Proxies: $PROXY_DIR"
echo 'Before first run, set your credentials:'
echo '  export MYPROVIDER_API_KEY="your-provider-api-key"'
echo '  export NLW_PROXY_LOCAL_TOKEN="choose-a-local-gateway-token"'
echo 'Then open a new terminal and run: nlwproxy'
