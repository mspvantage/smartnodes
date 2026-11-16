#!/usr/bin/env bash
# Live check of non-voting stake on the four-validator test network.
#
# Start the network with validator0's own account on the non-voting list:
#
#     NONVOTING=1 ./multival.sh up
#
# then run this script. validator0 holds half of all stake (400 of 800 SNODE).
# It votes YES; validator1 (200 SNODE) votes NO; the other two do not vote.
# On an ordinary chain YES would win 400 to 200. Here validator0's stake has
# no say, so the count must be YES 0, NO 200, and the proposal must fail.
# Throwaway keys only.

BIN=smartnodesd
D="$HOME/.smartnodes-multival"
CHAIN="smartnodes-multi-1"
NODE="tcp://localhost:26657"
tx_as() {  # tx_as VALIDATOR_INDEX KEYNAME ...
  local i="$1" key="$2"; shift 2
  $BIN tx "$@" --from "$key" --keyring-backend test --home "$D/validator$i" --chain-id $CHAIN --node $NODE \
    --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes 2>&1 | grep -E '^(code|raw_log|txhash)|rpc error'
}

[ -d "$D/validator0" ] || { echo "no test network found. Run: NONVOTING=1 ./multival.sh up"; exit 1; }

echo "== the non-voting list"
$BIN query rewards params --node $NODE
LISTED=$($BIN query rewards params --node $NODE -o json | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["params"].get("non_voting_addresses") or []))')
if [ "$LISTED" = "0" ]; then
  echo "The list is empty. Wipe the network and start it with: NONVOTING=1 ./multival.sh up"
  exit 1
fi

cat > /tmp/sn-proposal.json <<JSON
{
  "messages": [],
  "metadata": "non-voting stake trial",
  "deposit": "10000000usnode",
  "title": "Trial: can non-voting stake carry a vote?",
  "summary": "validator0 votes yes with half of all stake; validator1 votes no.",
  "expedited": false
}
JSON

echo
echo "== 1. A proposal is submitted"
tx_as 0 treasury gov submit-proposal /tmp/sn-proposal.json
sleep 6

echo
echo "== 2. validator0 (400 SNODE, non-voting) votes YES"
tx_as 0 validator0 gov vote 1 yes
echo "== 3. validator1 (200 SNODE) votes NO"
tx_as 1 validator1 gov vote 1 no
sleep 6

echo
echo "== 4. The count while voting is open"
echo "      (want yes 0, no 200000000; an ordinary chain would show yes 400000000)"
$BIN query gov tally 1 --node $NODE

echo
echo "== waiting 35 seconds for the 30-second voting period to end..."
sleep 35

echo "== 5. The result (want PROPOSAL_STATUS_REJECTED)"
$BIN query gov proposal 1 --node $NODE -o json | python3 -c '
import json,sys
p=json.load(sys.stdin)["proposal"]
t=p.get("final_tally_result",{})
print("   status:", p["status"])
print("   yes:", t.get("yes_count","0"), " no:", t.get("no_count","0"), " abstain:", t.get("abstain_count","0"), " veto:", t.get("no_with_veto_count","0"))'

echo
echo "== 6. validator0 still earns rewards and still produces blocks"
./multival.sh check 2>/dev/null | sed -n '/validators in the active set/,$p'

echo
echo "Finished. Copy everything above and send it back, then run ./multival.sh wipe"
