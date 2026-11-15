#!/usr/bin/env bash
# Live trial of the SmartNodes verify module with PENALTIES SWITCHED ON
# (stage 2a) on a local chain: one node is jailed for failing, another is
# slashed and jailed for tampering, and the first is unjailed.
#
# Run this in a second terminal while "ignite30 cosmos chain serve --reset-once"
# is running in the first, on a FRESH chain. It uses the throwaway test
# accounts alice and bob, and creates a third, carol, so that three probers
# can agree. It does not stop on errors: several steps are SUPPOSED to be
# rejected. It takes about eight minutes.

BIN=smartnodesd
CHAIN=smartnodes
KR="--keyring-backend test"
COMMON="$KR --chain-id $CHAIN --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes"
AS_ALICE="--from alice $COMMON"
AS_BOB="--from bob $COMMON"
AS_CAROL="--from carol $COMMON"
WAIT=6
BOND=1000000000   # 1,000 SNODE

step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
ok() { grep -E '^(code|raw_log|txhash)'; }
first_address() { jq -r '.. | .address? // empty' | head -1; }
usnode() { $BIN query bank balances "$1" -o json | jq -r '.balances[] | select(.denom=="usnode") | .amount'; }
round() { $BIN query verify round -o json | jq -r '.round // "0"'; }
height() { $BIN status 2>/dev/null | jq -r '.sync_info.latest_block_height // .SyncInfo.latest_block_height'; }
prober_field() { $BIN query verify list-prober -o json | jq -r --arg o "$1" --arg f "$2" '.prober[] | select(.owner==$o) | .[$f] // "0"'; }
status_name() { case "$1" in 1) echo "1 (waiting to join)";; 2) echo "2 (active)";; 3) echo "3 (leaving)";; 4) echo "4 (unbonding)";; *) echo "$1";; esac; }

# wait_new_round blocks until the round number changes, then one more block.
wait_new_round() {
  local cur; cur=$(round)
  echo "   waiting for round $((cur + 1)) to start (now round $cur, block $(height))..."
  while [ "$(round)" = "$cur" ]; do sleep 2; done
  sleep 2
  echo "   round $(round) started at block $(height)"
}

# seal ROUND NODE PROBER RESULT SPEED SALTHEX: the sealed form of a report,
# computed the same way the chain does.
seal() {
  python3 - "$CHAIN" "$@" <<'PY'
import hashlib, struct, sys
chain, rnd, node, prober, result, speed, salt = sys.argv[1], *map(int, sys.argv[2:7]), bytes.fromhex(sys.argv[7])
be = lambda v: struct.pack(">Q", v)
h = hashlib.sha256()
h.update(b"SMARTNODES-VERIFY-COMMIT-V1")
h.update(be(len(chain))); h.update(chain.encode())
for v in (rnd, node, prober, result, speed): h.update(be(v))
h.update(be(len(salt))); h.update(salt)
print(h.hexdigest())
PY
}

command -v jq >/dev/null || { echo "jq not found: sudo apt install -y jq"; exit 1; }

node_bond() { $BIN query registry get-node "$1" -o json | jq -r '.node.bond.amount'; }
UNJAIL_WAIT=120

ALICE=$($BIN keys show alice -a $KR)
BOB=$($BIN keys show bob -a $KR)
GOV=$($BIN query auth module-account gov -o json | first_address)
[ -n "$GOV" ] || { echo "could not read the governance address; is the chain running?"; exit 1; }
ROUND_BLOCKS=$($BIN query verify params -o json | jq -r '.params.round_blocks')
if [ "$ROUND_BLOCKS" -gt 100 ]; then
  echo "The round is too long for a trial. config.yml should set verify round_blocks to 40; restart the chain with --reset-once."
  exit 1
fi
if [ "$($BIN query registry list-node -o json | jq -r '.node | length')" != "0" ]; then
  echo "This chain already has nodes. Restart it with: ignite30 cosmos chain serve --reset-once"
  exit 1
fi

step "1. Alice registers two VPN nodes (ids 0 and 1), 1,000 SNODE bond each"
$BIN tx registry register-node "vpn0.example.com:51820" "$(head -c 32 /dev/urandom | base64)" 1 ${BOND}usnode $AS_ALICE | ok
sleep $WAIT
$BIN tx registry register-node "vpn1.example.com:51820" "$(head -c 32 /dev/urandom | base64)" 1 ${BOND}usnode $AS_ALICE | ok
sleep $WAIT
echo "node 0 bond: $(node_bond 0)   node 1 bond: $(node_bond 1)   (want $BOND each)"

step "2. A third test account, carol, is created and funded"
$BIN keys delete carol $KR --yes >/dev/null 2>&1
$BIN keys add carol $KR >/dev/null 2>&1
CAROL=$($BIN keys show carol -a $KR)
echo "carol: $CAROL"
$BIN tx bank send alice "$CAROL" 1100000000usnode $COMMON | ok
sleep $WAIT

step "3. Alice, Bob and Carol become probers"
$BIN tx verify register-prober $BOND $AS_ALICE | ok
$BIN tx verify register-prober $BOND $AS_BOB | ok
$BIN tx verify register-prober $BOND $AS_CAROL | ok
sleep $WAIT
A_ID=$(prober_field "$ALICE" id); B_ID=$(prober_field "$BOB" id); C_ID=$(prober_field "$CAROL" id)
echo "prober ids: alice $A_ID, bob $B_ID, carol $C_ID"

