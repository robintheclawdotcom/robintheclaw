import assert from "node:assert/strict";
import test from "node:test";

import { generateQuoteKey, validateBundle } from "./generate-mainnet-operator-keys.mjs";

test("generates a Go-compatible Ed25519 keypair", () => {
  const key = generateQuoteKey();
  const privateKey = Buffer.from(key.privateKeyBase64, "base64");
  const publicKey = Buffer.from(key.publicKeyBase64, "base64");
  assert.equal(privateKey.length, 64);
  assert.equal(publicKey.length, 32);
  assert.deepEqual(privateKey.subarray(32), publicKey);
});

test("rejects reused publisher identities", () => {
  const key = {
    address: "0x1111111111111111111111111111111111111111",
    privateKey: `0x${"11".repeat(32)}`,
  };
  assert.throws(
    () =>
      validateBundle({
        version: 1,
        quoteAuthority: generateQuoteKey(),
        sequencerPublishers: [key, key, key],
        aaplPublishers: [key, key, key],
      }),
    /sequencerPublishers/,
  );
});
