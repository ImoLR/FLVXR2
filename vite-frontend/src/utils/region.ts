// ISO 3166-1 alpha-2: fixed options, independent of browser locale support.
export const REGION_CODES =
  "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW".split(
    " ",
  );

const COMMON_NAMES: Record<string, string> = {
  HK: "香港",
  TW: "台湾",
  JP: "日本",
  SG: "新加坡",
  KR: "韩国",
  US: "美国",
  CN: "中国大陆",
  MO: "澳门",
  GB: "英国",
  DE: "德国",
  NL: "荷兰",
  FR: "法国",
  CA: "加拿大",
  AU: "澳大利亚",
  RU: "俄罗斯",
  MY: "马来西亚",
  TH: "泰国",
  VN: "越南",
  PH: "菲律宾",
  ID: "印度尼西亚",
  IN: "印度",
  TR: "土耳其",
  AE: "阿联酋",
};
const FIRST_REGIONS = ["HK", "TW", "JP", "SG", "KR", "US"];
export const MULTI_REGION = "multi";
export const UNKNOWN_REGION = "";
const displayNames =
  typeof Intl.DisplayNames === "function"
    ? new Intl.DisplayNames("zh-CN", { type: "region" })
    : undefined;

export function regionName(code?: string): string {
  if (!code) return "未设置地区";
  if (code === MULTI_REGION) return "多地区";
  const normalized = code.toUpperCase();

  if (COMMON_NAMES[normalized]) return COMMON_NAMES[normalized];
  try {
    return displayNames?.of(normalized) || normalized;
  } catch {
    return normalized;
  }
}

export function regionFlag(code?: string): string {
  if (!code || !/^[A-Z]{2}$/.test(code.toUpperCase())) return "";

  return Array.from(code.toUpperCase(), (letter) =>
    String.fromCodePoint(127397 + letter.charCodeAt(0)),
  ).join("");
}

export function regionLabel(code?: string): string {
  return [regionFlag(code), regionName(code)].filter(Boolean).join(" ");
}

export function compareRegions(a: string, b: string): number {
  const rank = (code: string) => {
    if (code === UNKNOWN_REGION) return 1001;
    if (code === MULTI_REGION) return 1000;
    const index = FIRST_REGIONS.indexOf(code);

    return index >= 0 ? index : FIRST_REGIONS.length;
  };

  return (
    rank(a) - rank(b) ||
    regionName(a).localeCompare(regionName(b), "zh-CN") ||
    a.localeCompare(b)
  );
}

export function exitRegionKey(codes?: string[]): string {
  const unique = [...new Set(codes || [])];

  return unique.length > 1 ? MULTI_REGION : unique[0] || UNKNOWN_REGION;
}

export function exitRegionLabel(codes?: string[]): string {
  const key = exitRegionKey(codes);

  return key === MULTI_REGION
    ? `多地区（${[...new Set(codes)].sort(compareRegions).map(regionLabel).join("、")}）`
    : regionLabel(key);
}
