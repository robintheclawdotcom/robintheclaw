import type { Metadata } from "next";
import PublicSite from "../../components/public-site";

export const metadata: Metadata = {
  title: "Roadmap | Robin the Claw",
  description: "The evidence-gated roadmap from deployed infrastructure to bounded autonomous execution.",
  alternates: {
    canonical: "/roadmap",
  },
  openGraph: {
    title: "Roadmap | Robin the Claw",
    description: "The evidence-gated roadmap from deployed infrastructure to bounded autonomous execution.",
    url: "/roadmap",
  },
};

export default function RoadmapPage() {
  return <PublicSite view="roadmap" />;
}
