import { useEffect, useState } from "react";

import { SITE_NAME_CHANGED_EVENT, siteConfig } from "@/config/site";

/**
 * Plan 292: the site name (app_name, default 「im.gamer」), re-rendered when
 * the config fetched after start-up differs from the cached one — so a
 * renamed site shows the new name on the same visit.
 */
export function useSiteName(): string {
  const [name, setName] = useState(siteConfig.name);

  useEffect(() => {
    const sync = () => setName(siteConfig.name);

    sync();
    window.addEventListener(SITE_NAME_CHANGED_EVENT, sync);

    return () => window.removeEventListener(SITE_NAME_CHANGED_EVENT, sync);
  }, []);

  return name;
}
