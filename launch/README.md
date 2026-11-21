# Launch kit: starting the SmartNodes test network

| File | What it is |
| --- | --- |
| `testnet.json` | The network's settings: chain id, starting balances, chain parameters |
| `build-genesis.sh` | Builds the genesis file from them: `draft`, then `final` |
| `rehearsal.sh` | Rehearses a whole launch on one machine with four validators |
| `upgrade-rehearsal.sh` | Rehearses a chain upgrade by vote on a running four-validator network |

## Launch day, in order

1. **Rehearse.** `bash rehearsal.sh` must end with 0 failed.
2. **Accounts.** On the server that will run them, make the faucet and
   issuer keys (`smartnodesd keys add faucet` / `issuer`), and collect each
   first validator's address. Put them all in `testnet.json` (no `CHANGE`
   left), and the seed nodes' `id@host:26656` in `seeds`.
3. **Draft.** `bash build-genesis.sh draft`. Send `out/genesis-draft.json` to
   each first validator. They follow the validator guide's "Genesis
   validators" section and send back their gentx file; put each in `gentx/`.
4. **Final.** Set `genesis_time` in `testnet.json` to the agreed start, a few
   hours ahead, in UTC (for example `2026-12-01T15:00:00Z`), run `draft`
   again, then `bash build-genesis.sh final`. It checks every gentx, and
   writes `out/genesis.json`, its checksum, and `out/network.txt`.
5. **Publish.** Put `out/genesis.json` on the website at
   `website/site/testnet/genesis.json`. In `website/build.py`, fill in
   `NETWORK` from `out/network.txt` and set `"launched": True`. Rebuild and
   push. Every guide now shows the real values.
6. **Start.** Each first validator places the final genesis file (checking
   its checksum) and starts before `genesis_time`. The chain begins when
   more than two thirds of the stake is online.
7. **Issuer key.** Make it with `ticketctl issuer-new` (on the issuer
   server, mode 0600), and add it by governance proposal, as the issuer
   trial does. Votes take an hour.
8. **Issuer and faucet.** Install `deploy/smartissuer.service` and
   `deploy/smartfaucet.service` with their settings files. Put the faucet
   behind HTTPS: for example a `faucet.smartnodes.tech` DNS record in
   Cloudflare, proxied (orange cloud), with Caddy or nginx on the server
   forwarding to `127.0.0.1:8090`. Then set the faucet address in
   `website/build.py` and push.
9. **First nodes.** Register a few entry, exit and bridge nodes so testers
   have something to use on day one.
10. **Announce.**
