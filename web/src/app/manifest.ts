import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Robin the Claw",
    short_name: "Robin",
    description: "A delta-neutral RWA trading agent built to find durable, risk-adjusted net profitability.",
    start_url: "/",
    display: "standalone",
    background_color: "#1f1e1c",
    theme_color: "#1f1e1c",
    icons: [
      { src: "/brand/icon-192.png?v=green", sizes: "192x192", type: "image/png" },
      { src: "/brand/icon-512.png?v=green", sizes: "512x512", type: "image/png" },
    ],
  };
}
