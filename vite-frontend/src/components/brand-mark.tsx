// Plan 292: im.gamer logo (手柄笑脸) + wordmark. Colours come from
// config/brand-palette.ts; nothing here hard-codes the brand colour.
import * as React from "react";

import { BRAND, BRAND_NAME, isBrandName } from "@/config/brand";
import { cn } from "@/lib/utils";

type BrandMarkProps = {
  size?: number;
  className?: string;
  /** Accessible name; omit for a decorative logo next to the wordmark. */
  title?: string;
  style?: React.CSSProperties;
};

/**
 * The 64×64 logo: gradient rounded square, white controller with a face.
 * Gradient / clip ids are per instance (useId), so several logos on one
 * page never share or clash.
 */
export function BrandMark({
  size = 36,
  className,
  title,
  style,
}: BrandMarkProps) {
  const uid = `imgb${React.useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;

  return (
    <svg
      aria-hidden={title ? undefined : true}
      className={cn("flex-shrink-0", className)}
      focusable="false"
      height={size}
      role={title ? "img" : undefined}
      style={style}
      viewBox="0 0 64 64"
      width={size}
      xmlns="http://www.w3.org/2000/svg"
    >
      {title ? <title>{title}</title> : null}
      {BRAND.split ? (
        <>
          <defs>
            <clipPath id={`${uid}c`}>
              <rect height="60" rx="18" width="60" x="2" y="2" />
            </clipPath>
          </defs>
          <g clipPath={`url(#${uid}c)`}>
            <rect fill={BRAND.from} height="64" width="32" x="0" y="0" />
            <rect fill={BRAND.to} height="64" width="32" x="32" y="0" />
          </g>
        </>
      ) : (
        <>
          <defs>
            <linearGradient id={uid} x1="0" x2="1" y1="0" y2="1">
              <stop offset="0" stopColor={BRAND.from} />
              <stop offset="1" stopColor={BRAND.to} />
            </linearGradient>
          </defs>
          <rect
            fill={`url(#${uid})`}
            height="60"
            rx="18"
            width="60"
            x="2"
            y="2"
          />
        </>
      )}
      <ellipse cx="24" cy="12" fill="#fff" opacity=".3" rx="14" ry="5" />
      <g fill="#fff">
        <rect height="24" rx="12" width="46" x="9" y="21" />
        <circle cx="18" cy="42" r="9" />
        <circle cx="46" cy="42" r="9" />
      </g>
      <ellipse cx="25" cy="31" fill="#1E293B" rx="3" ry="3.6" />
      <ellipse cx="39" cy="31" fill="#1E293B" rx="3" ry="3.6" />
      <circle cx="26.1" cy="29.7" fill="#fff" r="1" />
      <circle cx="40.1" cy="29.7" fill="#fff" r="1" />
      <path
        d="M28 37.2 Q32 40.6 36 37.2"
        fill="none"
        stroke="#1E293B"
        strokeLinecap="round"
        strokeWidth="2.4"
      />
      <circle cx="18.5" cy="36.5" fill="#FF8FA3" opacity=".85" r="2.7" />
      <circle cx="45.5" cy="36.5" fill="#FF8FA3" opacity=".85" r="2.7" />
    </svg>
  );
}

