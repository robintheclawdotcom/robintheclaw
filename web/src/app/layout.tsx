import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  metadataBase: new URL("https://robintheclaw.com"),
  title: "Robin the Claw",
  description: "A delta-neutral RWA trading agent built to find durable, risk-adjusted net profitability.",
  alternates: {
    canonical: "/",
  },
  icons: {
    icon: [
      { url: "/brand/icon-16.png?v=green", sizes: "16x16", type: "image/png" },
      { url: "/brand/icon-32.png?v=green", sizes: "32x32", type: "image/png" },
      { url: "/brand/icon-48.png?v=green", sizes: "48x48", type: "image/png" },
      { url: "/brand/icon-192.png?v=green", sizes: "192x192", type: "image/png" },
      { url: "/brand/icon-512.png?v=green", sizes: "512x512", type: "image/png" },
    ],
    shortcut: "/favicon.ico?v=green",
    apple: { url: "/brand/apple-touch-icon.png?v=green", sizes: "180x180", type: "image/png" },
  },
  openGraph: {
    title: "Robin the Claw",
    description: "A delta-neutral RWA trading agent built to find durable, risk-adjusted net profitability.",
    url: "/",
    siteName: "Robin the Claw",
    locale: "en_US",
    type: "website",
    images: [
      {
        url: "/brand/og-image.jpg?v=green",
        width: 1200,
        height: 630,
        alt: "Robin the Claw",
      },
    ],
  },
  twitter: {
    card: "summary_large_image",
    title: "Robin the Claw",
    description: "A delta-neutral RWA trading agent built to find durable, risk-adjusted net profitability.",
    images: ["/brand/og-image.jpg?v=green"],
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
