import * as React from "react";

import {
  DropdownMenu as BaseDropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem as BaseDropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger as BaseDropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

type Placement = "bottom-start" | "bottom-end" | "top-start" | "top-end";

interface DropdownContextValue {
  align: "start" | "end";
  setOpen: React.Dispatch<React.SetStateAction<boolean>>;
}

const DropdownContext = React.createContext<DropdownContextValue>({
  align: "start",
  setOpen: () => {},
});

export interface DropdownProps {
  children: React.ReactNode;
  placement?: Placement;
}

export function Dropdown({
  children,
  placement = "bottom-start",
}: DropdownProps) {
  const align: DropdownContextValue["align"] = placement.endsWith("end")
    ? "end"
    : "start";
  const [open, setOpen] = React.useState(false);
  const contextValue = React.useMemo(() => ({ align, setOpen }), [align]);

  return (
    <DropdownContext.Provider value={contextValue}>
      <BaseDropdownMenu open={open} onOpenChange={setOpen}>
        {children}
      </BaseDropdownMenu>
    </DropdownContext.Provider>
  );
}

// Radix opens the menu on pointerdown. For a finger that means a scroll which
// merely starts on the trigger (e.g. the 落地 协议 chip) opens it (plan 156).
// Touch/pen: cancel Radix's pointerdown-open and toggle on click instead — a
// scroll never produces a click. Mouse keeps Radix's press-to-open; keyboard
// (Enter/Space/ArrowDown) is untouched.
export function DropdownTrigger({ children }: { children: React.ReactNode }) {
  const { setOpen } = React.useContext(DropdownContext);
  const nonMousePressRef = React.useRef(false);

  return (
    <BaseDropdownMenuTrigger
      asChild
      onClick={() => {
        if (!nonMousePressRef.current) return;
        nonMousePressRef.current = false;
        setOpen((prev) => !prev);
      }}
      onPointerDown={(event) => {
        if (event.pointerType === "mouse") {
          nonMousePressRef.current = false;

          return;
        }
        nonMousePressRef.current = true;
        event.preventDefault();
      }}
    >
      {children}
    </BaseDropdownMenuTrigger>
  );
}

export interface DropdownMenuProps {
  "aria-label"?: string;
  children: React.ReactNode;
}

export function DropdownMenu({ children }: DropdownMenuProps) {
  const { align } = React.useContext(DropdownContext);

  return <DropdownMenuContent align={align}>{children}</DropdownMenuContent>;
}

export interface DropdownItemProps {
  children: React.ReactNode;
  className?: string;
  color?: "default" | "danger";
  onPress?: () => void;
  startContent?: React.ReactNode;
}

export function DropdownItem({
  children,
  className,
  color = "default",
  onPress,
  startContent,
}: DropdownItemProps) {
  return (
    <BaseDropdownMenuItem
      className={cn(
        color === "danger" ? "text-danger focus:text-danger" : "",
        className,
      )}
      onSelect={() => {
        onPress?.();
      }}
    >
      {startContent}
      {children}
    </BaseDropdownMenuItem>
  );
}

export { DropdownMenuLabel, DropdownMenuSeparator };
