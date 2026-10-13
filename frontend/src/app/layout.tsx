import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Farewise · Your way home, compared",
  description:
    "A personal Singapore ride comparison prototype. All fares are simulated.",
  appleWebApp: { capable: true, title: "Farewise", statusBarStyle: "default" },
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
