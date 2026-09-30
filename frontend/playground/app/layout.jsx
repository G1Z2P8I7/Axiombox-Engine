import "./globals.css";

export const metadata = {
  title: "AxiomBox — Kernel-Isolated RCE Sandbox & Resource Governor",
  description: "Native Windows 11 Win32 Job Object remote code execution sandbox with sub-25ms cold-start latency, microsecond CPU accounting, and hard kernel limits.",
  keywords: "sandbox, RCE, Win32, Job Objects, remote code execution, Judge0, LeetCode engine",
  icons: {
    icon: "/icon.svg",
  },
};

export const viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
};

export default function RootLayout({ children }) {
  return (
    <html lang="en" className="dark">
      <body className="min-h-screen flex flex-col bg-[#090d16] text-gray-100 antialiased selection:bg-indigo-500 selection:text-white">
        {children}
      </body>
    </html>
  );
}
