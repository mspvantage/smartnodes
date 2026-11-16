#!/usr/bin/env bash
# SmartNodes multi-validator test network (local, throwaway).
#
# Starts four validators on this machine with unequal stake and shows that
# half of validator income is paid in equal shares.
#
#   ./multival.sh up      build, create and start the four validators
#   ./multival.sh check   show each validator's share of rewards so far
#   ./multival.sh down    stop the validators
#   ./multival.sh wipe    stop them and delete the test network
#
# Run it from the repository folder. Stop "ignite30 cosmos chain serve" first:
# both use port 26657. All keys here are test keys with no value.
set -euo pipefail

D="$HOME/.smartnodes-multival"
BIN="smartnodesd"
CHAIN="smartnodes-multi-1"
STAKES="400000000,200000000,100000000,100000000"   # 400, 200, 100, 100 SNODE
N=4
RPC="tcp://localhost:26657"

die() { echo "ERROR: $*" >&2; exit 1; }

stop_nodes() {
  for i in $(seq 0 $((N-1))); do
    if [ -f "$D/validator$i.pid" ]; then
      kill "$(cat "$D/validator$i.pid")" 2>/dev/null || true
      rm -f "$D/validator$i.pid"
    fi
  done
}

cmd_up() {
  [ -f go.mod ] || die "run this from the repository folder"
  if ss -ltn 2>/dev/null | grep -q ':26657 '; then
    die "port 26657 is in use. Stop 'ignite30 cosmos chain serve' (or run ./multival.sh down) first."
  fi
  [ -d "$D" ] && die "$D already exists. Run ./multival.sh wipe first."

  echo "== building smartnodesd"
  go install ./cmd/smartnodesd
  command -v "$BIN" >/dev/null || die "smartnodesd not on PATH after build (is ~/go/bin on PATH?)"

  echo "== creating $N validators"
  "$BIN" multi-node --v "$N" --output-dir "$D" \
    --validators-stake-amount "$STAKES" \
    --chain-id "$CHAIN" --keyring-backend test \
    --minimum-gas-prices 0.025usnode

  echo "== creating a treasury test account"
  "$BIN" keys add treasury --keyring-backend test --home "$D/validator0" >/dev/null 2>&1
  TREASURY="$("$BIN" keys show treasury -a --keyring-backend test --home "$D/validator0")"

  echo "== applying the economics settings to the genesis file"
  # NONVOTING=1 ./multival.sh up   puts validator0's own account on the
  # non-voting list, to try the governance rule on a live network.
  NV_ADDR=""
  if [ -n "${NONVOTING:-}" ]; then
    NV_ADDR="$("$BIN" keys show validator0 -a --keyring-backend test --home "$D/validator0")"
    echo "   non-voting account: $NV_ADDR (validator0's operator)"
  fi
  python3 - "$D/validator0/config/genesis.json" "$TREASURY" "$NV_ADDR" <<'PY'
import json, sys
path, treasury, non_voting = sys.argv[1], sys.argv[2], sys.argv[3]
g = json.load(open(path))
s = g["app_state"]

# Same values as config.yml (economics draft 2).
s["staking"]["params"]["max_validators"] = 30
s["staking"]["params"]["min_commission_rate"] = "0.050000000000000000"
s["mint"]["minter"]["inflation"] = "0.020000000000000000"
p = s["mint"]["params"]
p["inflation_rate_change"] = "0.000000000000000000"
p["inflation_min"] = "0.020000000000000000"
p["inflation_max"] = "0.020000000000000000"
p["max_supply"] = "100000000000000"
s["distribution"]["params"]["community_tax"] = "0.020000000000000000"
# Test network only: the four validators stake 100 to 400 SNODE of their own,
# so the minimum self stake for the equal share is lowered to 100 SNODE here.
# The chain's default is 5,000 SNODE.
s["rewards"]["params"]["equal_share_min_self_stake"] = "100000000"
if non_voting:
    s["rewards"]["params"]["non_voting_addresses"] = [non_voting]
gp = s["gov"]["params"]
gp["voting_period"] = "30s"
gp["expedited_voting_period"] = "15s"
gp["max_deposit_period"] = "60s"

# A large test balance so that 2% issuance is big enough to see per block.
s["auth"]["accounts"].append({
    "@type": "/cosmos.auth.v1beta1.BaseAccount",
    "address": treasury, "pub_key": None,
    "account_number": "0", "sequence": "0",
})
s["bank"]["balances"].append({
    "address": treasury,
    "coins": [{"denom": "usnode", "amount": "80000000000000"}],
})
s["bank"]["supply"] = []   # empty: the chain totals the balances itself

json.dump(g, open(path, "w"), indent=2)
print("   genesis updated")
PY
  for i in $(seq 1 $((N-1))); do
    cp "$D/validator0/config/genesis.json" "$D/validator$i/config/genesis.json"
  done
  for i in $(seq 0 $((N-1))); do
    sed -i 's/^pprof_laddr = .*/pprof_laddr = ""/' "$D/validator$i/config/config.toml"
  done

  echo "== starting validators"
  for i in $(seq 0 $((N-1))); do
    nohup "$BIN" start --home "$D/validator$i" > "$D/validator$i.log" 2>&1 &
    echo $! > "$D/validator$i.pid"
  done

  echo "== waiting for blocks"
  for _ in $(seq 1 40); do
    H="$(curl -s localhost:26657/status 2>/dev/null | python3 -c 'import json,sys
try: print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])
except Exception: print(0)' 2>/dev/null || echo 0)"
    [ "${H:-0}" -ge 3 ] 2>/dev/null && break
    sleep 2
  done
  if [ "${H:-0}" -ge 3 ] 2>/dev/null; then
    echo "   chain is running, height $H"
    echo "   wait a minute, then run: ./multival.sh check"
  else
    echo "   no blocks yet. Last lines of validator0's log:"
    tail -n 25 "$D/validator0.log"
    exit 1
  fi
}

