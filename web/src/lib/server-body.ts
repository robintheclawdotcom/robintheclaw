export class BodyTooLargeError extends Error {}

export function validateContentLength(value: string | null, maximumBytes: number) {
  if (value === null) return;
  if (!/^\d+$/.test(value) || Number(value) > maximumBytes) {
    throw new BodyTooLargeError();
  }
}

export async function readBoundedBody(
  body: ReadableStream<Uint8Array> | null,
  maximumBytes: number,
): Promise<Uint8Array | undefined> {
  if (!body) return undefined;

  const reader = body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maximumBytes) throw new BodyTooLargeError();
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    throw error;
  }

  const result = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result;
}
