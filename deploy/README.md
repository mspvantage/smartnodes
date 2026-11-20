# Files for running SmartNodes on a server

| File | What it is |
| --- | --- |
| `smartnodesd.service` | The chain node or validator, under Cosmovisor |
| `smartnode@.service` + `node.env.example` | A VPN node (entry, exit or bridge); one settings file per node |
| `smartprobe.service` + `probe.env.example` | A prober |
| `journald-volatile.conf` | Keeps system logs in memory, as VPN nodes require |

Step-by-step guides: https://smartnodes.tech/validator.html and
https://smartnodes.tech/run-a-node.html
