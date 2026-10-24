import { useId, useState } from "react";
import toast from "react-hot-toast";

import { TintChip as Chip } from "./pill";
import { invalidateEgressPresets } from "./use-egress-presets";

import {
  EgressLandingNodeChoice,
  EgressOutbound,
  EgressRule,
  inboundProtocolOptions,
  presetAskAction,
  saveEgressPreset,
  splitLines,
  splitList,
  toPresetRule,
} from "@/api/landing-egress";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Input, Textarea } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { Switch } from "@/shadcn-bridge/heroui/switch";

const protocolOptions = [
  ["http", "HTTP"],
  ["tls", "TLS"],
  ["quic", "QUIC"],
  ["bittorrent", "BitTorrent"],
] as const;

function Examples({
  values,
  onPick,
}: {
  values: string[];
  onPick: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-xs text-default-500">示例</span>
      {values.map((value) => (
        <button
          key={value}
          className="rounded-full"
          type="button"
          onClick={() => onPick(value)}
        >
          <Chip
            className="cursor-pointer"
            color="secondary"
            size="sm"
            variant="flat"
          >
            {value}
          </Chip>
        </button>
      ))}
    </div>
  );
}

export function RuleEditor({
  rule,
  outbounds,
  landingNodes,
  onClose,
  onDone,
  hideSource = false,
  hideNodeScope = false,
  savePreset = false,
  askOption = false,
}: {
  rule: EgressRule;
  outbounds: Pick<EgressOutbound, "id" | "name">[];
  landingNodes: EgressLandingNodeChoice[];
  onClose: () => void;
  onDone: (rule: EgressRule) => void;
  // Plan 134: node rules have no 来源 IP / 来源端口; global and node rules
  // have no 适用落地节点.
  hideSource?: boolean;
  hideNodeScope?: boolean;
  // Plan 140 (admin lists): 「同时保存为预设」 on confirm; askOption offers
  // the 「插入时选择」 action while editing an ask-outbound preset.
  savePreset?: boolean;
  askOption?: boolean;
}) {
  const [form, setForm] = useState(rule);
  const [asPreset, setAsPreset] = useState(false);
  const [presetName, setPresetName] = useState("");
  const [askLater, setAskLater] = useState(false);
  const uid = useId();
  const [domains, setDomains] = useState(rule.domains.join("\n"));
  const [ips, setIPs] = useState(rule.ips.join("\n"));
  const [sources, setSources] = useState(rule.sourceIps.join("\n"));
  const set = (patch: Partial<EgressRule>) =>
    setForm((prev) => ({ ...prev, ...patch }));
  const append = (
    current: string,
    setter: (v: string) => void,
    value: string,
  ) => {
    const lines = splitLines(current);

    if (!lines.includes(value)) setter([...lines, value].join("\n"));
  };
  const appendPort = (key: "ports" | "sourcePorts", value: string) =>
    set({ [key]: form[key] ? `${form[key]},${value}` : value });

  const next: EgressRule = {
    ...form,
    domains: splitLines(domains),
    ips: splitList(ips),
    sourceIps: splitList(sources),
  };
  const isOutbound = next.action.startsWith("outbound:");
  // The rule is saved by the caller regardless; the preset is extra.
  const saveAsPreset = async () => {
    const ask = isOutbound && askLater;
    const res = await saveEgressPreset({
      name: (presetName || next.name || "").trim() || "我的预设",
      description: "",
      askOutbound: ask,
      insertTop: false,
      rules: [
        toPresetRule({ ...next, action: ask ? presetAskAction : next.action }),
      ],
    });

    if (res.code !== 0) {
      toast.error(`规则已保存，但预设未保存：${res.msg || "保存失败"}`);

      return;
    }
    toast.success(`已保存为预设「${res.data.name}」`);
    void invalidateEgressPresets();
  };
  const hasMatcher =
    next.domains.length +
      next.ips.length +
      next.sourceIps.length +
      next.protocols.length +
      next.landingNodeIds.length +
      (next.inboundProtocols?.length ?? 0) >
      0 || Boolean(next.ports || next.sourcePorts || next.network);
  const inbound = form.inboundProtocols ?? [];

  return (
    <Modal isOpen scrollBehavior="inside" size="lg" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>
          {rule.name ? `编辑规则 · ${rule.name}` : "规则"}
        </ModalHeader>
        <ModalBody className="space-y-5">
          <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
            <Input
              label="规则名称"
              placeholder="例如：AI 解锁"
              value={form.name}
              variant="bordered"
              onChange={(event) => set({ name: event.target.value })}
            />
            <div className="flex items-center gap-2 pb-2 text-sm">
              <Switch
                aria-label="启用规则"
                isSelected={form.enabled}
                onValueChange={(enabled) => set({ enabled })}
              />
              <span>启用</span>
            </div>
          </div>
          <Select
            label="出站"
            selectedKeys={[form.action]}
            variant="bordered"
            onSelectionChange={(keys) =>
              set({ action: String(Array.from(keys)[0]) })
            }
          >
            {[
              <SelectItem key="block">封锁</SelectItem>,
              // Plain 「直连」: 「（默认）」 read as the preselected choice while
              // new rules start as 封锁 (QA F28).
              <SelectItem key="direct">直连</SelectItem>,
              ...outbounds.map((o) => (
                <SelectItem key={`outbound:${o.id}`}>{o.name}</SelectItem>
              )),
              ...(askOption || form.action === presetAskAction
                ? [
                    <SelectItem key={presetAskAction}>
                      插入时选择（添加预设时再选出站）
                    </SelectItem>,
                  ]
                : []),
            ]}
          </Select>
          <p className="text-xs text-default-500">
            下面填写的条件需同时满足；同一项里多条内容任一命中即可。留空的项不参与匹配。
          </p>

          <section className="space-y-2">
            <h3 className="font-medium">目标域名</h3>
            <Textarea
              minRows={3}
              placeholder={"geosite:netflix\ndomain:example.com"}
              value={domains}
              variant="bordered"
              onChange={(event) => setDomains(event.target.value)}
            />
            <p className="text-xs text-default-500">
              每行一个：geosite:分类、domain:域名（含子域名）、full:完整域名、keyword:关键字、regexp:正则；直接写域名或
              *.example.com 等同 domain:
            </p>
            <Examples
              values={[
                "geosite:netflix",
                "geosite:openai",
                "geosite:cn",
                "domain:example.com",
              ]}
              onPick={(v) => append(domains, setDomains, v)}
            />
          </section>

          <section className="space-y-2">
            <h3 className="font-medium">目标 IP</h3>
            <Textarea
              minRows={3}
              placeholder={"geoip:cn\n1.2.3.0/24"}
              value={ips}
              variant="bordered"
              onChange={(event) => setIPs(event.target.value)}
            />
            <p className="text-xs text-default-500">
              每行一个：geoip:国家代码、CIDR 或单个 IP；geoip:!cn
              表示中国大陆以外
            </p>
            <Examples
              values={["geoip:cn", "1.2.3.0/24", "2001:db8::/32"]}
              onPick={(v) => append(ips, setIPs, v)}
            />
          </section>

          <section className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-2">
              <Input
                label="目标端口"
                placeholder="443,8000-9000"
                value={form.ports}
                variant="bordered"
                onChange={(event) => set({ ports: event.target.value })}
              />
              <Examples
                values={["443", "8000-9000"]}
                onPick={(v) => appendPort("ports", v)}
              />
            </div>
            <Select
              label="网络"
              selectedKeys={[form.network || "all"]}
              variant="bordered"
              onSelectionChange={(keys) => {
                const v = String(Array.from(keys)[0]);

                set({ network: v === "all" ? "" : (v as "tcp" | "udp") });
              }}
            >
              <SelectItem key="all">全部</SelectItem>
              <SelectItem key="tcp">TCP</SelectItem>
              <SelectItem key="udp">UDP</SelectItem>
            </Select>
          </section>

          {!hideSource && (
            <>
              <section className="space-y-2">
                <h3 className="font-medium">来源 IP</h3>
                <Textarea
                  minRows={3}
                  placeholder="geoip:cn"
                  value={sources}
                  variant="bordered"
                  onChange={(event) => setSources(event.target.value)}
                />
                <p className="text-xs text-default-500">
                  客户端的来源地址。只对直连客户端生效：经 FLVX
                  节点转发进来的流量和来源白名单不受影响。
                </p>
                <Examples
                  values={["geoip:cn", "203.0.113.0/24"]}
                  onPick={(v) => append(sources, setSources, v)}
                />
              </section>

              <section className="grid gap-3 sm:grid-cols-2">
                <Input
                  label="来源端口"
                  placeholder="1024-65535"
                  value={form.sourcePorts}
                  variant="bordered"
                  onChange={(event) => set({ sourcePorts: event.target.value })}
                />
              </section>
            </>
          )}

          <section className="space-y-2">
            <h3 className="font-medium">协议</h3>
            <div className="flex flex-wrap gap-4">
              {protocolOptions.map(([key, label]) => (
                <Checkbox
                  key={key}
                  isSelected={form.protocols.includes(key)}
                  onValueChange={(checked) =>
                    set({
                      protocols: checked
                        ? [...form.protocols, key]
                        : form.protocols.filter((p) => p !== key),
                    })
                  }
                >
                  {label}
                </Checkbox>
              ))}
            </div>
            <p className="text-xs text-default-500">
              按流量特征识别，例如 BitTorrent 下载。
            </p>
          </section>

          {/* Plan 149: admin scopes only (a node rule covers one node, so one
              protocol). Empty = every node. */}
          {!hideSource && (
            <section data-rule-inbound-protocols className="space-y-2">
              <h3 className="font-medium">节点协议</h3>
              <div className="flex flex-wrap gap-4">
                {inboundProtocolOptions.map(([key, label]) => (
                  <Checkbox
                    key={key}
                    isSelected={inbound.includes(key)}
                    onValueChange={(checked) =>
                      set({
                        inboundProtocols: checked
                          ? [...inbound, key]
                          : inbound.filter((p) => p !== key),
                      })
                    }
                  >
                    {label}
                  </Checkbox>
                ))}
              </div>
              <p className="text-xs text-default-500">
                只作用于这些协议的落地节点；不选 =
                全部节点。例如「禁止大陆直连」只选 SS2022：SS 节点经 FLVX
                转发使用，VLESS Reality 节点仍可从大陆直连。
              </p>
            </section>
          )}

          {!hideNodeScope && (
            <section className="space-y-2">
              <h3 className="font-medium">适用落地节点</h3>
              <Select
                isSearchable
                label="节点"
                placeholder="不选 = 使用该方案的全部节点"
                selectedKeys={form.landingNodeIds.map(String)}
                selectionMode="multiple"
                variant="bordered"
                onSelectionChange={(keys) =>
                  set({ landingNodeIds: Array.from(keys).map(Number) })
                }
              >
                {landingNodes.map((n) => (
                  <SelectItem
                    key={String(n.id)}
                    textValue={`${n.name} · ${n.serverName}${n.userName ? ` · ${n.userName}` : ""}`}
                  >
                    {n.name} · {n.serverName}
                    {n.userName ? ` · ${n.userName}` : ""}
                  </SelectItem>
                ))}
              </Select>
            </section>
          )}
          {!hasMatcher && (
            <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning">
              请至少填写一个匹配条件；想让全部流量走某个出站，请设置方案的「默认出站」。
            </p>
          )}
          {savePreset && (
            <section className="space-y-2 rounded-lg border border-divider p-3">
              {/* Labels so tapping the text toggles too (phones). */}
              <div className="flex items-center gap-2">
                <Checkbox
                  id={`${uid}-preset`}
                  isSelected={asPreset}
                  onValueChange={setAsPreset}
                />
                <label
                  className="cursor-pointer text-sm"
                  htmlFor={`${uid}-preset`}
                >
                  同时保存为预设
                </label>
              </div>
              {asPreset && (
                <div className="space-y-2 pl-1">
                  <Input
                    label="预设名称"
                    placeholder={form.name || "规则名称"}
                    size="sm"
                    value={presetName}
                    variant="bordered"
                    onChange={(event) => setPresetName(event.target.value)}
                  />
                  {isOutbound && (
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id={`${uid}-ask`}
                        isSelected={askLater}
                        onValueChange={setAskLater}
                      />
                      <label
                        className="cursor-pointer text-sm"
                        htmlFor={`${uid}-ask`}
                      >
                        插入时再选出站
                      </label>
                    </div>
                  )}
                  <p className="text-xs text-default-500">
                    以后可在「添加预设」里一键添加；规则本身照常保存，预设保存失败不影响它。
                  </p>
                </div>
              )}
            </section>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            isDisabled={!hasMatcher}
            onPress={() => {
              onDone(next);
              if (savePreset && asPreset) void saveAsPreset();
            }}
          >
            确定
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
