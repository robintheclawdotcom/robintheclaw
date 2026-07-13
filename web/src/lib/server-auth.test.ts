import { afterEach, describe, expect, it } from "vitest";
import { exportSPKI, generateKeyPair, SignJWT } from "jose";
import { SessionValidationError, verifyPrivySession } from "./server-auth";

const originalAppId = process.env.PRIVY_APP_ID;
const originalKey = process.env.PRIVY_VERIFICATION_KEY;

afterEach(() => {
  restore("PRIVY_APP_ID", originalAppId);
  restore("PRIVY_VERIFICATION_KEY", originalKey);
});

describe("Privy session verification", () => {
  it("accepts a current ES256 token for the configured application", async () => {
    const token = await configuredToken("app-test");
    await expect(verifyPrivySession(token)).resolves.toEqual({ did: "did:privy:user", sessionId: "session-1" });
  });

  it("rejects a token issued for another application", async () => {
    const token = await configuredToken("another-app", "app-test");
    await expect(verifyPrivySession(token)).rejects.toBeInstanceOf(SessionValidationError);
  });
});

async function configuredToken(audience: string, configuredApp = audience) {
  const { privateKey, publicKey } = await generateKeyPair("ES256");
  process.env.PRIVY_APP_ID = configuredApp;
  process.env.PRIVY_VERIFICATION_KEY = await exportSPKI(publicKey);
  return new SignJWT({ sid: "session-1" })
    .setProtectedHeader({ alg: "ES256" })
    .setIssuer("privy.io")
    .setAudience(audience)
    .setSubject("did:privy:user")
    .setIssuedAt()
    .setExpirationTime("5m")
    .sign(privateKey);
}

function restore(name: string, value: string | undefined) {
  if (value === undefined) delete process.env[name];
  else process.env[name] = value;
}
