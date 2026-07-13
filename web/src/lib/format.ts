import type { Amount } from "./app-types";

export function formatAmount(amount: Amount | null | undefined, maxFraction = 2): string {
  if (!amount) return "—";
  const negative = amount.raw.startsWith("-");
  const digits = negative ? amount.raw.slice(1) : amount.raw;
  if (!/^\d+$/.test(digits)) return "—";

  const padded = digits.padStart(amount.decimals + 1, "0");
  const whole = padded.slice(0, -amount.decimals || undefined);
  const fraction = amount.decimals > 0 ? padded.slice(-amount.decimals) : "";
  const visible = fraction.slice(0, maxFraction).replace(/0+$/, "");
  const grouped = BigInt(whole || "0").toLocaleString("en-US");
  return `${negative ? "-" : ""}${grouped}${visible ? `.${visible}` : ""} ${amount.symbol}`;
}

export function formatAddress(address: string | null | undefined): string {
  if (!address) return "Not available";
  return `${address.slice(0, 6)}…${address.slice(-4)}`;
}

export function formatDate(value: string | number): string {
  const date = new Date(typeof value === "number" ? value * 1000 : value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function titleFromKind(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map((part) => part[0]?.toUpperCase() + part.slice(1))
    .join(" ");
}
