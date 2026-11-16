#!/usr/bin/env bash
# Live check of the 20% commission ceiling on the four-validator test network.
#
# Start the network first:   ./multival.sh up
# Then run this script. It tries to create a fifth validator twice: once with
# a 50% maximum commission, which must be refused, and once with a 20%
# maximum, which must be accepted. Throwaway keys only.

BIN=smartnodesd
D="$HOME/.smartnodes-multival"
H0="$D/validator0"
CHAIN="smartnodes-multi-1"
NODE="tcp://localhost:26657"
TX="--from treasury --keyring-backend test --home $H0 --chain-id $CHAIN --node $NODE --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes"
TMPHOME="$(mktemp -d)"

[ -d "$H0" ] || { echo "no test network found. Run ./multival.sh up first."; exit 1; }

count() { $BIN query staking validators --node $NODE -o json | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["validators"]))'; }

# A fresh consensus key for the new validator.
$BIN init candidate --home "$TMPHOME" --chain-id "$CHAIN" >/dev/null 2>&1
PUBKEY="$($BIN comet show-validator --home "$TMPHOME")"

validator_file() {  # validator_file MONIKER MAX_RATE
  cat > "$TMPHOME/validator.json" <<JSON
{
  "pubkey": $PUBKEY,
  "amount": "50000000usnode",
  "moniker": "$1",
  "commission-rate": "0.10",
  "commission-max-rate": "$2",
  "commission-max-change-rate": "0.01",
  "min-self-delegation": "1"
}
JSON
}

echo "== the ceiling"
$BIN query rewards params --node $NODE
echo "== validators before: $(count)   (want 4)"
echo "== existing validators' commission (want rate 0.10, maximum 0.20)"
$BIN query staking validators --node $NODE -o json | python3 -c '
import json,sys
for v in json.load(sys.stdin)["validators"]:
    c=v["commission"]["commission_rates"]
    print("  ", v["description"]["moniker"], "rate", c["rate"][:4], "maximum", c["max_rate"][:4])'

echo
echo "== 1. A validator with 10% commission but a 50% maximum (should be REJECTED)"
validator_file greedy 0.50
$BIN tx staking create-validator "$TMPHOME/validator.json" $TX 2>&1 | tail -2
sleep 6
echo "   validators now: $(count)   (want 4)"

echo
echo "== 2. A validator with a 21% maximum, just over the ceiling (should be REJECTED)"
validator_file nearly 0.21
$BIN tx staking create-validator "$TMPHOME/validator.json" $TX 2>&1 | tail -2
sleep 6
echo "   validators now: $(count)   (want 4)"

echo
echo "== 3. The same validator with a 20% maximum (should be ACCEPTED)"
validator_file fair 0.20
$BIN tx staking create-validator "$TMPHOME/validator.json" $TX 2>&1 | grep -E '^(code|raw_log|txhash)'
sleep 8
echo "   validators now: $(count)   (want 5)"

echo
echo "== 4. The new validator has only 50 SNODE of its own stake, below this network's"
echo "      100 SNODE minimum, so it must NOT receive the equal share"
curl -s localhost:26657/block_results | python3 -c '
import json,sys
r=json.load(sys.stdin)["result"]
ev=[e for e in (r.get("finalize_block_events") or []) if e["type"].startswith("rewards_equal_share")]
for e in ev[-1:]:
    a={x["key"]:x["value"] for x in e["attributes"]}
    print("   equal share paid to", a.get("validators"), "validators   (want 4, not 5)")'

rm -rf "$TMPHOME"
echo
echo "Finished. Copy everything above and send it back, then run ./multival.sh wipe"
