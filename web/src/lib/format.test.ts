import { describe, expect, it } from "vitest";
import { formatAddress, formatAmount, titleFromKind } from "./format";

describe("product formatting", () => {
  it("formats integer token amounts without floating point loss", () => {
    expect(formatAmount({ raw: "123456789012345678901", decimals: 6, symbol: "tUSDG" }))
      .toBe("123,456,789,012,345.67 tUSDG");
  });

  it("preserves negative values and absent P&L", () => {
    expect(formatAmount({ raw: "-1250000", decimals: 6, symbol: "USD" })).toBe("-1.25 USD");
    expect(formatAmount(null)).toBe("—");
  });

  it("shortens addresses and humanizes activity kinds", () => {
    expect(formatAddress("0x1111111111111111111111111111111111111111")).toBe("0x1111…1111");
    expect(titleFromKind("strategy_state_changed")).toBe("Strategy State Changed");
  });
});
