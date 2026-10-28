import { useState } from "react";
import { X } from "lucide-react";

import {
  PoolServer,
  RelatedServer,
  TrafficPool,
  landingGB,
} from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import { LandingTone, Pill } from "@/pages/landing/pill";

// Plan 157: the user's 共享流量池 in the admin user dialog, directly under the
// per-server list. Member servers share one billed-bytes limit; a flow that
// crosses several members counts once.

export const serverKey = (s: PoolServer) => `${s.serverKind}:${s.serverRefId}`;

/** 入口 / 落地 tag text of a related server. */
export function serverRoleText(s?: RelatedServer) {
  if (!s) return "";
  const roles = [s.entry ? "入口" : "", s.landing ? "落地" : ""].filter(
    Boolean,
  );

  return roles.join("·") + (s.serverKind === "landing" ? "·共享" : "");
}

function roleTone(s?: RelatedServer): LandingTone {
  if (!s) return "slate";
  if (s.landing && !s.entry) return "cyan";
  if (s.entry && !s.landing) return "purple";

  return "teal";
}

/** Server name + its 入口/落地 tag, for pickers. */
export function RelatedServerLabel({
  server,
  name,
}: {
  server?: RelatedServer;
  name: string;
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <span className="truncate">{name}</span>
      {server && (
        <Pill className="shrink-0" tone={roleTone(server)}>
          {serverRoleText(server)}
        </Pill>
      )}
    </span>
  );
}

export function TrafficPoolsEditor({
  pools,
  candidates,
  nameOf,
  onChange,
}: {
  pools: TrafficPool[];
  candidates: RelatedServer[];
  nameOf: (s: PoolServer) => string;
  onChange: (pools: TrafficPool[]) => void;
}) {
  const [adding, setAdding] = useState<Record<number, string>>({});
  const byKey = new Map(candidates.map((s) => [serverKey(s), s]));
  const used = new Set(pools.flatMap((p) => p.members.map(serverKey)));
  const update = (index: number, patch: Partial<TrafficPool>) =>
    onChange(pools.map((p, i) => (i === index ? { ...p, ...patch } : p)));
  const nextName = () => {
    for (let n = pools.length + 1; ; n++) {
      const name = `流量池 ${n}`;

      if (!pools.some((p) => p.name === name)) return name;
    }
  };

  return (
    <div data-traffic-pools className="space-y-2">
      <h4 className="text-sm font-medium">共享流量池</h4>
      <p className="text-xs text-default-500">
        池内服务器共用一个流量额度（计费流量，周期跟随用户流量重置日）。同一条流量经过池内多台服务器只算一次，所以池的已用不等于各服务器已用之和。一台服务器只能加入该用户的一个池；池用完后，经过池内服务器的规则和池内服务器上的落地节点会暂停，到重置日或调高限额后自动恢复。
      </p>
      {pools.length === 0 && (
        <p className="text-sm text-default-500">暂无共享流量池。</p>
      )}
      {pools.map((pool, index) => {
        const pick = adding[index] || "";
        const options = candidates.filter((s) => !used.has(serverKey(s)));

        return (
          <div
            key={pool.id ? `pool-${pool.id}` : `new-${index}`}
            className="space-y-2 rounded-lg border border-divider p-3"
            data-traffic-pool={pool.name}
          >
            <div className="grid grid-cols-[minmax(0,1fr)_96px] items-end gap-2 sm:grid-cols-[minmax(0,1fr)_110px_auto_auto]">
              <Input
                label="池名称"
                maxLength={32}
                size="sm"
                value={pool.name}
                variant="bordered"
                onChange={(event) =>
                  update(index, { name: event.target.value })
                }
              />
              <Input
                aria-label={`${pool.name} 限额 GB`}
                label="限额 (GB)"
                min="0"
                size="sm"
                step="0.01"
                type="number"
                value={String(pool.limitBytes / 1073741824)}
                variant="bordered"
                onChange={(event) =>
                  update(index, {
                    limitBytes: Math.round(
                      Number(event.target.value) * 1073741824,
                    ),
                  })
                }
              />
              <div className="flex items-center gap-2 text-sm sm:block">
                <span className="text-xs text-default-500 sm:block">已用</span>
                <span
                  className={
                    pool.paused ? "font-semibold text-danger" : "font-semibold"
                  }
                >
                  {landingGB((pool.usedIn || 0) + (pool.usedOut || 0))} GB
                </span>
              </div>
              <Button
                className="justify-self-end"
                color="danger"
                size="sm"
                variant="flat"
                onPress={() => onChange(pools.filter((_, i) => i !== index))}
              >
                删除
              </Button>
            </div>
            {pool.paused && (
              <Pill tone="red">额度已用完，相关规则与落地节点已暂停</Pill>
            )}
            <div className="flex flex-wrap gap-1.5">
              {pool.members.map((member) => {
                const server = byKey.get(serverKey(member));

                return (
                  <Pill
                    key={serverKey(member)}
                    className="pr-1"
                    title={
                      server
                        ? serverRoleText(server)
                        : "已不是该用户的相关服务器"
                    }
                    tone={roleTone(server)}
                  >
                    <span className="inline-flex items-center gap-1">
                      {nameOf(member)}
                      {server && (
                        <span className="font-normal opacity-80">
                          {serverRoleText(server)}
                        </span>
                      )}
                      <button
                        aria-label={`从 ${pool.name} 移除 ${nameOf(member)}`}
                        className="rounded-full p-0.5 hover:bg-black/10 dark:hover:bg-white/10"
                        type="button"
                        onClick={() =>
                          update(index, {
                            members: pool.members.filter(
                              (m) => serverKey(m) !== serverKey(member),
                            ),
                          })
                        }
                      >
                        <X className="h-3 w-3" />
                      </button>
                    </span>
                  </Pill>
                );
              })}
              {pool.members.length === 0 && (
                <span className="text-xs text-warning">
                  还没有服务器，这个池暂不生效
                </span>
              )}
            </div>
            <div className="flex items-end gap-2">
              <Select
                className="min-w-0 flex-1"
                isDisabled={options.length === 0}
                label="加入服务器"
                placeholder={
                  options.length === 0 ? "相关服务器都已在池中" : "选择服务器"
                }
                selectedKeys={pick ? [pick] : []}
                size="sm"
                onSelectionChange={(keys) =>
                  setAdding({
                    ...adding,
                    [index]: String(Array.from(keys)[0] || ""),
                  })
                }
              >
                {options.map((server) => (
                  <SelectItem
                    key={serverKey(server)}
                    textValue={`${nameOf(server)} ${serverRoleText(server)}`}
                  >
                    <RelatedServerLabel name={nameOf(server)} server={server} />
                  </SelectItem>
                ))}
              </Select>
              <Button
                color="success"
                isDisabled={!pick}
                size="sm"
                variant="flat"
                onPress={() => {
                  const server = byKey.get(pick);

                  if (!server) return;
                  update(index, {
                    members: [
                      ...pool.members,
                      {
                        serverKind: server.serverKind,
                        serverRefId: server.serverRefId,
                        serverName: server.serverName,
                      },
                    ],
                  });
                  setAdding({ ...adding, [index]: "" });
                }}
              >
                加入
              </Button>
            </div>
          </div>
        );
      })}
      <Button
        color="success"
        size="sm"
        variant="flat"
        onPress={() =>
          onChange([
            ...pools,
            {
              name: nextName(),
              limitBytes: 0,
              members: [],
              usedIn: 0,
              usedOut: 0,
            },
          ])
        }
      >
        新建流量池
      </Button>
    </div>
  );
}
