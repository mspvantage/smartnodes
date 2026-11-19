#!/usr/bin/env bash
# Stage 1 live trial of the SmartNodes VPN: one real paid session through one
# node on this server.
#
# What it does:
#   - registers a ticket issuer key and an exit node on the local test chain;
#   - Bob buys three tickets;
#   - starts smartnode on this server;
#   - creates a separate network space, "snclient", that stands in for a
#     user's device. It has no route to the internet of its own;
#   - pays the node with one ticket from inside that space and checks that
#     internet traffic now flows, through the tunnel;
#   - tries tickets that must be refused;
#   - checks the node was paid before it served, and waits for the
#     session to end.
#
# Run it in a second terminal while "ignite30 cosmos chain serve --reset-once"
# is running in the first, on a FRESH chain. It asks for your sudo password
# once. It removes everything it set up when it finishes. Throwaway test keys
# only. It takes about seven minutes.

BIN=smartnodesd
CHAIN=smartnodes
KR="--keyring-backend test"
COMMON="$KR --chain-id $CHAIN --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes"
AS_ALICE="--from alice $COMMON"
AS_BOB="--from bob $COMMON"
WAIT=7

NS=snclient
HOSTIF=sn-host
CLIIF=sn-cli
HOSTIP=10.200.0.1
CLIIP=10.200.0.2
PORT=51820
SUBNET=10.77.0.0/24
TAG=smartnodes-trial
WORK=~/vpn-trial

step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
ok() { grep -E '^(code|raw_log|txhash)'; }
first_address() { jq -r '.. | .address? // empty' | head -1; }
usnode() { $BIN query bank balances "$1" -o json | jq -r '[(.balances // [])[] | select(.denom=="usnode") | .amount][0] // "0"'; }
in_client() { sudo ip netns exec $NS "$@"; }

for tool in jq curl wg ip iptables ticketctl smartnode smartvpn smartnodesd; do
  command -v $tool >/dev/null || { echo "$tool not found. See the instructions: wireguard-tools, jq and curl come from apt; the others from 'go install ./cmd/...'"; exit 1; }
done
SMARTNODE=$(command -v smartnode)
SMARTVPN=$(command -v smartvpn)
SMARTNODESD=$(command -v smartnodesd)
WAN=$(ip route show default | awk '{print $5; exit}')
[ -n "$WAN" ] || { echo "could not find this server's internet interface"; exit 1; }

echo "This trial needs root for networking. sudo may ask for your password now."
sudo -v || exit 1

if [ "$($BIN query registry list-node -o json 2>/dev/null | jq -r '.node | length')" != "0" ]; then
  echo "This chain already has nodes, or is not running. Start a fresh one with: ignite30 cosmos chain serve --reset-once"
  exit 1
fi

OLD_FORWARD=$(cat /proc/sys/net/ipv4/ip_forward)
cleanup() {
  echo
  echo "== cleaning up"
  sudo pkill -TERM -f "$SMARTNODE -key-file $WORK/node.key" 2>/dev/null
  sleep 3
  sudo ip netns del $NS 2>/dev/null
  sudo ip link del $HOSTIF 2>/dev/null
  sudo ip link del sn0 2>/dev/null
  sudo rm -rf /etc/netns/$NS
  sudo iptables -t nat -D POSTROUTING -s $SUBNET -o "$WAN" -m comment --comment $TAG -j MASQUERADE 2>/dev/null
  sudo iptables -D FORWARD -i sn0 -o "$WAN" -m comment --comment $TAG -j ACCEPT 2>/dev/null
  sudo iptables -D FORWARD -i "$WAN" -o sn0 -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment $TAG -j ACCEPT 2>/dev/null
  sudo iptables -D INPUT -i $HOSTIF -m comment --comment $TAG -j ACCEPT 2>/dev/null
  sudo iptables -D INPUT -i sn0 -m comment --comment $TAG -j ACCEPT 2>/dev/null
  sudo sysctl -q -w net.ipv4.ip_forward="$OLD_FORWARD"
  # The node ran the chain program as root; give the key folder back.
  sudo chown -R "$(id -u):$(id -g)" "$HOME/.smartnodes"
  stty sane 2>/dev/null
  echo "   done; this server is as it was"
}
trap cleanup EXIT