// "im.gamer" in Nunito Black (900), converted to outlines (no web font to
// download). Units: 1000 = 1em, baseline at y = 0.
const WORDMARK_LETTERS =
  "M140 9Q98 9 75 -16Q52 -41 52 -88V-404Q52 -451 75 -476Q98 -501 140 -501Q183 -501 206 -476Q230 -451 230 -404V-88Q230 -41 207 -16Q184 9 140 9ZM140 -572Q93 -572 67 -594Q42 -616 42 -658Q42 -699 67 -721Q93 -743 140 -743Q189 -743 214 -721Q239 -699 239 -658Q239 -616 214 -594Q189 -572 140 -572Z M413 9Q371 9 348 -14Q325 -37 325 -81V-412Q325 -455 347 -478Q370 -501 412 -501Q454 -501 476 -478Q498 -455 498 -412V-357L487 -406Q508 -450 550 -477Q591 -503 648 -503Q702 -503 740 -477Q779 -451 796 -398H782Q805 -447 851 -475Q897 -503 953 -503Q1011 -503 1048 -481Q1085 -458 1103 -410Q1122 -363 1122 -291V-81Q1122 -37 1098 -14Q1075 9 1032 9Q990 9 967 -14Q944 -37 944 -81V-286Q944 -329 931 -348Q918 -367 888 -367Q851 -367 832 -341Q812 -315 812 -267V-81Q812 -37 789 -14Q766 9 723 9Q680 9 657 -14Q634 -37 634 -81V-286Q634 -329 621 -348Q608 -367 578 -367Q542 -367 522 -341Q503 -315 503 -267V-81Q503 9 413 9Z M1708 191Q1653 191 1605 184Q1557 176 1522 162Q1493 152 1481 133Q1469 114 1470 93Q1471 72 1481 55Q1492 38 1510 30Q1528 23 1550 31Q1594 49 1630 54Q1666 60 1691 60Q1752 60 1783 34Q1813 9 1813 -45V-108H1821Q1806 -66 1760 -39Q1714 -13 1660 -13Q1594 -13 1545 -43Q1496 -74 1468 -129Q1441 -184 1441 -258Q1441 -314 1457 -359Q1473 -404 1502 -436Q1530 -468 1571 -486Q1611 -503 1660 -503Q1716 -503 1761 -477Q1805 -451 1820 -409L1810 -368V-412Q1810 -455 1834 -478Q1857 -501 1899 -501Q1941 -501 1964 -478Q1986 -455 1986 -412V-66Q1986 62 1914 127Q1842 191 1708 191ZM1715 -144Q1745 -144 1766 -158Q1787 -171 1799 -197Q1811 -222 1811 -258Q1811 -312 1785 -342Q1759 -372 1715 -372Q1687 -372 1665 -359Q1643 -345 1632 -320Q1620 -294 1620 -258Q1620 -204 1646 -174Q1672 -144 1715 -144Z M2251 11Q2194 11 2150 -10Q2107 -31 2082 -68Q2058 -104 2058 -151Q2058 -205 2086 -236Q2114 -267 2176 -281Q2238 -294 2341 -294H2396V-210H2341Q2302 -210 2277 -205Q2251 -199 2239 -188Q2227 -176 2227 -158Q2227 -135 2244 -120Q2260 -105 2291 -105Q2316 -105 2336 -116Q2355 -127 2367 -146Q2379 -166 2379 -192V-308Q2379 -344 2361 -359Q2342 -374 2297 -374Q2273 -374 2243 -368Q2213 -363 2176 -349Q2149 -339 2130 -346Q2111 -353 2100 -370Q2090 -387 2090 -407Q2091 -427 2103 -445Q2116 -463 2142 -473Q2190 -491 2231 -497Q2271 -503 2305 -503Q2387 -503 2441 -480Q2494 -456 2520 -408Q2547 -359 2547 -283V-81Q2547 -37 2526 -14Q2505 9 2465 9Q2424 9 2403 -14Q2381 -37 2381 -81V-107L2387 -93Q2382 -61 2364 -38Q2346 -15 2317 -2Q2288 11 2251 11Z M2728 9Q2686 9 2663 -14Q2640 -37 2640 -81V-412Q2640 -455 2662 -478Q2685 -501 2727 -501Q2769 -501 2791 -478Q2813 -455 2813 -412V-357L2802 -406Q2823 -450 2865 -477Q2906 -503 2963 -503Q3017 -503 3055 -477Q3094 -451 3111 -398H3097Q3120 -447 3166 -475Q3212 -503 3268 -503Q3326 -503 3363 -481Q3400 -458 3418 -410Q3437 -363 3437 -291V-81Q3437 -37 3413 -14Q3390 9 3347 9Q3305 9 3282 -14Q3259 -37 3259 -81V-286Q3259 -329 3246 -348Q3233 -367 3203 -367Q3166 -367 3147 -341Q3127 -315 3127 -267V-81Q3127 -37 3104 -14Q3081 9 3038 9Q2995 9 2972 -14Q2949 -37 2949 -81V-286Q2949 -329 2936 -348Q2923 -367 2893 -367Q2857 -367 2837 -341Q2818 -315 2818 -267V-81Q2818 9 2728 9Z M3798 11Q3708 11 3643 -20Q3577 -52 3542 -110Q3507 -168 3507 -247Q3507 -323 3540 -380Q3574 -438 3633 -471Q3691 -503 3766 -503Q3821 -503 3866 -486Q3911 -468 3943 -434Q3975 -401 3992 -354Q4008 -307 4008 -249Q4008 -229 3997 -219Q3986 -210 3962 -210H3656V-294H3878L3862 -281Q3862 -318 3852 -341Q3842 -364 3822 -376Q3803 -388 3774 -388Q3743 -388 3720 -373Q3698 -359 3686 -331Q3674 -303 3674 -261V-252Q3674 -181 3705 -151Q3737 -120 3803 -120Q3825 -120 3853 -125Q3882 -131 3908 -141Q3934 -151 3953 -145Q3972 -138 3983 -123Q3993 -107 3994 -88Q3995 -68 3985 -49Q3975 -31 3952 -21Q3917 -5 3878 3Q3838 11 3798 11Z M4166 9Q4122 9 4098 -14Q4075 -37 4075 -81V-412Q4075 -455 4097 -478Q4120 -501 4162 -501Q4204 -501 4226 -478Q4248 -455 4248 -412V-371H4238Q4248 -431 4289 -466Q4331 -501 4393 -503Q4422 -504 4436 -488Q4451 -473 4452 -431Q4452 -395 4437 -374Q4422 -353 4380 -349L4357 -347Q4304 -342 4280 -318Q4256 -293 4256 -245V-81Q4256 -37 4233 -14Q4210 9 4166 9Z";
const WORDMARK_DOT =
  "M1299 6Q1254 6 1227 -21Q1201 -48 1201 -92Q1201 -135 1227 -161Q1254 -188 1299 -188Q1346 -188 1372 -161Q1397 -135 1397 -92Q1397 -48 1372 -21Q1346 6 1299 6Z";

type WordmarkProps = {
  className?: string;
  style?: React.CSSProperties;
};

/**
 * The 「im.gamer」 wordmark. Sized by font-size (like text): letters use
 * currentColor, the dot the brand accent (--brand-dot).
 */
export function Wordmark({ className, style }: WordmarkProps) {
  return (
    <svg
      aria-label={BRAND_NAME}
      className={cn("brand-wordmark", className)}
      role="img"
      style={style}
      viewBox="40 -750 4416 945"
      xmlns="http://www.w3.org/2000/svg"
    >
      <path d={WORDMARK_LETTERS} fill="currentColor" />
      <path d={WORDMARK_DOT} fill="var(--brand-dot, var(--brand-accent))" />
    </svg>
  );
}

type BrandNameProps = {
  name: string;
  className?: string;
  /** Classes for a custom (non-brand) site name shown as text. */
  textClassName?: string;
};

/** Site name: the wordmark for 「im.gamer」, plain text for a custom app_name. */
export function BrandName({ name, className, textClassName }: BrandNameProps) {
  if (isBrandName(name)) {
    return <Wordmark className={className} />;
  }

  return <span className={cn(className, textClassName)}>{name}</span>;
}