step "4. Governance switches penalties on (jail after 1 failed round, unjail wait $UNJAIL_WAIT seconds)"
cat > /tmp/verify-proposal.json <<JSON
{
  "messages": [
    {
      "@type": "/smartnodes.verify.v1.MsgUpdateParams",
      "authority": "$GOV",
      "params": {
        "round_blocks": "$ROUND_BLOCKS",
        "probers_per_node": "3",
        "sample_bps": "10000",
        "min_prober_bond": "$BOND",
        "unbonding_seconds": "1814400",
        "fails_before_jail": "1",
        "tamper_slash_bps": "1000",
        "report_only": false,
        "unjail_wait_seconds": "$UNJAIL_WAIT",
        "report_reward": "10000"
      }
    }
  ],
  "metadata": "",
  "deposit": "10000000usnode",
  "title": "Switch verification penalties on",
  "summary": "Local trial: turn report-only off.",
  "expedited": false
}
JSON
$BIN tx gov submit-proposal /tmp/verify-proposal.json $AS_ALICE | ok
sleep $WAIT
$BIN tx gov vote 1 yes $AS_ALICE | ok
echo "waiting 40 seconds for the 30-second voting period to end..."
sleep 40
echo "proposal status: $($BIN query gov proposal 1 -o json | jq -r '.proposal.status')   (want PROPOSAL_STATUS_PASSED)"
echo "report_only now: $($BIN query verify params -o json | jq -r '.params.report_only // false')   (want false)"

step "5. A new round starts and the three probers join the active set"
wait_new_round
R=$(round)
echo "active probers: $($BIN query verify round -o json | jq -r '.active_probers // "0"')   (want 3)"

step "6. Round $R: all three seal FAIL for node 0 and TAMPER for node 1"
SA=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
SB=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
SC=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
# result 2 = fail, 3 = tamper
$BIN tx verify commit-reports "$R" 0 "$(seal "$R" 0 "$A_ID" 2 0 "$SA")" $AS_ALICE | ok
$BIN tx verify commit-reports "$R" 0 "$(seal "$R" 0 "$B_ID" 2 0 "$SB")" $AS_BOB | ok
$BIN tx verify commit-reports "$R" 0 "$(seal "$R" 0 "$C_ID" 2 0 "$SC")" $AS_CAROL | ok
sleep $WAIT
$BIN tx verify commit-reports "$R" 1 "$(seal "$R" 1 "$A_ID" 3 0 "$SA")" $AS_ALICE | ok
$BIN tx verify commit-reports "$R" 1 "$(seal "$R" 1 "$B_ID" 3 0 "$SB")" $AS_BOB | ok
$BIN tx verify commit-reports "$R" 1 "$(seal "$R" 1 "$C_ID" 3 0 "$SC")" $AS_CAROL | ok
sleep $WAIT

step "7. Next round: the reports are opened"
wait_new_round
$BIN tx verify reveal-report "$R" 0 2 0 "$SA" $AS_ALICE | ok
$BIN tx verify reveal-report "$R" 0 2 0 "$SB" $AS_BOB | ok
$BIN tx verify reveal-report "$R" 0 2 0 "$SC" $AS_CAROL | ok
sleep $WAIT
$BIN tx verify reveal-report "$R" 1 3 0 "$SA" $AS_ALICE | ok
$BIN tx verify reveal-report "$R" 1 3 0 "$SB" $AS_BOB | ok
$BIN tx verify reveal-report "$R" 1 3 0 "$SC" $AS_CAROL | ok
sleep $WAIT

step "8. Round after: the tally jails node 0 and slashes and jails node 1"
wait_new_round
sleep $WAIT
echo "node 0 record (want jailed: true, fails: 1, no slashed amount):"
$BIN query verify node-record 0
echo "node 0 bond: $(node_bond 0)   (want $BOND: being offline never costs bond)"
echo
echo "node 1 record (want jailed: true, tamper_rounds: 1, slashed: $((BOND / 10))):"
$BIN query verify node-record 1
echo "node 1 bond: $(node_bond 1)   (want $((BOND - BOND / 10)): 10% burned)"
echo
echo "prober accepted counts: alice $(prober_field "$ALICE" accepted), bob $(prober_field "$BOB" accepted), carol $(prober_field "$CAROL" accepted)   (want 2 each)"

step "8b. A jailed node is not probed"
$BIN query verify assignment 0
R2=$(round)
echo "sealing a report on jailed node 0 (should be REJECTED):"
$BIN tx verify commit-reports "$R2" 0 "$(seal "$R2" 0 "$B_ID" 1 0 "$SB")" $AS_BOB 2>&1 | tail -2

step "9a. Bob, who does not run node 0, tries to unjail it (should be REJECTED)"
$BIN tx verify unjail 0 $AS_BOB 2>&1 | tail -2
step "9b. Alice unjails node 0 before the wait is over (should be REJECTED)"
$BIN tx verify unjail 0 $AS_ALICE 2>&1 | tail -2

step "10. After the wait, Alice unjails node 0"
echo "waiting $UNJAIL_WAIT seconds..."
sleep $UNJAIL_WAIT
$BIN tx verify unjail 0 $AS_ALICE | ok
sleep $WAIT
echo "node 0 record (want no jailed field, jail_count: 1; it is verified again only when it next passes):"
$BIN query verify node-record 0
step "10b. Unjailing it a second time (should be REJECTED)"
$BIN tx verify unjail 0 $AS_ALICE 2>&1 | tail -2
echo
echo "node 0 can be probed again:"
$BIN query verify assignment 0

echo
echo "Trial finished. Copy everything above and send it back."
