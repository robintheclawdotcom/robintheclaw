export type OriginRequest = {
  headers: Headers;
  nextUrl: URL;
};

export function isSameOriginRequest(request: OriginRequest) {
  const origin = request.headers.get("origin");
  if (!origin) return true;

  try {
    const expected = new URL(process.env.APP_ORIGIN ?? request.nextUrl.origin);
    return new URL(origin).origin === expected.origin;
  } catch {
    return false;
  }
}
