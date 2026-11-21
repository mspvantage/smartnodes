#!/usr/bin/env bash
# SmartNodes server setup: prepares a fresh Ubuntu or Debian server for one
# role, following the guides at https://smartnodes.tech.
#
#   sudo bash smartnodes-setup.sh --role entry
#
# Roles: validator, entry, exit, bridge, prober.
#
# It prepares the server (firewall, swap, logs), downloads the SmartNodes
# release and checks its checksum, installs the programs, and puts the right
# service file and settings template in place. It ends with a pass/fail
# check.
#
# It never makes, reads or moves keys, never registers anything on the
# chain, and never starts a service: those steps need you, and are in the
# guide it points you to at the end. Running it again is safe.
#
# Options:
#   --role ROLE        validator, entry, exit, bridge or prober (required)
#   --version VERSION  the SmartNodes release to install (default below)
#   --yes              do not ask before making changes
#   --dry-run          only print what it would do
#   --skip-firewall    leave the firewall alone
#   --skip-services    do not run systemctl (no systemd here, as in a container)
set -euo pipefail

VERSION_DEFAULT="v0.1.0"
REPO="https://github.com/mspvantage/smartnodes"
INSTALL_DIR="/opt/smartnodes"

ROLE=""
VERSION="$VERSION_DEFAULT"
YES=0
DRY=0
SKIP_FW=0
SKIP_SVC=0

usage() { sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
die() { printf 'smartnodes-setup: %s\n' "$*" >&2; exit 1; }
# run does a change, or in --dry-run only shows it.
run() {
  if [ "$DRY" = 1 ]; then printf '  would run: %s\n' "$*"; else "$@"; fi
}
# write puts text in a file (mode 0644 unless given), or shows it.
write() {
  local path=$1 mode=${3:-0644}
  if [ "$DRY" = 1 ]; then printf '  would write: %s\n' "$path"; return; fi
  printf '%s' "$2" > "$path.tmp" && chmod "$mode" "$path.tmp" && mv "$path.tmp" "$path"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --role) ROLE=${2:-}; shift 2 ;;
    --version) VERSION=${2:-}; shift 2 ;;
    --yes|-y) YES=1; shift ;;
    --dry-run) DRY=1; shift ;;
    --skip-firewall) SKIP_FW=1; shift ;;
    --skip-services) SKIP_SVC=1; shift ;;
    -h|--help) usage 0 ;;
    *) die "unknown option $1 (see --help)" ;;
  esac
done

case "$ROLE" in
  validator|entry|exit|bridge|prober) ;;
  "") usage 1 ;;
  *) die "unknown role '$ROLE': use validator, entry, exit, bridge or prober" ;;
esac
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || die "'$VERSION' is not a release version such as v0.1.0"

VPN=0; DISGUISE=0
case "$ROLE" in entry|exit|bridge|prober) VPN=1 ;; esac
case "$ROLE" in entry|bridge|prober) DISGUISE=1 ;; esac

# ---------------------------------------------------------------- checks
step "Checking this server"
[ "$DRY" = 1 ] || [ "$(id -u)" = 0 ] || die "run it as root: sudo bash $0 --role $ROLE"
command -v apt-get >/dev/null || die "this script is for Ubuntu or Debian (apt was not found)"
case "$(uname -m)" in
  x86_64) ARCH=amd64; XRAY_ASSET="Xray-linux-64.zip" ;;
  aarch64|arm64) ARCH=arm64; XRAY_ASSET="Xray-linux-arm64-v8a.zip" ;;
  *) die "unsupported machine type $(uname -m): releases are for amd64 and arm64" ;;
esac
if [ "$SKIP_SVC" = 0 ] && ! command -v systemctl >/dev/null; then
  die "systemd was not found; use --skip-services if this is a container"
fi
say "  role: $ROLE   release: $VERSION   machine: $ARCH"

say
say "This will:"
[ "$SKIP_FW" = 0 ] && say "  - set up the firewall (ufw), keeping your SSH port open"
[ "$VPN" = 1 ] && say "  - turn swap off and keep system logs in memory only"
[ "$ROLE" = validator ] && say "  - make a 'smartnodes' user to run the chain node"
say "  - download SmartNodes $VERSION, check its checksum, and install the programs"
[ "$DISGUISE" = 1 ] && say "  - download Xray, check its checksum, and install it"
say "  - put the service file and a settings template in place (not started)"
say "It will not make keys, register anything, or start anything."
if [ "$YES" = 0 ] && [ "$DRY" = 0 ]; then
  read -r -p "Go ahead? [y/N] " answer
  [[ "$answer" =~ ^[Yy] ]] || die "nothing was changed"
fi

# -------------------------------------------------------------- packages
step "Installing packages"
PKGS="curl jq ca-certificates tar"
[ "$SKIP_FW" = 0 ] && PKGS="$PKGS ufw"
[ "$VPN" = 1 ] && PKGS="$PKGS wireguard-tools iproute2 iptables"
[ "$DISGUISE" = 1 ] && PKGS="$PKGS unzip"
run apt-get update -qq
# shellcheck disable=SC2086
run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $PKGS

