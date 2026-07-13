import type { Metadata } from "next";
import { AppProviders } from "../../components/app-providers";
import { AppShell } from "../../components/app-shell";

export const metadata: Metadata = {
  title: "Robin | Strategy Operations",
  description: "Monitor capital, exposure, execution, and performance.",
};

export default function ApplicationLayout({ children }: { children: React.ReactNode }) {
  return <AppProviders><AppShell>{children}</AppShell></AppProviders>;
}
