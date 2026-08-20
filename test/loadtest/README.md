# Load testing

`k6/ws_load_test.js` drives the engine over real WebSocket connections: each
virtual user (VU) authenticates, finds a match, and plays a full
tic-tac-toe game to completion, pacing its own moves with a small random
"think time" so traffic looks like real players rather than a tight loop.

## Running it

Start the stack (server + Redis):

```sh
docker compose -f deploy/docker/docker-compose.yml up -d
```

Run k6 against it, on the same Docker network so `http://server:8080`
resolves:

```sh
docker run --rm --network docker_default \
  -v "$(pwd)/test/loadtest:/loadtest" -w /loadtest \
  -e BASE_URL=http://server:8080 \
  grafana/k6 run k6/ws_load_test.js
```

Or, against a server running outside Docker (e.g. `go run ./cmd/server`):

```sh
BASE_URL=http://localhost:8080 k6 run test/loadtest/k6/ws_load_test.js
```

## Profiles

Set `-e PROFILE=<name>` (default `smoke`):

| Profile  | Peak VUs | Peak rooms | Duration | Use for |
|----------|---------:|-----------:|---------:|---|
| `smoke`  | 20       | ~10        | 40s      | Quick sanity check after a change |
| `medium` | 300      | ~150       | 80s      | A meaningful mid-size data point |
| `large`  | 2,200    | ~1,100     | 90s      | Exceeds the 1,000+ concurrent room target in a short run |
| `full`   | 2,000    | ~1,000     | 6 min    | Longer steady-state soak; run on dedicated hardware |

See `results/BENCHMARKS.md` for real numbers from running these against
this project's own `docker-compose.yml` stack.

## What it measures

- **`action_latency_ms`** (Trend) — time from a VU sending `ACTION_MOVE` to
  receiving the matching `STATE_DIFF` broadcast. This is the headline
  server-side round-trip latency number; p95/p99 are what
  `BENCHMARKS.md` reports.
- **`match_found_rate`** (Rate) — fraction of VUs matched into a room
  within 10s of sending `FIND_MATCH`.
- **`action_timeouts`** (Counter) — moves that never got acked within 5s.
- **`ws_connect_errors`** (Counter) — WebSocket handshake/connection
  failures.
- **`games_completed`** (Counter) — games that reached a terminal state.

`options.thresholds` in the script gates CI-style pass/fail on these; tune
them once you have a baseline for your own hardware.

## Output

A custom `handleSummary` prints a compact text summary to stdout and writes
the full k6 JSON summary to `results/latest-summary.json` (gitignored —
it's a generated artifact, not tracked source).
