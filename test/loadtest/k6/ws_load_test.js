// k6 load test for the real-time state synchronization engine.
//
// Each virtual user (VU) plays the role of one player: it authenticates via
// POST /session, opens a WebSocket, sends FIND_MATCH, and then plays a full
// tic-tac-toe game to completion (or until MAX_GAME_MS elapses) — pacing
// its own moves with a small random "think time" so sustained traffic looks
// like real players, not a tight send loop. With enough concurrent VUs this
// produces 1000+ concurrently active rooms (two VUs per room), which is
// exactly the scenario the project's benchmark target describes.
//
// Usage (see test/loadtest/README.md for the full walkthrough):
//   BASE_URL=http://localhost:8080 k6 run test/loadtest/k6/ws_load_test.js
//   # or scale to the target stage set:
//   BASE_URL=http://localhost:8080 k6 run --env PROFILE=full test/loadtest/k6/ws_load_test.js
//
// Metrics of interest (see summary handler at the bottom):
//   action_latency_ms   - Trend: time from sending ACTION_MOVE to the
//                         matching STATE_DIFF broadcast being received.
//                         p95/p99 of this is the headline benchmark number.
//   match_found_rate    - Rate: fraction of VUs that got matched into a
//                         room at all within MATCH_TIMEOUT_MS.
//   action_timeouts     - Counter: moves sent that never got acked in time.
//   ws_connect_errors   - Counter: WebSocket connections that failed.

import ws from "k6/ws";
import http from "k6/http";
import { check, sleep } from "k6";
import { Trend, Counter, Rate } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const WS_URL = BASE_URL.replace(/^http/, "ws") + "/ws";
const GAME_TYPE = __ENV.GAME_TYPE || "tictactoe";

const MATCH_TIMEOUT_MS = 10000; // give up waiting for FIND_MATCH after this
const ACTION_TIMEOUT_MS = 5000; // give up waiting for a move's ack after this
const MAX_GAME_MS = 30000; // hard cap so a stuck VU doesn't hang the run
const MIN_THINK_MS = 50; // pacing between "it's my turn" and sending a move
const MAX_THINK_MS = 300;

export const actionLatency = new Trend("action_latency_ms", true);
export const matchFoundRate = new Rate("match_found_rate");
export const actionTimeouts = new Counter("action_timeouts");
export const wsConnectErrors = new Counter("ws_connect_errors");
export const gamesCompleted = new Counter("games_completed");

// PROFILE=full is the 1000+-room benchmark target described in the README;
// the default profile is a much smaller smoke-test sizing suitable for a
// laptop, so `k6 run` with no flags still finishes in a reasonable time and
// produces a valid (if smaller-magnitude) result.
const PROFILES = {
  smoke: {
    stages: [
      { duration: "10s", target: 20 },
      { duration: "20s", target: 20 },
      { duration: "10s", target: 0 },
    ],
  },
  medium: {
    stages: [
      { duration: "20s", target: 300 },
      { duration: "45s", target: 300 },
      { duration: "15s", target: 0 },
    ],
  },
  full: {
    // ~2000 concurrent VUs pairing into 1000+ concurrent rooms, held long
    // enough for steady-state p95/p99 to be meaningful.
    stages: [
      { duration: "60s", target: 500 },
      { duration: "60s", target: 2000 },
      { duration: "180s", target: 2000 },
      { duration: "60s", target: 0 },
    ],
  },
  large: {
    // A shorter run than `full` that still comfortably exceeds 1000
    // concurrent rooms (~2200 VUs / ~1100 rooms) for a brief steady-state
    // window — used to validate the script and collect a real high-
    // concurrency data point without the multi-minute runtime of `full`.
    stages: [
      { duration: "40s", target: 2200 },
      { duration: "30s", target: 2200 },
      { duration: "20s", target: 0 },
    ],
  },
};

export const options = {
  scenarios: {
    players: {
      executor: "ramping-vus",
      exec: "playOneGame",
      startVUs: 0,
      stages: PROFILES[__ENV.PROFILE || "smoke"].stages,
      gracefulStop: "15s",
    },
  },
  // k6's default summaryTrendStats is ["avg", "min", "med", "max", "p(90)",
  // "p(95)"]. p(99) is NOT included unless listed explicitly here. Without
  // this, v["p(99)"] in textSummary() below is undefined, silently falls
  // back to 0, and gets misreported as a real p99 measurement.
  summaryTrendStats: ["avg", "min", "med", "max", "p(90)", "p(95)", "p(99)"],
  thresholds: {
    // These gate CI-style pass/fail; tune once you have a baseline for your
    // hardware (see test/loadtest/results/BENCHMARKS.md for recorded runs).
    action_latency_ms: ["p(95)<250", "p(99)<500"],
    match_found_rate: ["rate>0.95"],
    ws_connect_errors: ["count<10"],
  },
};