rm -rf "$WORK" && mkdir -p "$WORK" && cd "$WORK" || exit 1
ALICE=$($BIN keys show alice -a $KR)
BOB=$($BIN keys show bob -a $KR)
GOV=$($BIN query auth module-account gov -o json | first_address)

step "1. Governance registers a ticket issuer key (1 SNODE per ticket)"
PUBKEY=$(ticketctl issuer-new -out issuer.json)
NOW=$(date +%s)
cat > proposal.json <<JSON
{
  "messages": [
    {
      "@type": "/smartnodes.tickets.v1.MsgAddIssuerKey",
      "authority": "$GOV",
      "pubkey": "$PUBKEY",
      "price": { "denom": "usnode", "amount": "1000000" },
      "sign_end": "$((NOW + 86400))",
      "redeem_end": "$((NOW + 172800))"
    }
  ],
  "metadata": "vpn trial",
  "deposit": "10000000usnode",
  "title": "Register ticket issuer key 0",
  "summary": "Local VPN trial.",
  "expedited": false
}
JSON
$BIN tx gov submit-proposal proposal.json $AS_ALICE | ok
sleep $WAIT
$BIN tx gov vote 1 yes $AS_ALICE | ok
echo "waiting 40 seconds for the vote to finish..."
sleep 40
echo "proposal status: $($BIN query gov proposal 1 -o json | jq -r '.proposal.status')   (want PROPOSAL_STATUS_PASSED)"

step "2. Bob buys three tickets"
BLINDED=$(ticketctl wallet-new -out wallet.json -chain-id $CHAIN -key-id 0 -count 3)
$BIN tx tickets buy-tickets 0 $BLINDED $AS_BOB | ok
sleep $WAIT
BLIND_SIGS=$(ticketctl issuer-sign -issuer issuer.json $BLINDED)
$BIN tx tickets fulfil-purchase 0 $BLIND_SIGS $AS_ALICE | ok
sleep $WAIT
ONCHAIN_SIGS=$($BIN query tickets get-purchase 0 -o json | jq -r '.purchase.signatures | join(" ")')
ticketctl wallet-unblind -wallet wallet.json -pubkey "$PUBKEY" $ONCHAIN_SIGS

step "3. Alice registers this server as exit node 0"
(umask 077; wg genkey > node.key)
NODEPUB=$(wg pubkey < node.key)
echo "node public key: $NODEPUB"
$BIN tx registry register-node "$HOSTIP:$PORT" "$NODEPUB" 2 5000000000usnode $AS_ALICE | ok
sleep $WAIT
$BIN query registry get-node 0

step "4. A separate network space stands in for the user's device"
sudo ip netns add $NS
sudo ip link add $HOSTIF type veth peer name $CLIIF
sudo ip link set $CLIIF netns $NS
sudo ip address add $HOSTIP/24 dev $HOSTIF
sudo ip link set $HOSTIF up
sudo ip netns exec $NS ip address add $CLIIP/24 dev $CLIIF
sudo ip netns exec $NS ip link set $CLIIF up
sudo ip netns exec $NS ip link set lo up
sudo mkdir -p /etc/netns/$NS
echo "nameserver 1.1.1.1" | sudo tee /etc/netns/$NS/resolv.conf >/dev/null
# Let the node pass its clients' traffic to the internet.
sudo sysctl -q -w net.ipv4.ip_forward=1
sudo iptables -t nat -A POSTROUTING -s $SUBNET -o "$WAN" -m comment --comment $TAG -j MASQUERADE
sudo iptables -I FORWARD -i sn0 -o "$WAN" -m comment --comment $TAG -j ACCEPT
sudo iptables -I FORWARD -i "$WAN" -o sn0 -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment $TAG -j ACCEPT
# This server's firewall may only allow known ports in. Let the device's
# network space reach the node: the payment exchange (TCP) and WireGuard
# (UDP) both use port $PORT.
sudo iptables -I INPUT -i $HOSTIF -m comment --comment $TAG -j ACCEPT
sudo iptables -I INPUT -i sn0 -m comment --comment $TAG -j ACCEPT
echo "the device can reach the node's address:"
in_client ping -c 1 -W 2 $HOSTIP | grep -E 'packets transmitted'
echo "but it has no internet of its own (want: no internet):"
in_client curl -s -m 5 https://api.ipify.org >/dev/null && echo "   UNEXPECTED: it has internet" || echo "   no internet"