# -------------------------------------------------------------- firewall
if [ "$SKIP_FW" = 0 ]; then
  step "Firewall"
  # Keep every port SSH listens on open, so this never locks you out.
  SSH_PORTS=$( (sshd -T 2>/dev/null || true) | awk '$1 == "port" {print $2}' | sort -u)
  [ -n "$SSH_PORTS" ] || SSH_PORTS=22
  for p in $SSH_PORTS; do run ufw allow "$p/tcp" comment 'SSH'; done
  case "$ROLE" in
    validator) run ufw allow 26656/tcp comment 'SmartNodes chain p2p' ;;
    entry) run ufw allow 51820 comment 'SmartNodes node'; run ufw allow 443/tcp comment 'SmartNodes disguise' ;;
    exit) run ufw allow 51820 comment 'SmartNodes node' ;;
    bridge) run ufw allow 443/tcp comment 'SmartNodes disguise' ;;
    prober) ;;
  esac
  run ufw --force enable
fi

# ------------------------------------------------- swap and logs (VPN nodes)
if [ "$VPN" = 1 ]; then
  step "Keeping user data off the disk"
  run swapoff -a
  if grep -Eq '^[^#].*\sswap\s' /etc/fstab 2>/dev/null; then
    run cp /etc/fstab "/etc/fstab.smartnodes-backup"
    run sed -i -E '/^[^#].*\sswap\s/s/^/#/' /etc/fstab
    say "  swap turned off for good (old /etc/fstab kept as /etc/fstab.smartnodes-backup)"
  fi
  run mkdir -p /etc/systemd/journald.conf.d
  write /etc/systemd/journald.conf.d/smartnode.conf $'[Journal]\nStorage=volatile\nRuntimeMaxUse=64M\n'
  if [ "$SKIP_SVC" = 0 ]; then run systemctl restart systemd-journald; fi
fi

# ------------------------------------------------------------------ user
if [ "$ROLE" = validator ]; then
  step "The smartnodes user"
  if id smartnodes >/dev/null 2>&1; then
    say "  already there"
  else
    run adduser --disabled-password --gecos "" smartnodes
  fi
fi

# --------------------------------------------------------------- release
step "Downloading SmartNodes $VERSION"
NAME="smartnodes_${VERSION}_linux_${ARCH}"
DEST="$INSTALL_DIR/$VERSION"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
if [ "$DRY" = 1 ]; then
  say "  would download $REPO/releases/download/$VERSION/$NAME.tar.gz and SHA256SUMS, and check the file"
else
  curl -fsSL -o "$TMP/$NAME.tar.gz" "$REPO/releases/download/$VERSION/$NAME.tar.gz" \
    || die "could not download $NAME.tar.gz: is $VERSION released? See $REPO/releases"
  curl -fsSL -o "$TMP/SHA256SUMS" "$REPO/releases/download/$VERSION/SHA256SUMS" || die "could not download SHA256SUMS"
  (cd "$TMP" && grep " $NAME.tar.gz\$" SHA256SUMS | sha256sum -c --status -) \
    || die "the download does not match its checksum: nothing was installed. Try again later."
  say "  checksum OK"
  mkdir -p "$DEST"
  tar -xzf "$TMP/$NAME.tar.gz" -C "$DEST" --strip-components=1
  ln -sfn "$DEST" "$INSTALL_DIR/current"
  install -m 0755 "$DEST"/bin/* /usr/local/bin/
  say "  installed: $(ls "$DEST/bin" | tr '\n' ' ')"
fi

# ------------------------------------------------------------------ xray
if [ "$DISGUISE" = 1 ]; then
  step "Downloading Xray"
  if [ "$DRY" = 1 ]; then
    say "  would download $XRAY_ASSET and its checksum from the latest Xray release, check it, and install /usr/local/bin/xray"
  else
    XRAY_URL="https://github.com/XTLS/Xray-core/releases/latest/download"
    curl -fsSL -o "$TMP/xray.zip" "$XRAY_URL/$XRAY_ASSET" || die "could not download Xray"
    curl -fsSL -o "$TMP/xray.dgst" "$XRAY_URL/$XRAY_ASSET.dgst" || die "could not download Xray's checksum"
    WANT=$(awk -F'= ' '/^SHA2-256=/ {print $2}' "$TMP/xray.dgst" | tr -d '[:space:]')
    GOT=$(sha256sum "$TMP/xray.zip" | cut -d' ' -f1)
    [ -n "$WANT" ] && [ "$WANT" = "$GOT" ] || die "Xray does not match its checksum: not installed"
    say "  checksum OK"
    unzip -o -q "$TMP/xray.zip" xray -d "$TMP"
    install -m 0755 "$TMP/xray" /usr/local/bin/xray
    say "  installed: $(/usr/local/bin/xray version 2>/dev/null | head -1)"
  fi
fi

# --------------------------------------------------------- service files
DEPLOY="$INSTALL_DIR/current/deploy"
[ "$DRY" = 1 ] && DEPLOY="(the release's deploy folder)"
ENV_FILE=""
if true; then
  step "Service file and settings template"
  run mkdir -p /etc/systemd/system
  case "$ROLE" in
    validator)
      run cp "$DEPLOY/smartnodesd.service" /etc/systemd/system/smartnodesd.service ;;
    entry|exit|bridge)
      run mkdir -p /etc/smartnode
      run chmod 700 /etc/smartnode
      run cp "$DEPLOY/smartnode@.service" /etc/systemd/system/smartnode@.service
      ENV_FILE="/etc/smartnode/$ROLE.env"
      if [ -e "$ENV_FILE" ]; then
        say "  $ENV_FILE already exists: left as it is"
      elif [ "$DRY" = 1 ]; then
        say "  would write $ENV_FILE from the template"
      else
        flags=$(sed -n 's/^NODE_FLAGS="\(.*\)"$/\1/p' "$DEPLOY/node.env.example")
        case "$ROLE" in
          exit) flags=$(printf '%s' "$flags" | sed 's/ -reality-port [^ ]* -xray [^ ]* -reality-dir [^ ]*//') ;;
          bridge) flags=$(printf '%s' "$flags" | sed 's|-listen 0.0.0.0:51820 -iface sn0 -subnet 10.77.0.0/24|-listen 127.0.0.1:51830 -iface sb0 -subnet 10.79.0.0/24|')
                  flags="$flags -bridge-address CHANGE" ;;
        esac
        write "$ENV_FILE" "# Settings for smartnode@$ROLE. Replace every CHANGE, then: sudo systemctl enable --now smartnode@$ROLE
