# Test helpers

Small clients for the pikopod control plane, one per test runner. Same
Apache-2.0 licence as the rest of the repository; copy them into any project.

| Helper | Runtime | Dependencies |
| --- | --- | --- |
| `jest/pikopod.js` | Node 18+ (any runner: Jest, Vitest, `node --test`) | none, uses `fetch` |
| `pytest/pikopod.py` | Python 3.9+ (any runner: pytest, unittest) | none, uses `urllib` |

Both talk to a running `pikopod up` (or `pikopod up --spec <file>`) and expose
the same calls: `fork`, `mode`, `verify`, `chaos`, `clearFaults`, `emit`,
`requests`, `seed`, `snapshot`, `restore`, `reset`. See
https://docs.pikopod.com/scenarios/from-a-test.
