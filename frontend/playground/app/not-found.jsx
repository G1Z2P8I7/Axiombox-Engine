import Link from "next/link";
import { ShieldAlert, ArrowLeft } from "lucide-react";

export default function NotFound() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center p-6 text-center">
      <div className="w-16 h-16 rounded-2xl bg-indigo-500/10 border border-indigo-500/30 flex items-center justify-center text-indigo-400 mb-6">
        <ShieldAlert size={36} />
      </div>
      <h1 className="text-4xl font-extrabold tracking-tight text-white mb-2">404 - Page Not Found</h1>
      <p className="text-gray-400 max-w-md mb-8">
        The sandbox partition or route you requested does not exist or has been reclaimed by the kernel.
      </p>
      <Link
        href="/"
        className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-sm transition-colors shadow-lg shadow-indigo-500/20"
      >
        <ArrowLeft size={16} />
        Return to Sandbox Playground
      </Link>
    </div>
  );
}
