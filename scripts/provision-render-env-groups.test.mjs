import assert from "node:assert/strict";
import test from "node:test";

import {
  configChanges,
  configGroups,
  hexGroups,
  indexGroups,
  validateConfig,
  validateConfigValues,
  validateGroupVariables,
} from "./provision-render-env-groups.mjs";

test("auth groups are unique 32-byte hex contracts", () => {
  assert.equal(Object.keys(hexGroups).length, 16);
  assert.equal(new Set(Object.values(hexGroups).flat()).size, 16);
});

test("indexes wrapped Render env-group responses", () => {
  const first = { id: "evg-1", name: "first" };
  const second = { id: "evg-2", name: "second" };
  const groups = indexGroups([
    { cursor: "cursor-1", envGroup: first },
    { cursor: "cursor-2", envGroup: second },
  ]);
  assert.equal(groups.get("first"), first);
  assert.equal(groups.get("second"), second);
});

test("rejects malformed Render env-group responses", () => {
  assert.throws(() => indexGroups([{ cursor: "cursor-1" }]), /response is invalid/);
});

test("validates auth and config group values", () => {
  validateGroupVariables(
    "auth",
    [{ key: "KEY", value: "ab".repeat(32) }],
    ["KEY"],
    "auth",
  );
  validateGroupVariables(
    "config",
    [{ key: "VALUE", value: "configured" }],
    ["VALUE"],
    "config",
  );
  assert.throws(
    () => validateGroupVariables("auth", [{ key: "KEY", value: "" }], ["KEY"], "auth"),
    /32-byte lowercase hex/,
  );
  assert.throws(
    () => validateGroupVariables("config", [{ key: "VALUE", value: " " }], ["VALUE"], "config"),
    /must be non-empty/,
  );
});

test("computes idempotent config convergence without exposing values", () => {
  const desired = { FIRST: "reviewed", SECOND: "configured" };
  assert.deepEqual(
    configChanges(
      "config",
      [
        { key: "FIRST", value: "stale" },
        { key: "EXTRA", value: "remove" },
      ],
      desired,
    ),
    {
      upserts: [
        { key: "FIRST", value: "reviewed" },
        { key: "SECOND", value: "configured" },
      ],
      removals: ["EXTRA"],
    },
  );
  const current = Object.entries(desired).map(([key, value]) => ({ key, value }));
  assert.deepEqual(configChanges("config", current, desired), { upserts: [], removals: [] });
  validateConfigValues("config", current, desired);
  assert.throws(
    () => validateConfigValues("config", [{ key: "FIRST", value: "stale" }], { FIRST: "reviewed" }),
    (error) => error.message === "config.FIRST does not match the reviewed value",
  );
});

test("rejects duplicate config keys during convergence", () => {
  assert.throws(
    () => configChanges(
      "config",
      [
        { key: "VALUE", value: "first" },
        { key: "VALUE", value: "second" },
      ],
      { VALUE: "reviewed" },
    ),
    /variables are invalid/,
  );
});

test("accepts the exact reviewed config manifest", () => {
  const groups = Object.fromEntries(
    Object.entries(configGroups).map(([name, keys]) => [
      name,
      Object.fromEntries(keys.map((key) => [key, "configured"])),
    ]),
  );
  assert.deepEqual(validateConfig({ groups }), groups);
});

test("rejects missing, extra, and empty config values", () => {
  assert.throws(() => validateConfig({ groups: {} }), /group names/);
  const groups = Object.fromEntries(
    Object.entries(configGroups).map(([name, keys]) => [
      name,
      Object.fromEntries(keys.map((key) => [key, "configured"])),
    ]),
  );
  groups["robin-lighter-market-config"].EXTRA = "configured";
  assert.throws(() => validateConfig({ groups }), /keys/);
  delete groups["robin-lighter-market-config"].EXTRA;
  groups["robin-lighter-market-config"].LIGHTER_AAPL_MARKET_INDEX = "";
  assert.throws(() => validateConfig({ groups }), /non-empty/);
});
