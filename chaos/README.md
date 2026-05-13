# Chaos / failover test

Drives a 3-node etcd cluster + etcd-ui through a sequence of node kills and
verifies the UI keeps serving reads and writes.

## Run

```bash
make build-image                                # build etcd-ui:dev
docker compose -f chaos/docker-compose.yml up -d
chaos/run.sh
docker compose -f chaos/docker-compose.yml down -v   # only after a pass
```

## Scenarios

### Base — `chaos/run.sh`

| # | What we do                              | What we assert                                  |
|---|-----------------------------------------|-------------------------------------------------|
| A | Kill a follower                          | UI keeps responding; writes succeed             |
| B | Kill the current leader                  | Writes pause briefly then succeed (re-election) |
| C | Restart killed nodes                     | Cluster reports 3/3 healthy members             |

### Extended — `chaos/scenarios.sh` (requires the toxiproxy overlay)

```bash
docker compose -f chaos/docker-compose.yml -f chaos/docker-compose.toxiproxy.yml up -d
chaos/scenarios.sh
```

| # | What we do                                                       | What we assert                         |
|---|------------------------------------------------------------------|----------------------------------------|
| D | Network partition: disable Toxiproxy for one member              | Writes succeed via quorum (2/3)         |
| E | Inject 250ms latency on one member's upstream                    | Writes complete within 2 s              |
| F | Bandwidth-throttle one member to 1 KB/s                          | Writes succeed; throttled member lags   |
| G | Pause one member (disk-full / hung-IO analog) and continue load  | Quorum holds; UI flags 2/3 healthy      |
| H | True split-brain: `docker network disconnect` cuts BOTH peer + client traffic, simulating an L2 partition | Majority keeps writing; reconnect → 3/3 |

Requires `jq` and `curl` on the host. Exit code 0 == pass. On failure
containers are left running for inspection (`docker logs chaos-etcd0`).

## CI

Add to `.github/workflows/chaos.yml` on a schedule (weekly is enough — chaos
runs take ~2 min and pull two images):

```yaml
name: chaos
on:
  schedule: [{ cron: "0 3 * * 0" }]
  workflow_dispatch:
jobs:
  chaos:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: make build-image
      - run: docker compose -f chaos/docker-compose.yml up -d
      - run: sudo apt-get install -y jq
      - run: chaos/run.sh
      - if: always()
        run: docker compose -f chaos/docker-compose.yml down -v
```
