import type { Metadata } from "next";
import PublicSite from "../../components/public-site";

export const metadata: Metadata = {
  title: "Docs | Robin the Claw",
  description: "Architecture, research, contracts, operations, and developer documentation for Robin the Claw.",
  alternates: {
    canonical: "/docs",
  },
};

export default function Docs() {
  return <PublicSite view="docs" />;
}
