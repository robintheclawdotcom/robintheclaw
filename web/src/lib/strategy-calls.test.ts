import { describe, expect, it } from "vitest";
import { depositCalls, mandateCall, parseTokenAmount, withdrawalCall } from "./strategy-calls";

const address = "0x1111111111111111111111111111111111111111" as const;
const other = "0x2222222222222222222222222222222222222222" as const;

describe("strategy calls", () => {
  it("parses decimal input into integer token units", () => {
    expect(parseTokenAmount("12.345", 6)).toBe(12_345_000n);
    expect(() => parseTokenAmount("0", 6)).toThrow("greater than zero");
    expect(() => parseTokenAmount("1.0000001", 6)).toThrow("no more than 6");
  });

  it("builds restricted owner and funding calls", () => {
    expect(mandateCall(address, true).data.slice(0, 10)).toBe("0xdcc279c8");
    expect(withdrawalCall(address, other, 1n).to).toBe(address);
    const deposit = depositCalls(address, other, 5n);
    expect(deposit).toHaveLength(2);
    expect(deposit.map((call) => call.to)).toEqual([address, other]);
    expect(deposit.every((call) => call.value === "0")).toBe(true);
  });
});
