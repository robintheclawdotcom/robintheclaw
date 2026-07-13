#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const config = JSON.parse(readFileSync(join(root, "config", "addresses.json"), "utf8"));
const address = /^0x[\da-fA-F]{40}$/;

function fail(message) {
  throw new Error(`Invalid address configuration: ${message}`);
}

for (const network of ["mainnet", "testnet"]) {
  const chain = config.chain[network];
  const deployment = config.deployment?.[network];
  if (!chain || !deployment) fail(`${network} is incomplete`);
  if (chain.chainId !== deployment.chainId) fail(`${network} chain ID mismatch`);

  for (const field of ["asset", "universalRouter"]) {
    const value = deployment[field];
    if (value !== null && !address.test(value)) fail(`${network}.${field} is not an address`);
  }
}

const mainnet = config.deployment.mainnet;
if (mainnet.status !== "verified") fail("mainnet must be verified");
if (!address.test(mainnet.asset) || !address.test(mainnet.universalRouter)) {
  fail("mainnet asset and router are required");
}
if (mainnet.asset.toLowerCase() !== config.core.USDG.toLowerCase()) {
  fail("mainnet asset must match core.USDG");
}
if (mainnet.universalRouter.toLowerCase() !== config.uniswapV4.UniversalRouter.toLowerCase()) {
  fail("mainnet router must match uniswapV4.UniversalRouter");
}

if (config.perp?.venue !== "lighter") fail("perp venue must be lighter");
if (config.perp?.collateral?.symbol !== "USDC") fail("Lighter collateral must be USDC");
if (config.perp?.collateral?.decimals !== 6) fail("Lighter USDC must use 6 decimals");
if (config.perp?.collateral?.settlementChain !== "ethereum") {
  fail("Lighter collateral settlement chain must be ethereum");
}

const spotTreasury = config.treasury?.spot;
const perpTreasury = config.treasury?.perp;
if (!spotTreasury || !perpTreasury) fail("treasury assets are required");
if (spotTreasury.symbol !== "USDG" || spotTreasury.chainId !== config.chain.mainnet.chainId) {
  fail("spot treasury must be Robinhood Chain USDG");
}
if (!address.test(spotTreasury.address)) fail("spot treasury asset is not an address");
if (spotTreasury.address.toLowerCase() !== config.core.USDG.toLowerCase()) {
  fail("spot treasury asset must match core.USDG");
}
if (spotTreasury.decimals !== 6) fail("spot USDG must use 6 decimals");
if (
  perpTreasury.symbol !== config.perp.collateral.symbol ||
  perpTreasury.decimals !== config.perp.collateral.decimals ||
  perpTreasury.settlementChain !== config.perp.collateral.settlementChain
) {
  fail("perp treasury must match the Lighter collateral configuration");
}

const testnet = config.deployment.testnet;
if (testnet.status === "verified" && (!address.test(testnet.asset) || !address.test(testnet.universalRouter))) {
  fail("a verified testnet requires an asset and router");
}

console.log("address configuration is valid");
