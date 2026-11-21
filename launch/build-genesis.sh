#!/usr/bin/env bash
# Builds the SmartNodes test network's genesis file from testnet.json, in two
# steps, the usual way for a Cosmos network with several first validators:
#
#   bash build-genesis.sh draft
#       Makes the draft genesis file: the chain's settings and the starting
#       balances of every account in testnet.json, including each invited
#       validator's. Send it to the invited validators. Each makes a signed
#       genesis transaction against it (the validator guide, "Genesis
#       validators") and sends back the file, which goes in gentx/.
#
#   bash build-genesis.sh final
#       Checks every file in gentx/, adds them, checks the result, and writes
#       out/genesis.json with its SHA-256 checksum and out/network.txt: the
#       values to publish on the website and announce.
#
# Options (after the step): --settings FILE (default testnet.json),
# --gentx DIR (default gentx), --out DIR (default out), --binary PATH.
# It needs smartnodesd and jq, and touches nothing outside these folders.
set -euo pipefail
cd "$(dirname "$0")"

STEP=${1:-}; shift || true
SETTINGS=testnet.json; GENTX=gentx; OUT=out; BIN=smartnodesd
while [ $# -gt 0 ]; do
  case "$1" in
    --settings) SETTINGS=$2; shift 2 ;;
    --gentx) GENTX=$2; shift 2 ;;
    --out) OUT=$2; shift 2 ;;
    --binary) BIN=$2; shift 2 ;;
    *) echo "unknown option $1" >&2; exit 1 ;;
  esac
done
die() { echo "build-genesis: $*" >&2; exit 1; }
command -v jq >/dev/null || die "jq is needed: sudo apt install jq"
command -v "$BIN" >/dev/null || die "$BIN not found"
[ -f "$SETTINGS" ] || die "no settings file $SETTINGS"
jq -e . "$SETTINGS" >/dev/null || die "$SETTINGS is not valid JSON"
grep -q '"CHANGE"' "$SETTINGS" && die "$SETTINGS still has addresses marked CHANGE"

CHAIN=$(jq -r .chain_id "$SETTINGS")
DENOM=$(jq -r '.denom // "usnode"' "$SETTINGS")
HOME_DIR="$OUT/home"
GEN="$HOME_DIR/config/genesis.json"

draft() {
  rm -rf "$HOME_DIR" && mkdir -p "$OUT"
  "$BIN" init coordinator --chain-id "$CHAIN" --home "$HOME_DIR" >/dev/null 2>&1
  # The chain's settings: the denomination everywhere, then testnet.json's
  # overrides on top of the defaults.
  jq --arg d "$DENOM" --slurpfile s "$SETTINGS" '
      .app_state.staking.params.bond_denom = $d
    | .app_state.mint.params.mint_denom = $d
    | if .app_state.gov.params.min_deposit then .app_state.gov.params.min_deposit |= map(.denom = $d) else . end
    | if .app_state.gov.params.expedited_min_deposit then .app_state.gov.params.expedited_min_deposit |= map(.denom = $d) else . end
    | . * $s[0].genesis_overrides
    | if ($s[0].genesis_time // "") != "" then .genesis_time = $s[0].genesis_time else . end
    ' "$GEN" > "$GEN.new" && mv "$GEN.new" "$GEN"
  # Starting balances.
  jq -r '(.accounts + .validators)[] | "\(.address) \(.coins)"' "$SETTINGS" | while read -r addr coins; do
    "$BIN" genesis add-genesis-account "$addr" "$coins" --home "$HOME_DIR" >/dev/null \
      || die "could not add account $addr"
  done
  cp "$GEN" "$OUT/genesis-draft.json"
  echo "Draft genesis file: $OUT/genesis-draft.json"
  echo "  chain id: $CHAIN, accounts: $(jq '.app_state.bank.balances | length' "$GEN")"
  echo "Send it to the invited validators; put the files they send back in $GENTX/, then run: bash build-genesis.sh final"
}

final() {
  [ -f "$GEN" ] || die "no draft yet: run 'bash build-genesis.sh draft' first"
  mkdir -p "$GENTX"
  shopt -s nullglob
  files=("$GENTX"/*.json)
  [ ${#files[@]} -gt 0 ] || die "no genesis transactions in $GENTX/"
  # Each file must be one create-validator message from an account in the
  # draft, for this chain, in this denomination.
  for f in "${files[@]}"; do
    jq -e '.body.messages | length == 1 and .[0]["@type"] == "/cosmos.staking.v1beta1.MsgCreateValidator"' "$f" >/dev/null \
      || die "$f is not a single create-validator transaction"
    jq -e --arg d "$DENOM" '.body.messages[0].value.denom == $d' "$f" >/dev/null || die "$f stakes something other than $DENOM"
    rate=$(jq -r '.body.messages[0].commission.max_rate' "$f")
    awk -v r="$rate" 'BEGIN { exit !(r <= 0.20) }' || die "$f has a maximum commission above 20%"
    echo "  ok: $(jq -r '.body.messages[0].description.moniker' "$f")"
  done
  rm -rf "$HOME_DIR/config/gentx" && mkdir -p "$HOME_DIR/config/gentx"
  cp "${files[@]}" "$HOME_DIR/config/gentx/"
  "$BIN" genesis collect-gentxs --home "$HOME_DIR" >/dev/null 2>&1 || die "collect-gentxs failed: a transaction does not match the draft (wrong chain id, or an account without enough coins)"
  "$BIN" genesis validate --home "$HOME_DIR" >/dev/null || die "the final genesis file is not valid"
  cp "$GEN" "$OUT/genesis.json"
  SUM=$(sha256sum "$OUT/genesis.json" | cut -d' ' -f1)
  echo "$SUM  genesis.json" > "$OUT/genesis.json.sha256"
  SEEDS=$(jq -r '.seeds | join(",")' "$SETTINGS")
  cat > "$OUT/network.txt" <<TXT
chain id:        $CHAIN
genesis file:    publish $OUT/genesis.json at https://smartnodes.tech/testnet/genesis.json
genesis sha256:  $SUM
genesis time:    $(jq -r .genesis_time "$OUT/genesis.json")
validators:      ${#files[@]}
seeds:           ${SEEDS:-(add your seed nodes to testnet.json)}
TXT
  echo
  cat "$OUT/network.txt"
}

case "$STEP" in
  draft) draft ;;
  final) final ;;
  *) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
