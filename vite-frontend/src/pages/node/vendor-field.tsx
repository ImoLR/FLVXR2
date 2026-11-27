import type { NodeVendorOption } from "@/api";

import { TriangleAlert } from "lucide-react";

import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 318: 「VPS 厂商」 belongs to the node (the VPS). Required, multi
// (seller + upstream cloud, e.g. 火山广州 = Leikwanhost + 火山引擎). The admin
// only picks vendors; domains live in 全局规则 → 内置 → 管理厂商. The last
// option 「其他（列表里没有）」 + a name box marks a vendor that is not in the
// list yet: red ⚠, no rule, until the admin adds the vendor there and picks
// it here (never linked automatically).

export const VENDOR_OTHER_KEY = "__other";
export const MAX_NODE_VENDORS = 4;

export interface NodeVendorValue {
  vendors: string[];
  vendorOther: string;
  vendorOtherOn: boolean;
}

export const otherVendorText = (name: string) =>
  `厂商「${name}」不在管理厂商列表，请先在管理厂商补齐域名，再回节点选择`;

/** Client-side check of the required field ({} = ok). */
export function validateNodeVendors(
  v: NodeVendorValue,
  options: NodeVendorOption[],
): { vendors?: string; vendorOther?: string } {
  const other = v.vendorOther.trim();

  if (v.vendorOtherOn && !other)
    return { vendorOther: "请填写「其他」厂商的名称" };
  if (v.vendorOtherOn) {
    const listed = options.find(
      (o) =>
        o.name.toLowerCase() === other.toLowerCase() ||
        o.key === other.toLowerCase(),
    );

    if (listed)
      return {
        vendorOther: `厂商「${listed.name}」已在列表里，请直接选择它`,
      };
  }
  if (v.vendors.length === 0 && !v.vendorOtherOn)
    return {
      vendors:
        "请选择 VPS 厂商（必填；列表里没有就选「其他（列表里没有）」并填写名称）",
    };
  if (v.vendors.length > MAX_NODE_VENDORS)
    return { vendors: `VPS 厂商最多选 ${MAX_NODE_VENDORS} 个` };

  return {};
}

/** The node dialog field: multi-select + 「其他」 name box. */
export function NodeVendorField({
  value,
  options,
  loading,
  isEdit,
  errors,
  onChange,
}: {
  value: NodeVendorValue;
  options: NodeVendorOption[];
  loading?: boolean;
  isEdit?: boolean;
  errors: { vendors?: string; vendorOther?: string };
  onChange: (next: NodeVendorValue) => void;
}) {
  const selected = [
    ...value.vendors,
    ...(value.vendorOtherOn ? [VENDOR_OTHER_KEY] : []),
  ];
  const known = new Set(options.map((o) => o.key));
  const domains = Array.from(
    new Set(
      value.vendors.flatMap(
        (k) => options.find((o) => o.key === k)?.domains || [],
      ),
    ),
  );
  const other = value.vendorOther.trim();
  const empty = value.vendors.length === 0 && !value.vendorOtherOn;

  return (
    <div data-node-vendor-field className="space-y-2">
      <Select
        isRequired
        isSearchable
        description={
          "必填，可多选（例：火山广州 = Leikwanhost + 火山引擎）。只选厂商，域名在「全局规则 → 内置 → 管理厂商」里维护；这台节点做落地时会屏蔽这些厂商的官网"
        }
        errorMessage={errors.vendors}
        isDisabled={loading}
        isInvalid={!!errors.vendors}
        label="VPS 厂商"
        placeholder={loading ? "正在读取厂商列表…" : "选择这台 VPS 的厂商"}
        searchPlaceholder="搜索厂商"
        selectedKeys={selected}
        selectionMode="multiple"
        onSelectionChange={(keys) => {
          const next = Array.from(keys).map(String);
          // Keep the pick order: existing picks first, then new ones.
          const kept = value.vendors.filter((k) => next.includes(k));
          const added = next.filter(
            (k) => k !== VENDOR_OTHER_KEY && !kept.includes(k),
          );

          onChange({
            ...value,
            vendors: [...kept, ...added],
            vendorOtherOn: next.includes(VENDOR_OTHER_KEY),
          });
        }}
      >
        {[
          ...options.map((o) => (
            <SelectItem key={o.key} textValue={o.name}>
              {o.name}
            </SelectItem>
          )),
          // A stored key that is no longer in the list (shown so it can be removed).
          ...value.vendors
            .filter((k) => !known.has(k))
            .map((k) => (
              <SelectItem key={k} textValue={k}>
                {k}（已从列表删除）
              </SelectItem>
            )),
          <SelectItem key={VENDOR_OTHER_KEY} textValue="其他（列表里没有）">
            其他（列表里没有）
          </SelectItem>,
        ]}
      </Select>
      {value.vendorOtherOn && (
        <Input
          isRequired
          errorMessage={errors.vendorOther}
          isInvalid={!!errors.vendorOther}
          label="其他厂商名称"
          maxLength={32}
          placeholder="例如：某某云"
          value={value.vendorOther}
          variant="bordered"
          onChange={(e) => onChange({ ...value, vendorOther: e.target.value })}
        />
      )}
      {value.vendorOtherOn && other && !errors.vendorOther && (
        <p
          data-node-vendor-other-warn
          className="flex items-start gap-1 text-xs text-danger"
        >
          <TriangleAlert
            aria-hidden="true"
            className="mt-0.5 h-3.5 w-3.5 shrink-0"
          />
          <span>
            {otherVendorText(other)}
            {value.vendors.length > 0
              ? "（已选的厂商照常屏蔽）"
              : "；在那之前这台节点做落地时不屏蔽任何厂商官网"}
          </span>
        </p>
      )}
      {isEdit && empty && !errors.vendors && (
        <p className="flex items-start gap-1 text-xs text-warning">
          <TriangleAlert
            aria-hidden="true"
            className="mt-0.5 h-3.5 w-3.5 shrink-0"
          />
          <span>这台节点还没设置 VPS 厂商，保存前请选择</span>
        </p>
      )}
      {domains.length > 0 && (
        <p className="break-words text-xs text-default-500">
          做落地时屏蔽：{domains.join("、")}（含子域名）
        </p>
      )}
    </div>
  );
}

