import { useEffect, useState } from "react";

export function usePageVisible() {
  const [visible, setVisible] = useState(
    () => document.visibilityState !== "hidden",
  );

  useEffect(() => {
    const onVisibility = () =>
      setVisible(document.visibilityState !== "hidden");

    document.addEventListener("visibilitychange", onVisibility);

    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, []);

  return visible;
}
