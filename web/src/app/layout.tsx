import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Robin the Claw",
  description: "A bounded, verifiable delta-neutral RWA trading system on Robinhood Chain.",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
