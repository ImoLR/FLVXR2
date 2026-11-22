import { Navbar } from "@/components/navbar";
import { cn } from "@/lib/utils";

export default function DefaultLayout({
  children,
  className,
  mainClassName,
}: {
  children: React.ReactNode;
  /** Page background (plan 292: the login page's brand glow). */
  className?: string;
  mainClassName?: string;
}) {
  return (
    <div
      className={cn(
        "relative flex flex-col min-h-screen",
        className || "bg-white dark:bg-black",
      )}
    >
      <Navbar />
      <main
        className={cn(
          "container mx-auto max-w-7xl px-4 sm:px-6 flex-grow",
          mainClassName ?? "pt-4 sm:pt-16",
        )}
      >
        {children}
      </main>
    </div>
  );
}