function issueSession() {
  const res = http.post(`${BASE_URL}/session`, JSON.stringify({}), {
    headers: { "Content-Type": "application/json" },
  });
  check(res, { "session issued": (r) => r.status === 200 });
  const body = res.json();
  return { token: body.token, playerId: body.playerId };
}

export function playOneGame() {
  const { token, playerId } = issueSession();

  let matched = false;
  let myTurn = false;
  let nextCellIdx = 0;
  let pendingSince = null;
  let matchDeadline = Date.now() + MATCH_TIMEOUT_MS;
  let gameOver = false;

  const params = {
    headers: { Authorization: `Bearer ${token}` },
    tags: { game: GAME_TYPE },
  };

  const res = ws.connect(WS_URL, params, function (socket) {
    socket.on("open", function () {
      socket.send(JSON.stringify({ type: "FIND_MATCH", payload: { gameType: GAME_TYPE } }));
    });

    socket.on("message", function (data) {
      const env = JSON.parse(data);

      if (env.type === "ROOM_STARTED" || env.type === "STATE_SNAPSHOT") {
        matched = true;
        const snap = env.snapshot || {};
        myTurn = snap.turn === playerId;
        if (typeof snap.moveCount === "number") nextCellIdx = snap.moveCount;
      } else if (env.type === "STATE_DIFF") {
        if (env.ackPlayer === playerId && pendingSince !== null) {
          actionLatency.add(Date.now() - pendingSince);
          pendingSince = null;
        }
        const diff = env.diff || {};
        if (diff.cell) nextCellIdx++;
        myTurn = diff.turn === playerId;
        if (env.terminal) {
          gameOver = true;
          gamesCompleted.add(1);
          socket.close();
        }
      } else if (env.type === "ERROR") {
        // A rejected action (e.g. stale seq, not-your-turn under heavy
        // pacing jitter) is expected occasionally under load; just stop
        // waiting on it rather than failing the VU.
        pendingSince = null;
      }

      if (matched && myTurn && !gameOver && nextCellIdx < 9) {
        myTurn = false;
        const x = nextCellIdx % 3;
        const y = Math.floor(nextCellIdx / 3) % 3;
        socket.setTimeout(function () {
          pendingSince = Date.now();
          socket.send(
            JSON.stringify({
              type: "ACTION_MOVE",
              seq: nextCellIdx + 1,
              ts: Date.now(),
              payload: { x: x, y: y },
            })
          );
        }, MIN_THINK_MS + Math.random() * (MAX_THINK_MS - MIN_THINK_MS));
      }
    });

    socket.on("error", function () {
      wsConnectErrors.add(1);
    });

    // Watchdog: enforce match/action/overall timeouts without blocking the
    // k6 event loop, and close the socket once we're done either way.
    socket.setInterval(function () {
      const now = Date.now();
      if (!matched && now > matchDeadline) {
        matchFoundRate.add(false);
        socket.close();
        return;
      }
      if (pendingSince !== null && now - pendingSince > ACTION_TIMEOUT_MS) {
        actionTimeouts.add(1);
        pendingSince = null;
      }
    }, 500);

    socket.setTimeout(function () {
      if (!gameOver) socket.close();
    }, MAX_GAME_MS);

    socket.on("close", function () {
      if (matched) matchFoundRate.add(true);
    });
  });

  check(res, { "ws handshake succeeded": (r) => r && r.status === 101 });
  sleep(1);
}

export function handleSummary(data) {
  return {
    stdout: textSummary(data),
    "results/latest-summary.json": JSON.stringify(data, null, 2),
  };
}

// Minimal inline summary (avoids depending on an external k6 helper library
// for a text table) — full metric detail is in the JSON summary file.
// Dispatches on each metric's declared `type` (as k6 reports it in the
// summary data) rather than guessing from which value fields are present,
// since a Counter's values include both `count` and a derived `rate`
// (count/duration) and would otherwise be misdetected as a Rate metric.
function textSummary(data) {
  const m = data.metrics;
  const line = (name, metric) => {
    if (!metric) return `${name}: (no data)\n`;
    const v = metric.values;
    switch (metric.type) {
      case "trend":
        return `${name}: avg=${v.avg.toFixed(1)}ms p95=${(v["p(95)"] || 0).toFixed(1)}ms p99=${(v["p(99)"] || 0).toFixed(1)}ms max=${(v.max || 0).toFixed(1)}ms\n`;
      case "rate":
        return `${name}: rate=${(v.rate * 100).toFixed(1)}%\n`;
      case "counter":
        return `${name}: count=${v.count}\n`;
      default:
        return `${name}: ${JSON.stringify(v)}\n`;
    }
  };
  return (
    "\n=== realtime-engine load test summary ===\n" +
    line("action_latency_ms", m.action_latency_ms) +
    line("match_found_rate", m.match_found_rate) +
    line("action_timeouts", m.action_timeouts) +
    line("ws_connect_errors", m.ws_connect_errors) +
    line("games_completed", m.games_completed) +
    "==========================================\n"
  );
}
