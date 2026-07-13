import type { Metadata } from "next";
import { AppProviders } from "../../components/app-providers";
import { AppShell } from "../../components/app-shell";

export const metadata: Metadata = {
  title: "Robin App | Strategy Dashboard",
  description: "Manage your Robin strategy account, wallets, capital, and performance.",
};

export default function ApplicationLayout({ children }: { children: React.ReactNode }) {
  return <AppProviders><AppShell>{children}</AppShell></AppProviders>;
}