/** What a node card says about its vendor (null = nothing to do). */
export function nodeVendorAttention(node: {
  vendors?: string[];
  vendorOther?: string;
}): { level: "warn" | "error"; text: string } | null {
  if (node.vendors === undefined) return null; // not an admin view
  const other = node.vendorOther?.trim();

  if (other) return { level: "error", text: otherVendorText(other) };
  if (node.vendors.length === 0)
    return {
      level: "warn",
      text: "未设置 VPS 厂商：做落地时「屏蔽本机厂商官网」不生效，请在「编辑」里选择",
    };

  return null;
}

/** The card / list ⚠ next to the node name (admins only). */
export function NodeVendorBadge({
  node,
  onEdit,
}: {
  node: { vendors?: string[]; vendorOther?: string };
  onEdit?: () => void;
}) {
  const a = nodeVendorAttention(node);

  if (!a) return null;
  const red = a.level === "error";

  return (
    <Dropdown>
      <DropdownTrigger>
        <Button
          aria-label={red ? "厂商不在列表" : "未设置 VPS 厂商"}
          className={`h-6 min-w-0 shrink-0 gap-1 px-1 text-xs ${red ? "text-danger" : "text-warning"}`}
          color={red ? "danger" : "warning"}
          data-node-vendor-badge={a.level}
          size="sm"
          title={a.text}
          variant="light"
          onClick={(event) => event.stopPropagation()}
        >
          <TriangleAlert aria-hidden="true" className="h-4 w-4 shrink-0" />
          <span className="hidden sm:inline">厂商</span>
        </Button>
      </DropdownTrigger>
      <DropdownMenu aria-label="VPS 厂商提示">
        <DropdownMenuLabel className="max-w-[min(22rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
          <p
            className={`flex items-center gap-1 font-medium ${red ? "text-danger" : "text-warning"}`}
          >
            <TriangleAlert
              aria-hidden="true"
              className="h-3.5 w-3.5 shrink-0"
            />
            {red ? "厂商不在管理厂商列表" : "未设置 VPS 厂商"}
          </p>
          <p className="mt-1.5 break-words text-foreground">{a.text}</p>
          <p className="mt-2 text-default-500">仅管理员可见</p>
        </DropdownMenuLabel>
        {onEdit && (
          <DropdownItem
            className="mx-1 mb-1 justify-center rounded-md bg-primary/15 text-xs font-medium text-primary"
            onPress={onEdit}
          >
            编辑节点
          </DropdownItem>
        )}
      </DropdownMenu>
    </Dropdown>
  );
}
