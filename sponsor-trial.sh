#!/usr/bin/env bash
# Live trial of sponsored bonds on a local chain: governance names a sponsor,
# the sponsor pool is funded, and the sponsor registers a node for an
# operator who holds no tokens at all.
#
# Run this in a second terminal while "ignite30 cosmos chain serve --reset-once"
# is running in the first, on a FRESH chain. It uses the throwaway test
# accounts alice and bob, and creates a third, carol, who is never given any
# tokens. It does not stop on errors: several steps are SUPPOSED to be
# rejected. It takes about a minute and a half.

BIN=smartnodesd
CHAIN=smartnodes
KR="--keyring-backend test"
COMMON="$KR --chain-id $CHAIN --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes"
AS_ALICE="--from alice $COMMON"
AS_BOB="--from bob $COMMON"
WAIT=6
BOND=1000000000   # 1,000 SNODE

step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
ok() { grep -E '^(code|raw_log|txhash)'; }
first_address() { jq -r '.. | .address? // empty' | head -1; }
usnode() { $BIN query bank balances "$1" -o json | jq -r '[(.balances // [])[] | select(.denom=="usnode") | .amount][0] // "0"'; }

command -v jq >/dev/null || { echo "jq not found: sudo apt install -y jq"; exit 1; }

ALICE=$($BIN keys show alice -a $KR)
BOB=$($BIN keys show bob -a $KR)
GOV=$($BIN query auth module-account gov -o json | first_address)
POOL=$($BIN query auth module-account registry_sponsor_pool -o json | first_address)
[ -n "$POOL" ] || { echo "could not read the sponsor pool address; is the chain running with the new code?"; exit 1; }
$BIN keys delete carol $KR --yes >/dev/null 2>&1
$BIN keys add carol $KR >/dev/null 2>&1
CAROL=$($BIN keys show carol -a $KR)
echo "alice (funds the pool):          $ALICE"
echo "bob   (will be the sponsor):     $BOB"
echo "carol (operator with no tokens): $CAROL"
echo "sponsor pool:                    $POOL"
if [ "$($BIN query registry list-node -o json | jq -r '.node | length')" != "0" ]; then
  echo "This chain already has nodes. Restart it with: ignite30 cosmos chain serve --reset-once"
  exit 1
fi
KEY1=$(head -c 32 /dev/urandom | base64)
KEY2=$(head -c 32 /dev/urandom | base64)

step "1. Sponsorship is off by default: Bob tries to sponsor a node (should be REJECTED)"
$BIN query registry params
$BIN tx registry sponsor-node "$CAROL" "vpn0.example.com:51820" "$KEY1" 1 $AS_BOB 2>&1 | tail -2

step "2. Governance names Bob as the sponsor"
cat > /tmp/sponsor-proposal.json <<JSON
{
  "messages": [
    {
      "@type": "/smartnodes.registry.v1.MsgUpdateParams",
      "authority": "$GOV",
      "params": {
        "min_bond_entry": "1000000000",
        "min_bond_exit": "5000000000",
        "unbonding_seconds": "1814400",
        "sponsor": "$BOB",
        "max_sponsored_nodes": "1"
      }
    }
  ],
  "metadata": "sponsor trial",
  "deposit": "10000000usnode",
  "title": "Name the sponsor account",
  "summary": "Local trial: switch sponsored bonds on, at most one node.",
  "expedited": false
}
JSON
$BIN tx gov submit-proposal /tmp/sponsor-proposal.json $AS_ALICE | ok
sleep $WAIT
$BIN tx gov vote 1 yes $AS_ALICE | ok
echo "waiting 40 seconds for the 30-second voting period to end..."
sleep 40
echo "proposal status: $($BIN query gov proposal 1 -o json | jq -r '.proposal.status')   (want PROPOSAL_STATUS_PASSED)"
echo "sponsor now:     $($BIN query registry params -o json | jq -r '.params.sponsor')   (want $BOB)"

step "3. The pool is empty: sponsoring now (should be REJECTED: the pool cannot cover the bond)"
$BIN tx registry sponsor-node "$CAROL" "vpn0.example.com:51820" "$KEY1" 1 $AS_BOB 2>&1 | tail -2

step "3a. A direct transfer to the pool (should be REJECTED: the account is blocked)"
$BIN tx bank send alice "$POOL" 1000usnode $COMMON 2>&1 | tail -2

step "4. Alice funds the pool with 3,000 SNODE through the funding transaction"
$BIN tx registry fund-sponsor-pool 3000000000 $AS_ALICE | ok
sleep $WAIT
echo "pool balance: $(usnode "$POOL")   (want 3000000000)"

step "4a. Alice, who is not the sponsor, tries to sponsor a node (should be REJECTED)"
$BIN tx registry sponsor-node "$CAROL" "vpn0.example.com:51820" "$KEY1" 1 $AS_ALICE 2>&1 | tail -2

step "5. Bob sponsors a node for Carol, who has no tokens"
echo "carol's balance before: $(usnode "$CAROL")   (want 0)"
$BIN tx registry sponsor-node "$CAROL" "vpn0.example.com:51820" "$KEY1" 1 $AS_BOB | ok
sleep $WAIT
echo "the node (want operator carol, bond 1000000000usnode, sponsored 1000000000):"
$BIN query registry get-node 0
echo "pool balance: $(usnode "$POOL")   (want 2000000000: 1,000 SNODE lent out)"

step "5a. A second sponsored node (should be REJECTED: the limit is one)"
$BIN tx registry sponsor-node "$CAROL" "vpn1.example.com:51820" "$KEY2" 1 $AS_BOB 2>&1 | tail -2

step "6. Alice, a stranger to this node, tries to take it out of service (should be REJECTED)"
$BIN tx registry deregister-node 0 $AS_ALICE 2>&1 | tail -2

step "7. The sponsor takes the node out of service to recover the bond"
$BIN tx registry deregister-node 0 $AS_BOB | ok
sleep $WAIT
echo "node status: $($BIN query registry get-node 0 -o json | jq -r '.node.status')   (want 2: unbonding)"

step "7a. Withdrawing before the 21 days are over (should be REJECTED)"
$BIN tx registry withdraw-bond 0 $AS_BOB 2>&1 | tail -2
echo
echo "pool balance: $(usnode "$POOL")   (want 2000000000: the bond returns only after unbonding)"
echo "carol's balance: $(usnode "$CAROL")   (want 0: the bond was never hers)"

echo
echo "Trial finished. Copy everything above and send it back."