step "5. smartnode starts on this server"
# Started in the background by sudo itself, so it does not take over this
# terminal.
sudo -b bash -c "exec '$SMARTNODE' -key-file '$WORK/node.key' -node-id 0 -chain-id $CHAIN -listen $HOSTIP:$PORT \
  -session 3m -smartnodesd '$SMARTNODESD' -from alice -keyring-backend test -home '$HOME/.smartnodes' \
  > '$WORK/smartnode.log' 2>&1 < /dev/null"
sleep 4
cat smartnode.log
echo "the device can reach the node's payment port:"
in_client timeout 5 bash -c "echo > /dev/tcp/$HOSTIP/$PORT" 2>/dev/null && echo "   yes" || echo "   NO: something on this server is blocking port $PORT; send this output back"

step "6. The device pays the node with one ticket and connects"
T0=$(ticketctl wallet-spend -wallet wallet.json -node-id 0 -index 0)
ALICE_BEFORE=$(usnode "$ALICE")
START=$(date +%s.%N)
in_client "$SMARTVPN" up -node-id 0 -node-key "$NODEPUB" -endpoint $HOSTIP:$PORT -chain-id $CHAIN -key-id 0 -ticket "$T0"
echo "connecting took $(python3 -c "print(round($(date +%s.%N) - $START, 1))") seconds   (want 5 to 15: the node waits for the chain to accept the ticket)"

step "7. Traffic flows through the tunnel"
SERVER_IP=$(curl -s -m 10 https://api.ipify.org)
DEVICE_IP=$(in_client curl -s -m 10 https://api.ipify.org)
echo "this server's public address:           $SERVER_IP"
echo "what websites see from the device:      ${DEVICE_IP:-nothing}   (want the same as above)"
sudo wg show sn0 | grep -E 'peer|allowed ips|latest handshake|transfer'

step "8a. The same ticket again, with a new session key (should be REFUSED)"
in_client "$SMARTVPN" up -iface sv1 -node-id 0 -node-key "$NODEPUB" -endpoint $HOSTIP:$PORT -chain-id $CHAIN -key-id 0 -ticket "$T0"
step "8b. A ticket made out to node 7 (should be REFUSED)"
T_OTHER=$(ticketctl wallet-spend -wallet wallet.json -node-id 7 -index 1)
in_client "$SMARTVPN" up -iface sv1 -node-id 0 -node-key "$NODEPUB" -endpoint $HOSTIP:$PORT -chain-id $CHAIN -key-id 0 -ticket "$T_OTHER"
step "8c. The device is given the wrong key for the node (should FAIL, revealing nothing)"
WRONG=$(wg genkey | wg pubkey)
T2=$(ticketctl wallet-spend -wallet wallet.json -node-id 0 -index 2)
in_client "$SMARTVPN" up -iface sv1 -node-id 0 -node-key "$WRONG" -endpoint $HOSTIP:$PORT -chain-id $CHAIN -key-id 0 -ticket "$T2"

step "9. The node was paid before it served"
HEX=$(echo "$T0" | base64 -d | head -c 32 | od -An -tx1 | tr -d ' \n')
echo "ticket redeemed on chain: $($BIN query tickets ticket-spent 0 "$HEX" -o json | jq -r '.spent // false')   (want true)"
echo "alice was paid:           $(( $(usnode "$ALICE") - ALICE_BEFORE )) usnode   (want a little under 950000: 95% of one ticket, less the redemption fee)"

step "10. The node's log holds totals only"
cat smartnode.log
echo
echo "times the device's address appears in the log: $(grep -c "$CLIIP" smartnode.log)   (want 0)"
echo "times the ticket appears in the log:           $(grep -c "${HEX:0:16}" smartnode.log)   (want 0)"

step "11. The session ends after its three minutes, and nothing leaks"
echo "waiting for the session to end (up to 4 minutes)..."
for i in $(seq 1 48); do
  [ "$(sudo wg show sn0 peers | wc -l)" = "0" ] && break
  sleep 5
done
echo "sessions open on the node: $(sudo wg show sn0 peers | wc -l)   (want 0)"
echo "can the device still reach the internet? (want: no)"
in_client curl -s -m 5 https://api.ipify.org >/dev/null && echo "   UNEXPECTED: yes" || echo "   no: the tunnel is closed and the device has no other way out"
in_client "$SMARTVPN" down

echo
echo "Trial finished. Copy everything above and send it back."
