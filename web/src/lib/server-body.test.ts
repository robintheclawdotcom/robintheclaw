import { describe, expect, it } from "vitest";
import { BodyTooLargeError, readBoundedBody, validateContentLength } from "./server-body";

describe("bounded request bodies", () => {
  it("rejects malformed and oversized content lengths", () => {
    expect(() => validateContentLength("abc", 10)).toThrow(BodyTooLargeError);
    expect(() => validateContentLength("11", 10)).toThrow(BodyTooLargeError);
    expect(() => validateContentLength("10", 10)).not.toThrow();
    expect(() => validateContentLength(null, 10)).not.toThrow();
  });

  it("rejects oversized chunked bodies before buffering the remainder", async () => {
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(6));
        controller.enqueue(new Uint8Array(5));
      },
      cancel() {
        cancelled = true;
      },
    });
    await expect(readBoundedBody(body, 10)).rejects.toBeInstanceOf(BodyTooLargeError);
    expect(cancelled).toBe(true);
  });

  it("preserves an accepted body exactly", async () => {
    const encoded = new TextEncoder().encode('{"ok":true}');
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoded.subarray(0, 4));
        controller.enqueue(encoded.subarray(4));
        controller.close();
      },
    });
    expect(await readBoundedBody(body, encoded.length)).toEqual(encoded);
  });
});
