# signal

Read-only measurement. Confirms the basis exists and tracks how it moves before any execution
code is written.

## Run

```bash
node src/basis.mjs
```

Reads the Lighter perp book for every name in `config/addresses.json` `universe`, prints each
name's basis (perp mark vs index, in bps) sorted by magnitude, and appends every observation to
`data/basis-YYYYMMDD.jsonl`.

## What to watch

The thesis is that the basis is tight during U.S. market hours and widens off-hours and on
weekends, when the underlying spot venues are thin. A single reading proves the pipeline; a
series proves (or kills) the edge. Schedule it to build the series:

```bash
# every 15 minutes via cron
*/15 * * * * cd /path/to/robin-the-claw/signal && /usr/bin/node src/basis.mjs >> scan.log 2>&1
```

## Not yet

- Spot leg: compare the Lighter index against the live Uniswap v4 AMM price for the Stock Token
  (a second, cross-venue basis). Needs the v4 Quoter and per-token pool keys.
- Funding: model Lighter's own charged funding. The public `funding-rates` endpoint returns
  external venue reference rates, not Lighter's, so the raw references are stored for later, not
  turned into a carry figure here.
