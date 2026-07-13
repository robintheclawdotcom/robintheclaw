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

  it("uses proxy host and protocol on Render", () => {
    expect(isSameOriginRequest(request("https://robintheclaw.com", {
      host: "internal:10000",
      "x-forwarded-host": "robintheclaw.com",
      "x-forwarded-proto": "https",
    }))).toBe(true);
  });

  it("rejects foreign and malformed origins", () => {
    const headers = { host: "robintheclaw.com", "x-forwarded-proto": "https" };
    expect(isSameOriginRequest(request("https://example.com", headers))).toBe(false);
    expect(isSameOriginRequest(request("not-a-url", headers))).toBe(false);
  });
});