# Every option is explained by: smartnode -h
NODE_FLAGS=\"$flags\"
" 0600
      fi ;;
    prober)
      run mkdir -p /etc/smartnode
      run chmod 700 /etc/smartnode
      run cp "$DEPLOY/smartprobe.service" /etc/systemd/system/smartprobe.service
      ENV_FILE="/etc/smartnode/probe.env"
      if [ -e "$ENV_FILE" ]; then say "  $ENV_FILE already exists: left as it is"
      else run install -m 0600 "$DEPLOY/probe.env.example" "$ENV_FILE"; fi ;;
  esac
  if [ "$SKIP_SVC" = 0 ]; then run systemctl daemon-reload; fi
fi

# ----------------------------------------------------------------- check
step "Checking the result"
PASS=0; FAIL=0
check() {
  if [ "$DRY" = 1 ]; then printf '  (dry run) would check: %s\n' "$1"; return; fi
  if eval "$2" >/dev/null 2>&1; then printf '  PASS  %s\n' "$1"; PASS=$((PASS+1)); else printf '  FAIL  %s\n' "$1"; FAIL=$((FAIL+1)); fi
}
check "smartnodesd $VERSION is installed" "smartnodesd version 2>&1 | grep -qF '$VERSION'"
[ "$ROLE" = validator ] && check "cosmovisor is installed" "command -v cosmovisor"
[ "$VPN" = 1 ] && check "smartnode and smartvpn are installed" "command -v smartnode && command -v smartvpn"
[ "$ROLE" = prober ] && check "smartprobe is installed" "command -v smartprobe"
[ "$DISGUISE" = 1 ] && check "xray is installed" "xray version"
[ "$VPN" = 1 ] && check "swap is off" "[ -z \"\$(swapon --show --noheadings)\" ]"
[ "$VPN" = 1 ] && check "system logs are kept in memory" "grep -q '^Storage=volatile' /etc/systemd/journald.conf.d/smartnode.conf"
if [ "$SKIP_FW" = 0 ]; then
  check "the firewall is on" "ufw status | grep -q 'Status: active'"
  check "SSH stays open" "ufw status | grep -Eq '^($(echo "$SSH_PORTS" | paste -sd'|'))/tcp'"
fi
[ -n "$ENV_FILE" ] && check "$ENV_FILE is readable by root only" "[ \"\$(stat -c %a $ENV_FILE)\" = 600 ]"

# ------------------------------------------------------------- next steps
step "Next steps"
case "$ROLE" in
  validator) say "Follow https://smartnodes.tech/validator.html from step 3 (set up the node)." ;;
  entry|exit) say "Follow https://smartnodes.tech/run-a-node.html from step 3 (your operator key). The settings file is $ENV_FILE." ;;
  bridge) say "Follow https://smartnodes.tech/run-a-node.html#bridge. The settings file is $ENV_FILE." ;;
  prober) say "Follow https://smartnodes.tech/run-a-node.html#prober from step 1. The settings file is $ENV_FILE." ;;
esac
if [ "$DRY" = 1 ]; then
  say "(dry run: nothing was changed)"
  exit 0
fi
say
say "Setup check: $PASS passed, $FAIL failed."
[ "$FAIL" = 0 ] || exit 1