cmd_check() {
  [ -d "$D" ] || die "no test network. Run ./multival.sh up first."
  echo "== height"
  curl -s localhost:26657/status | python3 -c 'import json,sys
print("  ", json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])'

  echo "== validators in the active set"
  "$BIN" q staking validators --node "$RPC" -o json | python3 -c 'import json,sys
v=json.load(sys.stdin)["validators"]
for x in sorted(v,key=lambda x:x["description"]["moniker"]):
    print("  ", x["description"]["moniker"], "stake", int(x["tokens"])//10**6, "SNODE", x["status"])'

  echo "== latest equal-share event"
  curl -s localhost:26657/block_results | python3 -c 'import json,sys
r=json.load(sys.stdin)["result"]
ev=[e for e in (r.get("finalize_block_events") or []) if e["type"].startswith("rewards_equal_share")]
if not ev: print("   none in the latest block")
for e in ev:
    print("  ", e["type"], {a["key"]:a["value"] for a in e["attributes"] if a["key"]!="mode"})'

  echo "== share of rewards so far"
  TMP="$(mktemp)"
  SNAP="$(curl -s localhost:26657/status | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])')"
  for i in $(seq 0 $((N-1))); do
    VAL="$("$BIN" keys show "validator$i" --bech val -a --keyring-backend test --home "$D/validator$i")"
    # Every validator is read at the same block. Without this, validators
    # read later have had more blocks to earn in and look larger.
    "$BIN" q distribution validator-outstanding-rewards "$VAL" --node "$RPC" --height "$SNAP" -o json >> "$TMP"
    echo >> "$TMP"
  done
  python3 - "$TMP" "$STAKES" <<'PY'
import json, sys
def find(o):
    if isinstance(o, str):
        return float(o[:-len("usnode")]) if o.endswith("usnode") and o[0].isdigit() else None
    if isinstance(o, dict):
        if o.get("denom") == "usnode" and "amount" in o: return float(o["amount"])
        for v in o.values():
            r = find(v)
            if r is not None: return r
    if isinstance(o, list):
        for v in o:
            r = find(v)
            if r is not None: return r
    return None
dec, text, pos, rewards = json.JSONDecoder(), open(sys.argv[1]).read(), 0, []
while True:
    while pos < len(text) and text[pos].isspace(): pos += 1
    if pos >= len(text): break
    obj, pos = dec.raw_decode(text, pos)
    rewards.append(find(obj) or 0.0)
stakes = [int(x) for x in sys.argv[2].split(",")]
tr, ts, n = sum(rewards), sum(stakes), len(stakes)
if tr == 0:
    print("   no rewards yet; wait a few more blocks"); sys.exit(0)
print("   validator   stake   by stake only   expected (half equal)   actual")
ok = True
for i,(r,s) in enumerate(zip(rewards, stakes)):
    by_stake = 100*s/ts
    expected = 50/n + 50*s/ts
    actual = 100*r/tr
    ok = ok and abs(actual-expected) < 0.5
    print(f"   validator{i}  {s//10**6:5d}   {by_stake:10.2f}%   {expected:17.2f}%   {actual:9.2f}%")
print("   RESULT:", "matches the equal-share rule" if ok else "DOES NOT MATCH - send this output back")
PY
  rm -f "$TMP"
}

case "${1:-}" in
  up)    cmd_up ;;
  check) cmd_check ;;
  down)  stop_nodes; echo "stopped" ;;
  wipe)  stop_nodes; rm -rf "$D"; echo "stopped and deleted $D" ;;
  *)     echo "usage: $0 up | check | down | wipe"; exit 1 ;;
esac
