import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 5,
  userScalable: true,
};

export const metadata: Metadata = {
  title: "Hangar — Manage your AI coding agents on native Windows",
  description: "The Windows desktop app for running multiple AI coding agents in parallel.",
  keywords: ["hangar", "windows", "native windows", "claude code", "codex", "gemini", "copilot cli", "aider", "tmux-free", "ai agents"],
  authors: [{ name: "thirschel" }],
  metadataBase: new URL("https://thirschel.github.io/Hangar/"),
  openGraph: {
    title: "Hangar",
    description: "The Windows desktop app for running multiple AI coding agents in parallel.",
    url: "https://thirschel.github.io/Hangar/",
    type: "website",
    images: [{ url: "https://thirschel.github.io/Hangar/og-hangar.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Hangar",
    description: "The Windows desktop app for running multiple AI coding agents in parallel.",
    images: ["https://thirschel.github.io/Hangar/og-hangar.png"],
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className={`${geistSans.variable} ${geistMono.variable}`}>
        {children}
      </body>
    </html>
  );
}