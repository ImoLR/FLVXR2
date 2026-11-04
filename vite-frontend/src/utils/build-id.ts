// The running frontend build id (plan 209). The build writes it into
// index.html (<meta name="flvx-build">) and dist/version.json; it is not
// baked into the JS any more, so a release changes only the chunks whose
// code changed — the others keep their file names and stay cached.

const BUILD_ID_PATTERN = /^[0-9A-Za-z._-]{1,128}$/;

let cached: string | undefined;

// "" when unknown (dev server, no meta).
export const currentBuildId = (): string => {
  if (cached === undefined) {
    const value =
      typeof document !== "undefined"
        ? (document
            .querySelector('meta[name="flvx-build"]')
            ?.getAttribute("content") ?? "")
        : "";

    cached = BUILD_ID_PATTERN.test(value) ? value : "";
  }

  return cached;
};
