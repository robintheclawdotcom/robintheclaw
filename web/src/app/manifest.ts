import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Robin the Claw",
    short_name: "Robin",
    description: "A bounded, verifiable delta-neutral RWA trading system on Robinhood Chain.",
    start_url: "/",
    display: "standalone",
    background_color: "#1f1e1c",
    theme_color: "#1f1e1c",
    icons: [
      { src: "/brand/icon-192.png", sizes: "192x192", type: "image/png" },
      { src: "/brand/icon-512.png", sizes: "512x512", type: "image/png" },
    ],
  };
}
