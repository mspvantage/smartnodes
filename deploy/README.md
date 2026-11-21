# Files for running SmartNodes on a server

| File | What it is |
| --- | --- |
| `smartnodesd.service` | The chain node or validator, under Cosmovisor |
| `smartnode@.service` + `node.env.example` | A VPN node (entry, exit or bridge); one settings file per node |
| `smartprobe.service` + `probe.env.example` | A prober |
| `journald-volatile.conf` | Keeps system logs in memory, as VPN nodes require |
| `smartnodes-setup.sh` | Prepares a fresh server for one role: `sudo bash smartnodes-setup.sh --role entry` |

Step-by-step guides: https://smartnodes.tech/validator.html and
https://smartnodes.tech/run-a-node.html
| `smartissuer.service` + `issuer.env.example` | Test network only: signs ticket purchases automatically |
| `smartfaucet.service` + `faucet.env.example` | Test network only: gives out test SNODE, behind an HTTPS proxy |
