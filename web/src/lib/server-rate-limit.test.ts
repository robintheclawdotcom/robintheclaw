import { describe, expect, it } from "vitest";
import { takeRateLimit } from "./server-rate-limit";

describe("server rate limit", () => {
  it("limits a subject within a fixed window and resets afterwards", () => {
    expect(takeRateLimit("test-a", "session", 2, 1_000, 1_000).allowed).toBe(true);
    expect(takeRateLimit("test-a", "session", 2, 1_000, 1_100).allowed).toBe(true);
    expect(takeRateLimit("test-a", "session", 2, 1_000, 1_200).allowed).toBe(false);
    expect(takeRateLimit("test-a", "session", 2, 1_000, 2_001).allowed).toBe(true);
  });

  it("isolates subjects and scopes", () => {
    expect(takeRateLimit("test-b", "first", 1, 1_000, 1_000).allowed).toBe(true);
    expect(takeRateLimit("test-b", "second", 1, 1_000, 1_000).allowed).toBe(true);
    expect(takeRateLimit("test-c", "first", 1, 1_000, 1_000).allowed).toBe(true);
  });
});
