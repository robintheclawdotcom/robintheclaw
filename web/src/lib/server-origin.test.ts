import { describe, expect, it } from "vitest";
import { isSameOriginRequest } from "./server-origin";

function request(origin?: string, headers: Record<string, string> = {}, url = "http://internal:10000/app") {
  const values = new Headers(headers);
  if (origin) values.set("origin", origin);
  return { headers: values, nextUrl: new URL(url) };
}

describe("same-origin requests", () => {
  it("accepts requests without an Origin header", () => {
    expect(isSameOriginRequest(request())).toBe(true);
  });

  it("uses the configured public origin on Render", () => {
    process.env.APP_ORIGIN = "https://robintheclaw.com";
    try {
      expect(isSameOriginRequest(request("https://robintheclaw.com", {
        host: "internal:10000",
      }))).toBe(true);
    } finally {
      delete process.env.APP_ORIGIN;
    }
  });

  it("rejects foreign, malformed, and spoofed forwarded origins", () => {
    expect(isSameOriginRequest(request("https://example.com"))).toBe(false);
    expect(isSameOriginRequest(request("not-a-url"))).toBe(false);
    expect(isSameOriginRequest(request("https://evil.example", {
      "x-forwarded-host": "evil.example",
      "x-forwarded-proto": "https",
    }))).toBe(false);
  });
});
