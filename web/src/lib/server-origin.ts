export type OriginRequest = {
  headers: Headers;
  nextUrl: URL;
};

export function isSameOriginRequest(request: OriginRequest) {
  const origin = request.headers.get("origin");
  if (!origin) return true;

  let parsed: URL;
  try {
    parsed = new URL(origin);
  } catch {
    return false;
  }

  const forwardedProtocol = first(request.headers.get("x-forwarded-proto"));
  const requestProtocol = request.nextUrl.protocol.slice(0, -1);
  const candidates = [
    { host: first(request.headers.get("host")), protocol: forwardedProtocol ?? requestProtocol },
    { host: first(request.headers.get("x-forwarded-host")), protocol: forwardedProtocol ?? requestProtocol },
    { host: request.nextUrl.host, protocol: requestProtocol },
  ];

  return candidates.some(({ host, protocol }) =>
    Boolean(host && parsed.host === host && parsed.protocol === `${protocol}:`),
  );
}

function first(value: string | null) {
  return value?.split(",", 1)[0]?.trim() || null;
}
