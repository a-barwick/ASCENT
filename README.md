# ASCENT

**Make impossible missions real.**

ASCENT is a strategy game about directing the industrial programs behind
high-stakes missions beyond Earth. Win a mandate, assemble a plan, control scarce
suppliers and capacity, clear readiness gates, and decide when the program is
truly ready to commit.

The current proof of concept is **ORPHEUS-1**. A flight-critical bearing anomaly
threatens a lunar cargo-link deployment while the launch window closes and a
rival program gains ground. Review the engineering, supplier, and commercial
evidence; authorize a response; then issue **GO** or **SCRUB** and live with the
modeled consequence.

## Run

```sh
pnpm install --frozen-lockfile
pnpm --filter @ascent/web dev
```

Open `http://localhost:5173`.

This repository currently contains a client-only Svelte proof of concept. Game
state lives in the browser and resets when the scenario is replayed or the page
is reloaded. There is no persistent campaign, backend authority, account system,
or multiplayer simulation yet.

For the intended campaign and the exact current boundary, see
[Program campaign](docs/product/program-campaign.md).

Source-available under the ASCENT Proprietary Source License.
