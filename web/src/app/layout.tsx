import type { Metadata } from "next";
import { AppShell } from "@/components/AppShell";
import { OfflineBanner } from "@/components/ui/offline-banner";
// public-ai semantic tokens v1, product-image-web profile. These files are
// adopted byte-for-byte and hash-pinned by painuo-tokens.lock.json; import
// order is tokens -> profile aliases -> app baseline.
import "@/styles/generated/painuo/v1/tokens.css";
import "@/styles/generated/painuo/v1/aliases.css";
import "./globals.css";

export const metadata: Metadata = {
  title: "产品图工作台",
  description: "产品图独立应用(HUI-1739 I0 底座)",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" data-pn-surface="work.light" data-pn-theme="dark">
      <body>
        <OfflineBanner />
        <AppShell>{children}</AppShell>
      </body>
    </html>
  );
}
