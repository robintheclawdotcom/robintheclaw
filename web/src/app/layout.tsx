import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  metadataBase: new URL("https://robintheclaw.com"),
  title: "Robin the Claw",
  description: "A bounded, verifiable delta-neutral RWA trading system on Robinhood Chain.",
  alternates: {
    canonical: "/",
  },
  icons: {
    icon: [
      { url: "/brand/icon-16.png", sizes: "16x16", type: "image/png" },
      { url: "/brand/icon-32.png", sizes: "32x32", type: "image/png" },
      { url: "/brand/icon-48.png", sizes: "48x48", type: "image/png" },
      { url: "/brand/icon-192.png", sizes: "192x192", type: "image/png" },
      { url: "/brand/icon-512.png", sizes: "512x512", type: "image/png" },
    ],
    shortcut: "/favicon.ico",
    apple: { url: "/brand/apple-touch-icon.png", sizes: "180x180", type: "image/png" },
  },
  openGraph: {
    title: "Robin the Claw",
    description: "A bounded, verifiable delta-neutral RWA trading system on Robinhood Chain.",
    url: "/",
    siteName: "Robin the Claw",
    locale: "en_US",
    type: "website",
    images: [
      {
        url: "/brand/og-image.jpg",
        width: 1200,
        height: 630,
        alt: "Robin the Claw",
      },
    ],
  },
  twitter: {
    card: "summary_large_image",
    title: "Robin the Claw",
    description: "A bounded, verifiable delta-neutral RWA trading system on Robinhood Chain.",
    images: ["/brand/og-image.jpg"],
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
